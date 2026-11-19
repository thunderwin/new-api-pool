# 第三方 Capability API（2026-11-19 更新）

> 状态：**Phase 0 同步 JSON + Phase 1 异步任务基础链路 + Bright Data / AISA / DeepSeek 能力已实现**。
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
>
> 2026-11-04 追加（已实现并离线核实）：新增 DeepSeek Search `web-search-v1`；
> 通用 Manifest 支持数值范围、原生类型常量、对象数组字段投影，以及“声明路径为空时
> 成功且不计费”的 `response.success.empty_ok`。
>
> 2026-11-05 追加（已实现并离线核实）：新增 AISA DataForSEO `keyword-ideas-v1`
> 和 `keyword-volume-v1`；通用 Manifest 支持输入数组长度预检和对象数组的相对字段路径投影。
>
> 2026-11-06 追加（已实现并离线核实）：`keyword-ideas-v1`、`keyword-volume-v1`、
> `serp-google-v1` 改为 DataForSEO 直连，公共 ID、输入、默认值和输出字段不变；
> 通用 Manifest 新增 HTTP Basic 鉴权，并支持从响应正文 `quantity_path` 读取成本，
> 缺失时按 `reserve_usd` 兜底。线上发布状态以本次部署报告为准。
>
> **2026-11-19 追加（已实现并离线核实，线上发布以部署记录为准）**：新增 11 个
> DataForSEO「AI Optimization」直连能力（`ai-answer-*` 四个引擎问答、
> `ai-answer-scrape-*` 两个网页版抓取、`serp-google-ai-mode-v1`、三个
> `llm-mentions-*`、`ai-keyword-volume-v1`），全部只写 Manifest；
> `serp-google-v1` 只做加法，新增可选输入 `load_async_ai_overview`；通用 Manifest
> 新增 `request.enums` 枚举约束。验收命令
> `go test ./capability/... ./controller/...` 与 `go build ./...` 均通过。
>
> 2026-11-19 追加（已实现并离线核实）：新增第一个**原生能力** `geo-site-audit-v1`。
> 它不调用任何第三方 API，由中转站用自己的 Go 代码抓取公开网页并做确定性 GEO 判断；
> Manifest 用 `provider: native` + `native: geo-site-audit` 指向进程内处理器，
> 计费、消费日志、错误 envelope 和 registry（kill switch）仍走统一路径，
> 不新增 controller 或路由。线上发布状态以部署记录为准。
>
> 2026-11-19 追加（已实现并离线核实，**待部署**）：新增 3 个**本站模型联网问答**能力
> `ai-answer-grok-v1` / `ai-answer-kimi-v1` / `ai-answer-doubao-v1`。它们
> `provider: relay`，经本站公开 API 域名 `https://api.tokwork.com/v1/responses`
> 调用自家 OpenAI 兼容接口（模型常量 + `tools: [{"type":"web_search"}]`），
> 凭证是运维账号的 API token，只写在服务端变量 `CAPABILITY_RELAY_SELF_KEY`。
> 通用 Manifest 同时新增 `response.optional_output`。线上发布状态以部署记录为准。

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
| `ranges` | 输入数值约束：`min`、`max`、`integer`。在预扣费和上游调用前校验，不是数字或超出范围返回 `400 invalid_request` |
| `array_lengths` | 输入数组长度约束：`min`、`max`。在预扣费和上游调用前校验；不是数组或超出范围返回 `400 invalid_request` |
| `enums` | 输入枚举约束：`{字段: [允许值...]}`。在预扣费和上游调用前校验；值不在列表中（按字符串比较）返回 `400 invalid_request`，未传且无默认值时跳过。Manifest 加载阶段拒绝空字段名、空列表和非标量允许值 |

### 鉴权

Manifest 的 `auth.type` 支持 `bearer`、`header`、`query`、`basic`。凭证引用只写变量名，
`secret_ref` 和 `password_ref` 必须匹配 `CAPABILITY_[A-Z0-9_]+`。

HTTP Basic 用两个服务端变量，执行器自行拼出
`Authorization: Basic base64(username:password)`：

```json
{
  "auth": {
    "type": "basic",
    "secret_ref": "CAPABILITY_DATAFORSEO_LOGIN",
    "password_ref": "CAPABILITY_DATAFORSEO_PASSWORD"
  }
}
```

任一变量缺失时，在预扣费和上游调用之前返回 `500 credential_error`。用户名和密码
不会进入公共错误、消费日志或响应；调用方不能通过输入覆盖。

`response` 还可声明：

| 字段 | 语义 |
|---|---|
| `success.path` + `success.values` | 上游 JSON 中业务状态必须命中允许值，否则返回稳定错误码（默认 `upstream_target_status`） |
| `success.non_empty` | 指定正文路径必须非空，否则返回 `upstream_empty_body` |
| `success.empty_ok` | 与 `success.non_empty` 配套。路径为空数组、空对象、空字符串或缺失时仍视为成功；输出照常映射，结算为 0 并退回预扣费。未声明时保持原 `upstream_empty_body` 失败语义 |
| `success.failure_code` / `success.empty_code` | 覆盖上述两个稳定错误码 |
| `status_header` / `error_header` | 供应商以 HTTP 200 包装错误时，从响应头读取目标状态和错误标记；错误标记触发失败，重试按目标状态判断 |
| `error_code_path` | 从 JSON 错误体提取上游 code；正文为字符串时忽略 |
| `error_map` | 上游 code 或 HTTP 状态到 `{code, http_status, message, log_code}` 的稳定映射；用户只看到公共文案 |
| `collections` | 对象数组投影：`{path, fields}` 或 `{path, field_paths}`。`fields` 保留平铺字段；`field_paths` 把相对 JSON 路径映射成新字段名，缺失值输出 `null`。输出只保留声明字段；数组为空时输出 `[]` |
| `optional_output` | `response.output` 中允许缺失的键数组。路径不存在时该键输出 `null`，不返回 `502 missing_output_field`；每个键必须在 `response.output` 里声明过，未列出的键保持缺字段即失败 |

