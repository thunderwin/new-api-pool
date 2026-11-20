# 架构决策记录（ADR）

> 为什么这么写、为什么不那么写。一旦写下就**不要随便改方向**——除非有新事实。

---

## ADR-0001：走 fork + 自编译路线，不走 plugin / sidecar

### 背景

QuantumNous/new-api 没有官方插件机制。"号池管理"涉及的改动跨越路由、菜单、表结构、ORM、定时任务，单纯外挂一个 sidecar 服务只能"伪集成"——管理员要在两个后台来回跳，UI 风格不统一。

### 决策

直接 **fork**，把号池管理做进 new-api 的源码：
- 改 `model/main.go` 的 AutoMigrate 列表
- 改 `router/main.go` 加新路由组
- 改 `web/src/components/layout/SiderBar.jsx` 加一级菜单
- 改 `web/src/App.jsx` 加路由
- 新增 `controller/pool.go`、`service/pool_*.go`、`model/pool_*.go`、`web/src/pages/Pool/*.jsx`

**编译产物本地用，不发包**。

### 后果

- ✅ UI / 体验完全一致
- ✅ 表结构 / 路由 / 缓存可以无缝复用
- ✅ 不依赖 plugin 框架（new-api 没有）
- ❌ 永久放弃了和上游 main 的 merge——需要新功能要么自己加，要么手挑 cherry-pick
- ❌ 升级 new-api 上游 = 一次重型 rebase 工程

### 状态：accepted（2026-04）

---

## ADR-0002：删除所有 chromedp 自动 Runner

### 背景

最初版本（v1.x）实现了 7 个商业站点的全自动 Runner（Anthropic / OpenAI / Mistral / Cohere / Together / OpenRouter / mock-debug）。期望"管理员一键 → worker 跑通 → 自动得到 Key"。

实测（2026-04）：
| 站点 | 阻拦 | 结果 |
|---|---|---|
| OpenRouter | Cloudflare Turnstile | 检测 isTrusted=false，必败 |
| OpenAI | Turnstile + 设备指纹 | 同上 |
| Anthropic | Turnstile + 信用卡 | 同上 |
| Mistral / Together / Cohere | Turnstile / hCaptcha | 同上 |
| Cerebras（曾经能跑） | Google reCAPTCHA + 中国 IP gstatic.com 被墙 | 后端 timeout |
| HuggingFace | hCaptcha | 必败 |

直接表现：Runner 一启 → 5 分钟 timeout → fail → 用户看到一片红 → 损害信任。

### 决策

- 删除 `service/pool_runner_anthropic.go` `pool_runner_openai.go` `pool_runner_mistral.go` `pool_runner_cohere.go` `pool_runner_together.go` `pool_runner_openrouter.go` `pool_runner_mock.go` `pool_runner_helpers.go`
- 删除 `service/pool_browser.go`（chromedp 浏览器会话池）
- 保留 `RecipeRunner` 接口和 `RegisterRunner` / `GetRunner` 注册表（接口不有罪，未来谁有打码服务可自加）
- 所有 19 个内置 Recipe 一律 `ManualMode = true`，UI 给步骤 + 文档链接
- `Recipe.AutomationCapability` 字段保留作为信息字段，告诉用户哪些 Recipe 理论可全自动

启动时一次性迁移：
```sql
UPDATE pool_recipes SET manual_mode=1
WHERE manual_mode=0 AND (webhook_url IS NULL OR webhook_url='')
```

### 后果

- ✅ 不再骗用户、不再产生不真实的失败任务
- ✅ "半自动"3-10 分钟，比 Runner 5 分钟跑+大概率失败更好
- ✅ 代码体积减小约 2000 行，go.mod 不再依赖 chromedp（间接依赖还在保留以备后用）
- ❌ 失去"全自动"卖点
- ❌ 需要后续任何想做自动化的人重新写 Runner（接口在）

### 状态：accepted（2026-04）

---

## ADR-0003：号池管理作为一级菜单，不走二级 / 浮窗

### 背景

最初设想：号池管理是设置页里的一个 tab。但功能复杂度（5 个独立子页 + 巡检 + 告警 + 计费）远超一个 tab 能容纳。

### 决策

- 在 `web/src/components/layout/SiderBar.jsx` 加一级菜单 `pool`，items 含 5 个子菜单。
- 视觉对齐 new-api 现有：用 Semi UI Typography / Tag / Card / Form / Modal，写 `PoolPageLayout` 共用页面壳保留 60px 顶距 + 副标题 + Tag + extra slot。
- 菜单仅 `isAdmin()` 可见。

