# 第三方 Capability API（2026-10-29 更新）

> 状态：**Phase 0 同步 JSON + Phase 1 异步任务基础链路 + Bright Data / AISA 能力已实现**。
> 规则见仓库根目录 [`RULES.md`](../../RULES.md)。
>
> 2026-10-23 追加：异步 per-unit（估算预扣 + 终态差额结算）和
> `/v1/tasks/{id}/content` 文件下载已实现并通过单测；线上 smoke 通过。
>
> 2026-10-23 追加：同步执行器支持必填输入、URL allowlist、POST body
> 常量/环境变量覆盖，以及业务成功条件；新增 Bright Data Web Unlocker
> `web-unlock-v1`。部署记录见
> [`../03-deployment/tokwork-nbg1.md`](../03-deployment/tokwork-nbg1.md)。
>
> 2026-10-29 追加：通用执行器支持 POST 数组 body、静态请求头、响应头用量、
> 固定预扣上限和稳定错误映射；接入 AISA 的 16 个搜索/SEO 能力。
>
> 2026-10-29 追加（已实现并离线核实）：付费 Capability 不随调用方断开而取消；
> 一次调用使用单一整体截止时间；已写出请求在传输错误后不重试；2xx 后处理失败和
> 结果未知的按用量调用按实际成本或预扣上限结算。每日 AISA 用量自动对账。

## 路由

