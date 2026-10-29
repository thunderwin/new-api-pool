# 第三方 API 接入规则

> 适用范围：New API fork（`new-api-pool/`）接入所有**非大模型**或**非 OpenAI/Anthropic/Gemini 原生协议**的第三方 API。
> 状态：**强制规范**。新增第三方 API 必须先按本文件设计，不允许直接在 controller 里写一次性接口。
>
> 核实日期：2026-10-22。
> 本文件区分三类内容：
> - **现状（已核实）**：当前代码真实支持的能力
> - **规则（强制）**：以后所有接入必须遵守
> - **计划（尚未实现）**：目标架构，需要后续代码落地
>
> **2026-10-22 更新**：Phase 0（同步 JSON）和 Phase 1（异步任务基础链路）已实现并上线，
> 详见 [`docs-pool/07-reference/capability-api.md`](docs-pool/07-reference/capability-api.md)。

---

## 0. 一句话

**不要按“供应商”接 API，要按“能力（Capability）”接 API。**

每个第三方 API 先抽象成一个稳定的能力契约（例如 `ocr-v1`、`weather-v1`），
再由 Manifest 声明它是同步还是异步、返回 JSON 还是文件、怎么鉴权、怎么映射字段、
按什么单位计费。供应商只是这个能力背后的可替换路由。

---

## 1. 现状（已核实）

当前 New API fork 的原生协议入口包括：

| 能力 | 现有入口 |
|---|---|
| OpenAI Chat | `POST /v1/chat/completions` |
| OpenAI Responses | `POST /v1/responses` |
| Anthropic Messages | `POST /v1/messages` |
| Gemini 原生 | Gemini format |
| 图片 | `POST /v1/images/generations` |
| 视频 / 异步任务 | `/v1/videos` + `relay/channel/task/*` |
| Embeddings | `/v1/embeddings` |

已核实：

- `ChannelTypeCustom` 只允许自定义完整 URL（支持 `{model}` 替换），**仍然走 OpenAI 协议**；
  它不是任意 JSON 的通用 REST 透传。
- `relay/channel/task/` 已有 Sora / Kling / Suno / Vidu 等异步适配器，可作为异步 API 的参考实现。
- 计费目前有按 token、按次（`ModelPrice`）、多规格（`ImageSizePrice`）和计费表达式。

**结论：普通 REST API 不能只填一个 base URL 就原生接入；必须经过能力适配层。**

---

## 2. 分类：两个主轴

接入任何 API 前，先填这张表：

| 维度 | 取值 | 影响 |
|---|---|---|
| 执行方式 | `sync` / `async` | 同步直接返回；异步要 submit → poll/webhook → content |
| 返回内容 | `json` / `binary` / `stream` | JSON 走统一 envelope；文件走 content 路由；流式走 SSE |
| 计费单位 | `per_call` / `per_unit` / `per_token` / `tiered` | 决定定价字段和预扣/结算方式 |

### 2.1 同步

适合：

- OCR、翻译、搜索、天气、地址解析、内容审核
- 一次性 JSON 请求 → JSON 响应
- 短耗时（建议 < 60 秒）

### 2.2 异步

适合：

- 视频生成、长文档处理、批量任务、爬取、转码
- 提交后返回 job id，之后轮询或 webhook 回调
- 需要预扣费、失败释放、内容下载

> 不要把异步 API 伪装成 `/v1/chat/completions` 同步返回。那会破坏超时、重试、计费和任务状态语义。

---

## 3. 强制规则（MUST）

### R1. 一个第三方 API = 一个 Capability Manifest

所有接入必须有一个 Manifest（当前实现为 JSON），放在
`capability/manifests/<provider>-<capability>.json`。

禁止：

- 在 controller 里写死某个供应商的 URL / 请求体 / 响应解析
- 为一个 API 复制一套 add/update/list/billing 逻辑
- 把价格硬编码在 Go 代码里

### R2. 公共 ID 表示“能力”，不表示“供应商”

公共 ID 格式：

```
<capability>-v<major>
```

例子：

```
ocr-v1
weather-v1
translate-v1
document-parse-v1
image-upscale-v1
```

供应商 ID 只在内部使用：

```
provider_id: aliyun
capability_id: ocr
route_id: aliyun-ocr-v1
manifest_version: 1.0.0
```

如果两个供应商提供同一个能力，用户仍调用 `ocr-v1`，系统按优先级/价格/健康度路由；
只有质量或 SLA 明显不同时，才暴露 `ocr-fast-v1` / `ocr-accurate-v1` 这类**产品差异** ID。

### R3. 同步和异步必须走不同执行器