### 后果

- ✅ 业务复杂度匹配
- ✅ 完全融入 new-api 风格（Semi UI 颜色变量）
- ✅ 用户感知是 "new-api 自带功能" 而非外挂
- ❌ 一级菜单数量 +1（共 7-8 个），未来如果再加可能要分级

### 状态：accepted（2026-04）

---

## ADR-0004：Recipe 加 `DefaultModels` 字段

### 背景

自动建渠道时 `channel.Models` 留空 → 路由匹配不到 → 用户看到"渠道有了但用不了"，要去原生「渠道管理」点「获取模型列表」补全才行——多一步痛苦。

### 决策

- `PoolRecipe` 加 `DefaultModels` 字段（text，逗号分隔）。
- Seed 给所有 19 条 Recipe 都填好 `DefaultModels`：
  - Gemini AI Studio：`gemini-2.5-pro,gemini-2.5-flash,...,text-embedding-004`
  - DeepSeek：`deepseek-chat,deepseek-reasoner`
  - 等等
- 自动建渠道时：`channel.Models = recipe.DefaultModels`
- `upsertPoolRecipe` 仅在 existing 为空时同步该字段（保留用户改过的）

### 后果

- ✅ 真正"零配置"，建出的渠道立即可路由
- ✅ 模型清单作为 Recipe 元数据维护，新模型上线时改一行 seed
- ❌ 上游模型清单变化需要主动维护（如 GPT-5 出来）
- ❌ DefaultModels 不一定 = 该 Key 实际有权访问的模型——但路由不到比 401 更糟

### 状态：accepted（2026-04）

---

## ADR-0005：channel 写入必须用 `ch.Insert()`，不允许 `DB.Create(ch)`

### 背景

历史 bug：`controller/pool.go::AddPoolAccount` 一处 `model.DB.Create(ch).Error`。建出来的 channel 在「渠道管理」页能看见，但调 `/v1/...` 永远 `No available channel for model X under group default`。根因：`DB.Create` 不调 `AddAbilities()`，路由表完全空。

### 决策

- 任何号池侧建 channel 的代码全部改成 `ch.Insert()`（自动 AddAbilities）
- `coding-standards.md` 写明这个规则
- `controller/pool.go::AddPoolAccount` `ManualSubmitJobResult` `BindPoolAccountChannel` `service/pool_worker.go::runJob` 全部统一

### 后果

- ✅ 自动建渠道立即可用，不再有 ghost channel
- ❌ 强行依赖 new-api 一个内部 API；如果上游改了 `Insert` 签名要适配

### 状态：accepted（2026-04）

---

## ADR-0006：channel.Group 用逗号双值 `"default,<provider>"`

### 背景

最初实现：`channel.Group = recipe.Provider`（如 "gemini"）。结果 default 组的用户调 gemini-2.5-flash → 路由查 abilities `WHERE group='default' AND model='gemini-2.5-flash'` → 0 行。

### 决策

```go
chGroup := "default"
if groupName != "" && groupName != "default" {
    chGroup = "default," + groupName
}
ch.Group = chGroup
```

`AddAbilities()` 里的 `strings.Split(channel.Group, ",")` 笛卡尔积写两份。

### 后果

- ✅ default 用户 + provider 组用户都能命中
- ✅ 老板想区分定价（vip 组只走付费 channel）依然可以——后台手动改某个 channel 为 `vip` 即可
- ⚠️ Reverse：删除一个 channel 时 abilities 自动级联（DeleteAbilities）——但记得**别用 `DB.Delete(ch)`**，要走 `ch.Delete()` 同理

### 状态：accepted（2026-04）

---

## ADR-0007：Telegram 通道默认值通过环境变量预置

### 背景

私人 fork 早期把默认 Bot Token + Chat Id 硬编码在 `model/main.go` 里方便启动。
开源前发现这等于把 token 公开 → 任何人都能用这个 bot 推送 → 必改。

### 决策（开源后修正）

- `model/main.go::SeedDefaultTelegramConfig` 调用改为读环境变量：
  - `POOL_TELEGRAM_BOT_TOKEN`
  - `POOL_TELEGRAM_CHAT_ID`