所有路由使用现有 `sk-` API token（`middleware.TokenAuth()`）：

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/v1/capabilities` | 能力目录（ID / provider / mode / billing） |
| `POST` | `/v1/capabilities/{capability}` | 同步 Capability |
| `POST` | `/v1/tasks` | 异步提交 |
| `GET` | `/v1/tasks/{id}` | 异步状态（懒轮询） |
| `GET` | `/v1/tasks/{id}/content` | 文件内容（manifest 配置了 content 时可用） |

## 同步契约

```json
POST /v1/capabilities/weather-v1
{
  "input": { "latitude": 52.52, "longitude": 13.41 },
  "options": {},
  "idempotency_key": "optional"
}
```

返回：

```json
{
  "success": true,
  "id": "20261022051153...",
  "capability": "weather-v1",
  "output": { "temperature_2m": 10.6, "wind_speed_10m": 12.8, "time": "2026-10-22T07:00" },
  "usage": { "unit": "call", "quantity": 1 },
  "cost_usd": 0,
  "provider_request_id": ""
}
```

## Manifest 请求与成功条件

同步 Manifest 的 `request` 可声明以下通用字段：

| 字段 | 语义 |
|---|---|
| `required` | 必填输入字段名数组。字段缺失、`null`、空字符串/空数组/空对象且没有 Manifest 默认值时，在预扣费和上游请求之前返回 `400 invalid_request` |
| `url_inputs` | 映射输入字段到 `allowed_hosts`。只接受 HTTPS；主机必须精确匹配列表项或以 `.<域名>` 结尾；拒绝 userinfo、IP 字面量和非 443 端口 |
| `consts` | 固定字段。GET 放入 query，POST 只放入 body；POST 不会把常量混入未声明 query |
| `env` | POST body 字段到服务端环境变量名的映射；同名用户输入和常量会被环境变量值覆盖。变量缺失返回 `credential_error` |
| `body_wrap` | 只允许空值或 `"array"`；`"array"` 把组装好的 POST object 包成单元素数组 |
| `headers` | 常量请求头。拒绝 `Authorization`、`Host`、`Content-Length`、Cookie 和 hop-by-hop 头，也拒绝非法名称和换行注入 |

`response` 还可声明：

| 字段 | 语义 |
|---|---|
| `success.path` + `success.values` | 上游 JSON 中业务状态必须命中允许值，否则返回稳定错误码（默认 `upstream_target_status`） |
| `success.non_empty` | 指定正文路径必须非空，否则返回 `upstream_empty_body` |
| `success.failure_code` / `success.empty_code` | 覆盖上述两个稳定错误码 |
| `status_header` / `error_header` | 供应商以 HTTP 200 包装错误时，从响应头读取目标状态和错误标记；错误标记触发失败，重试按目标状态判断 |
| `error_code_path` | 从 JSON 错误体提取上游 code；正文为字符串时忽略 |
| `error_map` | 上游 code 或 HTTP 状态到 `{code, http_status, message, log_code}` 的稳定映射；用户只看到公共文案 |

业务失败按调用是否可能已经到达上游计费，不再一律退款。错误响应和日志不回传上游正文；
目标状态码可以作为数字记录。

AISA 的 402 余额不足属于**运营方账户余额**，统一映射为 `503 upstream_unavailable`；
401 / `authentication_unavailable` 也是 `503`，日志分别记为上游鉴权错误。
`estimated_price_exceeds_max_price` 映射为 `422 cost_cap_exceeded`，提示缩小月份、
指标或条数。其他上游 message 不原样返回。

## 异步契约

提交：

```json
POST /v1/tasks
{
  "capability": "ssl-analyze-v1",
  "input": { "host": "www.google.com" },
  "idempotency_key": "ssl-google-20261022"
}
```

返回 `202`：

```json
{ "success": true, "id": "task_...", "status": "queued" }
```

轮询：

```json
GET /v1/tasks/task_...
```

运行时返回 `status: "running"`；终态返回 `succeeded` + `output`，或 `failed` + `error_code`。

## 已接入的能力

| ID | 上游 | 模式 | 计费 | 说明 |
|---|---|---|---|---|
| `weather-v1` | Open-Meteo | 同步 | $0 / 次 | 坐标 → 当前温度/风速，无需 key |
| `ssl-analyze-v1` | SSL Labs | 异步 | $0 / 次 | host → 提交分析、轮询状态、返回 endpoints |
| `web-unlock-v1` | Bright Data Web Unlocker | 同步 | $0.003 / 次 | 抓取允许的 Wayfair 页面，返回目标状态码与 HTML |
| `site-traffic-snapshot-v1` | AISA / Similarweb | 同步 | 上游实际成本 | 域名单月流量快照 |
| `site-traffic-trend-v1` | AISA / Similarweb | 同步 | 上游实际成本 | 域名月度流量与 engagement 趋势 |
| `traffic-channels-v1` | AISA / Similarweb | 同步 | 上游实际成本 | 营销渠道流量来源 |
| `site-referrals-v1` | AISA / Similarweb | 同步 | 上游实际成本 | 引荐网站 |
| `similar-sites-v1` | AISA / Similarweb | 同步 | 上游实际成本 | 相似网站 |
| `backlinks-summary-v1` | AISA / DataForSEO | 同步 | 上游实际成本 | 外链汇总 |
| `referring-domains-v1` | AISA / DataForSEO | 同步 | 上游实际成本 | 引荐域名 |
| `backlink-anchors-v1` | AISA / DataForSEO | 同步 | 上游实际成本 | 外链锚文本 |
| `backlink-competitors-v1` | AISA / DataForSEO | 同步 | 上游实际成本 | 外链竞品 |
| `domain-rank-overview-v1` | AISA / DataForSEO | 同步 | 上游实际成本 | 域名排名与自然流量概览 |
| `organic-competitors-v1` | AISA / DataForSEO | 同步 | 上游实际成本 | 自然搜索竞品 |
| `ranked-keywords-v1` | AISA / DataForSEO | 同步 | 上游实际成本 | 域名排名关键词 |
| `keyword-gap-v1` | AISA / DataForSEO | 同步 | 上游实际成本 | 两域名关键词交集/差距 |
| `serp-google-v1` | AISA / DataForSEO | 同步 | 上游实际成本 | Google 自然搜索 SERP |
| `ai-brand-mentions-v1` | AISA / DataForSEO | 同步 | 上游实际成本 | LLM 品牌提及聚合指标 |
| `page-seo-audit-v1` | AISA / DataForSEO | 同步 | 上游实际成本 | 单页 SEO 审计 |

Manifest 在 `capability/manifests/`，编译时嵌入二进制。

### `web-unlock-v1`

- 输入只有 `url`，必须为 HTTPS。
- 仅允许 `wayfair.com`、`wayfair.ca`、`wayfair.co.uk`、`wayfair.de` 及其子域名。
- 发送目标只有 `url`；`zone` 来自 `CAPABILITY_BRIGHTDATA_ZONE`，
  `format: "json"` 由 Manifest 常量固定。用户不能通过输入覆盖这两个字段。
- Bearer key 来自 `CAPABILITY_BRIGHTDATA_API_KEY`；两个环境变量任一缺失都返回
  `credential_error`，不预扣费、不调用上游。
- 只有 Bright Data 返回 JSON envelope、目标 `status_code` 为 `200` 且 `body`
  非空时才成功。Bright Data 错误、目标非 200、空正文均退款。
- 输出：`{ "status": 200, "html": "<目标页面正文>" }`。
- 超时 `90s`，最大响应 `8 MiB`；上游 `429/502/503/504` 最多再试一次。

### AISA 搜索与 SEO 能力（2026-10-29）

基址为 `https://api.aisa.one/apis/v1`，Bearer key 来自
`CAPABILITY_AISA_API_KEY`。Manifest 只暴露白名单参数；DataForSEO 请求按 AISA 契约
包成单元素数组。地区/语言默认为美国 (`location_code=2840`) / 英语 (`language_code=en`)，
Similarweb 默认 `country=ww`、`granularity=monthly`。

