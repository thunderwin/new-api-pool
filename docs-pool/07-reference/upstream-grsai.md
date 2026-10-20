# 上游调研：Grsai（grsaiapi.com）— 2026-10-10

> 状态：**只做了调研，没有接入**。第二个上游候选，与 [`apimaster`](../03-deployment/tokwork-nbg1.md) 互补。
> 下面所有"实测"都是 2026-10-10 用 `curl` 在本机和生产服务器 `2.31.8.21` 上跑出来的。

## 1. 一句话

**一家以「图片 / 视频生成」为主的国内系聚合中转**，模型数量和模式（按次 / 按秒 / 按 token）
跟 apimaster 不一样，**31 个模型里有 22 个是我们现在没有的**，其中 nano-banana 全系和
gemini 文本系是补充品。但它的文本模型覆盖面**比 apimaster 窄得多**（没有 Claude / GPT 通用系 / DeepSeek）。

## 2. 身份与节点（实测）

| 项 | 值 | 怎么核实 |
|---|---|---|
| 官网 / 控制台 | `https://grsai.com`（`https://grsai.ai` 同一套界面） | 两个域名返回同一份 HTML |
| API 全球节点 | `https://grsaiapi.com` → `154.9.228.11` | `dig` + `whois` |
| 归属 | AS979 NetLab Global，**洛杉矶**（`ipinfo`：Los Angeles, CA, US） | `ipinfo.io/154.9.228.11` |
| API 国内节点 | `https://grsai.dakka.com.cn` → 114.66.x / 110.42.x（中国大陆） | `dig` |
| 接口文档 | Apifox 分享页 `https://qmy27nhsd9.apifox.cn/452418916e0`（8 个接口页公开可读） | 直接抓 HTML，正文在静态 HTML 里 |
| 服务端 | `nginx/1.23.4` 反代一个 Go 服务（报错体 `"type":"rix_api_error"`） | 响应头 + 报错体 |

**延迟（生产服务器 Nuremberg → 全球节点）**：ping RTT **150ms**，TCP 连接 150ms、TLS 完成 304ms。
比 apimaster（纽伦堡同城）差，但可用；国内节点对德国服务器没有意义（更远），**用全球节点**。

## 3. 它提供什么接口（4 套，2026-10-10 核实）

| 套件 | 端点 | 说明 |
|---|---|---|
| OpenAI 兼容 · 文本 | `POST /v1/chat/completions` | 只服务它的 gemini / gpt-image 系；返回标准 `usage` |
| OpenAI 兼容 · 出图 | `POST /v1/images/generations`、`POST /v1/images/edits`（multipart） | `model` + `prompt` + `image[]` + `size` / `quality` |
| Grsai 原生 | `POST /v1/api/generate` + `GET /v1/api/result?id=` | 自家异步格式（`status` / `results[].url`），视频走这条 |
| Gemini 原生 | `POST ...:generateContent` / `:streamGenerateContent` | 官方格式直通 |

**它没有的东西**（别指望用现成脚本同步）：

- ❌ `/v1/models`、`/api/status`、`/api/pricing`、`/api/user/self`、`/dashboard/billing/*` 全部 `404 page not found`
  —— **不是 new-api / one-api 系**，本仓库自带的 `/api/ratio_sync/fetch` 对它无效
- ❌ 没有余额 / 用量查询接口（用 key 查不了剩余积分）
- ✅ 但**模型 + 价格表是公开网页**：`https://grsai.com/zh/dashboard/models` 不带 cookie 就返回 31 个模型
  的完整价目（HTML 服务端渲染），**这是唯一能自动化的定价来源**，可抓

## 4. 实测：key 是通的（2026-10-10）

用用户给的 key 直接打，**全部返回 200 且真的出结果**：

| 调用 | 结果 |
|---|---|
| `gemini-2.5-flash` / `gemini-2.5-pro` chat | 200，返回 `usage.prompt_tokens/completion_tokens` |
| `nano-banana-fast`、`nano-banana-2`、`nano-banana-2-lite`、`nano-banana-pro` 出图 | 200，返回图片 URL |
| `gpt-image-2`、`gpt-image-2.5` 出图 | 200，返回 `data[0].url` + `usage` |

> 注意：`nano-banana`（无后缀）已下架，报 `已下架，请使用nano-banana-fast模型`。
> 视频模型名和文本模型名**不通用**：`gpt-4o` / `claude-*` / `deepseek-*` / `sora-2` / `veo-3` 都报
> `model not found`，说明它只挂了自家白名单里的模型。

## 5. 模型与价格全表（抓自 `grsai.com/zh/dashboard/models`，2026-10-10）

### 图片 / 视频（按次、按秒，单位人民币）

