# TokWork API 调用指南（对外）

> **读者**：调用 TokWork 网关的开发者 / 集成方。本文可以直接发给客户。
> **内容**：怎么拿 Key、怎么鉴权、怎么用 OpenAI / Anthropic / Gemini 三种格式调用大模型、
> 怎么调用第三方 API（Capability）、怎么计费、出错怎么办。
> **最后核实**：2026-10-23。维护者请先看文末「维护者备注」。

---

## 0. 一页速览

| 项 | 值 |
|---|---|
| Base URL | `https://api.tokwork.com/v1` |
| 鉴权 | `Authorization: Bearer sk-xxxxxxxx`（各协议的其它写法见 §2） |
| 获取 Key / 充值 / 查账单 | <https://www.tokwork.com/console> |
| 当前可调用的模型 | `GET /v1/models`（以它为准） |
| 公开价目（免登录） | `GET https://api.tokwork.com/api/pricing` |
| 支持的请求格式 | OpenAI Chat Completions · OpenAI Responses · Anthropic Messages · Gemini generateContent · OpenAI Images |
| 第三方 API | `GET /v1/capabilities`、`POST /v1/capabilities/{id}`、`POST /v1/tasks` |
| 计价货币 | 美元（USD），从账户余额实时扣减 |

**最快上手**：已经在用 OpenAI SDK 的项目，只改两处 —— `base_url` 换成 `https://api.tokwork.com/v1`，
`api_key` 换成你的 TokWork Key。

---

## 1. 获取 API Key

1. 打开 <https://www.tokwork.com/register> 注册，登录后进入控制台 <https://www.tokwork.com/console>。
2. 在 **API Key** 页新建一个 Key，复制保存（形如 `sk-` 开头的一串字符）。
3. 账户需要有**正余额**才能调用付费模型；余额耗尽后请求会被拒绝（`403 insufficient_user_quota`），
   不会产生欠费。
4. 每次调用的模型、token 数、扣费金额、耗时、request id 都能在控制台 **调用记录** 页查到。

**安全建议**

- Key 等同于密码，拿到 Key 的人就能花你的余额。**不要写进前端代码、移动端安装包或公开仓库。**
- 每个应用 / 环境单独建一个 Key，泄露时只需停用那一个。
- Key 末尾**不要追加任何后缀**。`sk-xxx-<数字>` 这种写法是网关保留的管理功能，
  普通账户使用会返回 `403`。

---

## 2. 鉴权

所有 `/v1/*` 接口都用同一个 Key，按你使用的协议选一种传法即可：

| 协议 / 场景 | 传 Key 的方式 | 适用路径 |
|---|---|---|
| **OpenAI 格式（推荐）** | `Authorization: Bearer sk-xxx` | 所有 `/v1/*` |
| Anthropic 格式 | `x-api-key: sk-xxx`（同时带 `anthropic-version`） | `/v1/messages`、`/v1/models` |
| Gemini 格式 | `x-goog-api-key: sk-xxx` 或 URL 参数 `?key=sk-xxx` | `/v1beta/models/*` |

- `Bearer` 前缀可写可不写；`sk-` 前缀也可写可不写。
- 鉴权失败统一返回 `401`：

```json
{
  "error": {
    "code": "",
    "message": "Invalid token (request id: 2026102301432410397676...)",
    "type": "new_api_error"
  }
}
```

- 每个响应都带响应头 `X-Oneapi-Request-Id`。**联系技术支持时请附上这个 id。**

---

## 3. 查询可用模型与价格

### 3.1 你的 Key 能调哪些模型

```bash
curl https://api.tokwork.com/v1/models \
  -H "Authorization: Bearer $TOKWORK_API_KEY"
```

返回 OpenAI 标准的模型列表，`data[].id` 就是请求里要填的 `model`。
**这个列表是唯一权威来源** —— 模型会随上游上下架，请不要在代码里写死一份清单而不去核对。

### 3.2 公开价目

```bash
curl https://api.tokwork.com/api/pricing
```

免登录。按 token 计费的模型看 `model_ratio` / `completion_ratio` / `cache_ratio`，
按次计费的模型看 `model_price`。换算方式见 §6。

---

## 4. 调用大模型

网关对外提供四种请求格式。**同一个模型可以用任意一种格式调用**，网关负责协议转换；
你的项目原来用哪家的 SDK，就继续用哪家的。