- `SyncExecutor`：一次请求 → 一次响应；可流式。
- `AsyncTaskExecutor`：提交 → 持久化 task → 轮询/webhook → content。

两者共享 Manifest、鉴权、计费、日志和 HTTP 客户端，但**不要共享生命周期逻辑**。

### R4. 统一公共契约

同步：

```http
POST /v1/capabilities/{capability}
{
  "input": { ... },
  "options": { ... },
  "idempotency_key": "optional"
}
```

返回：

```json
{
  "id": "req_...",
  "capability": "ocr-v1",
  "output": { "...": "..." },
  "usage": { "unit": "page", "quantity": 1 },
  "cost_usd": 0.01,
  "provider_request_id": "..."
}
```

异步：

```http
POST /v1/tasks
{ "capability": "video-generate-v1", "input": {...}, "idempotency_key": "..." }
→ 202 { "id": "task_...", "status": "queued" }

GET /v1/tasks/{id}
GET /v1/tasks/{id}/content
```

兼容层（可选）：对纯文本能力可以额外提供 `/v1/chat/completions` shim，
但它是兼容层，不是主契约；内部仍走 Capability Manifest。

### R5. 计费只能来自 Manifest

每份 Manifest 必须声明 `billing`：

```yaml
billing:
  type: per_call | per_unit | per_token | tiered
  unit: call | page | second | megabyte | item | token
  price_usd: 0.01
  quantity_path: "$.usage.pages"   # per_unit 必填
  tiers: [...]                      # tiered 必填
```

规则：

- 按次：写 `ModelPrice`，`quota_type=1`
- 按规格：写 `ImageSizePrice`
- 按 token：写 `ModelRatio/CompletionRatio`
- 按页/秒/MB/item：由 Capability 执行器计算 usage，交给统一计费引擎预扣/结算
- **禁止**在 adapter 里直接改用户余额或写死价格

### R6. 幂等

- 同步：可选 `idempotency_key`，建议 24 小时 TTL。
- 异步：必须支持幂等键；重复提交返回同一个 task，不能重复预扣。
- 幂等键入库前做哈希，不把原始键写入日志。
- 并发安全依赖数据库唯一约束/事务，不依赖“先查再插”的应用层判断。

### R7. 密钥与日志

- Manifest 只引用 `secret_ref`，不写真实密钥。
- 上游 key 只存在于服务器环境变量/密钥管理器。
- **禁止**记录完整 API key、Authorization、Cookie、请求/响应原文。
- 只记录：request_id、capability、provider、上游 request id、状态、耗时、usage、cost、错误码。
- 错误响应只返回稳定文案，不返回数据库错误、内部路径、上游原始错误体。
- 含密钥/内容的响应加 `Cache-Control: no-store`。

### R8. 网络与可靠性

每个 Capability 必须声明并执行：

- connect/read/total timeout
- 最大响应体大小
- 重试次数与指数退避（只对幂等请求/明确可重试错误）
- SSRF 防护：域名/IP allowlist，禁止私网地址
- 熔断与并发上限
- 上游 4xx 不盲目重试；429/5xx 才按策略重试

补充（2026-10-29 已实现）：

- 付费上游调用必须脱离调用方连接取消；调用方断开后仍完成上游请求、结算和消费日志。
- Manifest timeout 是整次调用的总预算，必须覆盖全部尝试和退避。
- 只有确定请求未写出的传输错误才能重试；请求一旦写出，传输错误必须视为结果未知，
  不得自动重试。实现使用 `net/http/httptrace` 的 `WroteRequest`。
- 2xx 后处理失败不能默认退款。按用量能力的执行错误必须携带是否收到响应、
  计量字段和“是否可能已扣费”状态，由统一结算层按实际成本或 reserve 结算。
- 结算失败要重试；最终失败时保留预扣，并写 `billing_failed` 消费日志和 ERROR 日志。

### R9. 可观测性

每次调用至少记录：

```
request_id, capability_id, provider_id, route_id,
upstream_request_id, mode(sync/async), status,
latency_ms, usage_unit, usage_quantity, cost_usd, error_code
```

按 capability/provider 建指标：成功率、P50/P95 延迟、成本、错误码分布、重试率。

错误路径同样记录 `upstream_request_id`、`usage_source` 和应收
`quota`/`cost_usd`；日志不得包含 key、请求体或响应体。客户错误响应可在已结算时返回
`cost_usd` 和 `usage_source`，但不返回上游原文。

### R10. 版本与生命周期

- 公共 capability ID 一旦发布**不可改变语义**；破坏性变化升 `v2`。
- Manifest 有 `manifest_version`。
- 供应商下线/切换必须走 canary，不允许直接删旧路由。
- 每个 capability 有 kill switch，可在不改代码的情况下停用。

