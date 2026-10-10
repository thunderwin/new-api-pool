# 踩坑大全（开发实录）

> 这是这个 fork 从 v0.1 到 v0.5 真实趟过的坑、用脑子撞出的解法。
> 跟 [troubleshooting.md](../06-operations/troubleshooting.md) 的区别：
> 那篇按"现象"写给运维查；本篇按"开发顺序"写给写代码的人避雷。

---

## 索引

- [A. 路由 / 渠道相关（伤害最大的几个）](#a-路由--渠道相关伤害最大的几个)
- [B. GORM / SQLite 数据层](#b-gorm--sqlite-数据层)
- [C. 自动化注册（chromedp / 反爬）](#c-自动化注册chromedp--反爬)
- [D. UI / Semi Design / 前端](#d-ui--semi-design--前端)
- [E. 编译 / embed / 二进制](#e-编译--embed--二进制)
- [F. Telegram / 第三方 API](#f-telegram--第三方-api)
- [G. 流程 / 协作 / 文档](#g-流程--协作--文档)
- [H. 部署 / 分域 / CORS](#h-部署--分域--cors)

---

## A. 路由 / 渠道相关（伤害最大的几个）

### A1. 自动建出来的 channel 路由不到 — `No available channel for model X under group default`

**伤害指数**：⭐⭐⭐⭐⭐（彻底骗自己 + 排查 4 小时）

#### 现象
后台「渠道管理」里看到 channel 是绿的、`status=1`；调 `/v1/chat/completions` 必报 `No available channel`。

#### 假设链路
1. group 不对？查 → 是 `default,gemini`，✅
2. status 不对？查 → 是 1，✅
3. models 没填？查 → 已经填了，✅
4. 是不是 `abilities` 表没数据？查 → **0 行**

#### 真根因
建 channel 时用了 `model.DB.Create(ch)`，**完全跳过** `Channel.Insert()` 里的 `AddAbilities()`。

```go
// ❌ 错的
model.DB.Create(ch)
// ✅ 对的
ch.Insert()                              // 内部会走 AddAbilities()
```

`AddAbilities()` 把 `channel.Group` × `channel.Models` 笛卡尔积写到 `abilities` 表，**这才是路由**。

#### 修复
搜全代码：
```bash
rg 'DB\.Create\(ch\)|DB\.Create\(&ch\)' new-api-src/
```
全部替换为 `ch.Insert()`（涉及 `controller/pool.go::AddPoolAccount` `ManualSubmitJobResult` `BindPoolAccountChannel` + `service/pool_worker.go::runJob`）。

#### 预防
- 在 [`coding-standards.md`](../02-development/coding-standards.md) 写死规则
- 在 [`decisions.md`](../05-architecture/decisions.md#adr-0005channel-写入必须用-chinsert不允许-dbcreatech) 写 ADR-0005
- code review 时 `rg` 一遍这个模式

---

### A2. `channel.Group = "gemini"`，default 用户没法用

**伤害指数**：⭐⭐⭐⭐

#### 现象
跟 A1 长得一模一样，`No available channel for model X under group default`。

#### 真根因
最初实现 `controller/pool.go::AddPoolAccount` 写：
```go
ch.Group = recipe.Provider     // "gemini"
```

而用户 token 默认 `users.group = 'default'` → `abilities WHERE group='default' AND model='gemini-2.5-flash'` → 0 行。

#### 修复
group 写双值：
```go
chGroup := "default"
if groupName != "" && groupName != "default" {
    chGroup = "default," + groupName
}
ch.Group = chGroup
```

`AddAbilities` 内部 `strings.Split(ch.Group, ",")` 会写两份记录（`default` 一份 + `gemini` 一份），两类用户都命中。

#### 预防
- ADR-0006 立规矩
- 测试用例：建一个 channel 后查 `abilities` 表是否有 `default` 行

---

### A3. `channel.Models = ""` 导致 `AddAbilities` 啥都没写

**伤害指数**：⭐⭐⭐

#### 现象
`abilities` 表有几行 `default,xxx` 但每个 channel 只有 1-2 条 model。

#### 真根因
建 channel 时 `models` 留空，等待用户进「渠道管理」点「获取模型列表」补全。但用户根本不知道要补全。

#### 修复
- 给 `PoolRecipe` 加 `DefaultModels` 字段（text, 逗号分隔）
- Seed 19 条 Recipe 全部填好（OpenAI 写 30+ 模型，Gemini 写 8 个，DeepSeek 写 2 个等）
- 自动建 channel 时 `ch.Models = recipe.DefaultModels`

#### 副作用
DefaultModels 写满 ≠ 该 Key 实际有权限的模型。但路由不到比 401 更糟。

---

### A4. `channels.tag` 是 `*string` 不是 `string`

**伤害指数**：⭐⭐

#### 现象
```go
ch := &model.Channel{
    Tag: "pool-auto",            // ❌ 编译报错：cannot use string as *string
}
```

#### 修复
```go
poolTag := "pool-auto"
ch.Tag = &poolTag
```

#### 预防
看 model/channel.go 字段类型 → 知道有些指针字段是为了"区分 NULL vs 空字符串"。

---

## B. GORM / SQLite 数据层

### B1. GORM `default:true` bool 陷阱（导致 ManualMode 全部=true）

**伤害指数**：⭐⭐⭐⭐⭐（最阴险）

#### 现象
Seed 文件写：
```go
{Key: "deepseek-direct", ManualMode: true},
{Key: "openai-runner",   ManualMode: false},  // 想让它走自动 runner
```
启动后查 db → **全是 `manual_mode=1`**。

#### 真根因
PoolRecipe 字段：
```go
ManualMode bool `gorm:"default:true"`
```
GORM 上送 `Create()` 时，`ManualMode=false` 是 zero-value，**触发 default 规则被覆盖成 true**。

`Save()` 也一样，因为 GORM 不知道你"是真的想存 false"还是"留空"。

#### 修复
1. 字段去掉 `gorm:"default:true"`
2. Seed 显式写 `ManualMode: true/false`，不依赖 default
3. 一次性迁移 SQL 修正旧数据：
   ```sql
   UPDATE pool_recipes SET manual_mode=1
   WHERE manual_mode=0 AND (webhook_url IS NULL OR webhook_url='');
   ```

#### 预防（[ADR-0009](../05-architecture/decisions.md#adr-0009seed-函数加非空-fallback-保护避免-gorm-默认值陷阱)）
- 任何 `bool` 字段**都不要**写 `gorm:"default:..."`
- 任何 `int` 字段写 `gorm:"default:0"` 也要小心 zero-value
- Seed 都用结构体字面量显式赋值，不靠 default

---

### B2. SQLite `group` / `order` / `key` 关键字必须反引号

**伤害指数**：⭐⭐

#### 现象
```sql
SELECT group FROM tokens;
-- Error: in prepare, no such column: group_
```

报"no such column" 把人误导得以为字段不存在。

#### 修复
```sql
SELECT `group` FROM tokens;
SELECT `key`, value FROM options;
```

GORM ORM 写法没事（自动加引号），手写 SQL / `model.DB.Raw(...)` 必须自己加。

#### 预防
排查脚本里的 SQL 第一时间检查关键字。

#### ⚠️ 但反引号只在 SQLite / MySQL 成立，PostgreSQL 会直接炸

**2026-10-10 补记，伤害指数 ⭐⭐⭐⭐**。PG 把 `` ` `` 当**运算符字符**（不是标识符引号），
所以 `` WHERE `key` = ? `` 被解析成「运算符 ` key 运算符 ` = ?」：

```
syntax error at or near "="        ← 来自 `key` = ?
syntax error at or near "IN"       ← 来自 `key` IN (...)
```

报错位置落在 `=` / `IN` 上，**完全看不出根因是反引号**，极易查错方向。

**正确写法**：别手写标识符引号，交给 GORM 按方言自己加
（PG → `"key"`，MySQL → `` `key` ``）：

```go
DB.Where(map[string]interface{}{"key": key}).First(&r)          // 单值
DB.Where(map[string]interface{}{"key": []string{"a", "b"}})     // IN
DB.Model(&M{}).Where(map[string]interface{}{"key": k}).
    UpdateColumn(col, gorm.Expr(col+" + 1"))                    // 自增
```

**连带伤**：查询报错 → 代码以为记录不存在 → 去 `Create` → 撞唯一键冲突。
线上当时同时出现 `options_pkey` 重复，修好引号后自动消失。
**看到"主键冲突"先怀疑它前面那条查询是不是静默失败了。**

---

### B3. 项目同时存在 `one-api.db` 和 `new-api.db`

**伤害指数**：⭐⭐⭐⭐

#### 现象
purge mock 数据：
```bash
sqlite3 new-api.db "DELETE FROM pool_jobs WHERE recipe_key='debug-mock';"
# 0 rows affected
```
重启服务 mock 数据还在。

#### 真根因
项目目录同时有两个 db 文件（早期改名残留）：
- `one-api.db` ← 实际运行的
- `new-api.db` ← 误命名 / 历史残留

服务启动时按代码里的 `DB_PATH` 默认 `one-api.db`。

#### 修复
- `file *.db` 看 sqlite 头确认是 sqlite db
- `lsof -p $(pgrep new-api-macos) | grep -i sqlite` 看进程实际打开哪个
- 删冗余文件，只留 `one-api.db`

#### 预防
- 部署文档明确标注唯一 db 路径
- 备份脚本只对 `one-api.db` 操作

---

### B4. SQLite WAL 模式备份要拷三个文件

**伤害指数**：⭐⭐

#### 现象
拷了 `one-api.db`，恢复后发现少了几小时数据。

#### 真根因
SQLite WAL 模式下，未 checkpoint 的写入在 `*.db-wal` 里，shared memory 在 `*.db-shm`。**只拷主 db = 拿了快照不一致的状态**。

#### 修复
- 冷备份：先停服，再 `cp one-api.db one-api.db-wal one-api.db-shm`
- 热备份：用 `sqlite3 one-api.db ".backup 'backup.db'"`（自动处理一致性）

详见 [`backup-restore.md`](../06-operations/backup-restore.md)。

---

### B5. `DELETE FROM pool_recipes` 后启动时表为空？

**伤害指数**：⭐⭐

#### 现象
本地测试时 `DELETE FROM pool_recipes;`，然后重启 → 表里没有数据。
但是预期 `SeedDefaultPoolRecipes()` 应该重新插入。

#### 真根因
`SeedDefaultPoolRecipes()` 只在表为空时插入新条目，但**单条记录的更新逻辑**是 `upsertPoolRecipe(existing, seed)`——如果 db 里**已有**一条同 key 的，不会强制重置。
但如果整个表空，分支走的是 INSERT 全部。

如果你 `DELETE` 了一部分，`SeedDefault` **不会回填那部分**。

#### 修复
启动时迁移逻辑加：
```go
for _, seed := range defaults {
    var existing PoolRecipe
    err := DB.Where("`key` = ?", seed.Key).First(&existing).Error
    if errors.Is(err, gorm.ErrRecordNotFound) {
        DB.Create(&seed)            // 缺哪条补哪条
    } else {
        upsertPoolRecipe(&existing, &seed)
    }
}
```

#### 预防
不要靠"DELETE → 重启自愈"。要重置就 `DELETE` 后手动 INSERT，或写专门的 reset 脚本。

---

## C. 自动化注册（chromedp / 反爬）

### C1. chromedp 的 `isTrusted=false` 一击致命

**伤害指数**：⭐⭐⭐⭐⭐（直接毙了 7 个 Runner）

#### 现象
跑 OpenRouter Runner，能进 sign-up 页，能填 email + password，点 Continue → 卡在 Cloudflare Turnstile checkbox 几分钟 → timeout。

#### 真根因
Cloudflare Turnstile 检查 `event.isTrusted`：
- 真人鼠标点击 = `true`
- chromedp `chromedp.Click(...)` = `false`

被发现 = 不允许通过。

#### 尝试过的工具
- `chromedp.MouseClick` 按坐标 → 仍 isTrusted=false
- 注 fake `Object.defineProperty(MouseEvent.prototype, 'isTrusted', {get: () => true})` → CF 能识别注入
- residential proxy + headed 模式 → 通过率仍 < 30%
- undetected_chromedriver / puppeteer-extra-plugin-stealth → 略好但不稳定

#### 修复
**放弃**。删除所有商业站点的 chromedp Runner（[ADR-0002](../05-architecture/decisions.md#adr-0002删除所有-chromedp-自动-runner)）。

保留 RecipeRunner 接口（万一以后接打码服务再说）。
所有 19 条内置 Recipe 改 `ManualMode=true`。

#### 教训
- **诚实优于显得能干**：跑得了 30% 不如直接告诉用户"半自动 3 分钟"
- 反爬是个无底洞，业务侧没人会因为这个给你预算

---

### C2. Google reCAPTCHA + 中国 IP = 必死

**伤害指数**：⭐⭐⭐⭐

#### 现象
Cerebras 注册（曾经能跑）：
1. 浏览器自动化 ✅ 邮件验证 ✅ 设置密码 ✅ 进 onboarding
2. 选 Free plan → "This organization does not exist"
3. console 里 `recaptcha__zh_cn.js` + `Failed to fetch`

#### 真根因
Cerebras 注册流第二步触发 reCAPTCHA v3 → 客户端从 `gstatic.com` 取脚本 → **国内被墙** → reCAPTCHA timeout → 后端拒建 organization。

注册账号其实**已建**，但卡在 organization 关联那步。

#### 修复
- 用境外住宅代理 / V2Ray：解决得了，但成本高
- 走 OAuth 而不是邮箱注册：reCAPTCHA 不在 OAuth 流程里
- 改 Recipe 标记 `ManualMode=true` 让用户自己用 VPN 注册

#### 教训
即使浏览器能自动化，**网络层**也可能挡你（reCAPTCHA / hCaptcha 都依赖外部 CDN）。

---

### C3. hCaptcha 视觉拼图比 Turnstile 更严

**伤害指数**：⭐⭐⭐⭐

#### 现象
HuggingFace 注册卡在"找出不一样的图片"拼图。点击 iframe 没响应。

#### 真根因
hCaptcha iframe 是跨域 + 加密通信，外部脚本根本无法 DOM 操作里面的图片。
即使能截图传给打码服务，回传坐标点击仍被 isTrusted 检测干掉。

#### 修复
放弃。同 C1。

---

### C4. mail.tm 免费邮箱被部分服务拉黑

**伤害指数**：⭐⭐⭐

#### 现象
用 mail.tm 临时邮箱注册 → 提交时被拒"This email address is not allowed"。

#### 真根因
OpenAI / Anthropic / Cerebras 都维护 disposable email 黑名单（mail.tm / temp-mail / guerrillamail 全在内）。

#### 修复
- 用真实邮箱（gmail + 别名 `you+xxx@gmail.com`）
- 用付费一次性邮箱服务（domain 没在黑名单里）
- 标记这些 Recipe 为 `ManualMode + RequiredMaterials="真实邮箱（不要用 tempmail）"`

---

### C5. 5sim 短信服务有"按国家产品定价"

**伤害指数**：⭐⭐

#### 现象
账号有 30 RUB（约 $0.4），尝试 OpenAI 短信验证 → "Insufficient funds"。

#### 真根因
OpenAI 用美国号码（USA），单价 ~30 RUB。但**俄罗斯本地号** 5 RUB。系统不会自动选最便宜的。

#### 修复
- `service/pool_sms_provider.go::fivesimProvider` 加 `PriceLimit` 参数
- API 选号要传 `country=any` + `maxPrice` 让 5sim 路由
- 文档注明充值至少 100 RUB 起步（保证够买美区号几次）

---

## D. UI / Semi Design / 前端

### D1. 编辑 Modal 打开是空的

**伤害指数**：⭐⭐⭐⭐（直接被老板骂）

#### 现象
点「编辑」 → Modal 弹出 → 表单字段全空。

#### 真根因
`<Form initValues={editing}>` 的 `initValues` **只在 Form mount 时读一次**。第一次打开 Modal mount 完，第二次打开 Modal 已经在 mount 状态，`initValues` 不会重新读取。

#### 修复
三连：
```jsx
<Modal destroyOnClose visible={open} ...>
  <Form
    key={editing?.id}              // ① 不同 id → 重新 mount
    initValues={editing}
  />
</Modal>
```

加 `destroyOnClose` 也起作用（Modal 关掉时整体卸载）。

`key={editing?.id}` 是更稳的"强制重渲染"信号。

#### 预防
- 在 [`coding-standards.md`](../02-development/coding-standards.md) 立规矩
- code review 看到 `<Form initValues>` 必有这个 key

---

### D2. `IllustrationNoContent` 不存在

**伤害指数**：⭐

#### 现象
```jsx
import { IllustrationNoContent } from '@douyinfe/semi-illustrations';
// ❌ Module has no exported member 'IllustrationNoContent'
```

#### 真根因
Semi 包 `@douyinfe/semi-illustrations` 只导出特定几个 Illustration（NoResult / NoAccess / Construction），没有 NoContent。

#### 修复
找近义词：
```jsx
import { IllustrationNoResult } from '@douyinfe/semi-illustrations';
```

#### 预防
开发前先 `node -e "console.log(Object.keys(require('@douyinfe/semi-illustrations')))"` 看导出清单。

---

### D3. Notification 太密集触发 Semi 警告

**伤害指数**：⭐

#### 现象
连续点几次按钮 → Console 大量 `Notification with same content already exists`。

#### 修复
给 notification 一个 ID：
```jsx
Notification.error({
  id: 'pool-bind-error',     // 同 id 会自动去重
  title: '...',
  content: '...',
});
```

或用 `useRef` 防抖。

---

### D4. Semi Form 的 `Field` 嵌套 `Switch` 不更新

**伤害指数**：⭐⭐

#### 现象
切换 `<Form.Switch>` → 视图正常变化，但 `formApi.getValues().manual_mode` 还是旧的。

#### 真根因
Switch 不是受控组件时，要用 `field` props 让 Form 接管。

#### 修复
```jsx
<Form.Switch
  field="manual_mode"        // ✅ Form 接管
  label="..."
/>
```

不要混用 `value/onChange` 和 `field`。

---

## E. 编译 / embed / 二进制

### E1. `go build` 用了缓存的旧 dist

**伤害指数**：⭐⭐⭐

#### 现象
前端改了文案，编译完跑起来浏览器仍是老版本。

#### 真根因
Go 编译器看 `//go:embed web/dist` 的源文件没变，**直接用 build cache**——即使 `dist/index.html` 改了。

#### 修复
强制重编：
```bash
cd new-api-src
touch main.go              # 让 go 觉得源码变了
go build -a -o ../new-api/new-api-macos
```

`-a` = 重编所有依赖。

#### 预防
- 写一个 `build.sh` 标准脚本，里面带 `touch + -a`
- CI 永远 fresh build

---

### E2. macOS binary 在 Linux `cannot execute binary file`

**伤害指数**：⭐

#### 现象
SCP `new-api-macos` 到 ubuntu → 跑不起来。

#### 真根因
macOS arm64 ↔ Linux x64 不兼容，要交叉编译。

#### 修复
```bash
GOOS=linux  GOARCH=amd64 go build -o new-api-linux-x64
GOOS=linux  GOARCH=arm64 go build -o new-api-linux-arm64
GOOS=darwin GOARCH=arm64 go build -o new-api-macos
```

#### 预防
- 命名带架构后缀
- 部署文档先 `uname -m` 再下二进制

---

### E3. `start.command` 双击没反应

**伤害指数**：⭐⭐

#### 现象
点了没反应；右键打开 → 弹"无法验证开发者"。

#### 修复
```bash
chmod +x start.command new-api-macos
xattr -d com.apple.quarantine start.command new-api-macos 2>/dev/null
```

#### 预防
README / start.command 里写 first-run 说明。

---

### E4. `start.command` 升级时 binary 还在跑

**伤害指数**：⭐⭐

#### 现象
脚本：
```bash
#!/bin/bash
mv ~/Downloads/new-api-macos new-api-macos
./new-api-macos
```
执行 → "Text file busy"。

#### 真根因
旧进程还在跑，`mv` 时 macOS 不允许覆盖正在执行的二进制。

#### 修复
分两步：
```bash
pkill -f new-api-macos
sleep 2
mv new-api-macos.new new-api-macos
./new-api-macos
```

或文件名分版本：`new-api-macos-v0.5.0`，软链 `new-api-macos -> new-api-macos-v0.5.0`。

---

## F. Telegram / 第三方 API

### F1. Telegram `chat not found`

**伤害指数**：⭐⭐

#### 现象
Bot Token + Chat Id 都填了，发消息返回 400。

#### 真根因
`chat_id` 用错了：
- 用了**用户名** `@yourname` → 私聊不一定支持
- 用了**群组数字** 但 bot 没被加进群

#### 修复
1. 让 bot 先发一条任意消息到目标位置
2. 用 `getUpdates` 读真实 chat id：
   ```bash
   curl "https://api.telegram.org/bot<TOKEN>/getUpdates" | jq '.result[].message.chat'
   ```
3. 把那个数字 id 填到「Telegram 设置」

#### 预防
`SendTelegramMessage` 解析 API 返回的 `description` 字段（"chat not found" / "bot was blocked by the user"），把人类可读错误回传到前端。

---

### F2. Telegram MarkdownV2 转义

**伤害指数**：⭐

#### 现象
告警消息里有 `_` 或 `*` → Telegram 不解析或返回 `can't parse entities`。

#### 修复
```go
func tgEscape(s string) string {
    chars := "_*[]()~`>#+-=|{}.!"
    for _, c := range chars {
        s = strings.ReplaceAll(s, string(c), "\\"+string(c))
    }
    return s
}
```

或干脆用 `parse_mode=HTML`，转义 `<`/`>`/`&` 即可。

---

### F3. mail.tm token 30 分钟过期

**伤害指数**：⭐⭐

#### 现象
长流程跑到一半 → mail.tm API 返回 401。

#### 修复
拿 token 时一并存 expire ts，每次 API 调用前检查，过期就重新 login。

---

## G. 流程 / 协作 / 文档

### G1. "我后面再补文档" = 永远不补

**伤害指数**：⭐⭐⭐⭐

#### 现象
v0.2 加了 5 个新接口，commit 写"docs todo"。半年后排查问题，前端写的字段名和后端看到的不一致 → 4 小时往返才查清楚。

#### 修复
把"改文档"挂到**同一个 commit**：
```
git add controller/pool.go docs/07-reference/pool-api.md
git commit -m "feat(pool): 加 BindPoolAccountChannel + 文档"
```

写 PR / 合并清单时把"改文档"列死，不勾不让合。

---

### G2. mock 数据混进生产 db

**伤害指数**：⭐⭐⭐

#### 现象
为了开发 worker 写了 `debug-mock` Recipe + Runner，本地建了 5 条 mock PoolAccount + Channel。后来在生产 db 里发现还在。

#### 真根因
- `SeedDefaultPoolRecipes()` 把 `debug-mock` 也写进默认了
- 老板看到一堆名字奇怪的 channel + 价格估算虚高

#### 修复
- 删 `service/pool_runner_mock.go`
- 删 `buildDefaultRecipeSeeds()` 里的 `debug-mock` 项
- 一次性清理 SQL：
  ```sql
  DELETE FROM channels WHERE name LIKE 'mock%';
  DELETE FROM pool_accounts WHERE provider='mock';
  DELETE FROM pool_recipes WHERE \`key\`='debug-mock';
  DELETE FROM pool_jobs WHERE recipe_key='debug-mock';
  DELETE FROM abilities WHERE channel_id IN (...) ;
  ```

#### 预防
- 永远不把 `mock` / `test` / `debug` 字样的 Seed 放默认列表
- 临时数据用专门的 seed 函数 + `if env=="dev"` 包起来

---

### G3. 用 Job ID `2` 而不是 `1` 测试

**伤害指数**：⭐

#### 现象
准备演示 manual-result：
```bash
curl -X POST $BASE/api/pool/jobs/2/manual-result -d '{...}'
# Error: record not found
```

#### 真根因
我创建了 Job 但拿到 `job_id=1`，curl 默写了 2。

#### 修复
脚本里**必从响应里拿 id**，不要硬编码：
```bash
JOB_ID=$(curl -s ... | jq -r '.data.job_id')
curl -X POST $BASE/api/pool/jobs/$JOB_ID/manual-result ...
```

---

### G4. 提交时把 `.env` 拖进去

**伤害指数**：⭐⭐⭐⭐⭐（最严重的隐患）

#### 现象（虚惊）
`git status` 里有 `.env`，差点 `git add .`。

#### 修复
- `.gitignore` 提前写好：
  ```
  .env
  *.env.local
  one-api.db*
  data/logs/
  backups/
  ```
- 装 `git secrets`：`brew install git-secrets && git secrets --install`

#### 预防
- 项目根放 `.env.example`（不含真值）
- pre-commit hook 检查含 `sk-` `AIza` `AKIA` 的字符串

---

### G5. 一次改太多东西，回滚都不知道改回哪个

**伤害指数**：⭐⭐⭐

#### 现象
"加 Recipe + 改路由 + 改 UI + 改 Telegram"塞一个 commit。事后发现 Telegram 部分有 bug，想 revert 单 Telegram 改动 → 整 commit 回滚就把其他全没了。

#### 修复
- 一个改动 = 一个 commit
- 改前先 `git status` 看清当前 dirty
- 改完 `git add -p` 分块加（不是一锅 `git add .`）

---

## H. 部署 / 分域 / CORS

> 场景：前端 React 部署在 Cloudflare Pages（`admin.xxx.com`），
> 后端只做 API 跑在自建服务器（`api.xxx.com`）。

### H1. 用 curl 验证 CORS = 自欺欺人

**伤害指数**：⭐⭐⭐⭐⭐（2026-10-09 / 10-10 连续踩了三个来回）

`curl` **不执行浏览器的 CORS 规则**。下面这些它一概不报错：
预检不通过、`Allow-Headers` 少了某个自定义头、`*` 与凭证互斥……
全都返回 200/204。于是"我测过了，是通的"——用户一开浏览器就报错。

**唯一可信的验证方式**是按规范逐条断言（见本节末尾脚本），
或者直接在浏览器 Network 面板里看。

### H2. 登录态是 session cookie，不是 token

**伤害指数**：⭐⭐⭐⭐

`controller/user.go` 的 `setupLogin` **只写 session**，响应体里没有 token；
`middleware/auth.go` 的 `authHelper` 优先读 `session.Get("username")`，
只有 session 为空时才回退去查 `Authorization` 里的 access token。

跨域 XHR 默认 `credentials: 'same-origin'` —— **既不保存响应里的 `Set-Cookie`，也不回传**。
表现极具迷惑性：

- 登录接口 200，UI 也跳进去了（用户信息来自响应体）
- **一刷新就掉登录**，`/api/user/self` 401，提示"未登录或登录已过期"

**修**：前端 axios 加 `withCredentials: true` + 后端 CORS 放行对应 Origin。

### H3. 凭证模式下两个通配符都会失效

**伤害指数**：⭐⭐⭐⭐

浏览器规则（当 `Access-Control-Allow-Credentials: true` 时）：

| 头 | 非凭证 | 凭证模式 |
|---|---|---|
| `Access-Control-Allow-Origin: *` | ✅ 通配 | ❌ **无效**，必须回显具体 Origin |
| `Access-Control-Allow-Headers: *` | ✅ 通配 | ❌ **被当成字面量头名**，自定义头全被拒 |

`Allow-Headers` 这条尤其阴——报错是
`Request header field new-api-user is not allowed by Access-Control-Allow-Headers`，
看着像"少配了一个头"，其实是 `*` 在凭证模式下不作数。

**修**：凭证模式下回显浏览器实际请求的头（`Access-Control-Request-Headers`）。

### H4. 为什么不能对所有 Origin 都回显

回显任意 Origin + `Allow-Credentials: true` = **任意网站都能拿受害者的登录 Cookie
读接口响应**——攻击者开个页面就能把 `/api/token/` 里的密钥读走。

但全收紧又会挡住第三方网页客户端调 `/v1/*`（它们自带 API Key、不带 Cookie）。

**所以策略要分流**：

| Origin | Allow-Origin | Allow-Credentials |
|---|---|---|
| 命中 `FRONTEND_BASE_URL` / `TRUSTED_ORIGINS` | 回显该 Origin | `true` |
| 其它 | `*` | 不带 |

### H5. `api` 域名必须是灰云（仅 DNS）

橙云会被 Cloudflare 的 **100 秒 HTTP 超时**掐断 LLM 长流式响应（524）。
界面域名（静态站）反而适合橙云。

### H6. 尾斜杠 301 会丢掉 CORS 头（只在跨域部署时炸）

gin 对**没注册**的路径做「去尾斜杠」的 301 重定向，而重定向发生在**中间件链之前** ——
`middleware.CORS()` 根本没机会运行，于是 301 响应里没有 `Access-Control-Allow-Origin`。

- 同源部署（前端由同一个 Go 进程托管）**完全感觉不到**：浏览器照常跟随重定向。
- 分域部署（前端在 Pages、后端在 api 域名）时，浏览器在**第一跳**就判定 CORS 失败：
  `No 'Access-Control-Allow-Origin' header is present on the requested resource`。

2026-10-10 实测中招的三条路径（前端写 URL 时手滑多了个尾斜杠）：
`/api/data/self/`、`/api/log/self/`、`/api/mj/self/`，对应客户端的
**数据看板**和**使用日志**两个页面——现象是"页面白屏"，控制台里才看得到真正的原因。

**两条都要做**：

1. 前端不要写尾斜杠（改了 `useDashboardData.js` / `useUsageLogsData.jsx` / `useMjLogsData.js`）；
2. 后端顺手注册带尾斜杠的别名（`router/api-router.go`），兜住还没刷新到新包的浏览器。

**排查套路**：跨域下任何「接口莫名失败」，先 `curl -o /dev/null -w '%{http_code}'` 看一眼是不是 301。

### H7. 跨域登录要同时过三关：CORS 白名单 / CSP connect-src / New-API-User 头

营销站的登录框（`www.tokwork.com`）直接 POST 网关的 `/api/user/login`，三关缺一不可，
而且**三关失败的症状都是"登录不了"**，不看浏览器控制台根本分不清：

| 关卡 | 漏了会怎样 | 浏览器控制台里的原话 |
|---|---|---|
| 后端 `TRUSTED_ORIGINS` 白名单 | 预检失败 | `blocked by CORS policy: ... Access-Control-Allow-Headers` |
| 营销站 CSP `connect-src` | 请求根本没发出去 | `Refused to connect because it violates the document's Content Security Policy` |
| 控制台 localStorage 里的用户 id | 登录成功、跳过去 401 | `GET /api/user/self 401` |

前两条是配置，第三条是**架构**：`New-API-User` 头是控制台在 axios 实例创建时从
自己域的 localStorage 读的，跨域写不进去 —— 只能用 URL 带一次 id，
且必须在 bundle 加载前落盘（写在 `index.html` 里，见 §8.6 的 SSO）。

> **后续（2026-10-10）**：第三条最后是用**后端加接口**解决的，不是继续绕：
> `GET /api/user/whoami` 只认 session cookie、不要那个头，前端启动时问一次就知道自己是谁。
> 于是 `?sso_user=` 和 `index.html` 里那段引导脚本全删了。
> **教训**：跨域身份交接这类问题，与其在前端堆约定（URL 传参、localStorage 时序、
> 引导脚本必须在 bundle 之前），不如在后端开一个"只读、不放大权限"的入口把它一次性解决。
> 见 §8.6.9。

### H8. 抄了设计令牌 ≠ 用了同一个 UI 库

客户门户第一版是手搓 CSS + 只用 Kumo 的设计令牌（颜色/圆角/字体），
结果被一眼看穿："为什么达不到 Cloudflare 那种精致程度？"

原因不在配色，而在**组件行为**：按钮的 hover / focus ring / disabled / loading、
弹窗的焦点陷阱与进出场、表格的行 hover 与粘性表头、空状态的插画与引导文案 ——
这些都是组件里的状态机，不是几个 CSS 变量能表达的。

而 `@cloudflare/kumo`（我们早就装了）**一共导出 96 个组件**，
当时只 import 了它的 CSS（`styles/standalone`），是最大的浪费。
改成用 `Button` / `Input` / `Table` / `LayerDialog` / `Badge` / `Empty` / `Meter`
加 `@phosphor-icons/react` 之后，观感和交互立刻对上了 —— 因为那就是 Cloudflare
自己在用的那套东西。

**结论**：要对齐某个产品的设计系统，**先问"它的组件库能不能直接用"**。
能就装它、用它；只抄 token 等于只抄了一半，而"精致"恰恰来自另一半（状态与动效）。

### 验收脚本（改完 CORS 必须跑）

```bash
ORIGIN="https://admin.tokwork.com"
BASE="https://api.tokwork.com"
curl -s -D - -o /dev/null -X OPTIONS -H "Origin: $ORIGIN" \
  -H "Access-Control-Request-Method: GET" \
  -H "Access-Control-Request-Headers: new-api-user" \
  "$BASE/api/status" | grep -i "^access-control"
# 必须看到：
#   access-control-allow-origin: https://admin.tokwork.com   ← 不是 *
#   access-control-allow-credentials: true
#   access-control-allow-headers: new-api-user                ← 不是 *
```

---

## 总结：踩坑后形成的 5 条铁律

1. **Channel 入库 → `ch.Insert()`，绝不用 `DB.Create(ch)`**
2. **bool 字段 → 不写 `gorm:"default:..."`**
3. **Modal 编辑表单 → `destroyOnClose + key + initValues` 三件套**
4. **改代码 → 同 commit 改文档**
5. **不存在的反爬绕过——chromedp 不能赢 Cloudflare/reCAPTCHA/hCaptcha，承认这一点节省 80% 时间**

---

> 下一次踩到新坑，**马上**追加到这里。半年后接手的人会感谢你（很可能就是你自己）。
