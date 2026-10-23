# 当前进度与后续规划

> 号池主体截至 **2026-04-28**；Capability 补充核实于 **2026-10-23**。
> 这个文档**每次发版都要更新**。看这一页 = 知道现在能做什么、还差什么、下一步做什么。

---

## 1. 一句话现状

> **号池管理 5 子菜单全部跑通，半自动注册管线 + 自动建渠道 + 一键绑定 + Telegram 告警 + 收支对账 全部可用。**
> **不内置任何商业站点的全自动注册** —— 所有 19 条内置 Recipe 走"3-10 分钟人工 + 系统自动入号池建渠道"。

---

## 2. 已完成（v0.1 → v0.5）

### 2.1 基础设施
- [x] fork 自 [QuantumNous/new-api](https://github.com/QuantumNous/new-api)，本地编译产物 `new-api-macos`
- [x] 双击 `start.command` 启动，单进程 + SQLite + 嵌入前端
- [x] 首次访问的初始化向导：管理员账号 / 密码由用户自设，**系统没有内置默认账号**
- [x] 数据库 4 张新表：`pool_accounts` / `pool_recipes` / `pool_jobs` / `pool_alert_history`
- [x] 一级菜单「号池管理」+ 5 子菜单（仅 admin 可见）

### 2.2 上游账号管理
- [x] CRUD（增删改查 + 软删）
- [x] Key 入库脱敏，独立 `GET .../full_key` 拿明文
- [x] **自动建 channel**：表单勾「同步建渠道」+ 选 type → 系统建带 `tag=pool`/`pool-manual`/`pool-auto` 的 channel
- [x] **一键绑定**：自动按 name 匹配 / 多候选弹选择器 / 强制指定 channel_id
- [x] 改 `key_raw` 同步刷 `channels.key`
- [x] 列表按 provider 过滤 + 余额/到期/状态展示

### 2.3 Recipes & 注册管线
- [x] 19 条内置 Recipe（涵盖 OpenAI / Anthropic / Gemini / DeepSeek / Groq / Cerebras / SiliconFlow / OpenRouter / Together / Mistral / Cohere / xAI / Moonshot / 智谱 / 讯飞 / 百度 / 阿里 / Replicate / HuggingFace）
- [x] 全部填好 `DefaultModels`（自动建 channel 时立即可路由）
- [x] 全部带 `manual_steps` / `required_materials` / `doc_url` / `output_format` / `difficulty`
- [x] **半自动管线**：入队 → 拿步骤 → 线下完成 → 「录入结果」回填 → 自动建 PoolAccount + Channel + abilities
- [x] Job 状态机：pending / manual_pending / running / success / failed / cancelled
- [x] 重试 / 取消
- [x] 外部 worker 回调接口（带 token 鉴权）
- [x] Worker 状态查询接口（max_concurrent / inflight / runner_keys）
- [x] EmailProvider / SmsProvider 抽象 + 默认实现（mailtm / 5sim / mock）
- [x] 自动化设置 Modal（前端可改 5sim key / 切换 provider，**不暴露明文**）

### 2.4 路由 / 渠道一致性
- [x] **关键修复**：所有自动 / 半自动建 channel 用 `ch.Insert()`（不是 `DB.Create`），保证 `abilities` 表立即写入
- [x] **关键修复**：`channel.Group = "default,<provider>"` 双值，default 用户立即可路由
- [x] **关键修复**：`channel.Models` 来自 `recipe.DefaultModels`，不留空
- [x] tag 区分 channel 来源：`pool` / `pool-auto` / `pool-manual`

### 2.5 收支对账（Billing）
- [x] today / 7d / 30d 周期切换
- [x] 收入（topups）/ 用户消耗（logs.quota）/ 估算上游成本（默认 30%）/ 毛利
- [x] 按 model 聚合
- [x] 按 group 聚合（带每组毛利）
- [x] 上游占比 ratio 走 option `PoolBillingUpstreamRatio`，可热改

### 2.6 巡检告警（Alerts）
- [x] cron 每 5 分钟自动巡检
- [x] 「立即巡检」手动触发
- [x] 5 条规则：channel_down / balance_low / fail_rate_high / expire_soon / health_check
- [x] 24h dedup 防重复推送
- [x] **Telegram 默认 token + chat_id 预置**（[ADR-0007](../05-architecture/decisions.md#adr-0007telegram-通道默认值预置)）
- [x] Telegram 配置 GET / PUT / TEST 三接口
- [x] 告警历史筛选 / 标记解决

### 2.7 总览（Overview）
- [x] 健康分（active accounts / open alerts / 上游分布加权）
- [x] 今日营收 / 消耗 / 毛利
- [x] Worker 状态卡
- [x] Telegram 状态卡
- [x] 按 provider 分组的账号余额聚合

### 2.8 一些不显眼但救命的细节
- [x] 表单 Modal 三件套修复（`destroyOnClose + key + initValues`）
- [x] 全部内置 Recipe 启动时迁移：`UPDATE pool_recipes SET manual_mode=1 WHERE manual_mode=0 AND webhook_url=''`
- [x] 所有 API key 字段在 GET 接口返回时脱敏（`****1234`）
- [x] PUT 接口支持 `[unchanged]` 哨兵值，避免掩码值反向覆盖原始 key

### 2.9 文档（v0.5）
- [x] `docs/` 目录 8 节 / 22+ 篇 markdown：
  - 01-overview（架构 / 仓库 / 术语）
  - 02-development（环境 / 加 Recipe / 编码规范）
  - 03-deployment（本地 / 生产）
  - 04-usage（管理员入门 / 号池管理详解 / 19 Recipe 操作清单）
  - 05-architecture（数据模型 / 路由原理 / 注册管线 / ADR 决策）
  - 06-operations（巡检告警 / 备份恢复 / 排错手册）
  - 07-reference（API / Changelog / 踩坑大全 / 进度规划）
  - 08-security（凭证 / 暴露面 / 应急响应）
  - 09-contributing（commit / PR / 风格）

### 2.10 第三方 Capability（2026-10-23）

- [x] 同步 JSON：`weather-v1`、`web-unlock-v1`
- [x] 异步任务：`ssl-analyze-v1`
- [x] 异步 per-unit 预估/差额结算与 `/v1/tasks/{id}/content`
- [x] Manifest 必填字段、URL allowlist、常量/环境变量 body 覆盖、业务成功条件
- [x] Bright Data Web Unlocker 仅开放 Wayfair HTTPS 域名；目标非 200/空正文失败退款
- [ ] 多供应商路由、后台 Manifest 编辑、SSE 流式仍是计划

---

## 3. 已删除 / 已废弃

| 项 | 删除原因 | 时间 |
|---|---|---|
| `pool_runner_anthropic/openai/mistral/cohere/together/openrouter/mock.go` | Cloudflare Turnstile / hCaptcha 必败（[ADR-0002](../05-architecture/decisions.md#adr-0002删除所有-chromedp-自动-runner)） | v0.4.3 |
| `pool_browser.go`（chromedp 浏览器池） | 无 Runner 引用 | v0.4.3 |
| `pool_runner_helpers.go` | 无 Runner 引用 | v0.4.3 |
| 「风控规则」二级菜单 + `pool_risk_rules` 表 | new-api 原生有 RPM/TPM/限速，重复造轮子（[ADR-0008](../05-architecture/decisions.md#adr-0008删除风控规则二级菜单)） | v0.4.0 |
| `debug-mock` Recipe + Runner | 干扰生产数据展示 | v0.4.3 |

---

## 4. 已知缺口（不一定要做，但要心里有数）

### 4.1 自动化层
- [ ] **没有**任何商业站点的全自动 Runner（chromedp 路线已放弃）
- [ ] 5sim 余额低告警**没有**，目前要靠人工去自动化设置 Modal 看
- [ ] mail.tm 邮箱 30 分钟过期，跨段流程**没有**自动续期
- [ ] 没接入打码服务（2captcha / anti-captcha），所以即使要重做自动化，captcha 仍是死路
- [ ] 没接住宅代理池，IP 单点暴露

### 4.2 审计层
- [ ] 管理员对 PoolAccount / Channel 的 CRUD **没有独立审计表**（只有 logs 表的 user 调用日志）
- [ ] `GET .../full_key` 调用**没记录到审计日志**（合规风险点）

### 4.3 计费层
- [ ] `PoolBillingUpstreamRatio` 是单一全局比例，**不分 provider** —— DeepSeek 上游成本 80%，OpenAI 30%，混在一起估算不准
- [ ] 没接入官方账单（OpenAI Usage API / Anthropic billing API），上游成本永远是估算
- [ ] PoolAccount.balance_usd 不会自动衰减（用户调一次 → 账单不会自动扣余额，只能人工每天手改）

### 4.4 通知层
- [ ] 仅 Telegram，**没有**钉钉 / 飞书 / 企微 / Slack
- [ ] Telegram 消息无 markdown 转义（含特殊字符可能 parse error）

### 4.5 多实例 / HA
- [ ] cron + worker 跑在每个进程上，**多实例会重复触发** —— 想横向扩容必须先做 leader election（Redis lock 即可）
- [ ] SQLite 单点，并发 ≥ 50 RPS 写入会瓶颈 —— 已在 [`03-deployment/production.md`](../03-deployment/production.md) 写了 Postgres 迁移方案，没实操过

### 4.6 UI / UX
- [ ] 「号池管理 → 自动注册」表单字段较多，没做 wizard 分步引导
- [ ] 「上游账号」批量操作（批量启停 / 批量改 group）**没有**
- [ ] 「告警历史」没有按日聚合视图，多了刷起来累
- [ ] 移动端**未适配**

### 4.7 国际化
- [ ] 全中文，**未做**多语言。new-api 原生有 i18n，号池模块的 zh/en 文案没写。

---

## 5. 下一步可做（按 ROI 排）

> 下面把"做什么 + 估时 + 价值"列清楚，挑能在 1-2 天内完成 + 收益高的先做。

### 🟢 高 ROI / 低成本（半天 - 1 天能做）

| # | 事 | 估时 | 价值 |
|---|---|---|---|
| 1 | **PoolAuditLog 表 + 关键路径埋点** | 0.5 天 | 合规心安，事后追溯"谁改了哪个 Key" |
| 2 | **`PoolBillingUpstreamRatio` 按 provider 分别配置** | 0.5 天 | 毛利数字立即更准 |
| 3 | **5sim 余额低告警**（< 50 RUB 推 Telegram） | 0.3 天 | 不再"出事才发现 SMS 没钱了" |
| 4 | **「号池管理 → 上游账号」批量启停** | 0.3 天 | 上游突然全 ban 时秒禁 |
| 5 | **告警按日聚合视图 + CSV 导出** | 0.5 天 | 周报 / 月报方便 |
| 6 | **`PoolBillingUpstreamRatio` 全局可视化滑杆**（前端而不是 SQL 改） | 0.3 天 | 老板能自己调 |

### 🟡 中等 ROI（1-2 天）

| # | 事 | 估时 | 价值 |
|---|---|---|---|
| 7 | **接入飞书 / 钉钉 webhook**（仿 Telegram 实现） | 1 天 | 国内团队群里直接收告警 |
| 8 | **多实例支持**：cron leader election + Redis 锁 | 1.5 天 | 接近高可用 |
| 9 | **Postgres 迁移脚本** + 一键导入 SQLite 数据 | 1.5 天 | 真要上量再切，但脚本备好 |
| 10 | **Pool 模块 i18n（中英）** | 1 天 | 团队有外籍 / 走出海路线 |
| 11 | **Recipe 注册流的 wizard 分步引导**（特别是 manual_steps） | 1.5 天 | 新人 5 分钟完成第一次注册 |
| 12 | **`/api/pool/health` 公开监控端点**（不带数据，只 200/500） | 0.3 天 | 接 uptime monitor 用 |

### 🔴 大工程（≥ 3 天，风险高）

| # | 事 | 估时 | 风险 / 价值 |
|---|---|---|---|
| 13 | **真接入 OpenAI / Anthropic billing API** 替代估算 | 3 天 | 各家账单 API 形状不一，维护成本高，但价值最大 |
| 14 | **接打码服务 + 住宅代理重做 1-2 个商业站点 Runner** | 3-5 天 | 成功率 ≤ 50%，仅适合"反爬较弱"的小站点（Replicate / Together / SiliconFlow） |
| 15 | **OAuth Runner**（Claude OAuth、Gemini AI Studio）—— 反爬通常更弱 | 2 天 | 真正可能跑通的全自动方向 |
| 16 | **移动端响应式适配 + PWA** | 3 天 | 业务老板手机能看 |
| 17 | **「号池管理」改成可拖拽自定义 Dashboard** | 5 天 | 提升日常感受，但 maintenance 成本高 |

---

## 6. 推荐的下个迭代（v0.6）打包

挑 1-5 + 6 + 12 这 5 件，估 2 天，**不引入新依赖**：

```
v0.6 — "看得清楚 + 报得及时"
- feat(pool): 加 PoolAuditLog 关键路径埋点
- feat(pool): PoolBillingUpstreamRatio 按 provider 分配置（前端可视化）
- feat(pool): 5sim 余额低告警 + 接入巡检
- feat(pool): 上游账号批量启停 + 批量改 group
- feat(pool): 告警按日聚合视图 + CSV 导出
- feat(pool): 公开 /api/pool/health 监控端点（不鉴权，只 200）
- docs: 同步更新 pool-api / pool-management / changelog
```

**v0.7 — "通道更广 + HA 雏形"** （4 天）：飞书/钉钉 + cron 单 leader + i18n 试点

**v0.8 — "迁 Postgres 演练"** （3 天）：迁移脚本 + benchmark + 一键回滚

---

## 7. 永远不做的事

- ❌ **不重新启用 chromedp 商业站点 Runner**（[ADR-0002](../05-architecture/decisions.md#adr-0002删除所有-chromedp-自动-runner)，决定是终态）
- ❌ **不主动 merge 上游 new-api**（[ADR-0001](../05-architecture/decisions.md#adr-0001走-fork--自编译路线不走-plugin--sidecar)，已 hard fork）
- ❌ **不在号池里做自营 LLM**（用 vLLM / SGLang 自己跑）—— 那是另一个产品
- ❌ **不做 SaaS 化分租户隔离** —— new-api 原生 group 够了，复杂度爆炸不值
- ❌ **不做实时聊天 / 流式 UI** —— 后台是管理界面，不是产品

---

## 8. 给"半年后回来的自己"的话

```
1. start.command 双击就能起
2. docs/README.md 是入口
3. ADR 决策都在 docs/05-architecture/decisions.md，看了再动手
4. 改 channel 入库一律 ch.Insert()
5. 改文档和改代码进同一个 commit
6. 出错时第一反应：data/logs/oneapi-*.log 看 200 行
7. 不要重新尝试 chromedp 商业站点
8. 上线前看 docs/08-security.md 的清单跑一遍
```

---

## 9. 维护本文档的规矩

- 每次发版（v0.X.0）→ 更新本文档第 2 / 3 / 4 节
- 每次决定不做某事 → 加到第 7 节
- 第 5 / 6 节是"动态规划"，做完一个就划掉一个
- **不要让本文档变成空话**：第 1 节"一句话现状"必须每月校准一次