### R11. 测试是接入的一部分

每个 Capability 必须有：

- 上游 contract test（录制 fixture，不发真实请求也能跑）
- 成功/4xx/5xx/超时/响应过大/字段缺失测试
- 计费精确性测试（与上游价格逐项对齐）
- 幂等与并发测试
- SSRF/密钥脱敏测试

### R12. 上线顺序

`dry-run → shadow（只记录不扣费）→ canary 5% → 全量`。

没有 shadow 数据和计费核对，不允许全量。

---

## 4. 目标架构（原生方案，Phase 0/1 已实现）

新增一个独立的 capability 层，不把逻辑塞进现有 relay controller：

```
capability/
├── manifest/        # Manifest schema + 校验
├── registry/        # 加载 capabilities/**/*.yaml
├── executor/
│   ├── sync.go      # 同步执行器
│   └── async.go     # 异步任务执行器
├── auth/            # bearer/header/query/HMAC/OAuth2 client credentials
├── mapping/         # 请求/响应字段映射（安全 DSL，不执行任意代码）
├── billing/         # PricingSpec -> 预扣/结算
├── httpclient/      # timeout/retry/SSRF/响应大小/熔断
├── store/           # task + idempotency 持久化
└── router.go        # /v1/capabilities, /v1/tasks
```

复用现有 New API：

- Channel：供应商凭证/区域/出口
- Ability：capability 公共 ID 的路由
- Token/User：现有 `sk-` 用户 key 与余额
- Logs：现有请求日志 + capability 维度字段
- Task：现有异步任务表/轮询机制

新增公共路由：

```
POST /v1/capabilities/{capability}   # 同步
GET  /v1/capabilities                # 能力目录
POST /v1/tasks                       # 异步提交
GET  /v1/tasks/{id}                  # 状态
GET  /v1/tasks/{id}/content          # 文件/二进制
```

> 这是一套**原生能力层**；接入新供应商通常只写 Manifest，不写新 controller。
> 只有协议非常特殊的供应商才实现一个 Go `Executor` 插件。

当前已落地的代码：

| 模块 | 路径 |
|---|---|
| Manifest / JSON path / validation | `capability/manifest.go` |
| 嵌入式 Registry | `capability/registry.go` |
| 同步 / 异步 HTTP 执行器 | `capability/executor.go` |
| Controller / billing / logging | `controller/capability.go` |
| 任务幂等表 | `model/capability_idempotency.go` |
| 路由 | `router/capability-router.go` |
| 试点 Manifest | `capability/manifests/*.json` |

2026-10-23 新增的 Manifest 通用能力：

- `request.required`：预扣费前校验必填输入；Manifest 默认值可补足调用方未传字段。
- `request.url_inputs`：限制 HTTPS URL 的允许域名，拒绝 userinfo、IP 字面量和非默认端口。
- `request.env`：把 POST body 字段绑定到服务端环境变量，缺失时返回 `credential_error`。
- `request.consts`：固定字段。GET 放入 query，POST 只放入 body，不混入未声明的 query。
- `response.success`：按业务状态码和正文非空判断成功。
- `response.status_header` / `response.error_header`：识别 HTTP 200 包装的上游错误。

2026-10-29 新增的 Manifest 通用能力：

- `request.body_wrap: "array"`：把组装好的 POST JSON object 作为单元素数组发送。
- `request.headers`：发送 Manifest 声明的静态请求头；拒绝 `Authorization`、`Host`、
  `Content-Length`、Cookie 和 hop-by-hop 头。
- `billing.quantity_header`：从成功响应头读取非负整数/小数用量，与 `quantity_path` 二选一。
- `billing.reserve_usd`：同步 `per_unit` 的固定预扣额；响应缺少合法数量头时按该金额结算，
  消费日志标记 `usage_source=reserve_fallback`。
- `response.error_code_path` + `response.error_map`：按上游业务错误码或 HTTP 状态映射为稳定
  公共错误码和 HTTP 状态；用户响应不回传上游原始 message。
- `/v1/capabilities` 的 `per_unit` 条目额外返回 `billing.max_usd`（等于 `reserve_usd`）。

---

## 5. Manifest 示例

### 5.1 同步 JSON（OCR）

