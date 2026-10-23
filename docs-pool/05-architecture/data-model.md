# 数据模型

> 重点关注**号池新增**的 4 张表 + new-api 原生最相关的 3 张（channels / abilities / tokens）。其它原生表（users / logs / options / topups / redemptions / midjourney_tasks ...）参考 [`model/`](../../model/)。

## 1. ER 简图

```
                        ┌────────────┐
                        │ pool_recipes│   注册剧本（19 条 seed）
                        └──────┬──────┘
                               │ 1:N
                               ▼
                        ┌────────────┐
                        │ pool_jobs  │   每次注册任务
                        └──────┬──────┘
                               │ 1:1
                               ▼ 完结时建
                        ┌──────────────┐  ChannelId  ┌──────────┐
                        │ pool_accounts│ ──────────► │ channels │
                        └──────────────┘     1:1     └────┬─────┘
                                                          │ Insert() 自动写
                                                          ▼
                                                    ┌──────────┐
                                                    │abilities │（路由命中表）
                                                    └──────────┘

  ┌────────────┐                 触发时
  │pool_health │  cron 每 5 min  ──→ 写  pool_alert_history    +  推 Telegram
  │  巡检引擎  │
  └────────────┘

  无外键约束（GORM Soft Delete + 索引），运维注意手工保持引用一致性。
```

## 2. 表清单

| 表 | 来源 | 行数估算 |
|---|---|---|
| `pool_accounts` | ★ fork 新增 | 数十 ~ 几百 |
| `pool_recipes` | ★ fork 新增 | 19+ |
| `pool_jobs` | ★ fork 新增 | 几千 / 月 |
| `pool_alert_history` | ★ fork 新增 | 几千 / 月 |
| `channels` | new-api 原生 | 数百 |
| `abilities` | new-api 原生 | 渠道数 × group 数 × model 数 |
| `tokens` | new-api 原生 | 终端用户的调用令牌 |
| `users` | new-api 原生 | 终端用户 |
| `options` | new-api 原生 | 全局配置（Telegram/5sim/PoolWorkerMaxConcurrent 等） |
| `logs` | new-api 原生 | 每次请求 + 每次扣费 |
| `topups` | new-api 原生 | 充值流水 |
| 其他 | new-api 原生 | midjourney_tasks / vendor_keys / quotas ... |

---

## 3. PoolAccount

源码：`model/pool_account.go`。表名 `pool_accounts`。

| 字段 | 类型 | 索引 | 含义 |
|---|---|---|---|
| `id` | int | PK | 自增 |
| `name` | string(128) | uniq + 软删除联合 | 同名账号在软删后可重用 |
| `provider` | string(64) | index | openai / gemini / claude / groq / ... |
| `account_type` | string(32) | — | official / oauth / free / third |
| `status` | int | index | 1=Active / 2=Warning / 3=Disabled |
| `key_masked` | string(128) | — | `sk-xxx...yyyy` 显示用，**不存原 key** |
| `balance_usd` | float | — | 上游余额（USD） |
| `used_quota` | int64 | — | 累积消耗（与 channel.used_quota 类似单位） |
| `expire_at` | int64 | index | unix sec，0 表示永久 |
| `channel_id` | int | index | 弱关联 `channels.id`，0=未绑 |
| `group_name` | string(64) | — | 默认 `default`；写到 `channels.group` 时为 `"default,<group>"` |
| `notes` | text | — | 备注 |
| `last_checked_at` | int64 | — | 巡检最近检查时间 |
| `created_time` / `updated_time` | int64 | — | unix sec |
| `deleted_at` | gorm.DeletedAt | uniq + name | 软删除 |

> **没有外键**：删除一个 PoolAccount 不会自动删 channel，必须 UI 操作时手动级联。

## 4. PoolRecipe

源码：`model/pool_recipe.go`。表名 `pool_recipes`。