- 仅当**两个**环境变量都非空时才 seed
- options 里已存在的不覆盖（`EnsureDefaultOption`）
- 同理 `POOL_FIVESIM_API_KEY` / `POOL_SMS_ACTIVATE_API_KEY`

### 后果

- ✅ 源码可安全开源
- ✅ Docker / systemd 部署可在 env 里给默认
- ✅ 后台 UI 仍可热改，覆盖环境变量
- ❌ 二进制双击启动场景需先 `export` 环境变量；不嫌麻烦就走后台 UI

### 状态：accepted（2026-04 开源版）

---

## ADR-0008：删除「风控规则」二级菜单

### 背景

最初规划"风控规则"作为号池一级菜单的 6 个子菜单之一。但后续发现：
- new-api 主体已有 RPM/TPM/分组限速 / IP 黑名单 / 用户日消耗上限
- 号池侧再做一套是重复
- 反爬 / 反羊毛属于网关层职责，不是号池职责

### 决策

- 删除 `web/src/pages/Pool/Risk.jsx`
- 删除 `controller/pool.go::Get/Add/Update/DeletePoolRiskRule`
- 删除 `model/PoolRiskRule` + 表
- 删除 `router/pool-router.go::/api/pool/risk*` 路由
- SiderBar 移除 `pool-risk` 项
- 一级菜单改为 5 子菜单：总览 / 上游账号 / 自动注册 / 收支对账 / 巡检告警

### 后果

- ✅ 职责清晰
- ✅ 减少代码 ~600 行
- ❌ 用户在号池页看不到"今天有人滥用我"——但去原生「日志」/「用户」页能看到

### 状态：accepted（2026-04）

---

## ADR-0009：Seed 函数加"非空 fallback"保护，避免 GORM 默认值陷阱

### 背景

GORM 的 `default` tag 会在字段为 zero-value（false/0/""）时填默认值。我们 Seed 时显式给 `ManualMode: false`，结果 GORM 把它当 zero-value 用 `default:true` 覆盖，导致历史表里所有 Recipe `manual_mode=true` 都对，但**早期想用自动模式的剧本就被错误地强制半自动**了。

### 决策

- `PoolRecipe.ManualMode` 字段去掉 `gorm:"default:true"`：
  ```go
  ManualMode  bool   `json:"manual_mode"`   // 注意：不设 default
  ```
- 在 `buildDefaultRecipeSeeds()` 里**显式**写 `ManualMode: true`/`false`
- 一次性迁移 SQL：`UPDATE pool_recipes SET manual_mode=1 WHERE manual_mode=0 AND webhook_url=''`

### 后果

- ✅ Seed 行为可预测
- ⚠️ 任何 bool 字段以后**都不要**写 `gorm:"default:..."`

### 状态：accepted（2026-04）

---

## ADR-0010：本地 SQLite + 嵌入 dist 单 binary 作为主分发形态

### 背景

可以选 Postgres + 多容器，但当前是个人项目 / 个位数用户量。

### 决策

- 默认 SQLite（`one-api.db`），数据全在一个文件里
- 前端 `web/dist/` 用 `//go:embed` 编进 binary，不需要单独 nginx

> 补充（2026-10-10）：`//go:embed` 仍然保留，但它不再是**唯一**的托管方式。
> 设了 `FRONTEND_BASE_URL` 之后，后端只做 API，网页路径 301 跳到外部前端
> （本 fork 实际部署就是把 React 放在 Cloudflare Pages、服务器只跑 API）。
> 见 [`../03-deployment/tokwork-nbg1.md`](../03-deployment/tokwork-nbg1.md) 与
> [`../07-reference/changelog.md`](../07-reference/changelog.md) v0.5.2。

- macOS arm64 binary 70-100MB，双击 `start.command` 起动
- 生产化时再切 Postgres + 多实例 + Caddy（蓝图都在 [`deploy/`](../../deploy/)）

### 后果

- ✅ 开发体验极轻：单仓库、单二进制、单数据库文件
- ✅ 备份 = `cp one-api.db`
- ⚠️ SQLite 扛不住 ≥ 50 用户高并发，超过即换 Postgres

### 状态：accepted（2026-04）

---

## ADR-0011：Claude 走原生 /v1/messages 渠道并由中转站补缓存断点

### 背景

线上 Claude 流量几乎都是 OpenAI 格式的 `/v1/chat/completions`，客户端不带任何 `cache_control`。
这条路径上：

