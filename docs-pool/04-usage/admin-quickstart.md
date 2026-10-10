# 管理员后台 5 分钟入门

> 第一次登入要做的几件事，别让初始密码 / 令牌 / 配置躺着不动。

## 1. 登入

1. 双击 `start.command`，浏览器自动打开 `http://localhost:3000`
2. **首次访问会自动跳转 `/setup` 初始化向导**，在那里设置管理员账号（≤12 字符）和密码（≥8 字符）。
   系统**没有**内置默认账号——如果谁告诉你用 `root / 123456`，那是过期信息。

## 2. 必做的 4 件事（5 分钟）

### 2.1 确认管理员密码强度（30 秒）
初始化向导只要求 ≥8 字符。建议改成强密码（≥ 16 字符）：右上角头像 → 个人设置 → 修改密码。

### 2.2 删默认 root 令牌（30 秒）
左侧菜单「令牌」→ 看是否有自动生成的初始 token，全部删掉。需要外部调用时单独建一个。

### 2.3 关闭公开注册（10 秒）
左侧「设置」→「通用」→ 关闭「允许新用户注册」。
> 内测期，新用户走邀请码（设置 → 注册 → 启用邀请码）或后台手动建。

### 2.4 检查号池菜单（30 秒）
左侧菜单应能看到「号池管理」一级，点开有 5 项：
```
号池管理
  ├─ 总览
  ├─ 上游账号
  ├─ 自动注册
  ├─ 收支对账
  └─ 巡检告警
```

如果**看不到**：
- 当前账号不是管理员？root 默认是。
- 启动失败？看 `logs/oneapi-*.log` 末尾。
- 详细排错：[06-operations/troubleshooting.md](../06-operations/troubleshooting.md)。

## 3. 第一次拿到上游 Key（10 分钟）

走最简单的：Google AI Studio 免费 Gemini。

1. 进「号池管理 → 自动注册」。
2. 找到 ⭐ Google AI Studio (免费 Gemini) 这一行，点「入队」。
3. 切到「任务历史」tab，应有新条 `manual_pending`。
4. 点「查看步骤」，按提示：翻墙打开 `https://aistudio.google.com/app/apikey` → 用 Gmail 登录 → Create API key → 复制 `AIza...` 开头 39 字符。
5. 回后台点「录入结果」，填：
   ```
   pool_account_name: gemini-personal-1
   key_raw: AIza...（你刚拿到的）
   ```
6. 提交后看：
   - 「上游账号」页应有新行 `gemini-personal-1`，状态 active。
   - 原生「渠道管理」页应有 tag `pool-manual` 的新渠道。
   - 用 default 组的 user token 调一次：
     ```bash
     curl http://localhost:3000/v1/chat/completions \
       -H "Authorization: Bearer sk-USER-TOKEN" \
       -d '{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}'
     ```
     应回 200 + 模型回答。

## 4. 配置巡检告警（1 分钟）

进「巡检告警 → Telegram 通道」：
- Bot Token：开源版默认空，需自己填（[拿 Bot Token 步骤](../06-operations/monitoring-alerts.md#怎么拿-bot-token--chat-id)）
- Chat Id：开源版默认空，需自己填
- 点「测试发送」，几秒内 Telegram 会收到「✅ 测试消息」。

收不到的常见原因：
- Bot 没加你的 chat 群 → 手动 `/start` 一次
- Token 错误 → API 会返回 `chat not found`
- 详见 [06-operations/troubleshooting.md](../06-operations/troubleshooting.md)。

## 5. 用户 / 令牌的概念

new-api 原生概念，号池**不动**它们：

| 实体 | 表 | 谁建 | 给谁用 |
|---|---|---|---|
| User | `users` | 后台「用户管理」 | 进入后台用、生成自己的 token |
| Token | `tokens` | 用户后台「我的令牌」 | 调 `/v1/...` 时 Bearer 头 |

**别和「上游 Key」弄混**：
- 上游 Key = `pool_accounts.key_raw` / `channels.key`，是你**给上游服务商的钱包**。
- User Token = `tokens.key`，是终端用户调你这个网关的凭证。

## 6. 一张图：你后台菜单都干嘛

```
控制台      ← new-api 原生：今日数据
渠道        ← new-api 原生：上游路由槽
令牌        ← new-api 原生：用户调用凭证
日志        ← new-api 原生：每次请求记录 + 计费
用户        ← new-api 原生：用户管理 / 充值
设置        ← new-api 原生：网关全局参数

号池管理     ← ★ 本 fork 新增
  ├─ 总览        健康分 / 今日吞吐 / 上游账号统计
  ├─ 上游账号    pool_accounts CRUD + 一键绑定渠道
  ├─ 自动注册    Recipe + Job：剧本管理 + 任务入队 / 录入结果
  ├─ 收支对账    新用户充值 vs 上游消耗，毛利估算
  └─ 巡检告警    告警历史 + 立即巡检 + Telegram 通道
```

每页详细文档：[pool-management.md](./pool-management.md)。

## 7. 哪些东西是默认值，可以直接用

| 项 | 默认值 | 改在哪 |
|---|---|---|
| 管理员 | **无默认值**，初始化向导里自设 | 头像 → 修改密码 |
| 端口 | 3000 | start.command 或 `--port` |
| DB | SQLite `one-api.db` | `SQL_DSN` 环境变量 |
| Redis | 关 | `REDIS_CONN_STRING` |
| 19 条内置 Recipe | 已 Seed | 「自动注册」页编辑 |
| Telegram Bot | 已 Seed | 「巡检告警 → Telegram」 |
| 5sim API Key | 已 Seed | 「自动注册 → 自动化设置」 |
| 邮箱 Provider | mailtm | 同上 |

完整：[07-reference/pool-api.md](../07-reference/pool-api.md) 自动化配置部分。