| ID | 用途与关键参数 | 单次上限 |
|---|---|---|
| `site-traffic-snapshot-v1` | 域名单月流量快照；`domain`、可选 `month` | $0.60 |
| `site-traffic-trend-v1` | 月度流量趋势；`domain`、`start_date`、`end_date`、`metrics` | $1.00 |
| `traffic-channels-v1` | 营销渠道来源；`domain`、`start_date`、`end_date` | $1.05 |
| `site-referrals-v1` | 引荐网站；`domain`、日期、`limit`（默认 5，上游最大 20） | $1.50 |
| `similar-sites-v1` | 相似网站；`domain`、日期、`limit`（默认 2） | $0.75 |
| `backlinks-summary-v1` | 外链汇总；`target`、链接范围 | $0.10 |
| `referring-domains-v1` | 引荐域名；`target`、`limit`（默认 5） | $0.10 |
| `backlink-anchors-v1` | 锚文本；`target`、`limit`（默认 5） | $0.10 |
| `backlink-competitors-v1` | 外链竞品；`target`、`limit`（默认 5） | $0.10 |
| `domain-rank-overview-v1` | 域名排名概览；`target`、地区、语言 | $0.05 |
| `organic-competitors-v1` | 自然搜索竞品；`target`、地区、语言、`limit` | $0.10 |
| `ranked-keywords-v1` | 排名关键词；`target`、地区、语言、`limit` | $0.30 |
| `keyword-gap-v1` | 关键词差距；`target1`、`target2`、地区、语言 | $0.10 |
| `serp-google-v1` | Google SERP；`keyword`、地区、语言、设备、`depth` | $0.05 |
| `ai-brand-mentions-v1` | AI 品牌露出；`target` 必须是 `[{"domain":"..."}]` 或 `[{"keyword":"..."}]` | $0.20 |
| `page-seo-audit-v1` | 单页审计；`url`，JS/浏览器渲染默认关闭 | $0.03 |

`X-AISA-Max-Price-USD` 是上游调用前的准入上限，不保证最终扣费不超过该金额；
平台仍按成功响应头 `X-AISA-Customer-Cost-Micros-USD` 的实际值结算。
Similarweb 的客户 `idempotency_key` 会作为 `Idempotency-Key` 发送；上游重复 key
返回 409，不重放原结果。AISA 只对 429/503 最多重试一次，不对 200 或 502 重试。

2026-10-29 对 16 个能力的默认请求逐个执行免费 quote：全部 HTTP 200 且返回
`object=cost_estimate`。默认预估合计 `$3.625236`；单次预估范围为
`$0.0051`（`page-seo-audit-v1`）到 `$1.50`（`site-referrals-v1`）。
`site-referrals-v1` 原计划默认 20 条报价 `$6.00`，受单次上限约束改为默认 5；
`similar-sites-v1` 默认从 5 调为 2，使报价从 `$1.875` 降到 `$0.75`。

