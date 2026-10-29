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

## v0.6.5 — AISA 搜索与 SEO Capability（2026-10-29）

| 类型 | 变更 |
|---|---|
| `+` | 接入 AISA 的 16 个同步能力：Similarweb 流量/渠道/引荐/相似站点，DataForSEO 外链/排名/竞品/关键词/SERP/AI 提及/单页审计 |
| `+` | Manifest 通用字段：`request.body_wrap`、`request.headers`、`billing.quantity_header`、`billing.reserve_usd`、`response.error_code_path`、`response.error_map` |
| `*` | POST 的 `request.consts` 只进入 body，不再意外混入 query；GET 仍按声明进入 query |
| `+` | 响应头用量按实际成功成本结算；同步 per-unit 可固定预扣，缺少或非法数量头时按 reserve 兜底并记录 `usage_source=reserve_fallback` |
| `+` | 上游 402 余额、401/鉴权依赖和 402 价格上限映射为稳定公共错误；不回传上游 message，日志保留可区分的上游错误类别 |
| `+` | `/v1/capabilities` 的 `per_unit` 条目增加 `billing.max_usd`，值等于 `reserve_usd` |
| `*` | `set-capability-secret.sh` 支持任意合法 `CAPABILITY_*` 变量名；不带参数保持 Bright Data 默认行为 |
| `+` | compose 透传 `CAPABILITY_AISA_API_KEY` |
| `+` | 离线测试覆盖数组 body、静态/幂等头、query 隔离、response header 计费、reserve fallback、零成本、退款、错误映射、脱敏和 16 个 Manifest 校验 |

### 免费 quote 验证

- 16 个能力默认请求全部 HTTP 200 且返回 `object=cost_estimate`
- 默认预估合计 `$3.625236`
- `site-referrals-v1` 默认 5 条预估 `$1.50`；20 条原预估 `$6.00`，因此降低默认条数
- `similar-sites-v1` 默认 2 条预估 `$0.75`

---

## v0.6.4 — Bright Data 网页抓取 Capability（2026-10-23）

| 类型 | 变更 |
|---|---|
| `+` | 新增同步能力 `web-unlock-v1`：Bright Data Web Unlocker Direct API，Wayfair HTTPS 页面 → `{status, html}` |
| `+` | Manifest 通用字段：`request.required`、`request.url_inputs`、`request.env`、`response.success`、`status_header`、`error_header` |
| `*` | POST body 的 `consts` 不再只服务 query；常量优先于用户输入，`request.env` 可绑定服务端秘密/配置 |
| `*` | URL 输入拒绝非 HTTPS、非允许域名、userinfo、IP 字面量和非默认端口；预扣费前返回 `400 invalid_request` |
| `*` | Bright Data HTTP 200 错误封装按 `x-brd-status-code` / `x-brd-error` 识别；`429/502/503/504` 最多重试一次 |
| `*` | 目标非 200、正文为空、上游错误、超过 8 MiB 均失败退款；错误响应不含上游正文 |
| `🐛` | `weather-v1` 声明 `latitude` / `longitude` 必填，修掉 `input: {}` 落到 `502 invalid_upstream_json` |
| `+` | 部署 compose 透传 `CAPABILITY_BRIGHTDATA_API_KEY` / `CAPABILITY_BRIGHTDATA_ZONE`；新增交互式密钥设置脚本 |
| `+` | 离线测试覆盖成功、重试、域名/URL 预检、常量覆盖、退款、8 MiB 上限和 3 个嵌入 Manifest |

---

## v0.6.3 — Capability 异步 per-unit + content（2026-10-23）

| 类型 | 变更 |
|---|---|
| `+` | 异步 `per_unit`：提交时按 `estimate_path` 预扣，完成时按 `quantity_path` 结算差额；manifest 必须提供 estimate_path |
| `+` | `GET /v1/tasks/:id/content`：按 manifest 的 content endpoint 下载文件，带 timeout/大小限制/鉴权/SSRF |
| `*` | Task.Data 记录 billing 快照（预扣、单价、单位、group ratio、settled），支持终态补扣/退款 |
| `+` | 测试：per-unit 预估 1 页 → 实际 3 页，验证差额补扣；content 返回 text/plain 文件 |

---

## v0.6.2 — 第三方 Capability API Phase 0/1（2026-10-22）

