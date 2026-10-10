# 排错手册（按现象索引）

> 出问题先来这里。**按现象**找，找不到再看 [pool-pipeline](../05-architecture/pool-pipeline.md) / [routing-abilities](../05-architecture/routing-abilities.md)。
>
> 排查的标准三段式：**现象 → 假设 → 验证 → 修复**。每个条目都按这个写。

---

## 索引

- [A. 启动相关](#a-启动相关)
- [B. 登录 / 后台访问](#b-登录--后台访问)
- [C. 渠道路由与上游调用](#c-渠道路由与上游调用)
- [D. 号池注册流程](#d-号池注册流程)
- [E. 巡检 / 告警 / Telegram](#e-巡检--告警--telegram)
- [F. 数据库 / 备份](#f-数据库--备份)
- [G. 编译 / 部署](#g-编译--部署)
- [H. 性能与并发](#h-性能与并发)

---

## A. 启动相关

### A1. 双击 `start.command` 闪一下就关

**假设**：
- 端口被占
- 二进制路径错位 / 没执行权限
- DB 文件损坏

**验证**：
```bash
cd <REPO>
ls -l new-api-macos                       # 应有 +x 权限
lsof -i :3000 2>/dev/null                 # 看端口
./new-api-macos 2>&1 | head -50           # 直接跑看头几行报错
```

**修复**：
- 端口冲突：`pkill -f new-api-macos` 或换端口（`./new-api-macos --port 3001`）
- 没权限：`chmod +x new-api-macos`
- DB 损坏：恢复备份（见 [backup-restore.md](./backup-restore.md#5-恢复--灾难重建)）

---

### A2. 启动后浏览器打开 `http://localhost:3000` 显示 `connection refused`

**假设**：
- 进程其实没起来
- 启动到一半 panic

**验证**：
```bash
ps aux | grep new-api-macos
tail -100 <REPO>/data/logs/oneapi-$(date +%Y-%m-%d).log
```

**修复**：
- 找 panic 关键词（`panic:` / `fatal:`），按报错处理
- 常见：磁盘满 → `df -h` → 清 `data/logs/` 老文件

---

### A3. macOS 弹"无法验证开发者"

**修复**：
```bash
xattr -d com.apple.quarantine <REPO>/new-api-macos
```

---

## B. 登录 / 后台访问

### B1. 登录提示 `用户名或密码错误`

**⚠️ 先纠正一个最常见的误判**：本项目**没有内置默认账号**。
`model/main.go` 里那个建 `root`/`123456` 的函数 `createRootAccountIfNeed()`
**定义了但从未被调用**（全仓库零调用点）。管理员账号是首次访问时在 `/setup`
初始化向导里自己设的 —— 见 [`../04-usage/admin-quickstart.md`](../04-usage/admin-quickstart.md)。

**第一步：先确认账号名**（管理员是 `role = 100` 的那个）：
```bash
# SQLite
sqlite3 one-api.db "SELECT id, username, role FROM users WHERE role = 100;"

# PostgreSQL
docker exec -e PGPASSWORD="$PG_PASSWORD" new-api-postgres \
  psql -U "$PG_USER" -d "$PG_DB" -c "SELECT id, username, role FROM users WHERE role = 100;"
```

**第二步：忘记密码就改库**。bcrypt 不可逆，只能换成新的哈希。
**自己现生成一个**，别用文档里写死的值：

```bash
python3 -c "import bcrypt,getpass; print(bcrypt.hashpw(getpass.getpass('新密码: ').encode(), bcrypt.gensalt(10)).decode())"
```

```sql
UPDATE users SET password = '<上一步输出的 bcrypt 串>' WHERE username = '<账号名>';
```

> 改完刷新浏览器即可，不必重启服务（会话里存的是 id/username，不校验密码哈希）。

---

### B2. 一级菜单看不到「号池管理」

**假设**：当前用户不是管理员（role < 10）。

**验证**：
```bash
sqlite3 one-api.db "SELECT id,username,role FROM users WHERE username='<你的用户名>';"
```

**修复**：
```sql
UPDATE users SET role=10 WHERE username='<你的用户名>';
```

刷新前端（不必重启服务）。

---

### B3. 进入「号池管理」白屏 / 卡死

**假设**：前端打包过期 / 浏览器缓存。

**修复**：
- 强刷：`Ctrl/Cmd + Shift + R`
- 清缓存：开 DevTools → 应用 → 清空站点数据
- 仍白屏 → 看 Console 报错截图，去 [`02-development/setup.md`](../02-development/setup.md) 找 dev 模式跑前端

---

## C. 渠道路由与上游调用

### C1. `Error: No available channel for model gemini-2.5-flash under group default`

**这是最常见的号池问题，必读 [routing-abilities.md](../05-architecture/routing-abilities.md)。**

**假设序列**（按命中率排）：

#### C1-假设 1：`abilities` 表没有对应行 → 渠道用 `DB.Create(ch)` 写的，没调 `Insert()`

```sql
SELECT * FROM abilities WHERE \`group\`='default' AND model='gemini-2.5-flash';
```

如果 0 行：
```sql
-- 找出这个 channel
SELECT id, name, type, \`group\`, models FROM channels WHERE name LIKE '%gemini%' ORDER BY id DESC LIMIT 5;
-- 取 id=N，重建 abilities：
DELETE FROM abilities WHERE channel_id=N;
-- 然后在后台「渠道管理 → 编辑」改一下随便 save 一次（save 会调 Insert/Update 重建）
```

或者手写一条记录补：
```sql
INSERT INTO abilities (\`group\`, model, channel_id, enabled, priority, weight)
VALUES ('default','gemini-2.5-flash', N, 1, 0, 1);
```

**根治**：所有自建 channel 代码必须 `ch.Insert()`，详见 [ADR-0005](../05-architecture/decisions.md#adr-0005channel-写入必须用-chinsert不允许-dbcreatech)。

#### C1-假设 2：`channel.group` 不含 `default`

```sql
SELECT id, name, \`group\` FROM channels WHERE id=N;
```

如果 group 是 `'gemini'` 而不是 `'default,gemini'`：
```sql
UPDATE channels SET \`group\`='default,gemini' WHERE id=N;
DELETE FROM abilities WHERE channel_id=N;
-- 后台编辑 -> save 一次重建 abilities
```

#### C1-假设 3：`channel.models` 是空字符串

```sql
SELECT id, name, models FROM channels WHERE id=N;
```

如果 `models=''`：
- 后台「渠道管理 → 编辑这个 channel → 模型 → 获取模型列表」一键拉
- 或 SQL 直接改：`UPDATE channels SET models='gemini-2.5-pro,gemini-2.5-flash,...';`
- **更好**：去 `model/pool_recipe.go` 给该 Recipe 的 `DefaultModels` 字段写好，下次新建自动有

---

### C2. 调上游 `401 Unauthorized` / `Invalid API key`

**假设**：
- Key 复制时少了 / 多了字符
- Key 已过期或被上游 revoke
- Key 对应 region 不允许该模型

**验证**：去对应官方控制台手动用这个 Key curl 一次同样的请求，能不能成。

**修复**：
- Key 真坏了 → 后台「号池管理 → 上游账号 → 编辑」录新 Key
- 注意：`PoolAccount.KeyMasked` 是脱敏显示的，**真 Key 在 `channels.key`**，所以编辑要同时改 channel.key（系统里 `UpdatePoolAccount` 已经会同步）

---

### C3. 上游返回 `429 too many requests` 频繁

**假设**：
- 该 channel 单点速率太低
- Recipe 默认账号没有付费

**修复**：
- 临时：在「上游账号」给这个 Provider 多录几个号 → 路由会按 weight 自动负载均衡
- 长期：买付费配额、改 channel.priority/weight
- 极端：在「巡检告警」配规则限速（暂未做，可走 new-api 原生 group rate limit）

---

### C4. 流式输出（SSE）卡顿 / 截断

**假设**：
- 反向代理 buffering 没关
- 上游也卡（不是本地问题）

**修复（Caddy）**：
```caddyfile
api.example.com {
    reverse_proxy localhost:3000 {
        flush_interval -1     # 关 buffer
    }
}
```

**修复（Nginx）**：
```nginx
proxy_buffering off;
proxy_cache off;
proxy_http_version 1.1;
proxy_set_header Connection "";
chunked_transfer_encoding off;
```

---

## D. 号池注册流程

### D1. 自动模式入队提示「该 Recipe 自动模式无法运行：未注册内部 Runner，也未配 Webhook URL」

**根因**：当前 fork **不内置任何商业站点的全自动 Runner**（[ADR-0002](../05-architecture/decisions.md#adr-0002删除所有-chromedp-自动-runner)）。

**修复**：
- 后台「号池管理 → 自动注册 → 编辑这个 Recipe → 开「半自动模式」」
- 重新入队 → 拿到 `manual_steps` → 按步骤注册 → 「录入结果」

---

### D2. 半自动模式录入结果后，「上游账号」里没出现

**验证**：
```sql
SELECT id, status, error_msg FROM pool_jobs ORDER BY id DESC LIMIT 5;
```

如果 `status=manual_pending`，说明 `ManualSubmitJobResult` 没成功调用过——前端拿到 `job_id` 但没 POST `/api/pool/jobs/:id/manual-result`。
开 DevTools → 网络面板看那次提交的 status / response。

如果 `status=success` 但 `pool_accounts` 里没新行：极罕见，看 `oneapi-*.log` 里 `[pool] create channel failed` 等关键字。

---

### D3. 自动建渠道勾上后，channel 没建出来 / 没绑

**假设**：
- `key_raw` 没填
- `channel_type` 选了 0
- `recipe.channel_type` 为 0（没有自动建渠道能力的 Recipe）

**验证**：F12 看 `POST /api/pool/accounts` 的请求 body 是不是齐全（`channel_type > 0` 且 `key_raw` 非空 且 `auto_create_channel=true`）。

**修复**：
- 缺 `channel_type` → 编辑 Recipe 给它填上（参考 [`02-development/add-recipe.md`](../02-development/add-recipe.md)）
- key 没拿到 → 完成线下注册先

---

### D4. 「一键绑定」点了无效

**假设**：
- 没找到同名 channel
- 找到多个，要从弹窗里选

**验证**：F12 网络看 `POST /api/pool/accounts/:id/bind` 返回。

**响应解读**：
- `bound=true, channel_id=N` → 成功
- `bound=false, candidates=[...]` → 多个候选，前端会弹 `BindPickerModal`，从里面选一个再点确认
- `error="没找到可自动绑定的渠道"` → 直接「编辑账号」勾「自动建渠道」

---

## E. 巡检 / 告警 / Telegram

### E1. Telegram 没收到任何消息

**逐项验证**：
1. `SELECT \`key\`, value FROM options WHERE \`key\` LIKE 'PoolTelegram%';` → token / chat_id 都不为空？
2. 后台「巡检告警 → 发送测试消息」点击 → 立即看响应。
3. 看 `oneapi-*.log` 里 Telegram 相关报错：
   - `chat not found` → chat_id 错（用 `https://api.telegram.org/bot<TOKEN>/getUpdates` 看真实 chat_id）
   - `Unauthorized` → token 错
   - `can't parse entities` → 消息里有未转义的 markdown 特殊字符（修 `pool_health.go::buildTelegramMessage`）
4. 直接 curl 验证 token：
   ```bash
   curl "https://api.telegram.org/bot<TOKEN>/getMe"
   ```

---

### E2. 巡检从来不主动跑（只能手动点）

**假设**：cron 没启动 / 时区错误。

**验证**：
```bash
grep -i 'pool/health' data/logs/oneapi-*.log | tail -5
```

如果完全没 `[pool/health] tick` 字样：
- 看 `service/cron.go` 有没有把 `RunPoolHealthCheck` 注册进 cron 列表
- 检查启动入口 `main.go` 有没有调 `service.StartCron()` （新版 fork 默认有）

---

### E3. 同一告警一直在重复推送

**假设**：去重逻辑被绕过 / `pool_alert_history.target_id` 不匹配。

**修复**：
- 临时：把那条 alert mark resolved（24h 内不再推同一规则同 target）
- 长期：去 `service/pool_health.go::dedupAlert` 看 dedup 用的字段对不对
- 如果你最近加了新规则没复用 dedupKey 工具，请补上

---

## F. 数据库 / 备份

### F1. `Error: in prepare, no such table: pool_accounts`

**假设**：连错了数据库（多个 `*.db` 文件）。

**验证**：
```bash
ls <REPO>/*.db
file <REPO>/one-api.db
```

确认 `one-api.db` 是真主库，**不要**对 `new-api.db` 等历史文件操作。

历史问题（v1 时期）：项目内一度同时有 `new-api.db` 和 `one-api.db`，导致部分 SQL 跑在错的库上。

---

### F2. `Error: in prepare, no such column: group_`

**根因**：SQLite 关键字 `group` 必须用反引号。

**修复**：
```sql
-- 错：
SELECT group_ FROM tokens;
-- 对：
SELECT \`group\` FROM tokens;
```

---

### F3. SQLite "database is locked"

**假设**：同一 db 文件被两个进程同时写。

**验证**：
```bash
lsof | grep one-api.db
```

**修复**：把多余的进程 kill 掉。如果是因为备份脚本拿了 lock，等几秒重试，或换 `sqlite3 .backup` 命令（它内部正确地用 shared lock）。

---

### F4. 数据库文件突然变得很大（几 GB）

**假设**：`logs` 表没清理。

**修复**：
```sql
SELECT COUNT(*) FROM logs;                          -- 看条数
SELECT COUNT(*) FROM logs WHERE created_at < <30天前ts>;
DELETE FROM logs WHERE created_at < <30天前ts>;
VACUUM;                                              -- 实际收回磁盘空间
```

或更优雅：在「日志设置」里配自动清理。

---

## G. 编译 / 部署

### G1. `go build` 报 `embed: no matching files found`

**根因**：前端没编译 / `web/dist` 不存在。

**修复**：
```bash
cd <REPO>/web
bun install
bun run build
ls dist/                                # 应有 index.html + assets/
cd .. && go build -o ../new-api/new-api-macos
```

---

### G2. 编译后跑起来，前端是老版本

**根因**：embed 缓存 / Go build 增量编译没识别 dist 改动。

**修复**：强制重编：
```bash
cd <REPO>
touch main.go
go build -a -o ../new-api/new-api-macos
```

`-a` 把所有依赖一起重编，慢但保证没缓存。

---

### G3. macOS 编译出来在 Linux 跑不了

**正常**：单二进制有架构，跨平台需交叉编译：
```bash
GOOS=linux GOARCH=amd64 go build -o new-api-linux-x64
GOOS=linux GOARCH=arm64 go build -o new-api-linux-arm64
```

不要把 macOS 二进制 SCP 到 Linux 服务器跑——会 `cannot execute binary file`。

---

### G4. start.command 弹"权限不足"

```bash
chmod +x <REPO>/start.command
chmod +x <REPO>/new-api-macos
xattr -d com.apple.quarantine <REPO>/start.command 2>/dev/null
```

---

## H. 性能与并发

### H1. 单次请求延迟 > 5s（不算 LLM 自身延迟）

**假设**：
- SQLite 写竞争（高 QPS 下 WAL fsync 慢）
- 上游 region 远（北京机器调美国服务）

**验证**：
```bash
curl -o /dev/null -s -w "DNS:%{time_namelookup}s CONN:%{time_connect}s SSL:%{time_appconnect}s TFB:%{time_starttransfer}s TOTAL:%{time_total}s\n" \
  https://api.openai.com/v1/models -H "Authorization: Bearer sk-..."
```

**修复**：
- 上游远 → 接 Cloudflare Workers / 香港 / 日本中转节点
- SQLite 慢 → ≥ 50 RPS 就该上 Postgres（[`03-deployment/production.md`](../03-deployment/production.md)）

---

### H2. CPU 占用接近 100%

**假设**：
- chromedp 残留进程（这个 fork 已删，但旧产物可能还有）
- 自检 cron 太猛
- 日志写盘过频繁

**验证**：
```bash
top -o cpu
ps aux | grep -E 'chrom|new-api' | head
```

**修复**：
- 停旧 chromedp：`pkill -f Chromium && pkill -f chrome`
- 确保跑的是新版 binary
- 调日志级别：`SQL_LOG_LEVEL=warn`（少打印）

---

### H3. 并发 100 用户后 502 / 504

**假设**：单进程瓶颈。

**修复**：
- 短期：垂直扩（4C8G → 8C16G），SQLite 改 Postgres
- 长期：多实例（参考 [`03-deployment/production.md`](../03-deployment/production.md#multi-region--ha)）

---

## 找不到怎么办

1. 看 `data/logs/oneapi-YYYY-MM-DD.log` 最近 200 行
2. 看 SQLite 异常表 `pool_alert_history` / `logs`
3. F12 看具体 API 响应 + 状态码
4. `git log --oneline -20` 在 new-api-src 里看最近改动
5. 还查不到 → 在 `decisions.md` 加一条 ADR 记录这次怎么解的，给下一个人留路