| 模型 | 计费 | 价格 | 备注 |
|---|---|---|---|
| `nano-banana-fast` | 按次 | ¥0.022 | 实测通；`gemini-3.1-flash-lite-image` 封装，编辑器 |
| `nano-banana-2-lite` | 按次 | ¥0.022 | `gemini-3.1-flash-lite-image` |
| `nano-banana-2` | 按次 | ¥0.06 | `gemini-3.1-flash-image-preview`，1K/2K/4K 同价 |
| `nano-banana-2.1` | 按次 | ¥0.06 | `gemini-nano-banana-2.1`（**我们已在售同名模型**） |
| `nano-banana-pro` | 按次 | ¥0.09 | `gemini-3-pro-image-preview` |
| `gpt-image-2` | 按次 | ¥0.03 | 支持 1K |
| `gpt-image-2.5` | 按次 | ¥0.03 | 支持 1K |
| `gpt-image-2-vip` | 按次 | ¥0.10 | 1K/2K/4K |
| `gpt-image-2.5-flare` | 按次 | ¥0.10 | **维护中** |
| `gpt-image-2.5-sunburst` | 按次 | ¥0.12 | **维护中** |
| `nano-banana-2-cl` / `nano-banana-pro-cl` / `nano-banana-2-2k-cl` / `nano-banana-2-4k-cl` / `nano-banana-pro-vip` / `nano-banana-pro-4k-vip` | 按次 | ¥0.30 / 0.50 / 0.45 / 0.65 / 0.50 / 0.90 | **同一个底层模型的不同渠道**，贵的写"高成本渠道，稳定" |
| `minimax-h3` | 按秒 | 480p ¥0.05、768p ¥0.07、1080p ¥0.15 | 视频，最长 15s（1080p 只 10s），要专业提示词 |

### 文本（按 token，单位人民币 / 百万 token）

| 模型 | 输入 | 输出 | 缓存 |
|---|---|---|---|
| `gemini-3.1-flash-lite` | 0.25 | 1.5 | — |
| `gemini-2.5-flash` | 0.30 | 2.0 | — |
| `gemini-3.5-flash-lite` | 0.30 | 2.5 | — |
| `gemini-3-flash` | 0.40 | 3.0 | — |
| `gemini-3.7-flash` / `gemini-3.8-flash` | 0.60 | 3.5 | — |
| `gemini-3.5-flash` | 1.20 | 10.0 | — |
| `gemini-2.5-pro` | 1.25 | 6.25 | — |
| `gemini-3-pro` / `gemini-3.1-pro` | 1.50 | 7.0 | — |
| `gpt-5.6-terra` | 0.90 | 5.2 | 0.09 |
| `gpt-5.5` | 2.20 | 13.5 | 0.225 |
| `gpt-5.6-sol` | 2.20 | 13.0 | 0.225 |
| `gpt-6-astra` | 4.00 | 20.0 | 0.40 |

> **两个坑**：
> 1. 它同一模型有 **-cl / -vip / 4k-vip 这类"多渠道改名"**，价差可达 10 倍（`nano-banana-2` ¥0.06 vs
>    `nano-banana-2-cl` ¥0.30）。接入时要**只认最低价档**，否则会按最贵的档卖。
> 2. 31 个模型 = **17 个图片/视频 + 14 个文本**；14 个文本里是 **10 个 gemini + 4 个新 GPT 名**，
>    主流 Claude / DeepSeek / Qwen
>    一个都没有 —— 它**替代不了 apimaster**，只能做补充。

## 6. 和我们现有目录的关系（2026-10-10 对比 `api.tokwork.com/api/pricing` 的 77 个）

- **名字直接对得上（9 个）**：`gpt-image-2`、`gpt-image-2.5-sunburst`、`gpt-image-2.5-flare`、
  `gpt-5.5`、`gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-6-astra`、`gemini-3.7-flash`、`gemini-3.8-flash`
  → 这 9 个可以直接比价，谁便宜走谁。
- **它没有、我们有（大量）**：Claude 全系、GPT-4o/5 系列、DeepSeek、Qwen、Kimi、GLM、Grok、Midjourney、Kling…
- **它独有（22 个）**：nano-banana 全系（fast / 2 / 2-lite / 2.1 / pro 及 4K、CL、VIP 变体）、
  `gpt-image-2-vip`、`gpt-image-2.5`、`minimax-h3`、gemini 文本系（2.5-flash/pro、3-flash/pro、3.1-pro、3.5-flash…）
  → 尤其 **`nano-banana-fast` ¥0.022/次** 和 **`gemini-2.5-flash` ¥0.3/M 输入**，是明显比现有价低的补充货源。

## 6.5 谁更便宜：和 apimaster 的**实际成本**逐项对比（2026-10-10）

数据来源：grsai 抓自公开价目页（人民币）；
apimaster 抓自 `GET /api/public/marketplace?model=X` 的 `user_price` / `media_pricing.billing_prices`
（**实际扣费价**，不是官方标价）；汇率取 `open.er-api.com` 2026-10-10 的 **1 USD = 6.7065 CNY**。
"我们售价" = apimaster 实际成本 × 1.25（现在的定价规则），单位美元。