业务失败按调用是否可能已经到达上游计费，不再一律退款。错误响应和日志不回传上游正文；
目标状态码可以作为数字记录。

`request.consts` 保留 JSON 原生类型，因此布尔值会以 `true` / `false` 发送，不会变成字符串。

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
| `keyword-ideas-v1` | DataForSEO 直连 | 同步 | 上游正文 `cost` | 种子词扩展相关关键词 |
| `keyword-volume-v1` | DataForSEO 直连 | 同步 | 上游正文 `cost` | 关键词搜索量与 12 个月趋势 |
| `serp-google-v1` | DataForSEO 直连 | 同步 | 上游正文 `cost` | Google 自然搜索 SERP |
| `ai-answer-chatgpt-v1` | DataForSEO 直连 | 同步 | 上游正文 `cost` | ChatGPT 问答答案与引用来源 |
| `ai-answer-claude-v1` | DataForSEO 直连 | 同步 | 上游正文 `cost` | Claude 问答答案与引用来源 |
| `ai-answer-gemini-v1` | DataForSEO 直连 | 同步 | 上游正文 `cost` | Gemini 问答答案与引用来源 |
| `ai-answer-perplexity-v1` | DataForSEO 直连 | 同步 | 上游正文 `cost` | Perplexity 问答答案与引用来源 |
| `ai-answer-scrape-chatgpt-v1` | DataForSEO 直连 | 同步 | 上游正文 `cost` | ChatGPT 网页版答案抓取 |
| `ai-answer-scrape-gemini-v1` | DataForSEO 直连 | 同步 | 上游正文 `cost` | Gemini 网页版答案抓取 |
| `serp-google-ai-mode-v1` | DataForSEO 直连 | 同步 | 上游正文 `cost` | Google AI Mode 答案块与 reference |
| `llm-mentions-search-v1` | DataForSEO 直连 | 同步 | 上游正文 `cost` | AI 答案提及的问题级明细 |
| `llm-mentions-top-domains-v1` | DataForSEO 直连 | 同步 | 上游正文 `cost` | AI 答案引用最多的域名 |
| `llm-mentions-top-pages-v1` | DataForSEO 直连 | 同步 | 上游正文 `cost` | AI 答案引用最多的页面 |
| `ai-keyword-volume-v1` | DataForSEO 直连 | 同步 | 上游正文 `cost` | AI 搜索量与 12 个月趋势 |
| `ai-brand-mentions-v1` | AISA / DataForSEO | 同步 | 上游实际成本 | LLM 品牌提及聚合指标 |
| `page-seo-audit-v1` | AISA / DataForSEO | 同步 | 上游实际成本 | 单页 SEO 审计 |
| `web-search-v1` | DeepSeek Search | 同步 | $0.001 / 次 | 网页搜索；空结果成功且免费 |
| `geo-site-audit-v1` | native（中转站自己抓取） | 同步 | $0 / 次 | 公开网站 GEO（生成式引擎优化）合规体检 |
| `ai-answer-grok-v1` | 本站 `/v1/responses`（`relay`） | 同步 | `total_tokens` × $0.00000025，max $0.06 | grok-4.7 联网问答，答案带 `url_citation` 引用 |
| `ai-answer-kimi-v1` | 本站 `/v1/responses`（`relay`） | 同步 | `total_tokens` × $0.0000008，max $0.03 | kimi-k3 联网问答，来源 URL 写在正文里 |
| `ai-answer-doubao-v1` | 本站 `/v1/responses`（`relay`） | 同步 | `total_tokens` × $0.0000005，max $0.03 | doubao-seed-2.1-pro 问答，模型自己决定是否联网 |

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

### AISA 搜索与 SEO 能力（15 个，2026-11-06）

基址为 `https://api.aisa.one/apis/v1`，Bearer key 来自
`CAPABILITY_AISA_API_KEY`。Manifest 只暴露白名单参数；经 AISA 转售的 DataForSEO 请求按 AISA 契约
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

### DataForSEO 直连关键词能力（2026-11-06）

官方文档和价格核实日期：2026-11-06。

- 基址：`https://api.dataforseo.com/v3`；sandbox 为 `https://sandbox.dataforseo.com/v3`。
- 鉴权：HTTP Basic；用户名来自 `CAPABILITY_DATAFORSEO_LOGIN`，密码来自
  `CAPABILITY_DATAFORSEO_PASSWORD`。调用方不能传入或覆盖凭证。
- 请求仍按 DataForSEO 的单元素数组格式发送；AISA 专用请求头和 cost mode 已移除。
- 成功条件：顶层请求成功且 `$.tasks.0.status_code == 20000`。
- 计费：`per_unit`、`unit=usd`、`price_usd=1`，读取响应体顶层 `$.cost`，按成本价结算。
  缺失 `cost` 时按 `reserve_usd` 结算，日志记 `usage_source=reserve_fallback`。