2026-10-29 发布后线上只对 DataForSEO 做了两笔真实付费 smoke：
`domain-rank-overview-v1` 与 `backlinks-summary-v1`（目标 `wikipedia.org`）均为 200，
实际分别产生 12,120 / 24,036 微美元上游成本；`cost_usd`、消费日志和余额变化一致。
AISA 余额共减少 `$0.036156`，验证后余 `$0.907844`。Similarweb 未做付费调用。

## 计费

- Manifest 的 `billing.price_usd` 是唯一价格来源，不读硬编码。
- `per_call`：提交/执行前预扣；同步成功结算，失败退款。
- `per_unit`：同步支持按上游返回的 `quantity_path` 或 `quantity_header` 结算；
  两者只能声明一个。响应头数字必须是非负整数或小数。
- 同步 `per_unit` 可声明 `reserve_usd`：调用前固定预扣该金额；成功但数量头缺失或非法时，
  按 `reserve_usd` 结算，消费日志的 `usage_source=reserve_fallback`，不返回 502。
  数量头为 `0` 时按 0 结算。失败仍全额退款。
- 异步 `per_unit`：manifest 必须声明 `estimate_path`，提交时按预估量预扣；
  任务完成时按 `quantity_path` 读取实际用量，差额补扣/退还。
- 价格为 0 的 capability 不创建 billing session，零余额用户也能调用。
- `GET /v1/capabilities` 对 `per_unit` 额外返回 `billing.max_usd`，当前等于
  `billing.reserve_usd`。

### AISA 按成本计价

AISA 能力的 `quantity_header` 是
`X-AISA-Customer-Cost-Micros-USD`，值为微美元；`billing.unit=micro_usd`。
本批 Manifest 的 `billing.price_usd=0.000001`，表示每 1 微美元上游成本收
$0.000001，即当前按 1 倍成本价。实际 quota 仍为：

```text
price_usd × quantity × QuotaPerUnit × GroupRatio
```

以后要加价只改 Manifest 的 `price_usd`。例如加价 20% 写 `0.0000012`；
不要改执行器或 controller 里的公式。

响应缺少 `X-AISA-Customer-Cost-Micros-USD` 表示成本未知，不表示免费；
本批能力因此保留 `reserve_usd` 兜底。`X-AISA-Max-Price-USD` 只是准入检查。
AISA 运营方余额耗尽的 402 不会变成客户自己的 `quota_insufficient`；客户收到
`503 upstream_unavailable`，维护者需要给 AISA 账户充值。

### 付费调用生命周期与失败计费（2026-10-29 已核实）

- 同步、异步提交、轮询和内容下载收到请求后，用
  `context.WithoutCancel(c.Request.Context())` 脱离调用方连接，再套 Manifest 的
  `limits.timeout`。客户端断开、桌面 Stop 或网关超时不会取消已经开始的付费上游操作。
- Manifest timeout 是一次能力调用的总预算，覆盖连接、所有重试和退避。执行器不再给
  每次尝试各发一份 timeout。
- 非 2xx 只按 `retry.retry_on` 重试。传输错误使用 `httptrace.WroteRequest` 判断：
  DNS、连接拒绝、TLS 握手失败等未写出请求可以重试；请求写出后一律不重试。
- 2xx 已经开始或可能已经计费，因此读到 2xx 后不按业务状态再重试。读正文失败、
  超过 `max_response_bytes`、JSON 无效、`response.success` 不通过、
  `missing_output_field`、`capabilitySyncUsage` 失败等返回原业务错误，但先计费。
- 同步按用量能力在请求写出后没拿到响应（例如总 deadline 到期、连接在响应途中断开）
  返回 `504 upstream_timeout` 或 `502 upstream_request_uncertain`。结果未知时按
  `reserve_usd` 结算，日志记 `usage_source=uncertain_reserve`；调用方不要立即重试。