| 类型 | 模型（grsai 名 ↔ 我们目录名） | grsai 成本 | apimaster 成本 | 我们售价 | grsai / apimaster |
|---|---|---|---|---|---|
| 图片/次 | `nano-banana-fast`（= `gemini-3.1-flash-lite-image`） | $0.00328 | $0.00900 | $0.01125 | **0.36× 便宜 64%** |
| 图片/次 | `nano-banana-2`（= `gemini-3.1-flash-image`） | $0.00895 | $0.01340 | $0.01675 | **0.67× 便宜 33%** |
| 图片/次 | `nano-banana-pro`（= `gemini-3-pro-image`） | $0.01342 | $0.02010 | $0.02513 | **0.67× 便宜 33%** |
| 图片/次 | `nano-banana-2.1`（= `gemini-nano-banana-2.1`） | $0.00895 | $0.01601 | $0.02001 | **0.56× 便宜 44%** |
| 图片/次 | `gpt-image-2` | $0.00447 | $0.00750 | $0.00937 | **0.60× 便宜 40%** |
| 图片/次 | `gpt-image-2.5-flare`（它维护中） | $0.01491 | $0.00750 | $0.00937 | 1.99× 贵 |
| 图片/次 | `gpt-image-2.5-sunburst`（它维护中） | $0.01789 | $0.00750 | $0.00937 | 2.39× 贵 |
| 视频/秒 | `minimax-h3` 768p（= `MiniMax-H3`） | $0.01044 | $0.04800 | $0.06000 | **0.22× 便宜 78%** |
| 文本/M in | `gemini-3.7-flash` | $0.08947 | $0.29986 | $0.37483 | **0.30× 便宜 70%** |
| 文本/M in | `gemini-3.8-flash` | $0.08947 | $0.29986 | $0.37483 | **0.30× 便宜 70%** |
| 文本/M in | `gemini-3.1-pro`（= `gemini-3.1-pro-preview`） | $0.22366 | $0.39982 | $0.49977 | **0.56× 便宜 44%** |
| 文本/M in | `gpt-5.5` | $0.32804 | $0.22316 | 分段表达式 | 1.47× 贵 |
| 文本/M in | `gpt-5.6-sol` | $0.32804 | $0.22327 | 分段表达式 | 1.47× 贵 |
| 文本/M in | `gpt-5.6-terra` | $0.13420 | $0.10707 | 分段表达式 | 1.25× 贵 |
| 文本/M in | `gpt-6-astra` | $0.59644 | $0.44654 | 分段表达式 | 1.34× 贵 |

**三条结论：**

1. **gemini 文本 + nano-banana 图片 + 视频**：grsai 便宜 33%~78%，是实打实的降本空间
   （例：`gemini-3.7-flash` 现在卖 $0.375/M 输入，走 grsai 加价 25% 只要 $0.112/M）。
2. **GPT 系（gpt-5.5 / 5.6 / 6-astra）**：grsai 比 apimaster **贵 25%~47%**，这几个继续走 apimaster。
3. **分辨率越高，grsai 越划算**：它对 nano-banana 全系 **1K/2K/4K 同价**，
   而 apimaster 按档加价（`gemini-3.1-flash-image` 4K 成本 $0.0302）—— 4K 场景差到 **0.30×**。

> 所以正确姿势不是"换上游"，而是**两个都留，按模型取最低价**（谁便宜走谁），
> 正好复用已有的"便宜优先 + 涨价自动调"机制。

## 7. 风险与待办

1. **key 已在聊天里明文出现过，建议轮换**（现在还没存到服务器）。
2. 价格只能靠**抓公开网页**，对方改版就得改解析；建议抓下来先 diff、变了告警，再自动改价。
3. 图片/视频模型名和我们现有目录**不同名**（`gemini-3.1-flash-image` ↔ `nano-banana-2`），
   要维护一张映射表，否则用户在我们这边看到的模型名和上游对不上。
4. 它两家节点的响应体完全一样，接的时候只填全球节点，别做双节点故障转移（没意义，反而增加成本）。

## 8. 计划（都还没做）

- [ ] 接入为第二个 channel（OpenAI 兼容类型，base url = `https://grsaiapi.com`）
- [ ] 写 `ops/sync-upstream-pricing-grsai.py`：抓 `grsai.com/zh/dashboard/models` → 取最低价档 → 加价写库
- [ ] 改造 `ops/sync-upstream-pricing.py`，从"单上游"改成"多上游每个模型取 min（各上游自己的价 × 各自 markup）"
- [ ] 决定 22 个独有模型里哪些真的上架（`-cl` / `-vip` 这类渠道改名档不该暴露给用户）