- 重试：仅 HTTP 429/503 最多再试一次；收到 2xx 后不按内部状态重试。

| ID | 输入与输出契约 | 单次上限 |
|---|---|---|
| `keyword-ideas-v1` | 1–20 个 `keywords`；可选 `location_name`（默认 `United States`）、`language_code`（默认 `en`）、`limit`（默认 50，最大 200）、`offset`（默认 0）；输出 `keywords[]` 七个字段 | $0.04 |
| `keyword-volume-v1` | 1–200 个 `keywords`；可选 `location_name`、`language_code`；`keywords` 默认 1 条；输出 `keywords[]` 七个字段 | $0.04 |
| `serp-google-v1` | `keyword`；可选 `location_code`（默认 2840）、`language_code`（默认 `en`）、`device`（默认 `desktop`）、`depth`（默认 10，官方最大 200）、`os`（默认 `windows`）、`calculate_rectangles`（默认 `false`）；输出 `result` | $0.21 |

价格依据：

- DataForSEO Labs Google 通用端点为每任务 `$0.012` + 每项 `$0.00012`。
  ideas 在 `limit=200` 时约 `$0.036`；volume 在 200 个关键词时约 `$0.036`。
  两者 reserve 取整为 `$0.04`。
- Google Organic Live 为每个最多 10 条结果的 SERP `$0.002`。`depth=200` 为 20 个
  SERP、`calculate_rectangles=true` 再加一个基础价；关键词带搜索操作符时再乘 5，
  上限约 `$0.202`，reserve 取 `$0.21`。
- 官方价格页：
  <https://dataforseo.com/pricing/dataforseo-labs/dataforseo-google-api>、
  <https://dataforseo.com/pricing/serp/google-organic-serp-api>。

DataForSEO 的错误映射覆盖 HTTP 401/402/429/500/503，以及内部状态码 40100（鉴权）、
40200/40210（余额）、40202/40209（限流）、40501/40503/40506（请求参数）、
50000/503xx（上游不可用）和 50401（超时）。公共错误只返回稳定文案，不回传
`status_message`。

`keyword-ideas-v1` 与 `keyword-volume-v1` 使用 `location_name`，默认
`United States` / `en`，可覆盖为 `Germany` / `de`。两个能力只返回 `keywords[]`，
每项固定为 `keyword`、`search_volume`、`cpc`、`competition`、`keyword_difficulty`、
`intent`、`monthly_searches`；上游没有难度、意图或趋势数据时输出 `null`。
`keyword_difficulty` 取自 DataForSEO 的 `keyword_properties.keyword_difficulty`，
为 0–100 的整数（对数刻度，越高越难进前十）。两个端点默认返回该字段，
不需要额外参数也不额外收费（官方文档核实日期：2026-11-11）。

`serp-google-v1` 继续输出完整 DataForSEO `result`。目标 SERP 含 `people_also_ask`
时基础问题项保留在 `output.result[0].items[]`；当前仍未暴露
`people_also_ask_click_depth`，不能请求展开追问层级。

### DataForSEO AI Optimization 能力（11 个，2026-11-19）

官方文档和上游价格核实日期：2026-11-19（`docs.dataforseo.com/v3/ai_optimization/...`、
`docs.dataforseo.com/v3/serp/google/ai_mode/live/advanced/`）。

与上一批直连能力共用同一套契约：基址 `https://api.dataforseo.com/v3`、HTTP Basic
鉴权（`CAPABILITY_DATAFORSEO_LOGIN` / `CAPABILITY_DATAFORSEO_PASSWORD`）、
请求包成单元素数组、成功条件 `$.tasks.0.status_code == 20000`、
`per_unit` + `quantity_path=$.cost` 按成本价结算（缺失 `cost` 时按 `reserve_usd` 兜底）、
仅 HTTP 429/503 最多再试一次、沿用同一套 `error_map`。
输出统一为 `result` = `$.tasks.0.result`，即上游结果数组原样返回，不做字段投影。

| ID | 上游端点（`/v3/...`） | 输入 | 单次上限 |
|---|---|---|---|
| `ai-answer-chatgpt-v1` | `ai_optimization/chat_gpt/llm_responses/live` | **必填** `prompt`；可选 `model`、`web_search`、`web_search_country_iso_code`、`web_search_city`、`system_message`、`temperature` | $0.20 |
| `ai-answer-claude-v1` | `ai_optimization/claude/llm_responses/live` | 同 chatgpt（`temperature` 上限 1） | $0.20 |
| `ai-answer-gemini-v1` | `ai_optimization/gemini/llm_responses/live` | **必填** `prompt`；可选 `model`、`web_search`、`system_message`、`temperature` | $0.10 |
| `ai-answer-perplexity-v1` | `ai_optimization/perplexity/llm_responses/live` | **必填** `prompt`；可选 `model`、`web_search`、`web_search_country_iso_code`、`system_message`、`temperature`（上限 1.9） | $0.10 |
| `ai-answer-scrape-chatgpt-v1` | `ai_optimization/chat_gpt/llm_scraper/live/advanced` | **必填** `keyword`；可选 `location_code`（默认 2840）、`language_code`（默认 `en`） | $0.01 |
| `ai-answer-scrape-gemini-v1` | `ai_optimization/gemini/llm_scraper/live/advanced` | 同上 | $0.01 |
| `serp-google-ai-mode-v1` | `serp/google/ai_mode/live/advanced` | **必填** `keyword`；可选 `location_code`（默认 2840）、`language_code`（默认 `en`）、`device`（`desktop`/`mobile`，默认 `desktop`） | $0.01 |
| `llm-mentions-search-v1` | `ai_optimization/llm_mentions/search/live` | **必填** `target`（1–10 个对象）；可选 `platform`、`location_code`、`language_code`、`limit`（默认 20，最大 100）、`order_by`（≤3 条）、`filters`（≤8 条） | $0.25 |
| `llm-mentions-top-domains-v1` | `ai_optimization/llm_mentions/top_domains/live` | **必填** `target`；可选 `platform`、`location_code`、`language_code`、`items_list_limit`（默认 5，最大 10）、`links_scope` | $0.25 |
| `llm-mentions-top-pages-v1` | `ai_optimization/llm_mentions/top_pages/live` | 同 top_domains | $0.25 |
| `ai-keyword-volume-v1` | `ai_optimization/ai_keyword_data/keywords_search_volume/live` | **必填** `keywords`（1–200 个）；可选 `location_code`（默认 2840）、`language_code`（默认 `en`） | $0.05 |