> 推荐优先使用 **OpenAI Chat Completions** 格式：它是网关的原生格式，不经过转换，
> 兼容性最好。

### 4.1 OpenAI Chat Completions —— `POST /v1/chat/completions`

**curl**

```bash
curl https://api.tokwork.com/v1/chat/completions \
  -H "Authorization: Bearer $TOKWORK_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-5",
    "messages": [
      {"role": "system", "content": "你是一个简洁的助手。"},
      {"role": "user", "content": "用一句话介绍你自己"}
    ]
  }'
```

**Python（`openai` 官方 SDK）**

```python
from openai import OpenAI

client = OpenAI(
    base_url="https://api.tokwork.com/v1",
    api_key="sk-xxx",              # 你的 TokWork Key
)

resp = client.chat.completions.create(
    model="gpt-6-sol",
    messages=[{"role": "user", "content": "你好"}],
)
print(resp.choices[0].message.content)
print(resp.usage)                  # prompt_tokens / completion_tokens，即计费依据
```

**Node.js（`openai` 官方 SDK）**

```js
import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "https://api.tokwork.com/v1",
  apiKey: process.env.TOKWORK_API_KEY,
});

const resp = await client.chat.completions.create({
  model: "deepseek-v4-pro",
  messages: [{ role: "user", content: "你好" }],
});
console.log(resp.choices[0].message.content);
```

**流式输出**：加 `"stream": true`，返回标准 SSE（`data: {...}` 逐块推送，以 `data: [DONE]` 结束）。
需要在流的最后一块拿到 token 用量时，再加 `"stream_options": {"include_usage": true}`。

```python
stream = client.chat.completions.create(
    model="gpt-6-sol",
    messages=[{"role": "user", "content": "写一首短诗"}],
    stream=True,
    stream_options={"include_usage": True},
)
for chunk in stream:
    if chunk.choices and chunk.choices[0].delta.content:
        print(chunk.choices[0].delta.content, end="", flush=True)
```

> 长输出建议一律用流式：一方面首字更快，另一方面能避免客户端 / 中间代理的读超时。

### 4.2 OpenAI Responses —— `POST /v1/responses`

```bash
curl https://api.tokwork.com/v1/responses \
  -H "Authorization: Bearer $TOKWORK_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-6-sol",
    "input": "用三点概括 HTTP/3 的优势"
  }'
```

Python：`client.responses.create(model="gpt-6-sol", input="...")`，`client` 的配置同 §4.1。

### 4.3 Anthropic Messages —— `POST /v1/messages`

适合已经在用 Anthropic SDK / Claude 生态工具的项目。

**curl**

```bash
curl https://api.tokwork.com/v1/messages \
  -H "x-api-key: $TOKWORK_API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-5",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

**Python（`anthropic` 官方 SDK）** —— 注意 `base_url` **不带** `/v1`，SDK 会自己拼：

```python
import anthropic

client = anthropic.Anthropic(
    base_url="https://api.tokwork.com",
    api_key="sk-xxx",
)

msg = client.messages.create(
    model="claude-sonnet-5",
    max_tokens=1024,
    messages=[{"role": "user", "content": "你好"}],
)
print(msg.content[0].text)
```

流式：`"stream": true`，返回 Anthropic 风格的 SSE 事件（`message_start` / `content_block_delta` / …）。

### 4.4 Gemini 原生格式 —— `POST /v1beta/models/{model}:generateContent`

```bash
curl "https://api.tokwork.com/v1beta/models/gemini-4-argon-contributor:generateContent" \
  -H "x-goog-api-key: $TOKWORK_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "contents": [{"role": "user", "parts": [{"text": "你好"}]}]
  }'
```

- 模型名写在 URL 里，不写在 body 里。
- 流式：把 `:generateContent` 换成 `:streamGenerateContent?alt=sse`。
- 也可以不用请求头，改成 URL 参数 `?key=sk-xxx`（不推荐：URL 容易被日志记录）。

### 4.5 图片生成 —— `POST /v1/images/generations`

```bash
curl https://api.tokwork.com/v1/images/generations \
  -H "Authorization: Bearer $TOKWORK_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "grok-imagine-image-2.0",
    "prompt": "夕阳下的橘猫，水彩风格",
    "n": 1
  }'
