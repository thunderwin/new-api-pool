# Changelog（fork 维护视角）

> 本文档记录 fork 自 [QuantumNous/new-api](https://github.com/QuantumNous/new-api) 之后的关键变更。
> 起点：fork 时间 2026-04（基于上游 main 的某次同步）。
> 上游 new-api 自身的变更**不在此文档范围内**，参考其 [release notes](https://github.com/QuantumNous/new-api/releases)。

记号说明：
- `+` 新增
- `-` 删除
- `*` 修改
- `!` 破坏性 / 需要数据迁移
- `🐛` Bug 修复

---

## v0.5.5 — 号池模块的 Postgres 兼容 + 配方重复自愈（2026-10-10）

| 类型 | 变更 |
|---|---|
| `🐛` | `controller/pool.go`：`top_ups.created_time` → `create_time`（列名写错，2 处） |
| `🐛` | `controller/pool.go`：`` `group` `` / `` `key` `` 反引号改为 `model.GroupCol()` / `model.KeyCol()` / `model.LogGroupCol()`（4+3 处） |
| `+` | `model/main.go`：导出 `GroupCol()` / `KeyCol()` / `LogGroupCol()`，把方言正确的标识符引用给 `controller` 用 |
| `+` | `model/pool_recipe.go`：`SeedDefaultPoolRecipes` 启动时自愈去重，清掉历史重复配方 |

### 症状

登录进后台后，**号池总览直接 500**：

```
panic: runtime error: invalid memory address or nil pointer dereference
  controller/pool.go:70
```

### 根因（两个，都属于"SQLite 能跑、PG 不行"）

**1. 列名对不上**：`top_ups` 表（GORM 由 `TopUp` 转出的表名）的实际列是 **`create_time`**，
代码却查 `created_time`。SQL 报列不存在 → GORM `.Row()` 拿不到行 → `.Scan()` 空指针。
→ 这解释了为什么是 **500/panic** 而不是普通报错。

**2. 反引号标识符**：`` Select("`group` as `group`") ``、`` Where("... AND `group` = ?") ``、
`` Where("`key` = ?") ``。`group` / `key` 在 PG 和 MySQL 都是关键字，必须引用，
但引号字符不同：**PG 用双引号，MySQL 用反引号**。

> 仓库里本来就有正确范式：`model/channel.go` 自己判断了 `common.UsingPostgreSQL`
> 来切换引号，`model/main.go` 也维护着 `commonGroupCol` / `commonKeyCol`。
> **只有 controller/pool.go 漏了**，直接硬写了反引号。

### 连带伤：配方被重复插入（19 → 57）

`upsertPoolRecipe` 用 `GetPoolRecipeByKey` 查有没有同 key 的配方，
查不到就 `Insert`。而那个函数用了反引号 → PG 下查询报语法错误 →
**"查询失败"被当成"配方不存在"** → 每次启动又插一批 → 3 次启动后
19 条配方变 57 条。

唯一索引没拦住，因为它是 `(key, deleted_at)` **复合**唯一索引，
而 `deleted_at` 为 NULL 时 **NULL 互不相等**，PG/SQLite 都允许重复插入。

**修法**：`SeedDefaultPoolRecipes` 开头做一次自愈去重
（只清 `deleted_at IS NULL` 的活跃行，同 key 留 `MIN(id)`）。

> 已核实 2026-10-10：
> - 部署后日志 `[pool] removed 38 duplicated recipe rows`，57 → 19
> - **幂等验证**：再重启一次，仍是 19/19，不再增长
> - 启动日志 SQLSTATE 报错 0 条、panic 0 条
> - `/api/pool/overview` 从 500 变 401（未登录的正常响应）

---

## v0.5.4 — 凭证模式下 Allow-Headers 不能用 `*`（2026-10-10）

| 类型 | 变更 |
|---|---|
| `🐛` | `middleware/cors.go`：凭证模式下改为回显 `Access-Control-Request-Headers`，修掉 v0.5.3 引入的预检失败 |

**v0.5.3 引入的回归**：v0.5.3 给面板 Origin 加了 `Allow-Credentials: true`，
但 `Access-Control-Allow-Headers` 还留在 `*`。CORS 规范里
**`Allow-Headers: *` 只在非凭证模式下是通配符**，凭证模式下会被当成字面量头名，
于是 axios 带的 `New-API-User` 等自定义头全被拒：

```
Request header field new-api-user is not allowed by
Access-Control-Allow-Headers in preflight response
```

表现是**登录页直接打不开接口**（连 `/api/status` 都过不去）。

**修**：凭证模式回显 `Access-Control-Request-Headers`；非凭证模式保持 `*`。

> 已核实 2026-10-10：用逐条断言脚本跑了 15 项检查全通过
> （预检 204 / Origin 回显 / Allow-Credentials / Allow-Headers 含 new-api-user /
> Method 含 GET,POST / 陌生 Origin 回 `*` 且无凭证 / 无 Origin 时 200）。

---

## v0.5.3 — 分域部署的登录态 + Postgres 方言（2026-10-10）

| 类型 | 变更 |
|---|---|
| `🐛` | 前端 axios 实例加 `withCredentials: true`（`web/src/helpers/api.js` 两处）：跨域请求不带凭证，session cookie 存不下也回不来 |
| `*` | `middleware/cors.go` 重写：命中 `FRONTEND_BASE_URL`/`TRUSTED_ORIGINS` 的 Origin 回显具体值并带 `Allow-Credentials: true`；其它 Origin 保持 `*` 且不带凭证 |
| `🐛` | `model/pool_recipe.go` 三处、`service/pool_worker.go` 一处的反引号标识符改为 GORM 方言感知写法 |

### 登录态（分域部署下「刷新即掉线」）

本项目登录态是 **session cookie**：`controller/user.go` 的 `setupLogin` 只写 session
（响应体里没有 token），`middleware/auth.go` 的 `authHelper` 优先读 `session.Get("username")`，
只有 session 为空时才回退去查 `Authorization` 里的 access token。

跨域 XHR 默认 `credentials: 'same-origin'`，即**不保存响应里的 Set-Cookie、也不回传**。
于是表现为：登录接口 200、UI 也跳进去了（用户信息来自响应体），但一刷新
`/api/user/self` 就 401，前端提示「未登录或登录已过期」。

同时 `*` 与凭证是互斥的：凭证模式要求 `Access-Control-Allow-Origin` 是具体 Origin。
原来 `AllowAllOrigins = true` + `AllowCredentials = true` 是无效组合，浏览器直接拒绝。

修法就是上表前两条：前端开 `withCredentials`，后端按 Origin 白名单回显。

### Postgres 方言（反引号）

**PostgreSQL 把反引号当运算符字符**（不像 MySQL 是标识符引号），
所以 `` WHERE `key` = ? `` 会被解析成「运算符 ` key 运算符 ` = ?」，
报 `syntax error at or near "="`；`` `key` IN (...) `` 则报 `at or near "IN"`。

受影响点（全部改为 `map[string]interface{}` / `clause.Eq` / `gorm.Expr`，
让 GORM 按当前方言自己加引号，PG→`"key"`、MySQL→`` `key` ``）：

- `model/pool_recipe.go` `GetPoolRecipeByKey` / `EnsureDefaultOption` /
  `SeedDefaultPoolRecipes` 里的两条 stub 清理
- `service/pool_worker.go` `incrementRecipeCounter`

还有一个**连带伤**：`EnsureDefaultOption` 的查询报错 → 代码以为选项不存在 →
执行 `Create` → 撞 `options_pkey` 唯一键冲突。修好引号后这个连带错自动消失。

> 已核实 2026-10-10：修复后线上启动日志里 SQLSTATE 报错从 19 条降到 **0 条**。

---

## v0.5.2 — 分域部署下前端交给外部托管（2026-10-10）

| 类型 | 变更 |
|---|---|
| `*` | `router/main.go` 的 `SetRouter`：去掉「master 节点强制忽略 `FRONTEND_BASE_URL`」的特例 |
| `*` | `SetRouter` 的重定向 `NoRoute`：`/api/*`、`/v1/*` 不再跳转，改回 JSON 404 |

**背景**：上游在 `SetRouter` 里写死
`if common.IsMasterNode && frontendBaseUrl != "" { frontendBaseUrl = "" }`，
即 master 节点永远兜底服务 `//go:embed` 进来那套前端。后果是「前端部署到 CDN、
后端只做 API」的分域部署在**单节点（=master）下无法生效**——设了环境变量也被清空，
`api` 域名上会一直挂着一份登录页。

本 fork 去掉该特例：只要设了 `FRONTEND_BASE_URL` 就生效，与节点角色无关。

**顺带修的**：原重定向逻辑把**所有**未匹配路径都 301 到前端，包括
`/api/does-not-exist` 这种——会把 API 客户端引到一个 HTML 页面。现在
`/api/*`、`/v1/*` 前缀的未注册路径走 `controller.RelayNotFound`，仍返回 JSON 404。

**影响面**：不设 `FRONTEND_BASE_URL` 时行为与之前完全一致（仍服务嵌入前端）。
只有显式开启分域部署的实例受影响。

---

## v0.5.1 — 分域部署 CORS 修复（2026-10-10）

| 类型 | 变更 |
|---|---|
| `🐛` | `router/api-router.go` 的 `SetApiRouter` 补挂 `middleware.CORS()` —— 修复 `/api/*` 真实请求不返回 `Access-Control-Allow-Origin` 的问题 |

**根因**：上游只在 `SetRelayRouter` 里用 `router.Use(middleware.CORS())` 全局挂 CORS，
但 gin 的中间件链是**注册时快照**（`RouterGroup.combineHandlers` 在注册路由那一刻拷贝
`group.Handlers`），而 `SetRouter` 里 `SetApiRouter` 排在 `SetRelayRouter` 之前执行 ——
所以 `/api/*` 的路由拿不到 CORS，`/v1/*` 和 dashboard 才有。

**为什么一直没被发现**：`OPTIONS` 预检看起来是正常的。因为大多数 `/api/*` 只注册了
GET/POST，OPTIONS 不匹配任何路由、落到 `NoRoute`，而 `engine.Use()` 会调用
`rebuild404Handlers()` 重建 NoRoute 的中间件链（那时 CORS 已在链上）。
于是「预检带 CORS 头、真实请求不带」——一个非常容易被误判成"已支持"的假阳性。

**影响面**：只影响「前端与后端分域部署」。同源部署（默认单体模式）不受影响。
分域部署下表现为：面板页能打开，但所有 `/api/*` 请求被浏览器按 CORS 拦截，
控制台报 `No 'Access-Control-Allow-Origin' header is present`。

---

## v0.5.0 — 文档化（2026-04-28）

| 类型 | 变更 |
|---|---|
| `+` | 新增 `docs/` 工程文档：8 节 / 22 篇 markdown，覆盖架构 / 开发 / 部署 / 使用 / 排错 / API / 安全 |
| `+` | ADR 决策记录：[`docs/05-architecture/decisions.md`](../05-architecture/decisions.md) 收录 10 条核心决策 |

---

## v0.4.3 — 注册自动化清理（2026-04-27）

| 类型 | 变更 |
|---|---|
| `-` | 删除 `service/pool_runner_anthropic.go` `pool_runner_openai.go` `pool_runner_mistral.go` `pool_runner_cohere.go` `pool_runner_together.go` `pool_runner_openrouter.go` `pool_runner_mock.go` `pool_runner_helpers.go` |
| `-` | 删除 `service/pool_browser.go`（chromedp 浏览器会话池） |
| `*` | `service/pool_runners.go` 注释更新：声明本仓库不再提供商业站点全自动 Runner |
| `*` | `model/pool_recipe.go::buildDefaultRecipeSeeds()`：所有商业站点 Recipe 默认 `ManualMode=true`，并加 5 条推荐 Recipe（Google AI Studio / Groq / Cerebras / DeepSeek / SiliconFlow） |
| `+` | 启动时一次性迁移：`UPDATE pool_recipes SET manual_mode=1 WHERE manual_mode=0 AND webhook_url=''` |
| `+` | `PoolRecipe.AutomationCapability` 字段（信息字段：`full`/`sms_paid`/`oauth_only`/`realname_only`/`manual`） |
| `🐛` | 删除 `chromedp` 残留进程导致 CPU 占用的隐患 |

**ADR**：[ADR-0002 删除所有 chromedp 自动 Runner](../05-architecture/decisions.md#adr-0002删除所有-chromedp-自动-runner)

---

## v0.4.2 — 路由 + 自动建渠道一致性（2026-04-26）

| 类型 | 变更 |
|---|---|
| `🐛` | **关键 bug**：所有自动 / 半自动建 channel 改用 `ch.Insert()` 而非 `model.DB.Create(ch)`。后者跳过 `AddAbilities()`，导致路由表 `abilities` 永远没有对应行，调用上游必现 `No available channel for model X under group default` |
| `*` | `controller/pool.go::AddPoolAccount` `BindPoolAccountChannel` `ManualSubmitJobResult`、`service/pool_worker.go::runJob` 全部改用 `ch.Insert()` |
| `+` | `PoolRecipe.DefaultModels`（text，逗号分隔）字段 + 19 条内置 Recipe 全部填好 |
| `*` | `channels.group` 永远写 `"default,<provider>"` 而不是 `"<provider>"`，确保 default 用户能命中 |
| `+` | `PoolAccount` 编辑改 `key_raw` 时同步更新关联 `Channel.Key`（避免改 PoolAccount 但渠道仍用旧 key 的鬼魂状态） |

**ADR**：[ADR-0004](../05-architecture/decisions.md#adr-0004recipe-加-defaultmodels-字段) / [ADR-0005](../05-architecture/decisions.md#adr-0005channel-写入必须用-chinsert不允许-dbcreatech) / [ADR-0006](../05-architecture/decisions.md#adr-0006channelgroup-用逗号双值-defaultprovider)

---

## v0.4.1 — 一键绑定 + 完整 Key 复制（2026-04-25）

| 类型 | 变更 |
|---|---|
| `+` | `GET /api/pool/accounts/:id/full_key`：管理员可拿明文 Key（用于复制） |
| `+` | `POST /api/pool/accounts/:id/bind`：自动按 name 匹配 / 多候选返回 / 强制指定 channel_id |
| `*` | 前端「上游账号」表的「渠道」列：未绑定显示「一键绑定」按钮，绑定后显示「已绑定 #N」 + Tooltip |
| `*` | 前端 Modal 编辑表单：`destroyOnClose + key={editing?.id} + initValues={editing}`，修复"编辑数据空"bug |

---

## v0.4.0 — 删除「风控规则」（2026-04-24）

| 类型 | 变更 |
|---|---|
| `!` | **数据迁移**：删除 `pool_risk_rules` 表（在 `model/main.go::AutoMigrate` 移除） |
| `-` | 删除 `controller/pool.go::Get/Add/Update/DeletePoolRiskRule` |
| `-` | 删除 `model/PoolRiskRule` 类型 + 表 |
| `-` | 删除 `web/src/pages/Pool/Risk.jsx` |
| `-` | 删除 `router/pool-router.go::/api/pool/risk*` 路由 |
| `*` | SiderBar 一级菜单 6 → 5 子菜单（总览 / 上游账号 / 自动注册 / 收支对账 / 巡检告警） |

**ADR**：[ADR-0008 删除「风控规则」二级菜单](../05-architecture/decisions.md#adr-0008删除风控规则二级菜单)

---

## v0.3.0 — 自动化基础设施（2026-04-22）

| 类型 | 变更 |
|---|---|
| `+` | `service/pool_email_provider.go`：抽象 `EmailProvider` 接口 + `mailtmProvider` 实现 |
| `+` | `service/pool_sms_provider.go`：抽象 `SmsProvider` 接口 + `fivesimProvider` 实现 |
| `+` | Option 表新增：`PoolEmailProvider` / `PoolSmsProvider` / `PoolFivesimApiKey` / `PoolSmsActivateApiKey` |
| `+` | `GET / PUT /api/pool/automation/config` |
| `+` | 前端「自动注册」页 ⚙️ 自动化设置 Modal，可配 5sim API key + 提供商选择 + 实时余额 |
| `+` | Worker 状态接口 `GET /api/pool/worker/status`：返回 inflight / max_concurrent / runner_keys |

---

## v0.2.5 — Telegram 默认值预置（2026-04-20）

| 类型 | 变更 |
|---|---|
| `+` | 启动时调 `service/SeedDefaultTelegramConfig`：默认 Bot Token + Chat Id 写入 options 表（仅在 key 不存在时） |
| `*` | `service/pool_health.go::SendTelegramMessage` 改进错误解析：`chat not found` / `Unauthorized` 直接 surface 到前端 |
| `+` | `Alerts.jsx` 错误展示更友好 |

**ADR**：[ADR-0007 Telegram 通道默认值预置](../05-architecture/decisions.md#adr-0007telegram-通道默认值预置)

---

## v0.2.0 — 号池管理 5 子菜单（2026-04-18）

| 类型 | 变更 |
|---|---|
| `+` | 一级菜单「号池管理」+ 5 子菜单（总览 / 上游账号 / 自动注册 / 收支对账 / 巡检告警 / 风控规则[v0.4.0 删除]） |
| `+` | 表：`pool_accounts` `pool_recipes` `pool_jobs` `pool_alert_history` |
| `+` | 完整后端：`controller/pool.go` `service/pool_*.go` `model/pool_*.go` `router/pool-router.go` |
| `+` | 完整前端：`web/src/pages/Pool/Overview.jsx` `Accounts.jsx` `Recipes.jsx` `Billing.jsx` `Alerts.jsx` `PoolPageLayout.jsx` |
| `*` | `web/src/components/layout/SiderBar.jsx`：`isAdmin()` 用户可见 |
| `*` | `web/src/App.jsx`：lazy 路由 `/pool/*` |

**ADR**：[ADR-0001 走 fork + 自编译路线](../05-architecture/decisions.md#adr-0001走-fork--自编译路线不走-plugin--sidecar) / [ADR-0003 一级菜单](../05-architecture/decisions.md#adr-0003号池管理作为一级菜单不走二级--浮窗)

---

## v0.1.0 — fork 起点（2026-04-15）

- fork from [QuantumNous/new-api](https://github.com/QuantumNous/new-api) main
- 编译产物 `new-api-macos`（macOS arm64）
- 数据库 `one-api.db`
- 启动脚本 `start.command`
- 默认管理员 `root` / `123456`

---

## 不会进入 changelog 的事

- 上游 new-api 自身改动
- 改注释 / 文档（除非破坏 API 形状）
- 重命名变量 / 内部重构
- 调整 lint / 格式化

---

## 升级建议

每次发布在本地建一个 git tag：

```bash
cd <REPO>
git tag v0.5.0 && git push --tags
```

数据迁移（`!` 标记）的版本要：
1. 部署前**完整备份** `one-api.db`（[`backup-restore.md`](../06-operations/backup-restore.md)）
2. 在测试库上跑一次启动 + 巡检 + 自检 API
3. 出问题立刻回滚（替换 binary + 还原 db）