#### 四个引擎的问答（`ai-answer-*-v1`）

- 输入 `prompt` 映射到上游 `user_prompt`（上游上限 500 字符，超长由上游拒绝）。
- `model` 映射到上游 `model_name`，默认值分别为 `gpt-6-sol`、`claude-sonnet-5-5`、
  `gemini-4-argon`、`sonar-pro`；传其他模型名按原样透传。
- `web_search` 默认 `true`（上游默认 `false`）；关掉后结果通常没有
  `items[].sections[].annotations[]` 和 `search_results[]`。
- `web_search_country_iso_code` 默认 `US`（chat_gpt / claude / perplexity）；
  `web_search_city` 只有 chat_gpt / claude 暴露且无默认值。
- `temperature` 不设默认值（不传就不发，沿用各引擎上游默认）：chatgpt 0–2、claude 0–1、
  gemini 0–2、perplexity 0–1.9，均按上游文档；超出范围在预扣费前返回
  `400 invalid_request`。
- **不暴露** `max_output_tokens`、`top_p`、`force_web_search`、`use_reasoning`、
  `message_chain`。实测 chat_gpt 传 `max_output_tokens` 会返回 `40501`（`Invalid Field`），
  且该系列端点会拒绝未声明的字段，因此也不把这类参数做成默认发送。
- 引擎差异按上游文档落在各自 Manifest：gemini 的任务字段表没有
  `web_search_country_iso_code` / `web_search_city`，因此 gemini 不发送这两个字段；
  perplexity 有 `web_search_country_iso_code`（仅 Sonar 系列生效）但没有
  `web_search_city`，因此只有国家码。
- 输出 `result[0]` 关键字段：`items[]`（`type=message` 时是答案分段）、
  `items[].sections[].annotations[]`（`url`/`title`，chat_gpt 另有
  `start_index`/`end_index`/`text`，perplexity 可能为 `null`）、`search_results[]`、
  `fan_out_queries[]`、`input_tokens`/`output_tokens`/`reasoning_tokens`、`money_spent`。
- 官方文档标注单任务执行时间最长 120 秒，Manifest 总超时设为 `150s`。

#### 网页版抓取（`ai-answer-scrape-*-v1`）

- 模拟消费者网页版（chatgpt.com / Gemini）页面，返回 `markdown`、`sources[]`、
  `search_results[]`、`fan_out_queries[]`、`brand_entities[]`（含 `title`/`category`）
  和 `items[]`。
- 输入只有 `keyword`、`location_code`、`language_code`；`force_web_search` 未暴露。
- 实测单次 $0.004；官方文档标注最长 120 秒，Manifest 总超时 `150s`。

#### Google AI Mode（`serp-google-ai-mode-v1`）

- 返回 Google AI Mode 结果页：`result[0].items[]` 里 `type=ai_overview` 的对象带
  `markdown`、`items[]`、`references[]`（`source`/`domain`/`url`/`title`/`text`）。
- `device` 只接受 `desktop`/`mobile`（`request.enums` 校验），默认 `desktop`。
- 实测单次 $0.004。

#### LLM Mentions（`llm-mentions-*`）

- `target` 是对象数组，**原样透传**，每项是 `{"domain": "..."}` 或
  `{"keyword": "..."}`，可带 `search_filter`、`search_scope`、`match_type`
  （domain 还支持 `include_subdomains`）；Manifest 只用 `array_lengths` 限制 1–10 个
  实体，不解释每个对象的内容，字段名写错由上游 `40501` 拒绝。
- `platform` 只接受 `chat_gpt`/`google`（`request.enums`），默认 `chat_gpt`
  （上游默认是 `google`，这里按调用方偏好改成 chat_gpt）。上游提示 chat_gpt 数据
  仅覆盖美国/英语，通过 google 平台查询其他地区。
- `limit` 上游默认 100、最大 1000；这里收紧为默认 20、最大 100，用于控制单次成本。
- `order_by`（最多 3 条）和 `filters`（最多 8 条）是字符串数组，原样透传，
  Manifest 只校验数组长度。
- `items_list_limit` 默认 5、最大 10；`links_scope` 只接受 `sources`/`search_results`。
- 输出：search 是 `result[0].items[]`（`question`/`answer`/`sources[]`/
  `search_results[]`/`ai_search_volume`/`monthly_searches[]`）；top_* 是
  `result[0].total`（按 location/language/platform/sources_domain 等维度的聚合）
  加 `result[0].items[]`（每项 `key` 加同样的维度数组）。