| 类型 | 变更 |
|---|---|
| `+` | `/v1/capabilities`、`/v1/capabilities/:id`、`/v1/tasks`、`/v1/tasks/:id`：用现有 `sk-` token 接入非 LLM 第三方 API |
| `+` | Manifest/Registry/HTTP executor：同步 JSON、异步 submit/poll、auth、JSON path 映射、timeout、响应大小、SSRF、重试 |
| `+` | `capability_idempotencies` 表：`(user_id, capability_id, key_hash)` 唯一索引，claim→submit→task id；并发安全 |
| `*` | Capability 任务使用 `tasks.platform=capability`，并从视频任务轮询器中排除，避免误判 channel 0 |
| `+` | 免费试点：`weather-v1`（Open-Meteo 同步）、`ssl-analyze-v1`（SSL Labs 异步） |
| `*` | `PreConsumeTokenQuota` 改为按 token id 查询，避免重复按 key 查询和 key 进入 SQL 日志 |
| `*` | 文档：新增 `capability-api.md`；`RULES.md` 标记 Phase 0/1 已落地 |

### 线上验证

- 同步天气：200，返回温度/风速
- 异步 SSL Labs：缓存任务 16 endpoints；新分析任务最终 4 endpoints，grade 全 B
- 同 idempotency key 重复提交：同 task id，`replayed=true`
- 原有 chat completion：200，内容 `OK`

---

## v0.6.1 — 管理员代用户创建 Token API（2026-10-20）

| 类型 | 变更 |
|---|---|
| `+` | `POST /api/user/:id/token`：管理员使用 access token + `New-Api-User` 为指定用户创建 token，返回完整 `data.key` |
| `+` | `tokens.idempotency_name`（可空）+ `idx_tokens_user_idempotency (user_id, idempotency_name)` 唯一索引：同名请求幂等，并发只建一条；软删除时清空，允许同名重建 |
| `*` | `controller/token.go` 抽出 `validateTokenForCreate` / `createTokenForUser`，普通 `/api/token/` 与代建接口共用名称、额度、数量上限和写入逻辑 |
| `*` | Token 插入与按 key 查询使用 `gorm/logger.Discard`，避免唯一索引冲突/慢查询日志打印完整 key；管理员接口日志只记录 caller/target user id |
| `+` | 测试：`controller/admin_user_token_test.go`（成功、非管理员、header 不匹配、目标不存在/禁用、角色层级、幂等、并发、删除后重建、共享校验、key 不落错误/日志、索引存在） |
| `*` | 文档：新增 `docs-pool/07-reference/admin-user-token.md`；更新数据模型与文档目录 |

> 迁移：重启 New API 时 GORM AutoMigrate 会自动增加 `idempotency_name` 列与唯一索引，无需手写 SQL。

---

## v0.6.0 — 主上游切换 Wokey（2026-10-20）

| 类型 | 变更 |
|---|---|
| `+` | 新渠道 `wokey`（id `2`）：`https://api.wokey.ai`，OpenAI 兼容，priority 100，**42 个在售模型**（40 文本 + 2 图片） |
| `+` | `ops/sync-wokey-pricing.py`：从 `/v1/models/pricing` 读实时价，按 **1:1（不加价）** 整体写入 ModelRatio / CompletionRatio / CacheRatio / CreateCacheRatio / ModelPrice / ImageSizePrice |
| `*` | apimaster 渠道 id `1` 停用（`status=2`），其 77 条 abilities 全部 `enabled=false`；`/api/pricing` 从 77 个模型收敛到 Wokey 的 42 个 |
| `🐛` | `setting/ratio_setting/model_ratio.go`：显式 `CompletionRatio` 现在**优先于**硬编码默认值（原顺序会让 gpt-5.6-luna/sol/terra 的输出倍率强制变成 8，和上游真实价不符） |
| `*` | 文档：§8.5 标记主上游已切换；价格、验证、回滚调研稿保存在本地但未提交 |
| `!` | 视频模型（grok-imagine-video-1.5 / -lite）**暂未上架**：Wokey 按 `duration_seconds × video_resolution` 计费，现有 Sora 适配器只认 `seconds` / `size`，直接上会算错价 |

### 验证

- `/api/pricing` 返回 42 个模型；42 个模型的输入/输出/缓存/图片单价与 Wokey 逐项 **0 差异**
- 公开 `/v1/chat/completions`（`claude-sonnet-5`）200
- 公开流式 `gpt-5.6-terra` 200（首测遇到一次上游 overload，重试成功）
- 公开 `/v1/images/generations`（`grok-imagine-image-2.0`）200，返回 `b64_json`
- 扣费对照：`gpt-5.6-terra` 扣 `$0.00001`，Wokey 实际 `$0.00001`；grok 图片扣 `$0.01`，Wokey 实际 `$0.01`

---

## v0.5.9 — admin 后台只给 root + 下线 console.tokwork.com（2026-10-20）

产品决定：`admin.tokwork.com` **只给项目主人（root）用**，客户一律走营销站 `/console`；
`console.tokwork.com` 这个旧域名不再需要，整个下线。