| 字段 | 类型 | 含义 |
|---|---|---|
| `id` | int | PK |
| `key` | string(64) | 唯一 slug，例如 `gemini-aistudio-free` |
| `name` | string(128) | 显示名（含 ⭐ 标记） |
| `provider` | string(64) | 与 PoolAccount.provider 同 |
| `version` | string(32) | seed 用 `2.0.0` |
| `manual_mode` | bool | true=半自动（默认所有 19 条），false=全自动需 Runner |
| `description` | text | 后台说明 |
| `doc_url` | string(512) | 直接跳上游注册地址 |
| `required_materials` | text | JSON 数组：`["国内手机号","¥1 充值"]` |
| `manual_steps` | text | 操作步骤，前端按 `\n` 拆 |
| `output_format` | string(128) | `sk-... (51 字符)` 等 |
| `difficulty` | string(16) | easy / medium / hard |
| `ip_requirement` | string(32) | any / residential / specific_country |
| `automation_capability` | string(32) | full / sms_paid / oauth_only / realname_only / manual |
| `sms_country` / `sms_operator` / `sms_product` | string | 自动 Runner 用（5sim 路由参数） |
| `channel_type` | int | 自动建渠道用：new-api 内置 type |
| `channel_base_url` | string(255) | 同上 |
| `default_models` | text | 自动建渠道时填 `channels.models`（逗号分隔） |
| `webhook_url` / `config_json` | text | 外部 worker 模式（兼容） |
| `enabled` | bool | 入队前必须为 true |
| `success_count` / `failure_count` | int | 累计计数 |
| 时间戳 + soft delete | — | 同 PoolAccount |

### Recipe 的 Seed 行为
启动时 `SeedDefaultPoolRecipes` 调 `upsertPoolRecipe(seed)`：
- Recipe 不存在 → INSERT。
- 存在 → 只更新元数据字段（`name/provider/description/doc_url/required_materials/manual_steps/output_format/difficulty/ip_requirement/automation_capability/sms_*/channel_type`）。
- **不会覆盖**：`enabled` / `webhook_url` / `config_json` / `version` / `success_count` / `failure_count`。
- `default_models`：仅当 existing 为空时同步。
- `manual_mode`：仅当 existing 没运行过且没配 webhook 时同步（防止 GORM 默认值陷阱）。

迁移补丁（v2.0 引入）：`UPDATE pool_recipes SET manual_mode=1 WHERE manual_mode=0 AND webhook_url=''`，把历史误标"自动"但没 Runner 的剧本统一刷回半自动。

## 5. PoolJob

源码：`model/pool_recipe.go`（同文件）。表名 `pool_jobs`。

| 字段 | 含义 |
|---|---|
| `id` | int PK |
| `recipe_key` | 关联 PoolRecipe.key |
| `status` | pending / manual_pending / running / success / failed / cancelled |
| `params_json` | text，入队时附加参数（一般空） |
| `result_json` | text，成功时含 `{pool_account_id, channel_id, by}` |
| `error_msg` | text |
| `started_at` / `finished_at` | int64 |
| `operator_id` | int，触发的管理员 user_id（手动操作时填） |
| `created_time` | int64 index |

> **没有 soft_delete**：失败 / 取消后保留作为审计；可以定期归档（90 天前的）。

## 6. PoolAlertHistory

源码：`model/pool_alert.go`。表名 `pool_alert_history`。

| 字段 | 含义 |
|---|---|
| `id` | int PK |
| `rule_key` | health_check / channel_down / balance_low / fail_rate_high / pool_account_expire |
| `severity` | info / warning / critical（默认 info） |
| `title` | 标题（推 Telegram 时第一行） |
| `message` | 详情（推 Telegram 时正文） |
| `target_type` | channel / pool_account / group / system |
| `target_id` | 关联 id |
| `resolved` | bool |
| `resolved_at` | int64 |
| `created_time` | int64 index |

## 7. Channel（new-api 原生，号池强相关）

源码：`model/channel.go`。表名 `channels`。

号池相关字段：

| 字段 | 关系 |
|---|---|
| `type` | 由 `Recipe.ChannelType` 给值 |
| `key` | 由 `PoolAccount.KeyRaw`（明文）写入 |
| `name` | `PoolAccount.Name` |
| `models` | `Recipe.DefaultModels` 复制 |
| `group` | 自动写 `"default," + Recipe.Provider` 或 `"default," + groupName` |
| `tag` | 标记来源：`pool` (手动绑定) / `pool-auto` (worker) / `pool-manual` (回填) |
| `base_url` | `Recipe.ChannelBaseURL` |

> `tag` 字段是号池**唯一区分自己创建的渠道**的方法。可在原生「渠道管理」页加 `tag=pool*` 过滤。

## 8. Ability（new-api 原生，路由核心）