```

返回 OpenAI 图片格式，图片在 `data[].b64_json`（base64 编码，自行解码保存）。
当前图片模型**按张计费，与尺寸无关**（见 §6）。

### 4.6 常用参数

请求体按对应协议的官方文档写即可，网关不改动语义。常用的：

| 参数 | 说明 |
|---|---|
| `model` | 必填，取值见 `GET /v1/models` |
| `stream` | `true` 开启流式 |
| `max_tokens` / `max_completion_tokens` | 输出上限；Anthropic 格式下 `max_tokens` 必填 |
| `temperature`、`top_p` | 采样参数，按模型能力生效 |
| `tools` / `tool_choice` | 函数调用（模型需支持） |

---

## 5. 调用第三方 API（Capability）

除大模型外，网关还把第三方 REST API 封装成统一的「能力（Capability）」。
**同一个 Key、同一个余额、同一份调用记录**，调用方不需要分别对接各家供应商。

### 5.1 能力目录 —— `GET /v1/capabilities`

```bash
curl https://api.tokwork.com/v1/capabilities \
  -H "Authorization: Bearer $TOKWORK_API_KEY"
```

```json
{
  "object": "list",
  "data": [
    {
      "id": "weather-v1",
      "provider": "open-meteo",
      "mode": "sync",
      "description": "Current weather for a coordinate (Open-Meteo, free, no key).",
      "billing": { "type": "per_call", "unit": "call", "price_usd": 0 }
    }
  ]
}
```

- `id`：能力 ID，格式 `<能力>-v<主版本>`。它代表**能力**而不是供应商 —— 背后的供应商可能被替换，
  接口契约保持不变。
- `mode`：`sync`（同步，一次请求拿结果）或 `async`（异步，提交后轮询）。**调用方式由它决定**。
- `billing`：计费方式与单价（美元）。

当前已上线（2026-10-23）：

| ID | 模式 | 输入 | 输出 | 单价 |
|---|---|---|---|---|
| `weather-v1` | 同步 | **必填** `latitude`、`longitude`；可选 `current`、`timezone` | `time`、`temperature_2m`、`wind_speed_10m` | 免费 |
| `ssl-analyze-v1` | 异步 | `host`（域名） | 各 endpoint 的 TLS 评级详情 | 免费 |
| `web-unlock-v1` | 同步 | **必填** `url`；仅限 Wayfair 四个主域名及子域名的 HTTPS 页面 | `status`、`html` | $0.003 / 次 |

### 5.2 同步调用 —— `POST /v1/capabilities/{id}`

```bash
curl https://api.tokwork.com/v1/capabilities/weather-v1 \
  -H "Authorization: Bearer $TOKWORK_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "input": { "latitude": 52.52, "longitude": 13.41 },
    "options": {}
  }'
```

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

`cost_usd` 是本次实际扣费，`usage` 是计费数量。

**网页抓取示例**（`web-unlock-v1`）：

```bash
curl https://api.tokwork.com/v1/capabilities/web-unlock-v1 \
  -H "Authorization: Bearer $TOKWORK_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "input": { "url": "https://www.wayfair.com/keyword.php?keyword=example" },
    "options": {}
  }'
```

```json
{
  "success": true,
  "capability": "web-unlock-v1",
  "output": {
    "status": 200,
    "html": "<!doctype html>..."
  },
  "usage": { "unit": "call", "quantity": 1 },
  "cost_usd": 0.003
}
```

- `url` 只接受 HTTPS，且主机必须是 `wayfair.com`、`wayfair.ca`、`wayfair.co.uk`、
  `wayfair.de` 之一或其子域名。其他域名、IP、userinfo、非 443 端口都返回
  `400 invalid_request`，不会调用上游。
- `html` 最大 8 MiB，单次超时 90 秒。
- 只有目标页 HTTP `200` 且正文非空才计费；Bright Data 错误、目标非 200、空正文都自动退款。

### 5.3 异步调用 —— `POST /v1/tasks` + `GET /v1/tasks/{id}`

**提交**（返回 `202`）：

```bash
curl https://api.tokwork.com/v1/tasks \
  -H "Authorization: Bearer $TOKWORK_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "capability": "ssl-analyze-v1",
    "input": { "host": "www.example.com" },
    "idempotency_key": "ssl-example-20261023"
  }'
```

```json
{ "success": true, "id": "task_...", "status": "queued" }
```

**轮询**：

```bash
curl https://api.tokwork.com/v1/tasks/task_... \
  -H "Authorization: Bearer $TOKWORK_API_KEY"