- OpenAI 兼容路径本身不认 `cache_control`，中转只能自己猜缓存点；
- 不显式打标记时，上游只会在请求结尾处自动缓存，而 agent 类客户端的对话历史是滑动窗口（前面被截掉），
  下一轮请求基本对不上上一轮的缓存前缀，结果是**每轮都在冷写**；
- 实测口径：约一半 Opus 花费是冷写（2026-11-20 核实）。

### 决策

- 新增 Anthropic 类型（type 14）渠道直连上游 `/v1/messages`，保留上游原生的显式缓存能力；
- 缓存断点由中转站在 **OpenAI→Claude 转换之后**自动补，通过**渠道级开关**
  `claude_auto_cache_control`（`dto.ChannelOtherSettings.ClaudeAutoCacheControl`）打开，**默认关闭**；
  关闭时请求与旧行为完全一致；
- **只补 5 分钟**（`{"type":"ephemeral"}`，不带 `ttl`），不引入 1 小时档；
- **只作用于 OpenAI→Claude 这条路径**（`ConvertOpenAIRequest`），原生 Claude 请求
  （`ConvertClaudeRequest`）不碰；请求里已存在任何 `cache_control` 时整体不动；
- 断点位置：最后一个函数工具、system 最后一个非空 text 块、最后一条消息的最后一个内容块、
  以及“上一轮结尾”（从倒数第二条往前最近一条 `user` 消息的最后一个内容块），单请求最多 4 个；
- thinking 由渠道的 `param_override` 删掉 `thinking` 保持关闭，**不改代码**；
- 旧的 OpenAI 兼容 Claude 渠道（type 1 走 `/v1/chat/completions`）**保留**，作为回滚路径。

### 后果

- ✅ 显式断点让下一轮请求能在同一位置命中上一轮写下的缓存，降低冷写比例；
- ✅ 默认关闭，未打开的渠道零影响；规则集中在
  `relay/channel/claude/auto_cache_control.go`，不改任何文本、顺序、角色；
- ❌ type 14 渠道**不支持 `/v1/responses`**（该路径在本 adaptor 里仍是 not implemented），
  需要 responses 的客户端只能继续走旧渠道；
- ⚠️ 客户端把历史改成缓存友好的形态（保留稳定前缀）是**后续计划，还没做**；
  在那之前，断点只能缓解、不能消除滑动窗口带来的冷写；
- ⚠️ 目前没有 UI 开关，只能通过渠道设置字段打开（本版本不改前端）。

### 实测结果（2026-11-20，已核实）

在服务器上用与桌面客户端相同的请求构造（pi-ai OpenAI 兼容路径），以管理员 token 指定渠道，
跑同一段建站 agent 会话（工具循环 + 截图图片 + 下一轮扁平化历史 + 压缩调用），模型 `claude-opus-5-5`，
逐笔对照 Wokey `/v1/requests`：

- **拒答收费**：type 14 渠道第一次跑出现 2 次上游拒答（`stop_reason` `refusal`，中转映射为
  `content_filter`，类别 `reasoning_extraction`），每次被收 $0.10 违规费加 token 费。Wokey 文档写明
  短时间内重复违规会先把该模型限到 1 并发、10 次/分钟，再犯暂停 24 小时，30 天内多个模型被暂停会限制整个账号。
  同一会话在 OpenAI 类型渠道上没有拒答；该账号此前最近 800 笔调用也没有拒答。触发原因**未查明**
  （待验证的候选：转换器给带 tool_calls 的 assistant 消息补的 `"..."` 占位文本、Wokey 原生路径与 OpenAI
  路径发给上游的内容差异、分类器本身的随机性）。
- **缓存命中不稳定**：第二次跑没有拒答，但有本该命中的调用读 0、重新写入约 2.6 万 token，
  整段花费比 OpenAI 类型渠道同场景多约 48%。显式断点**没有**带来可观察的节省。
- 结论：不切线上。`wokey-claude`（id 3）已禁用，代码和开关保留（默认关闭，对其他渠道零影响）。

### 状态：accepted（2026-11-20），已部署但**未启用**；线上切换搁置，等拒答原因和缓存稳定性查清

---

## 模板：要新加一个 ADR

```md
## ADR-NNNN：决策标题（动词开头）

### 背景
（事实，没有意见）

### 决策
（要改什么、不改什么）

### 后果
- ✅ 好处
- ❌ 代价

### 状态：proposed / accepted / superseded by ADR-XXXX
```

写完追加到本文档末尾，不要插队。