```yaml
id: ocr-v1
provider: aliyun
mode: sync
endpoint: https://ocr.example.com/v1/recognize
method: POST
auth:
  type: bearer
  secret_ref: ALIYUN_OCR_KEY
request:
  content_type: application/json
  template:
    image_url: "$.input.image_url"
    language: "$.input.language"
response:
  content_type: json
  output:
    text: "$.data.text"
    pages: "$.data.pages"
billing:
  type: per_unit
  unit: page
  price_usd: 0.005
  quantity_path: "$.data.pages"
limits:
  timeout: 30s
  max_response_bytes: 5MB
retry:
  attempts: 1
  retry_on: [429, 502, 503, 504]
```

### 5.2 异步任务（视频/长文档）

```yaml
id: video-generate-v1
provider: example-video
mode: async
submit:
  endpoint: https://video.example.com/jobs
  method: POST
  auth: { type: bearer, secret_ref: VIDEO_KEY }
  template:
    prompt: "$.input.prompt"
    duration: "$.input.duration_seconds"
poll:
  endpoint: https://video.example.com/jobs/{job_id}
  interval: 5s
  status_path: "$.status"
  success_values: [succeeded, completed]
  result_path: "$.video_url"
content:
  endpoint: https://video.example.com/jobs/{job_id}/content
billing:
  type: per_unit
  unit: second
  price_usd: 0.01
  quantity_path: "$.input.duration_seconds"
idempotency:
  header: Idempotency-Key
  ttl: 24h
limits:
  timeout: 600s
  max_response_bytes: 1MB
```

---

## 6. 接入新 API 的 Checklist

每接一个第三方 API，必须提供/确认：

1. 同步还是异步？
2. 输入/输出是 JSON、文件还是流？
3. 计费单位（次/页/秒/MB/item/token）和价格来源？
4. 上游鉴权方式（Bearer/header/query/HMAC/OAuth2）？
5. 上游错误码与可重试规则？
6. 用量从响应正文还是响应头读取？是否有单次成本上限和兜底预扣额？
7. 幂等键是否支持？TTL 多久？
8. 最大请求体/响应体/超时？
9. 是否需要固定出口 IP/白名单？
10. 一个成功响应和一个失败响应的完整样例？
11. 是否需要内容下载/签名 URL？
12. 失败时如何判断请求是否已写出、上游是否可能已经扣费；按用量能力是否有 reserve？

缺任何一项，不进入生产。

---

## 7. 分阶段落地（计划）

### Phase 0：骨架与同步 JSON ✅ 已实现（2026-10-22）

- Manifest schema/registry
- `POST /v1/capabilities/{id}` 同步 JSON
- bearer/header auth
- `per_call` / `per_unit` 计费
- contract/golden/计费/幂等测试
- 先接一个真实同步 API 试点（如 OCR 或天气）

### Phase 1：异步任务 ✅ 基础链路已实现（2026-10-22）

- `POST /v1/tasks`、`GET /v1/tasks/{id}`、`GET /v1/tasks/{id}/content`
- 轮询 + webhook
- 预扣/结算/失败释放（含异步 per_unit 预估 + 终态差额结算）
- 异步幂等与并发测试
- 先接一个真实异步 API 试点（视频/长文档）

### Phase 2：文件、流式与多供应商

- binary/content 路由（已实现）
- SSE 流式
- 多供应商优先级/价格/健康度路由
- provider fallback

### Phase 3：运营化

- 管理后台 Manifest 编辑/校验
- 指标看板、告警、canary、kill switch
- 成本对账与自动价格同步

---

## 8. 非目标（明确禁止）

- ❌ 把 New API 改成任意路径的通用反向代理
- ❌ 为每个第三方 API 写一个独立 controller
- ❌ 在 Go 代码里硬编码供应商价格
- ❌ 把 API key 写入 Manifest、日志、错误消息或响应
- ❌ 把异步任务伪装成同步 chat completion
- ❌ 用“先查再插”的应用层逻辑做幂等，绕过数据库唯一约束

---

## 9. 与现有代码的映射

| 新架构概念 | 复用现有 |
|---|---|
| Provider / 凭证 | `channels` |
| Capability 公共 ID | `abilities.model` + `/v1/models` |
| 同步请求 | 新增 `/v1/capabilities/*`（不塞进 chat controller） |
| 异步请求 | `tasks` 表 + task adaptor 框架 |
| 计费 | `ModelPrice` / `ModelRatio` / `ImageSizePrice` / billing expr |
| 用户 key / 余额 | `tokens` / `users.quota` |
| 日志 | `logs` + capability/provider/usage 字段 |

> 当前 `weather-v1`（同步）、`ssl-analyze-v1`（异步）和 `web-unlock-v1`
> （Bright Data Web Unlocker，同步）已接入；前者是免费试点，`web-unlock-v1`
> 按次收费。
> 多供应商路由、后台 Manifest 编辑、SSE 流式仍在 Phase 2/3。