- `per_call` 保持原有语义：执行错误全额退款，包括已写出后的超时。它仍存在
  “上游已扣费但网关只能看到传输错误”的理论风险；本次没有改变其价格或退款规则。

| 上游结果 | `per_unit`（按用量） | `per_call` |
|---|---|---|
| 2xx 且计量头有效 | 按头里的实际数量结算，可为 0 | 按次结算 |
| 2xx 但计量头缺失或非法，有 reserve | 按 `reserve_usd` 结算，`reserve_fallback` | 按次结算 |
| 2xx 后读正文/大小/JSON/success/output 处理失败 | 按有效成本头结算；无有效头按 reserve | 退款 |
| 请求已写出但没拿到响应 | 按 `reserve_usd` 结算，`uncertain_reserve` | 退款 |
| 非 2xx | 全额退款 | 全额退款 |
| 请求未写出（DNS/连接拒绝/TLS 等） | 全额退款 | 全额退款 |

错误响应在已经结算时带 `cost_usd` 和 `usage_source`，不返回上游正文。`cost_usd`
是本次实际应收金额，不是下一次调用的报价。

### `usage_source`

| 值 | 含义 |
|---|---|
| `response_header` | 从成功响应头取得实际用量 |
| `response_body` | 从成功响应正文的 `quantity_path` 取得实际用量 |
| `reserve_fallback` | 2xx 后成本未知，或后处理失败时按 `reserve_usd` 兜底 |
| `uncertain_reserve` | 请求已写出但结果未知，按 `reserve_usd` 结算 |
| `estimated` | 异步提交时按 `estimate_path` 预扣；完成后再按实际用量补差 |
| `call` | `per_call` 按次计费 |

### 结算失败

`SettleBilling` 失败时会最多尝试 3 次。仍失败时不会退款或假装成功：

- 钱包或订阅的预扣仍保留；钱包已扣的预扣额度在用户账上可见，订阅预扣记录也未释放。
- 消费日志写 `status=billing_failed`，`quota` 等于应收额度，并保留
  `upstream_request_id`、`usage_source`。
- 服务端写 `ERROR capability billing settlement failed after retries`，供人工补扣。

### AISA 每日对账

AISA 文档把 `GET https://api.aisa.one/v1/usage` 定义为按 key 读取的账户用量接口；
官方 CLI/API 文档将 `balance` / `usage` 列为免费，接口使用 UTC `1d` bucket，
`charged_micros_usd` 是实际扣费。核实日期：2026-10-29。

- 自动任务在主节点每天 UTC 01:00 后核对前一日，每 30 分钟检查一次；失败会继续重试，
  成功后当日不再跑。
- 上游金额取 `charged_micros_usd` 合计；中转站金额只汇总消费日志
  `other.provider=aisa` 的 `usage_quantity`，不使用加价后的 `quota`。
- 差额阈值为 `max(0.05 USD, 上游金额的 2%)`。超阈值写 WARN，并通过现有
  `NotifyRootUser` 通知 root。
- 手动补跑：

```bash
# 仓库 checkout
python3 ops/reconcile-capability-aisa.py --since 2026-10-29 --until 2026-10-29

# 生产机（部署到 /opt/new-api-pool/reconcile-capability-aisa.py）
python3 /opt/new-api-pool/reconcile-capability-aisa.py \
  --compose-dir /opt/new-api-pool \
  --since 2026-10-29 --until 2026-10-29
```

脚本只在 `new-api` 容器内引用 `$CAPABILITY_AISA_API_KEY`，数据库凭证只留在
Postgres 容器；输出和 `logs/aisa-capability-reconcile.jsonl` 只含日期、上游金额、
日志金额和差额。

## 幂等与并发

表：`capability_idempotencies`

- 唯一索引：`(user_id, capability_id, key_hash)`。
- `key_hash` 是 SHA-256，原始幂等键不落库、不落日志。
- 提交前先 claim；并发只有一个请求拿到 claim。
- 已 claim 且还没有 task id → `409 task_in_progress`。
- 已有 task id → 返回同一个 task，`replayed: true`。
- claim 超过 5 分钟且没有 task id → 视为崩溃残留，可被重新 claim。