| 类型 | 变更 |
|---|---|
| `🐛` | `web/src/helpers/bootstrapSession.js`（新增）+ `web/src/index.jsx`：渲染前用 cookie 调 `/api/user/whoami` 恢复身份。修掉「营销站登录 → 跳 admin → 又被踢回登录」的死循环 |
| `🐛` | `web/src/helpers/api.js`：request 拦截器**每次请求**重算 `New-API-User` 头（原来只在 axios 实例创建时取一次） |
| `+` | 同一个 `bootstrapSession`：**root 门禁** —— `role < 100` 的账号不渲染 SPA，直接送去 `www.tokwork.com/console`；未登录则送去营销站登录（`next` 回 admin）。账号类路径（`/login` 等）仍交给 `AuthSiteRedirect` |
| `*` | `ops/deploy/docker-compose.yml`：`FRONTEND_BASE_URL` 从 admin 改成 `https://${SITE_DOMAIN:-www.tokwork.com}` —— `api.tokwork.com/` 这类网页路径现在 301 到营销站，不再把人引到后台；`TRUSTED_ORIGINS` 前面固定拼上 `https://${ADMIN_DOMAIN}`（它不再是 FRONTEND_BASE_URL，漏了后台就登不上） |
| `*` | `ops/deploy/gen-env.sh`：新增 `SITE_DOMAIN`、`TRUSTED_ORIGINS` 默认值 |
| `-` | 营销站（`homepage/`）：`worker.mjs` 删掉 console 域名 301 逻辑，`wrangler.jsonc` 删掉该自定义域 |

### 影响面

- 门禁只在界面层；后端权限仍按角色校验（客户本来就调不了管理接口）。
- `console.tokwork.com` 不再解析，老链接直接失效（顺带消除了它 `/console` → `/console/console` 404 的 bug）。
- 线上 `.env` 的 `TRUSTED_ORIGINS` 改为 `https://www.tokwork.com,https://tokwork.com`（去掉 console）。

## v0.5.8 — 注册时保留邮箱 + 客户门户改用 Kumo 组件（2026-10-10）

| 类型 | 变更 |
|---|---|
| `🐛` | `controller/user.go` 的 `Register`：**用户填了邮箱就落库**。上游只在 `EmailVerificationEnabled` 为真时才存邮箱，而我们没开邮箱验证 —— 结果是注册表单推荐用户填邮箱（找回密码要用），库里却永远是空，`/api/reset_password` 永远匹配不到人。实测：注册时带 email，查库确认已存 |
| `*` | 客户门户（在营销站仓库 `homepage/`）改成用 `@cloudflare/kumo` 的**组件**而不是手搓 CSS：`Button` / `Input` / `Table` / `LayerDialog` / `Badge` / `Empty` / `Meter` + `@phosphor-icons/react`。之前只用了它的设计令牌，观感"像但不够精致"（缺 focus ring / loading / 焦点陷阱 / 空状态）—— 见 lessons-learned H8 |

> 邮箱留了空的情况：用户不填就不写，`/api/reset_password` 依然找不到人（这一页的文案
> 已经说明"没有邮箱就只能联系客服"）。

## v0.5.7 — 自研客户门户（whoami 接口 + 账号入口搬去营销站 + 面板只留亮色）（2026-10-10）

产品决定：**客户看到的一切自己写**，不用 new-api 自带的 SPA。
客户只需要「生成自己的 Key / 看调用历史 / 看模型价格」，而那个 SPA 是管理员后台。
门户落在营销站（`www.tokwork.com/console/**`，Next.js + Kumo 令牌），
`console.tokwork.com` 301 过去；`admin.tokwork.com` 上的 SPA 只留给自己用。

| 类型 | 变更 |
|---|---|
| `+` | `controller/user.go`：`WhoAmI` —— 只认 session cookie、**不要求 `New-API-User` 头**，返回当前登录者身份。自研门户靠它解决"要先有 id 才能拼头、而 id 只在 cookie 里"的死循环 |
| `+` | `router/api-router.go`：`GET /api/user/whoami` 路由（**别给它加 UserAuth()**，加了就回到死循环） |
| `*` | `web/src/App.jsx`：`/login`、`/register`、`/reset`、`/user/reset` 四条路由改成 `AuthSiteRedirect` —— 账号入口统一由营销站负责（`helpers/auth.jsx`，可用 `VITE_AUTH_SITE_URL` 覆盖站点地址） |
| `*` | `web/src/context/Theme/index.jsx`：**夜间模式下线**，永远亮色；挂载时清掉 `body[theme-mode]` / `html.dark` / 旧的 localStorage 值。`setTheme` 变成空操作（几十处组件还在用它） |
| `-` | `web/src/components/layout/headerbar/ActionButtons.jsx`：去掉主题切换按钮 |
| `*` | `web/src/index.css`：声明 `color-scheme: light`，让原生控件（滚动条、日期选择器）也走亮色 |
| `🐛` | `web/src/components/layout/PageLayout.jsx`：标签页标题和 favicon 改成以 `/api/status` 的 `system_name` / `logo` 为准。原来只在挂载时读一次 localStorage，新访客本地没缓存 → 页头写着自家品牌、标签页却是上游默认名 |