源码：`model/ability.go`。表名 `abilities`。**3 列联合主键**：`(group, model, channel_id)`。

| 字段 | 含义 |
|---|---|
| `group` | 用户 / token 所在分组 |
| `model` | 模型名（精确匹配） |
| `channel_id` | 命中的渠道 |
| `enabled` | 渠道 `status=enabled` 同步 |
| `priority` / `weight` | 路由排序权重 |
| `tag` | 拷贝 channel.tag |

**重要**：`abilities` 表由 `Channel.Insert()` / `Channel.Update()` 时根据 `channel.Group + channel.Models` 笛卡尔积**自动展开**写入。`model.DB.Create(ch)` 不会触发 → 路由命不中 → 出现 `No available channel for model X under group Y`。

例子：
- channel id=42, group="default,gemini", models="gemini-2.5-pro,gemini-2.5-flash"
- 写入 abilities：4 行
  - (default, gemini-2.5-pro, 42)
  - (default, gemini-2.5-flash, 42)
  - (gemini, gemini-2.5-pro, 42)
  - (gemini, gemini-2.5-flash, 42)

详见 [routing-abilities.md](./routing-abilities.md)。

## 9. Token（new-api 原生，终端用户调用凭证）

源码：`model/token.go`。表名 `tokens`。

号池侧不直接动它，但路由时会用：
- `tokens.group`（如果 token 自带分组）覆盖 user 默认分组
- 路由器拿 `token.group` 去 abilities 表查命中

管理员代用户创建接口（`POST /api/user/:id/token`）额外使用：

- `tokens.idempotency_name`（可空，`varchar(50)`）：管理员代建时写入 name；
  普通 `/api/token/` 创建的 token 保持 `NULL`。
- 唯一索引 `idx_tokens_user_idempotency (user_id, idempotency_name)`：保证并发下
  同一用户 + 同一 name 只创建一条；软删除前会把该列清空，允许同名重建。

接口契约、权限和测试见 [`admin-user-token.md`](../07-reference/admin-user-token.md)。

## 10. Channel.Type 速查表

| Type | Provider | Channel Base URL 例子 |
|---|---|---|
| 1 | OpenAI 兼容 | `https://api.openai.com` / Groq / Cerebras / Together / Fireworks ... |
| 14 | Anthropic | `https://api.anthropic.com` |
| 17 | 阿里 DashScope | `https://dashscope.aliyuncs.com` |
| 20 | OpenRouter | `https://openrouter.ai/api/v1` |
| 24 | Gemini AI Studio | `https://generativelanguage.googleapis.com` |
| 25 | Moonshot Kimi | `https://api.moonshot.cn` |
| 26 | 智谱 GLM | `https://open.bigmodel.cn` |
| 34 | Cohere | `https://api.cohere.ai` |
| 35 | MiniMax | `https://api.minimax.chat` |
| 40 | SiliconFlow | `https://api.siliconflow.cn` |
| 41 | Vertex AI | `<以 GCP project + region 区分>` |
| 42 | Mistral | `https://api.mistral.ai` |
| 43 | DeepSeek | `https://api.deepseek.com` |
| 48 | xAI Grok | `https://api.x.ai/v1` |
| 57 | OpenAI Codex (OAuth) | empty |

完整 type 定义：`new-api-src/relay/channel/types.go`。

## 11. options（号池相关 keys）

| Key | 默认值 | 用途 |
|---|---|---|
| `PoolTelegramBotToken` | seed 默认 | Telegram Bot |
| `PoolTelegramChatId` | seed 默认 | 接收 chat |
| `PoolEmailProvider` | `mailtm` | 自动注册邮箱 provider |
| `PoolSmsProvider` | `5sim` | 接码 provider |
| `PoolFivesimApiKey` | seed | 5sim Bearer JWT |
| `PoolSmsActivateApiKey` | seed | sms-activate API Key |
| `PoolWorkerMaxConcurrent` | 2 | worker 并发 |
| `PoolUpstreamCostRatio` | 0.6 | Overview 毛利估算系数 |

启动 Seed 函数：
- `SeedDefaultTelegramConfig` (`model/pool_recipe.go`)
- `SeedDefaultAutomationConfig` (同上)

`EnsureDefaultOption` 仅在 key 不存在时插入，**已配置的不覆盖**。