- 实测单次约 $0.10；reserve 取 $0.25。

#### AI 关键词搜索量（`ai-keyword-volume-v1`）

- 上游允许 1,000 个关键词；这里收紧为 1–200，默认 `location_code=2840`、
  `language_code=en`。
- 输出 `result[0].items[]`：`keyword`、`ai_search_volume`、`ai_monthly_searches[]`
  （`year`/`month`/`ai_search_volume`）。
- 实测 2 个关键词约 $0.01；reserve 取 $0.05。

#### `serp-google-v1` 的加法（不改已有语义）

新增可选输入 `load_async_ai_overview`（布尔）。**不声明默认值**，因此不传时字段
不进入请求体，默认请求体与 2026-11-06 版本完全一致（离线测试用改造前的
`serp-google-request.json` 逐字比对）；传 `true` 时上游把异步加载的 `ai_overview`
item（含 `references[]`）也一起返回，额外计 $0.002，仍在 $0.21 reserve 内；
传 `false` 时按原样发送 `false`（只读缓存的 `ai_overview`）。

#### 实测成本与 reserve 依据

用运营方账号逐个端点实测（2026-11-19）的单次成本：chat_gpt `gpt-6-sol` 联网
$0.036、claude `claude-sonnet-5-5` 联网 $0.046、gemini `gemini-4-argon` $0.021、
perplexity `sonar-pro` $0.012；llm_scraper $0.004；ai_mode $0.004；llm_mentions
search/top_domains/top_pages 约 $0.10；ai_keyword_data 2 个词约 $0.01。
reserve 在这些实测值上取整并留余量（0.20 / 0.10 / 0.01 / 0.25 / 0.05），
理由是联网搜索的 `money_spent` 会随引用源和 token 数变动，而 `$.cost` 是上游最终账单。

### `web-search-v1`（DeepSeek Search，2026-11-04）

官方文档核实日期：2026-11-04。

- 入口：`POST https://api.deepseek.com/v1/tools/search`
- 鉴权：Bearer key，服务端变量 `CAPABILITY_DEEPSEEK_API_KEY`，用户不能传入或覆盖
- 输入：必填 `query`；可选 `count`，默认 8，范围 1–20 的整数；
  可选 `ori_question`、`localization`
- 上游常量：`need_content: false`。官方文档确认其默认值就是 `false`；
  此时 `content` 是空字符串，而 `summary` 是必返字段，通常 600–800 字
- 输出：`{ "results": [...] }`；每项只保留 `title`、`url`、`snippet`、`summary`、
  `site_name`、`date`，不回传 `content`、`site_icon`、`authority`、`rerank_score`
- 价格：`per_call`，$0.001 / 次
- 空结果：HTTP 200 且 `results: []` 返回成功，`cost_usd = 0`，预扣费全额退回
- 超时/大小：总超时 30 秒，最大响应 2 MiB
- 重试：HTTP 408/429/500/502/503/504 最多再试 1 次；非 2xx 不计费
- 错误映射：

| 上游 | 公共响应 | 日志区分 |
|---|---|---|
| 400 / 413 | `400 invalid_request` | `upstream_invalid_request` / `upstream_request_too_large` |
| 401 | `503 upstream_unavailable` | `upstream_authentication` |
| 402 | `503 upstream_unavailable` | `upstream_balance_unavailable` |
| 408 / 504 | `504 upstream_timeout` | `upstream_read_timeout` / `upstream_downstream_timeout` |
| 429 | `429 upstream_rate_limited` | `upstream_rate_limited` |
| 500 / 502 / 503 | `503 upstream_unavailable` | provider failure / unavailable |

官方来源：

- <https://api-docs.deepseek.com/guides/search.md>
- <https://api-docs.deepseek.com/api/search.md>
- <https://api-docs.deepseek.com/quick_start/pricing.md>

DeepSeek 官方当前写的是每 1,000 次成功 Search 收 **$0.75**（约 ¥5），
账户级 Search 限速 100 QPS；只有 HTTP 200 且 `results` 非空才收费。
维护者 2026-11-04 实测服务器到 DeepSeek TTFB 约 0.3 秒，单次搜索 260–600ms。

### 本站模型联网问答（`ai-answer-grok-v1` / `ai-answer-kimi-v1` / `ai-answer-doubao-v1`，2026-11-19）

用途：AI 可见性要查一个买家问题在各家 AI 回答里有没有提到客户品牌。DataForSEO 已覆盖
ChatGPT / Claude / Gemini / Perplexity；这三个能力补上中转站自己的 Grok / Kimi / 豆包。
实测手段：经本站 OpenAI 兼容 `POST /v1/responses`，`tools: [{"type":"web_search"}]`。

| 能力 | 模型常量 | 单次实测 | 每 token 单价 | reserve |
|---|---|---|---|---|
| `ai-answer-grok-v1` | `grok-4.7` | 36–73 秒，约 6 万 token，$0.013–0.019 | $0.00000025 | $0.06 |
| `ai-answer-kimi-v1` | `kimi-k3` | 20–33 秒，约 1 万 token，约 $0.0075 | $0.0000008 | $0.03 |
| `ai-answer-doubao-v1` | `doubao-seed-2.1-pro` | 18–50 秒，约 $0.003 | $0.0000005 | $0.03 |

（实测日期 2026-11-19，使用运营方账号；豆包只在模型认为需要时联网，品牌推荐类问题常常不搜。）