## 任务隔离

Capability 任务写入现有 `tasks` 表，`platform = "capability"`。
已修改 `GetAllUnFinishSyncTasks` / `GetTimedOutUnfinishedTasks`，把
`platform = "capability"` 排除出原有视频任务轮询器，避免它把 capability task
误判为 “channel ID: 0” 并置为失败。

## 安全

- 上游 URL 只允许 HTTPS；默认拒绝私网/loopback/link-local。
- 用户提供的 URL 可由 Manifest `url_inputs` 进一步限制域名；匹配规则是精确域名或
  `.<允许域名>` 后缀，因此 `wayfair.com.evil.com` 不会命中 `wayfair.com`。
- 每个 Manifest 有 timeout 和 `max_response_bytes`。
- 不支持任意路径透传；只执行 Manifest 声明的 endpoint。
- API key 不出现在 capability 日志；只记录 capability/provider/status/usage/cost。
- 静态头在 Manifest `Validate()` 阶段拒绝鉴权、Cookie、Host、长度和 hop-by-hop 头。
- 上游非 2xx 只返回稳定错误码，不回传上游原始错误体。

## 线上实测（2026-10-22）

- `GET /v1/capabilities` → 2 个能力，unit=`call`
- `weather-v1` → 成功，返回 10.6°C / 12.8 km/h
- `ssl-analyze-v1`（`www.google.com`，缓存）→ 成功，16 个 endpoints，grade 全 B
- `ssl-analyze-v1`（`www.tokwork.com`，新分析）→ 先 `IN_PROGRESS`，最终成功，4 个 endpoints，grade 全 B
- 同一 idempotency key 重复提交 → 同一个 task id，`replayed: true`
- 既有 `/v1/chat/completions` 回归 → 200，内容 `OK`
- 最近 10 分钟应用日志中完整 key 出现次数：`0`

## 线上实测（2026-10-23，`web-unlock-v1`）

- 新二进制与服务器主机文件 SHA-256 一致，容器内 `/new-api` 相同；容器 healthy。
- `GET /v1/capabilities`（经 ericBot `/v1/gateway/capabilities`）返回 3 个能力：
  `weather-v1`、`ssl-analyze-v1`、`web-unlock-v1`。
- `weather-v1` 合法坐标成功；`input: {}` 返回 400 `invalid_request`，确认必填校验已上线。
- 极短 `MiniMax-M3.1-Flash` 非流式对话 200；模型目录返回 42 个模型。
- 生产环境已有 `CAPABILITY_BRIGHTDATA_ZONE=wayfair_unlocker`，尚无
  `CAPABILITY_BRIGHTDATA_API_KEY`。因此 `web-unlock-v1` 不调用上游，调用前后测试账户余额不变。
- ericBot 网关把未知上游 5xx 统一映射成 502 `gateway_error`，所以客户端看不到内部
  `credential_error`；未扣费语义已在 New API 层核实。
- 真实 Bright Data 成功抓取尚未验证：维护者设置 key 后再测。

## 线上实测（2026-10-28，`web-unlock-v1`）

- key 已设置；zone 改为账户中实际存在的 `web_unlocker2`。
- 同一 key 和 zone 抓非 premium 站点成功（目标 200）。
- 抓 wayfair.com 起初被 Bright Data 拒绝：`x-brd-error-code: premium`。维护者在 zone 设置里
  开启 “Premium domains” 约 3 分钟后，同一请求目标状态 200，返回 Wayfair 首页。
- Premium domains 的单价 Bright Data 未公开，按控制台显示为准。
- 经 `web-unlock-v1` 和 ericBot 网关的端到端调用尚待一次实际使用验证。

## 仍未实现（下一步）

- 多供应商优先级/价格/健康度路由
- 管理后台 Manifest 编辑与校验
- capability 价格目录接入 `/api/pricing`
- SSE 流式 Capability

> 异步 per-unit 和 content 已由单元测试覆盖（本地 mock 上游）；
> 两个免费试点仍是 per-call $0。`web-unlock-v1` 生产实测见第 6 节部署记录。
