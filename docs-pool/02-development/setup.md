# 开发环境搭建

> 目标：拉下源码 → 改一行代码 → 重新编译 → 看到改动生效，整个 loop 在 macOS 上 ≤ 5 分钟。

## 1. 系统要求

| 项 | 版本 | 备注 |
|---|---|---|
| OS | macOS 12+ / Linux | 文档以 macOS 为主，Linux 命令对应替换 `darwin` 为 `linux` |
| Go | **1.26+**（见 `go.mod` line 4） | `brew install go` |
| Bun | latest（前端包管理） | `curl -fsSL https://bun.sh/install \| bash`（Homebrew 没有官方包） |
| SQLite CLI | 3.x | `brew install sqlite`（仅排错时需要） |
| 浏览器 | Chrome / Edge / Safari 任意 | 看后台用 |

## 2. 拉源码（已经拉好就跳过）

源码目录：`<REPO>/`

如果是从 git 重新拉：
```bash
git clone https://github.com/QuantumNous/new-api.git new-api-src
cd new-api-src
# 然后把本仓库的 pool 相关文件覆盖进去（或直接 fork 自己 push 一份）
```

## 3. 一次性初始化

```bash
cd <REPO>

# Go 依赖（首次会拉 200MB+，go.sum 已锁定）
go mod download

# 前端依赖（用 bun，比 npm 快 10x）
cd web && bun install && cd ..
```

## 4. 编译

### 4.1 一次完整编译（最常用）

```bash
cd <REPO>

# 1) 编前端 → web/dist/
(cd web && bun run build)

# 2) 编后端 → 单文件 binary（前端 dist 用 //go:embed 嵌进去了）
go build -o new-api-macos .

# 3) 拷到运行体目录
go build -o new-api-macos .
```

产物体积约 70–100 MB（含整个前端 + tokenizer + 所有 LLM SDK）。

### 4.2 只改后端的快编译

```bash
cd <REPO>
go build -o new-api-macos .
```

### 4.3 只改前端的快编译

```bash
cd <REPO>/web
bun run build
cd ..
go build -o new-api-macos .
```

> ⚠️ Go 的 `//go:embed` 默认会缓存编译结果。当**只改了 web/dist/** 时偶尔会出现 binary 没更新的"假象"，强制重编：
> ```bash
> touch main.go && go build -a -o new-api-macos .
> ```

### 4.4 前端开发模式（边改边看）

```bash
cd <REPO>/web
bun run dev      # vite dev server: http://localhost:5173
```
`web/package.json` 里有 `"proxy": "http://localhost:3000"`，请求会代理到本地 new-api。**先用 4.1 起一次后端**，再开 dev server。

## 5. 启动

### 5.1 双击启动（推荐）

```bash
cd <REPO>
./start.command
```

启动后日志会输出：
```
[1/3] binary OK (XX MB) — 自编译 fork 版本
[2/3] 设置执行权限...
[3/3] 启动 new-api，监听 http://localhost:3000
首次启动：浏览器会进入初始化向导，请自己设置管理员账号（≤12 字符）和密码（≥8 字符）
号池管理入口：登录后侧边栏「号池管理」一级菜单
```

5 秒后会自动打开浏览器到 `http://localhost:3000`。

### 5.2 命令行启动 / 调试

```bash
cd <REPO>
GIN_MODE=debug ./new-api-macos --port 3000 --log-dir ./logs
```

环境变量：

| 变量 | 默认 | 含义 |
|---|---|---|
| `GIN_MODE` | release | `debug` 打开 gin 详细日志 |
| `PORT` | 3000 | 监听端口（也可用 `--port`） |
| `SQL_DSN` | sqlite | 改成 `pg://...` 即用 PostgreSQL |
| `REDIS_CONN_STRING` | empty | `redis://...` 启用 Redis 缓存 |
| `CHANNEL_UPDATE_FREQUENCY` | unset | 整数秒数，启用渠道自动更新 cron |
| `BATCH_UPDATE_ENABLED` | false | 计费批量更新（多节点用） |
| `ENABLE_PPROF` | false | `true` 开 pprof 在 :8005 |

## 6. 首次访问

1. 打开 `http://localhost:3000`
2. 登录：`root` / `123456`
3. **第一件事**：右上角头像 → 修改密码
4. 左侧菜单点开「号池管理」一级菜单 → 进「自动注册」页 → 应能看到 19 个内置 Recipe

如果一级菜单**看不到**：
- 检查当前账号是否管理员（root 默认是；普通用户菜单会被前端按 `isAdmin()` 隐藏）
- 看 `logs/` 里的启动日志有没有 seed 错误
- 检查 SQLite：`sqlite3 one-api.db "SELECT key,name,enabled FROM pool_recipes LIMIT 5"`

## 7. 数据库

| DB | 路径 | 内容 |
|---|---|---|
| 业务库 | `<REPO>/one-api.db` | 渠道 / 用户 / 令牌 / 号池 / 告警 / options |
| 日志库 | 同上（默认与业务库合一） | requests / consumption logs；可用 `LOG_SQL_DSN` 拆分 |

排错命令：
```bash
sqlite3 <REPO>/one-api.db ".tables" | tr ' ' '\n' | grep -E "pool|channel|ability"
sqlite3 <REPO>/one-api.db "SELECT id,name,type,status,tag,group_ FROM channels LIMIT 10"
sqlite3 <REPO>/one-api.db "SELECT * FROM pool_jobs ORDER BY id DESC LIMIT 5"
```

> SQLite 列名 `group` 是关键字，glebarez/sqlite 会编成 `group_`，但应用层读写正常用 `Group`/`group`。

## 8. 常见首次坑

| 现象 | 原因 | 修 |
|---|---|---|
| `go build` 报 `module ... go 1.26` | 本机 Go 太老 | 升 1.26+ |
| `bun: command not found` | 没装 bun | `curl -fsSL https://bun.sh/install \| bash`，重启 shell |
| binary 起不来 / 闪退 | macOS Gatekeeper | `xattr -d com.apple.quarantine new-api-macos`（start.command 已自动做） |
| binary 体积 < 50MB | embed 没生效，前端没编 | `(cd web && bun run build)` 后重编 |
| 后台看不到「号池管理」菜单 | 没 root 权限 / Recipe seed 失败 | 用 root 登录 + 看日志 |
| 注册号池 Recipe 列表是空 | Seed 没跑 | 第一次启动会自动 Seed；不行就 `DELETE FROM pool_recipes` 后重启 |
| 改完前端没生效 | embed 缓存 | `touch main.go && go build -a` |

## 9. 推荐编辑器配置

### VS Code / Cursor
- Go 插件：`golang.go`
- Tailwind 提示：`bradlc.vscode-tailwindcss`
- ESLint：`dbaeumer.vscode-eslint`
- 项目根放 `.vscode/settings.json`：
  ```json
  {
    "go.testTimeout": "300s",
    "[go]": { "editor.formatOnSave": true },
    "[javascriptreact]": { "editor.defaultFormatter": "esbenp.prettier-vscode" }
  }
  ```

### GoLand / WebStorm
- Run Config: `go run .`，工作目录设为 `new-api-src`。
- 前端 npm 脚本面板能直接看到 `bun run dev`/`build`。

## 10. 下一步

- 改第一行代码 → 改个文案：`new-api-src/web/src/pages/Pool/Overview.jsx` 标题改成你的名字 → 重编 → 刷新页面看到。
- 加新功能 → 看 [add-recipe.md](./add-recipe.md)。
- 提交规范 → [coding-standards.md](./coding-standards.md)。