- 入口：`POST https://api.tokwork.com/v1/responses`。这是本站自己的公开 API 域名，
  仍走现有出站 SSRF 防护（只允许 HTTPS、拒绝私网/loopback），
  **不需要也不应该**打开 `CAPABILITY_ALLOW_PRIVATE_URLS`，不走回环地址。
- 鉴权：Bearer，服务端变量 `CAPABILITY_RELAY_SELF_KEY`（一个本站运维账号的 API token）。
  缺失时在预扣费前返回 `500 credential_error`，不调用上游、不扣费。
  调用方不能通过输入覆盖凭证；值由运维用 `set-capability-secret.sh` 单独设置。
- 请求体：`model` 和 `tools: [{"type":"web_search"}]` 是 Manifest 常量；
  `input` = 必填 `prompt`；`instructions` = 可选 `system_message`（不传就不发送）；
  `max_output_tokens` 默认 1500，范围 200–4000 的整数（不传就发默认值）。
- 成功判定：`$.status` 为 `completed` 或 `incomplete`。`failed` 映射为
  `503 upstream_unavailable`；其他未识别的状态返回 `502 upstream_response_failed`。
- 输出：`result` = 整个 `$.output` 数组（`message` 分段、`web_search_call`、
  `url_citation` 注释都在里面），`output_text`、`usage`、`model` 是便捷字段，
  用 `response.optional_output` 声明：路径不存在时输出 `null`，不影响成功。
  中转站非流式透传上游正文，所以 `output_text` 是否存在取决于具体上游通道。
- 三个模型的语义差异（写在各 Manifest 的 `description` 里）：
  - Grok：稳定联网，`output` 里既有 `web_search_call` 也有 message 的 `url_citation` 注释。
  - Kimi：会联网（有 `web_search_call`），但没有 `url_citation` 注释，**来源 URL 写在正文里**。
  - 豆包：模型自己决定是否联网；搜了才有 `url_citation`，`tool_choice: "required"` 不生效。
- 计费：`per_unit`、`unit=token`、`quantity_path=$.usage.total_tokens`，
  单价按上表；缺失 `total_tokens` 时按 `reserve_usd` 结算并记 `usage_source=reserve_fallback`。
- 超时 `115s`（同步与网关上限 120 秒之内），最大响应 8 MiB，`retry.attempts=1`（不重试）。
- 不接的模型：Claude（上游把服务端工具变成客户端 `tool_use`）、Gemini（唯一模型已下架）、
  GLM、DeepSeek、longcat、MiniMax（约 49 万 token 且无引用）、hy4（已下架）；
  GPT 已有 DataForSEO ChatGPT，不重复接。

### `geo-site-audit-v1`（原生能力，2026-11-19）

第一个不调用第三方 API 的 Capability：中转站用自己的 Go 代码抓取公开网页，按固定规则表
做确定性 GEO 判断。它运行在海外服务器上，因此「AI 爬虫视角的可达性」比在中国客户端上测更准。

Manifest 写 `provider: native` + `native: geo-site-audit`，`mode: sync`，不声明
`endpoint` / `auth` / `response` / `submit` / `poll` / `content`。`ExecuteSync` 在通用
Manifest 校验之后按 `native` 字段分发到进程内处理器；预扣/结算、消费日志、错误 envelope、
幂等键和 registry（kill switch）全部复用统一路径。

输入：

| 字段 | 必填 | 说明 |
|---|---|---|
| `url` | 是 | 公开网站 URL。只接受 `https`、443 端口、无 userinfo，不允许 IP 字面量 |
| `pages` | 否 | 同站点额外页面 URL 数组，最多 10 个；跨注册域返回 `400 invalid_request` |
| `max_pages` | 否 | 抓取页面总数（含首页），默认 8，范围 1–15 的整数 |
| `training_policy` | 否 | `allow` / `block` / `any`，默认 `allow`，表示客户对训练类爬虫的既定政策 |

输出：

| 字段 | 说明 |
|---|---|
| `site` | 输入站点的注册域（eTLD+1） |
| `fetched_at` | 本次抓取时间（RFC 3339） |
| `training_policy` | 回显输入 |
| `summary` | `official` / `research` / `industry` 三组各自的 `pass` / `warn` / `fail` / `info` 计数 |
| `robots` | `found`；`content_signals[]`（robots.txt 里 `Content-Signal:` 的原始值）；`sitemaps[]`（`Sitemap:` 声明的 URL） |
| `pages[]` | `url`、`status`、`words`、`title`、`h1_count`、`schema_types[]`；抓取失败的页面也会出现并带 `error` |
| `checks[]` | `id`、`category`、`source`、`scope`、`status`、`evidence`（≤300 字符）、`reference`；页面级检查额外带 `url` |
| `notes[]` | 方法说明与跳过原因（不抓 JS、仅 UA 层、跨注册域 sitemap 跳过等） |

来源分级（`checks[].source`）：

- `official`：搜索/AI 厂商官方文档、标准或 Google 结构化数据文档。**只有这一级进入合规结论。**
- `research`：公开研究，例如 GEO 论文 <https://arxiv.org/abs/2311.09735>。只给建议。
- `industry`：行业经验（答案先行、`llms.txt`、实体一致性）。只给建议。

检查项（`status` 为 `pass` / `warn` / `fail` / `info`）：

