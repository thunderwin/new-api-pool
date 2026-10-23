# New API（号池管理 fork）— 工程文档

> 这是 [QuantumNous/new-api](https://github.com/QuantumNous/new-api) 的**自维护 fork**，
> 在原 new-api 基础上整合了「**号池管理**」一级菜单及其 6 个子模块，
> 目的：把上游账号采购 / 注册 / 巡检 / 对账 / 告警这一整套运营流程
> 直接做进 new-api 后台，避免再单跑一套独立系统。

---

## 1. 这份文档给谁看

| 读者 | 目标 | 读哪几篇 |
|---|---|---|
| **首次接手的开发者** | 5 分钟摸清仓库结构，1 小时跑起本地 | [01-overview](./01-overview/) → [02-development/setup.md](./02-development/setup.md) → [03-deployment/local.md](./03-deployment/local.md) |
| **API 调用方（客户）** | 拿 Key、鉴权、调各家模型和第三方 API、看懂扣费和报错 | [04-usage/customer-api-guide.md](./04-usage/customer-api-guide.md)（可直接发给客户） |
| **业务运营 / 老板** | 知道后台哪些菜单做什么、怎么登入、怎么补号 | [04-usage/admin-quickstart.md](./04-usage/admin-quickstart.md) → [04-usage/pool-management.md](./04-usage/pool-management.md) → [04-usage/recipe-walkthrough.md](./04-usage/recipe-walkthrough.md) |
| **要加新功能的工程师** | 加一个新上游 / 新 Recipe / 新 Provider | [05-architecture/](./05-architecture/) → [02-development/add-recipe.md](./02-development/add-recipe.md) |
| **运维 / SRE** | 备份、巡检、告警、出事故怎么办 | [06-operations/](./06-operations/) → [07-reference/pool-api.md](./07-reference/pool-api.md) |
| **安全合规** | 数据怎么放、密钥怎么管、上游 ToS 风险 | [08-security.md](./08-security.md) |

---

## 2. 完整目录

```
docs/
├── README.md                         ← 你在这里
│
├── 01-overview/                      系统是什么、长什么样
│   ├── architecture.md               架构图 + 数据流
│   ├── repo-layout.md                3 个目录（new-api / new-api-src / deploy）的分工
│   └── glossary.md                   术语表（Channel / Ability / Recipe / Job / Pool ...）
│
├── 02-development/                   写代码、改代码必备
│   ├── setup.md                      本地开发环境（Go + Bun + SQLite）
│   ├── add-recipe.md                 加一个新 Recipe / 新上游的完整 SOP
│   └── coding-standards.md           Go / React 风格 + commit 约定
│
├── 03-deployment/                    部署 / 升级 / 回滚
│   ├── local.md                      双击 start.command 单机起法
│   └── production.md                 生产部署链 deploy/，单服务器到多区域
│
├── 04-usage/                         后台怎么用 / 客户怎么调
│   ├── customer-api-guide.md         **对外**调用指南：鉴权、OpenAI/Anthropic/Gemini 三种格式、Capability、计费、错误码
│   ├── admin-quickstart.md           5 分钟入门管理员后台
│   ├── pool-management.md            「号池管理」一级菜单 + 6 子菜单完整说明
│   └── recipe-walkthrough.md         每个内置 Recipe 的人工注册步骤
│
├── 05-architecture/                  内部原理（写代码前先读）
│   ├── data-model.md                 表结构（PoolAccount/Recipe/Job/Alert/Channel/Ability）+ ER
│   ├── routing-abilities.md          new-api 的路由原理：Group + Models + abilities 三剑客
│   ├── pool-pipeline.md              注册管线：Worker → Runner → Account → Channel
│   └── decisions.md                  ADR 合集：为什么 fork / 为什么删 chromedp / 为什么是一级菜单
│
├── 06-operations/                    日常运维
│   ├── monitoring-alerts.md          健康巡检 + Telegram 告警 + 自检日志
│   ├── backup-restore.md             SQLite + data 目录备份恢复
│   └── troubleshooting.md            按现象分类的排错手册
│
├── 07-reference/                     接口 / 变更 / 实录
│   ├── pool-api.md                   /api/pool/* 完整 API 参考
│   ├── capability-api.md             第三方同步/异步 Capability API（2026-10-22 上线）
│   ├── changelog.md                  从 fork 起的关键节点
│   ├── lessons-learned.md            踩坑大全：路由 / GORM / 反爬 / UI / 编译 …
│   ├── status-and-roadmap.md         当前进度 + 待办池 + 永不做的事
│   ├── admin-user-token.md           管理员代用户创建 Token API（幂等 / 并发 / 权限）
│   ├── upstream-grsai.md             第二个上游候选：Grsai 的接口 / 模型 / 价格调研（2026-10-10，未接入）
│   └── upstream-wokey.md             当前主上游 Wokey：渠道 / 价格 / 验证 / 回滚（2026-10-20）
│
├── 08-security.md                    密钥处理 / 默认账号 / 暴露面 / 上游 ToS
└── 09-contributing.md                贡献协作约定
```

---

## 3. 一句话现状

> 仓库根目录约定写为 `<REPO>`，例如 `~/code/new-api-pool/`，下面所有相对路径都从那里算起。

- **代码**：`<REPO>/` 整个目录是 Go + React 源码（`main.go` / `controller/` / `service/` / `model/` / `router/` / `web/`）。
- **运行体**：`<REPO>/new-api-macos`（或 Linux 平台对应 binary）是自编译产物；双击 `start.command` 即可起服务。
- **数据**：`<REPO>/one-api.db`（SQLite），所有号池 / 渠道 / 用户 / 日志全在这里；`<REPO>/data/` 存日志和上传。
- **部署蓝图**：`<REPO>/deploy/` 是面向**生产化运营**的 12 周路线图、Docker Compose、Runbook、监控告警、合规文档（与本文档互补：deploy 写"上线后怎么运营"，docs-pool 写"代码本身怎么用怎么改"）。

---

## 4. 阅读建议

1. **第一次来 → 读 4 篇**：[architecture](./01-overview/architecture.md) → [repo-layout](./01-overview/repo-layout.md) → [setup](./02-development/setup.md) → [admin-quickstart](./04-usage/admin-quickstart.md)。
2. **要在号池里新增一个上游 → 读 2 篇**：[add-recipe](./02-development/add-recipe.md) + [pool-pipeline](./05-architecture/pool-pipeline.md)。
3. **要排障 → 1 篇起步**：[troubleshooting](./06-operations/troubleshooting.md)，按现象查；不在表里再看 [pool-api](./07-reference/pool-api.md) 自己 curl。
4. **看决策原因 → 1 篇**：[decisions](./05-architecture/decisions.md)（ADR），可以省掉一堆"为什么不那样写"的重复讨论。
5. **接手 / 准备开干 → 2 篇**：[lessons-learned](./07-reference/lessons-learned.md)（避雷）+ [status-and-roadmap](./07-reference/status-and-roadmap.md)（看做到哪了 / 该做什么）。

---

## 5. 维护本文档的规矩

- **代码改 → 文档改**：新增 / 删除 Recipe、改 API、改菜单、改字段 → 必须同步更新文档对应章节，进同一个 commit。
- **不放假图表**：所有 SQL / 数据 / 截图引用都要能反查到文件路径。
- **保持中文**：代码注释和文档统一用中文（项目个人 fork，不做国际化）。
- **变更进 changelog**：所有破坏性更改、字段迁移、默认值变化 → 进 [07-reference/changelog.md](./07-reference/changelog.md)。