### 影响面

- 客户侧不再出现 new-api 自带界面（唯一残留：admin SPA 页脚那行「设计与开发由 New API」
  署名 —— 见 `AGENTS.md` 规则 5，刻意保留）。
- 面板品牌换成自己的（`SystemName` / `Logo` 两个 option），顶栏导航全关、页脚换成自有文案。
- 旧链接 `console.tokwork.com/**` 自动跳到 `www.tokwork.com/console/**`。

---

## v0.5.6 — 控制台 Kumo 化 + 营销站 SSO + 尾斜杠 CORS 修复（2026-10-10）

| 类型 | 变更 |
|---|---|
| `*` | `web/src/index.css`：新增 Kumo 对齐的设计令牌（品牌蓝 `--semi-blue-0..9` 亮/暗两套、圆角 xs2/sm4/md6/lg8、`--semi-color-border` 10%、系统字体栈），硬编码的 `border-radius: 10px` 改走令牌 |
| `+` | `web/src/index.css`：`.tk-card` / `.tk-tile` / `.tk-card--selected` —— Kumo 的「1px 描边 + 小圆角」表面，替代 `rounded-2xl` + 投影 |
| `*` | `web/src/components/table/model-pricing/**`、`components/auth/{LoginForm,RegisterForm}.jsx`：模型广场卡片/图标块/骨架屏/登录注册卡换到上述表面类 |
| `*` | `PricingVendorIntro(.Skeleton).jsx`：供应商介绍条去掉蓝色渐变封面图，改中性表面（`public/cover-4.webp` 不再被引用） |
| `*` | `web/index.html`：新增 SSO 引导脚本 —— 从 `?sso_user=<id>` 恢复 localStorage 里的用户 id（axios 的 `New-API-User` 头依赖它），随后抹掉参数 |
| `🐛` | `router/api-router.go`：为 `/api/log/self/`、`/api/data/self/`、`/api/mj/self/` 注册尾斜杠别名（gin 的 301 丢 CORS 头 → 跨域白屏）<br>前端同步去掉这三处的尾斜杠（`useDashboardData.js` / `useUsageLogsData.jsx` / `useMjLogsData.js`） |
| `🐛` | `useDashboardData.js` 的 `loadQuotaData()` 补 `catch`：请求失败返回空数组，不再 unhandled rejection + 白屏 |
| `🐛` | `setting/ratio_setting/image_size_price.go`：`MatchImageSizeVariant` 支持「档位 + 质量」的价表（`1K low` / `1K medium`）与请求里的 `quality` 字段；`relay/helper/price.go` 一并读 `quality` |
| `+` | `setting/ratio_setting/image_size_price_test.go`：覆盖标准档位、质量档、非标准档位名三种价表形状 |

### 为什么要改图片尺寸计价（`MatchImageSizeVariant`）

上游 apimaster 的价表并不都是 `1K/2K/4K` 这种干净形状，实际见过的还有
`1K low` / `1K medium`（grok-imagine-image-2.0）、`fast/relax/turbo`（midjourney）、
`480P/720P`（视频模型）。

旧逻辑只认标准档位名，其它一律落到 `pickCheapest()`（**全局最低价**）——
也就是说 `grok-imagine-image-2.0` 无论出多大的图都只收 `$0.012`，
而 2K medium 的上游成本是 `$0.096`。**按次亏钱**。

现在按「精确档位名 → 档位名 + 请求里的 quality → 该档位下最低价」三级匹配；
请求没带 quality 时取该档最低价（宁可少赚，不要事后跟客户争）。
回到 `grok` 的例子：1K 最低 `$0.048`（原来 `$0.012`），2K 最低 `$0.072`。

> 还没解决：请求带 `quality: high` 而价表只有 `low/medium` 时会退回该档最低价；
> `fast/relax/turbo`、`480P/720P` 这类"档位名跟尺寸无关"的模型仍会退化成全局最低价。
> 这些模型目前都没接渠道（没卖），接之前要再补一轮。

### 影响面

- 控制台（admin / console）界面外观变化：圆角变小、描边变细、字体换系统栈。
- 客户可以从营销站登录框直接进控制台（`TRUSTED_ORIGINS` 增加两个营销站域名）。
- 客户端的「数据看板 / 使用日志」从白屏恢复。
- 图片模型（尤其 grok）的按次计费口径变化。

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