| 检查 ID | 类别 | 来源 | 说明 |
|---|---|---|---|
| `robots_ai_search_access` | crawl | official | 9 个 AI 搜索/检索爬虫（OAI-SearchBot、ChatGPT-User、PerplexityBot、Perplexity-User、Claude-SearchBot、Claude-User、Googlebot、Bingbot、Applebot）在首页和每个被测路径是否被 robots.txt 放行；被禁 `fail` |
| `robots_training_policy` | crawl | official | GPTBot、ClaudeBot、Google-Extended、Applebot-Extended、CCBot、meta-externalagent 与 `training_policy` 是否一致；不一致 `warn`，`any` 为 `info` |
| `robots_deprecated_tokens` | crawl | official | 出现已废弃的 `anthropic-ai` / `Claude-Web` token → `warn`（无效规则） |
| `ai_ua_reachability` | crawl | official | 8 个 AI UA 与浏览器 UA 各 GET 首页一次；浏览器 200 而 AI UA 被拒/挑战 → `fail`。只代表 User-Agent 层 |
| `sitemap_discovery` | crawl | official | robots `Sitemap:` 或 `/sitemap.xml`；不存在 → `warn`；lastmod 覆盖率写入证据；所有 lastmod 完全相同或最新 >180 天 → `warn` |
| `llms_txt` | crawl | industry | `/llms.txt` 是否存在；始终 `info`，并注明主流 AI 搜索不读取它 |
| `entity_consistency` | entity | industry | 各页 Organization/LocalBusiness 的 name、telephone、address 是否一致 |
| `page_http_status` | crawl | official | 页面 HTTP 非 200 或抓取失败 → `fail` |
| `page_indexability` | index | official | meta robots 或 X-Robots-Tag（含针对 googlebot/bingbot 的）里的 `noindex` → `fail` |
| `page_snippet_controls` | index | official | `nosnippet`、`max-snippet` 小于 50（含 0）→ `warn`；`data-nosnippet` 数量 → `info` |
| `page_canonical` | index | official | canonical 缺失 → `warn`；指向其它 URL → `info` 并写明目标 |
| `page_server_rendered_text` | render | official | 去掉 script/style/noscript 后的可见词数；<150 → `warn`；几乎为空且有 SPA 挂载点 → `fail` |
| `page_jsonld_parse` | schema | official | JSON-LD 解析失败 → `fail`；没有 JSON-LD → `info` |
| `page_jsonld_organization` | schema | official | Organization/LocalBusiness 缺 `name`/`url`/`logo`/`sameAs` → `warn` |
| `page_jsonld_product` | schema | official | Product 缺 `offers` → `warn`；Product `name` 不在可见正文 → `fail` |
| `page_jsonld_article` | schema | official | Article/BlogPosting/NewsArticle 缺 `datePublished`/`dateModified`/`author` → `warn` |
| `page_jsonld_faq` | schema | official | FAQPage 问题文本不在可见正文 → `fail`（Google 要求结构化数据与可见内容一致） |
| `page_title` | structure | official | title 缺失，或与其它被测页面重复 → `warn` |
| `page_meta_description` | structure | official | meta description 缺失 → `warn` |
| `page_h1` | structure | official | H1 不是恰好 1 个 → `warn` |
| `page_lang` | structure | official | `<html lang>` 缺失 → `warn` |
| `page_image_alt` | structure | official | 图片 alt 覆盖率 < 80% → `warn` |
| `page_article_date` | freshness | official | 文章类页面（Article/BlogPosting/NewsArticle，或路径含 `/blog/`、`/news/`、`/posts/`）没有可见日期 → `warn` |
| `answer_first_paragraph` | citability | industry | H1 后第一段 40–120 词 → `pass`，否则 `warn` |
| `content_lists_tables` | citability | industry | 列表与表格数量，`info` |
| `stats_density` | citability | research | 每千词中含 `%` 或带单位数字的个数，`info` |
| `quotes_in_content` | citability | research | blockquote 或长引语数量，`info` |
| `external_citations` | citability | research | 指向外部站点的链接数，`info` |
| `keyword_stuffing` | citability | research | 单个非停用词占比 > 4% → `warn` |

抓取流程与限制：

- `/robots.txt` 按 RFC 9309 解析：分组、最长匹配、Allow 优先；同时读取 Cloudflare 风格的
  `Content-Signal:` 行和 `Sitemap:` 行。
- 首页 → sitemap（robots 声明的优先，否则 `/sitemap.xml`；sitemap index 只展开一层，
  最多 3 个子 sitemap）→ 从 sitemap 按路径前缀分散挑页面，加上输入 `pages`，
  总数不超过 `max_pages`。
- 单请求超时 15 秒；整体受 Manifest `90s` 约束；并发上限 4；只发 GET；不带 cookie；
  默认 UA `Mozilla/5.0 (compatible; GEO-Audit/1.0)`。
- 单页响应上限 3 MiB；Manifest `max_response_bytes` 为 8 MiB。
- 不执行 JavaScript，不做 JS 渲染；`llms.txt` 只探测存在性，不计分。
- 日志只记录 capability、状态、耗时等元数据，不记录页面内容。

安全边界：

- 每次连接（包括每一跳重定向）都在拨号时校验解析出的 IP：拒绝 loopback / private /
  link-local / unspecified / multicast / CGNAT（100.64.0.0/10）/ IPv6 ULA，防 DNS rebinding。
- 重定向最多 5 跳、只能跳 `https`；所有抓取的 URL 必须与输入站点同一注册域
  （apex 及其子域）；sitemap 里跨注册域的 URL 跳过。
- 抓取失败只影响对应页面；只有首页抓不到才让整次调用失败。

错误码：