```

| `status` | 含义 | 下一步 |
|---|---|---|
| `queued` / `running` | 处理中 | 隔几秒再查（建议 5–10 秒，逐步拉长） |
| `succeeded` | 成功，结果在 `output` | 结束 |
| `failed` | 失败，原因在 `error_code` | 结束；**费用自动退回** |

- 任务只能被创建它的账户查询。
- **幂等**：带 `idempotency_key` 提交时，同一账户、同一能力、同一个 key 重复提交只会创建一个任务；
  重复提交返回同一个任务 id 并带 `"replayed": true`。如果前一次提交还没拿到任务 id，
  返回 `409 task_in_progress`，稍后重试即可。网络超时后重试提交时，**务必带上同一个 key**，
  以免重复扣费。（幂等目前只对异步任务生效。）

### 5.4 Capability 的错误格式

Capability 接口的业务错误使用独立的格式（与大模型接口不同）：

```json
{ "success": false, "error_code": "quota_insufficient", "message": "insufficient quota or billing is unavailable" }
```

| HTTP | `error_code` | 含义 |
|---|---|---|
| 400 | `invalid_request` | 请求体不是合法 JSON |
| 402 | `quota_insufficient` | 余额不足 |
| 404 | `capability_not_found` | 能力 ID 不存在，或同步/异步用错了入口 |
| 404 | `task_not_found` | 任务不存在或不属于你 |
| 409 | `task_in_progress` | 同一幂等 key 的提交正在进行 |
| 500 | `credential_error` | 运营方尚未配置上游凭证；未扣费 |
| 502 | `upstream_http_<状态码>`、`upstream_unreachable`、`invalid_upstream_json`、`upstream_response_too_large` 等 | 第三方供应商出错；已预扣的费用会退回 |
| 502 | `upstream_target_status` | 目标网页不是 HTTP 200；已退款，不返回网页正文 |
| 502 | `upstream_empty_body` | 目标网页正文为空；已退款 |

> Key 无效时仍返回 §2 那种 `401` 格式。

> 必填字段在预扣费之前校验；例如 `weather-v1` 传 `"input": {}` 会直接返回
> `400 invalid_request`。

---

## 6. 计费

### 6.1 规则

- **先预扣、后结算**：请求开始时按估算预扣，完成后按实际用量多退少补。
- **失败不收费**：请求出错（含上游出错）时，预扣部分全额退回。
- **余额为 0 即停**：不会透支。
- 每笔扣费都能在控制台 **调用记录** 里查到（模型、输入/输出 token、金额、request id）。

### 6.2 按 token 计费的模型（对话类）

```
费用 = 输入 token × 输入单价 + 输出 token × 输出单价 + 缓存命中 token × 缓存读单价
```

单价以「美元 / 100 万 token」计。用 `/api/pricing` 的字段自己换算：

```
输入单价 ($/1M)   = model_ratio × 2
输出单价 ($/1M)   = 输入单价 × completion_ratio
缓存读单价 ($/1M) = 输入单价 × cache_ratio
```

token 数以响应里的 `usage` 为准（流式请带 `stream_options.include_usage` 才能在客户端拿到）。

### 6.3 按次计费

- **图片模型**：按张计费，`/api/pricing` 中 `quota_type = 1`，单价即 `model_price`（美元 / 张）。
- **Capability**：按 `GET /v1/capabilities` 里的 `billing.price_usd` 计费；`price_usd = 0` 的能力免费，
  余额为 0 也能调用。

### 6.4 价格快照（2026-10-23，仅供参考）

> 价格会随上游调整。**以 `/api/pricing` 和控制台为准**，下表只是方便估算的快照。单位：美元。

| 模型 | 计费 | 输入 / 1M | 输出 / 1M | 缓存读 / 1M |
|---|---|---|---|---|
| `claude-fable-5` | token | 2.39 | 11.95 | 0.239 |
| `claude-fable-5-1` | token | 2.39 | 11.95 | 0.0598 |
| `claude-opus-5-5` | token | 0.60 | 3.00 | 0.03 |
| `claude-opus-5` | token | 0.75 | 3.75 | 0.075 |
| `claude-opus-4-8` | token | 0.75 | 3.75 | 0.075 |
| `claude-opus-4-7` | token | 0.75 | 3.75 | 0.075 |
| `claude-opus-4-6` | token | 0.75 | 3.75 | 0.075 |
| `claude-sonnet-5-5` | token | 0.30 | 1.50 | 0.015 |
| `claude-sonnet-5` | token | 0.30 | 1.50 | 0.03 |
| `claude-sonnet-4-6` | token | 0.45 | 2.25 | 0.045 |
| `claude-haiku-5-5` | token | 0.08 | 0.40 | 0.008 |
| `claude-haiku-4-5` | token | 0.20 | 1.00 | 0.02 |
| `gpt-6-astra` | token | 0.55 | 2.75 | 0.055 |
| `gpt-6.1-sol` | token | 0.18 | 0.90 | 0.009 |
| `gpt-6-sol` | token | 0.18 | 0.90 | 0.018 |
| `gpt-6-luna` | token | 0.09 | 0.45 | 0.009 |
| `gpt-5.6-sol` | token | 0.44 | 2.20 | 0.044 |
| `gpt-5.6-terra` | token | 0.20 | 1.20 | 0.02 |
| `gpt-5.6-luna` | token | 0.12 | 0.72 | 0.012 |
| `gpt-5.5` | token | 0.50 | 3.00 | 0.05 |
| `gemini-4-argon-contributor` | token | 1.20 | 6.00 | 0.06 |
| `grok-4.7` | token | 0.28 | 0.84 | 0.07 |
| `grok-4.6` | token | 0.28 | 0.84 | 0.07 |
| `grok-4.5` | token | 0.28 | 0.84 | 0.028 |
| `grok-4.3` | token | 0.1875 | 0.375 | 0.0188 |
| `deepseek-v4-pro` | token | 0.528 | 1.584 | 0.0176 |
| `deepseek-v4-flash` | token | 0.112 | 0.448 | 0.0022 |
| `deepseek-flash` | token | 0.105 | 0.42 | 0.0021 |
| `kimi-k3` | token | 0.60 | 3.00 | 0.06 |
| `kimi-for-coding` | token | 0.30 | 1.20 | 0.075 |
| `glm-5.3` | token | 0.28 | 0.88 | 0.052 |
| `glm-5.3-flash` | token | 0.075 | 0.25 | 0.015 |
| `doubao-seed-2.1-pro` | token | 0.1725 | 0.8625 | 0.0345 |
| `doubao-seed-2.1-lite` | token | 0.0575 | 0.1941 | 0.0115 |
| `doubao-seed-2.0-mini` | token | 0.0575 | 0.575 | 0.0115 |
| `MiniMax-M3` | token | 0.09 | 0.36 | 0.018 |
| `MiniMax-M3.1-Flash` | token | 0.075 | 0.30 | 0.0075 |
| `hy4-preview` | token | 0.417 | 1.2505 | 0.021 |
| `longcat-2.0` | token | 0.15 | 0.60 | 0.003 |
| `muse-spark-1.3-contributor` | token | 0.10 | 0.20 | 0.002 |
| `gpt-image-2.5` | 按张 | 0.01 / 张 | — | — |
| `grok-imagine-image-2.0` | 按张 | 0.01 / 张 | — | — |

---

## 7. 错误处理

### 7.1 大模型接口的错误格式

OpenAI 格式接口（含 `/v1/chat/completions`、`/v1/responses`、`/v1/images/*`、`/v1/models`）：

```json
{
  "error": {
    "message": "错误描述 (request id: ...)",
    "type": "new_api_error",
    "code": "model_not_found"
  }
}
```

`/v1/messages` 返回 Anthropic 风格（`{"type":"error","error":{...}}`），
`/v1beta/*` 返回 Gemini 风格。**鉴权失败（401）在所有协议下都是上面这种 OpenAI 格式。**

### 7.2 状态码

| HTTP | 典型 `code` | 含义 | 怎么办 |
|---|---|---|---|
| 400 | — | 请求体不合法 | 按报错修正参数，不要重试 |
| 401 | — | Key 缺失或无效 | 检查请求头与 Key |
| 403 | `insufficient_user_quota` | 余额不足 | 控制台充值后再试 |
| 403 | `pre_consume_token_quota_failed` | 该 Key 的额度用完 | 调整 Key 额度或换 Key |
| 403 | — | Key 被限制（模型 / IP / 分组）或账户被停用 | 联系管理员 |
| 429 | — | 触发限流 | 指数退避后重试 |
| 500 | — | 网关内部错误 | 带 request id 联系支持 |
| 502 / 504 | — | 上游供应商出错或超时 | 退避后重试；持续出现请联系支持 |
| 501 | `api_not_implemented` | 网关不支持该接口（见 §8） | 不要重试 |
| 503 | `model_not_found` | 该模型当前没有可用的上游（未开通或暂时下线） | 先查 `GET /v1/models`，换模型或稍后重试 |

### 7.3 重试建议

- **可以重试**：`429`、`500`、`502`、`503`、`504`、网络超时 —— 用指数退避（如 1s、2s、4s，最多 3–5 次）。
- **不要重试**：`400`、`401`、`403` —— 重试也不会成功。
- 上游偶发过载时会直接返回 `502`，失败请求不扣费，退避重试即可。

---

## 8. 暂未开放的接口

以下接口在网关上存在，但**目前没有上架对应模型**，调用会返回 `503 model_not_found`。
上架后对应模型会出现在 `GET /v1/models` 里：

- Embeddings `/v1/embeddings`、Rerank `/v1/rerank`
- 语音 `/v1/audio/*`
- 视频生成 `/v1/videos`、`/v1/video/generations`

以下接口**不支持**，固定返回 `501 api_not_implemented`：
`/v1/files`、`/v1/fine-tunes`、`/v1/images/variations`。

---

## 9. 常见问题

**Q：能不能把 Key 放在浏览器 / App 里直接调？**
不要。Key 泄露后任何人都能消耗你的余额。请在你自己的后端调用，前端只调你的后端。

**Q：同一个模型，用 OpenAI 格式和 Anthropic 格式调，价格一样吗？**
一样。价格只跟模型和用量有关，跟请求格式无关。

**Q：为什么 `GET /v1/models` 里没有某个模型？**
说明它当前没有上架或暂时下线。网关不会悄悄换成别的模型 —— 调用未上架的模型会明确返回
`503 model_not_found`。

**Q：怎么查余额和账单？**
控制台 <https://www.tokwork.com/console>：概览页看余额和今日用量，调用记录页看逐笔扣费。

**Q：遇到问题找谁？**
邮件 <support@tokwork.com>，附上 `X-Oneapi-Request-Id`、模型名、时间。

---

## 维护者备注（发给客户前可删掉本节）

**事实来源**

| 内容 | 来源 |
|---|---|
| 路由与鉴权方式 | `router/relay-router.go`、`router/capability-router.go`、`router/video-router.go`、`middleware/auth.go`（`TokenAuth`） |
| 401 错误格式、`X-Oneapi-Request-Id` | 2026-10-23 线上实测（不带 Key 请求 `/v1/*`） |
| `sk-xxx-<数字>` 普通用户 403 | `middleware/auth.go` `SetupContextForToken` |
| 余额不足 403 `insufficient_user_quota` | `service/billing_session.go` |
| 503 `model_not_found` | `middleware/distributor.go` |
| 501 `api_not_implemented` | `controller/relay.go` `RelayNotImplemented` |
| Capability 契约 / 错误码 / 幂等 | `controller/capability.go`、`capability/executor.go`、[capability-api.md](../07-reference/capability-api.md) |
| 价格快照与换算 | 2026-10-23 `GET /api/pricing`（42 个模型，分组倍率 default = 1）；换算口径见 §6.2 |
| 门户地址 | [tokwork-nbg1.md](../03-deployment/tokwork-nbg1.md) §8.6.9 |

**核实状态**

- ✅ 已线上实测：OpenAI Chat（含流式）、图片 `grok-imagine-image-2.0`（返回 `b64_json`）；
  Capability 同步/异步/幂等 —— 见 capability-api.md「线上实测」。
- ⚠️ **只核对了代码路径、还没用真实 Key 实测**（待验证）：
  `/v1/responses`、`/v1/messages`（Anthropic 格式 → 转 OpenAI 发给 Wokey）、
  `/v1beta/models/*`（Gemini 格式 → 转 OpenAI）。三者都由 `relay/channel/openai/adaptor.go`
  的 `Convert*Request` 处理；上线对外宣传前请各跑一次 §4 的 curl。
- ✅ 2026-10-23 曾复现：`weather-v1` 空 `input` → `502 invalid_upstream_json`。根因是执行器跳过缺失字段；
  同日已补 Manifest 必填校验，同类请求现在在预扣费前返回 `400 invalid_request`。
- ⚠️ 限流阈值未写进文档：`ModelRequestRateLimit` 是否开启、阈值多少取决于线上后台配置，未核实。

**会过期的内容**：§5.1 能力表、§6.4 价格快照。上下架模型或跑完 `sync-wokey-pricing.py` 后要更新。
