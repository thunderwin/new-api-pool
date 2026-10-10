# 仓库结构

> 全部代码、文档、部署蓝图、启动脚本都在**同一个仓库根目录** `<REPO>/` 下。
> 数据 / 日志 / 二进制等运行时产物在 `<REPO>/data/` 和 `<REPO>/*.db`，已被 `.gitignore` 排除。

---

## 1. 顶层目录树

```
<REPO>/                            ← 你 git clone 出来的目录
├── main.go                        ← 进程入口
├── go.mod  go.sum
├── makefile
├── start.command                  ← macOS 双击启动脚本
├── docker-compose.dev.yml         ← 本地开发用 Compose（SQLite）
│
├── controller/                    ← Gin HTTP handlers
│   └── pool.go                    ★ 号池所有 handler（≈1244 行 / 22 endpoints）
├── router/                        ← 路由表
│   └── pool-router.go             ★ /api/pool/* 全部路由
├── service/                       ← 业务服务 / cron / 第三方集成
│   ├── pool_worker.go             ★ Job 调度（dispatcher + retry）
│   ├── pool_runners.go            ★ Runner 接口 + 注册表
│   ├── pool_email.go              ★ EmailProvider 抽象
│   ├── pool_mailtm.go             ★ mail.tm 客户端实现
│   ├── pool_sms.go                ★ SmsProvider 抽象 + 5sim
│   └── pool_health.go             ★ 巡检 cron + Telegram 推送
├── model/                         ← GORM 模型 / 迁移 / Seed
│   ├── main.go                    ← AutoMigrate 列表 + 启动 Seed
│   ├── pool_account.go            ★
│   ├── pool_recipe.go             ★ 含 19 条内置 Recipe seed + DefaultModels
│   └── pool_alert.go              ★
├── middleware/                    ← AdminAuth / RouteTag / RateLimit ...
├── relay/                         ← 上游协议适配（OpenAI / Anthropic / Gemini / ...）
├── i18n/  oauth/  pkg/  setting/  dto/  common/  constant/
│
├── web/                           ← 前端（Bun + Vite + React + Semi UI）
│   ├── package.json
│   ├── vite.config.js
│   ├── public/                    ← 静态资源（logo / favicon）
│   ├── dist/                      ← `bun run build` 产物（//go:embed 嵌入 binary）
│   └── src/
│       ├── App.jsx                ← 全局 Routes（含 lazy /pool/*）
│       ├── components/layout/SiderBar.jsx     ★ 一级菜单注册号池入口
│       └── pages/Pool/            ★ 一级菜单 5 个子页 + 共用 Layout
│           ├── PoolPageLayout.jsx
│           ├── Overview.jsx
│           ├── Accounts.jsx
│           ├── Recipes.jsx
│           ├── Billing.jsx
│           └── Alerts.jsx
│
├── docs/                          ← 上游 new-api 自带文档（i18n / channel / openapi）
├── docs-pool/                     ← ★ 你正在看的工程文档（本仓库 fork 新增）
│   ├── README.md                  ← 总入口
│   ├── 01-overview/
│   ├── 02-development/
│   ├── 03-deployment/
│   ├── 04-usage/
│   ├── 05-architecture/
│   ├── 06-operations/
│   ├── 07-reference/
│   ├── 08-security.md
│   └── 09-contributing.md
│
├── deploy/                        ← ★ 生产部署蓝图（fork 自带）
│   ├── ROADMAP.md                 ← 12 周上线路线图
│   ├── docker-compose.prod.yml    ← 生产 Compose（Postgres + Redis + Caddy）
│   ├── caddy/                     ← 反向代理配置
│   ├── monitoring/                ← Prometheus / Grafana / Alertmanager
│   ├── runbooks/                  ← 9 个 P0 事故 runbook
│   ├── legal/                     ← ToS / Privacy / AUP / Refund 模板
│   ├── compliance/                ← 数据保留 / GDPR / 个保法
│   ├── billing/  bi/  risk-control/  customer-ops/  ...
│   └── docs/                      ← 面向调用方的 API 用户文档
│
├── ops/                           ← ★ 生产环境**实际在跑**的部署脚本（区别于上面的蓝图）
│   ├── README.md                  ← 使用顺序 + 和 deploy/ 的区别
│   ├── hetzner-prep.sh            ← 服务器初始化（swap / Docker / ufw）
│   └── deploy/                    ← Dockerfile / compose / Caddyfile / 密钥生成 / 备份 / rclone
│
├── README.md                      ← 仓库门面（fork 自我介绍）
├── README.zh_CN.md  README.fr.md  README.ja.md  README.zh_TW.md  ← 上游多语言 README
├── LICENSE                        ← AGPL-3.0（继承自上游 new-api）
├── .gitignore                     ← 已排除 *.db / data/ / *.exe / dist / .env / 备份目录
└── .env.example                   ← 环境变量模板
```