| 错误码 | HTTP | 场景 |
|---|---|---|
| `invalid_request` | 400 | `url` / `pages` / `max_pages` / `training_policy` 不合法，预扣费之前返回 |
| `site_not_allowed` | 400 | 站点解析到非公网地址、重定向到 http 或跨注册域、超过 5 跳 |
| `site_unreachable` | 502 | 首页 DNS / 连接 / TLS 失败 |
| `site_timeout` | 504 | 首页在总截止时间内没有响应 |
| `site_response_too_large` | 502 | 首页超过单页 3 MiB 上限 |

单个页面抓取失败时整次调用仍然成功，失败页面出现在 `pages[]` 并带 `error`
（`blocked` / `unreachable` / `timeout` / `response_too_large` / `http_<状态码>`），
对应 `page_http_status` 为 `fail`。

## 计费

- Manifest 的 `billing.price_usd` 是唯一价格来源，不读硬编码。
- `per_call`：提交/执行前预扣；同步成功结算，失败退款。
- `per_unit`：同步支持按上游返回的 `quantity_path` 或 `quantity_header` 结算；
  两者只能声明一个。响应头数字必须是非负整数或小数。
- 同步 `per_unit` 可声明 `reserve_usd`：调用前固定预扣该金额；成功但数量头或
  `quantity_path` 缺失/非法时，
  按 `reserve_usd` 结算，消费日志的 `usage_source=reserve_fallback`，不返回 502。
  数量头为 `0` 时按 0 结算。失败仍全额退款。
- 异步 `per_unit`：manifest 必须声明 `estimate_path`，提交时按预估量预扣；
  任务完成时按 `quantity_path` 读取实际用量，差额补扣/退还。
- 价格为 0 的 capability 不创建 billing session，零余额用户也能调用。
- 声明 `response.success.empty_ok` 的同步能力在指定路径为空时仍返回 200，
  `usage.quantity = 0`、`cost_usd = 0`，预扣费通过结算差额退回；
  消费日志记 `usage_source=empty_response` 和 0 quota。
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

### DataForSEO 按响应体成本计价

DataForSEO 14 个直连能力（3 个关键词/SERP + 11 个 AI Optimization）的
`billing.quantity_path` 都是 `$.cost`，
`billing.unit=usd`、`billing.price_usd=1`。返回值就是该次任务实际花费的美元数，
平台按 1 倍成本价计费。成本缺失时使用 Manifest 的 `reserve_usd`；上限见上面直连能力表。

### 本站 `relay` 问答按 token 计价（2026-11-19）

3 个本站模型问答能力（`ai-answer-grok-v1` / `ai-answer-kimi-v1` / `ai-answer-doubao-v1`）
的 `billing.type=per_unit`、`billing.unit=token`、`billing.quantity_path=$.usage.total_tokens`，
`billing.price_usd` 是每 token 美元（可直接写成 `2.5e-7`、`8e-7`、`5e-7`）。
实际 quota 仍是 `price_usd × quantity × QuotaPerUnit × GroupRatio`；`QuotaPerUnit`
为 500000，即 1 quota = $0.000002，三个单价分别对应约 8 / 2.5 / 4 个 token 一个 quota
单位（结果是整数 quota，四舍五入），不会把这些单价归零。
缺 `total_tokens` 时按 `reserve_usd` 结算并记 `usage_source=reserve_fallback`。

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
| `empty_response` | 声明 `empty_ok` 的路径为空；本次按 0 结算 |

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
- `CAPABILITY_RELAY_SELF_KEY` 只作为 Bearer 头发往 Manifest 里写死的
  `https://api.tokwork.com/v1/responses`；调用方不能传参覆盖，响应和日志只出现稳定错误码。
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

## 线上实测（2026-11-04，`web-search-v1`）

- 发布提交 `6d41c905`（后续秘密脚本兼容修复为 `86319b28`）；二进制 SHA-256
  `092ca2eaf2a4d4255668427e8f22c11accc6f338a72d1652cbddd2ff6fafdf69`，
  服务器主机与容器内 `/new-api` 一致。
- 公开 `/api/status` 返回 200；`new-api-app` 为 healthy；近 10 分钟启动错误摘要为 0。
- `.env` 中 `CAPABILITY_DEEPSEEK_API_KEY` 的名称计数为 1，容器内检查结果为“已设置”，
  未读取或输出值。
- 后端容器使用自身 `$CAPABILITY_DEEPSEEK_API_KEY` 直连 DeepSeek Search：
  `query: "OpenAI API pricing"`、`count: 3` → HTTP 200，结果 3 条。该冒烟不经过中转站计费。
- 未带 token 调 `POST /v1/capabilities/web-search-v1` 返回 401；未带 token 调
  `GET /v1/capabilities` 同样返回 401。按本次约束没有查找任何用户 token，
  因此带鉴权的目录读取和真实用户端到端扣费调用留给维护者从客户端完成。
- 同一二进制包含 `web-search-v1` 嵌入 Manifest；离线测试确认嵌入 Manifest 总数 20，
  且 `GET /v1/capabilities` 的目录生成路径包含该能力及其 `per_call` 定价。

## 仍未实现（下一步）

- 多供应商优先级/价格/健康度路由
- 管理后台 Manifest 编辑与校验
- capability 价格目录接入 `/api/pricing`
- SSE 流式 Capability

> 异步 per-unit 和 content 已由单元测试覆盖（本地 mock 上游）；
> 两个免费试点仍是 per-call $0。`web-unlock-v1` 生产实测见第 6 节部署记录。
