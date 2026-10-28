# 第三方 Capability API（2026-10-23 更新）

> 状态：**Phase 0 同步 JSON + Phase 1 异步任务基础链路 + `web-unlock-v1` 已实现**。
> 规则见仓库根目录 [`RULES.md`](../../RULES.md)。
>
> 2026-10-23 追加：异步 per-unit（估算预扣 + 终态差额结算）和
> `/v1/tasks/{id}/content` 文件下载已实现并通过单测；线上 smoke 通过。
>
> 2026-10-23 追加：同步执行器支持必填输入、URL allowlist、POST body
> 常量/环境变量覆盖，以及业务成功条件；新增 Bright Data Web Unlocker
> `web-unlock-v1`。部署记录见
> [`../03-deployment/tokwork-nbg1.md`](../03-deployment/tokwork-nbg1.md)。

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
| `required` | 必填输入字段名数组。字段缺失、`null` 或空字符串时，在预扣费和上游请求之前返回 `400 invalid_request` |
| `url_inputs` | 映射输入字段到 `allowed_hosts`。只接受 HTTPS；主机必须精确匹配列表项或以 `.<域名>` 结尾；拒绝 userinfo、IP 字面量和非 443 端口 |
| `consts` | 同时作用于 query 与 POST body 的常量；同名用户输入/默认值会被常量覆盖 |
| `env` | POST body 字段到服务端环境变量名的映射；同名用户输入和常量会被环境变量值覆盖。变量缺失返回 `credential_error` |

`response` 还可声明：

| 字段 | 语义 |
|---|---|
| `success.path` + `success.values` | 上游 JSON 中业务状态必须命中允许值，否则返回稳定错误码（默认 `upstream_target_status`） |
| `success.non_empty` | 指定正文路径必须非空，否则返回 `upstream_empty_body` |
| `success.failure_code` / `success.empty_code` | 覆盖上述两个稳定错误码 |
| `status_header` / `error_header` | 供应商以 HTTP 200 包装错误时，从响应头读取目标状态和错误标记；错误标记触发失败，重试按目标状态判断 |

业务失败沿用统一退款路径。错误响应和日志不回传上游正文；目标状态码可以作为数字记录。

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

## 计费

- Manifest 的 `billing.price_usd` 是唯一价格来源，不读硬编码。
- `per_call`：提交/执行前预扣；同步成功结算，失败退款。
- `per_unit`：同步支持按上游返回的 `quantity_path` 结算（如页数）。
- 异步 `per_unit`：manifest 必须声明 `estimate_path`，提交时按预估量预扣；
  任务完成时按 `quantity_path` 读取实际用量，差额补扣/退还。
- 价格为 0 的 capability 不创建 billing session，零余额用户也能调用。

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