---

## 2. 运行时产物（git 不追踪，启动后才出现）

| 路径 | 内容 | 备份策略 |
|---|---|---|
| `<REPO>/new-api-macos`（或 `new-api-linux-x64` 等） | 自编译二进制（含前端 dist） | 重新编译即可，不必备份 |
| `<REPO>/one-api.db` | SQLite 业务库（**全部状态在这一个文件**） | ✅ 必须备份 |
| `<REPO>/one-api.db-wal` / `*.db-shm` | SQLite WAL / 共享内存 | ✅ 一起备份（详见 [`backup-restore.md`](../06-operations/backup-restore.md)） |
| `<REPO>/data/logs/oneapi-YYYY-MM-DD.log` | 应用 + 巡检 + worker 日志 | ⚠️ 选备份（排查问题用） |
| `<REPO>/data/uploads/` | 用户上传文件（如启用） | ⚠️ 选备份 |

---

## 3. 三层文档定位

| 目录 | 角色 | 给谁看 | 何时改 |
|---|---|---|---|
| `docs/` | **上游 new-api 文档**（i18n、channel 类型介绍、API openapi） | 任何 new-api 用户 | 跟上游同步时同步 |
| `docs-pool/` | **本 fork 工程文档**（架构 / 开发 / 部署 / 运维 / 排错 / API） | 改代码 + 维护本 fork 的人 | 任何让代码或行为变化的 commit |
| `deploy/` | **部署蓝图**（Compose / Runbook / SOP / 合规） | 运维 + 老板 | 推到生产 / 出事故 / 法律体检 |

---

## 4. 编译 / 启动的标准动作

```bash
# 0. 一次性
cd <REPO>
cd web && bun install && cd ..

# 1. 改源码后重新编译（前端 + 后端）
cd <REPO>/web && bun run build && cd ..
go build -o new-api-macos .                    # 或 GOOS=linux GOARCH=amd64 go build -o new-api-linux-x64

# 2. 启动
./start.command                                 # macOS 双击或终端跑
# 或： ./new-api-macos --port 3000

# 3. 升级
pkill -f new-api-macos
go build -o new-api-macos .
./start.command
```

详见 [`02-development/setup.md`](../02-development/setup.md) / [`03-deployment/local.md`](../03-deployment/local.md)。

---

## 5. 路径速查（写文档 / 改代码时复制）

| 子系统 | 关键文件 |
|---|---|
| 路由表 | `router/pool-router.go` |
| HTTP handler | `controller/pool.go` |
| ORM 模型 | `model/pool_account.go` `pool_recipe.go` `pool_alert.go` |
| 业务服务 | `service/pool_worker.go` `pool_runners.go` `pool_health.go` |
| Email / SMS | `service/pool_email.go` `pool_mailtm.go` `pool_sms.go` |
| 前端页面 | `web/src/pages/Pool/*.jsx` |
| 菜单注册 | `web/src/components/layout/SiderBar.jsx`、`web/src/App.jsx` |
| Migration / Seed | `model/main.go`（AutoMigrate 列表 + 启动 Seed） |
| 启动入口 | `main.go` `start.command` |
| 部署 | `deploy/docker-compose.prod.yml` `deploy/caddy/` `deploy/monitoring/` |
