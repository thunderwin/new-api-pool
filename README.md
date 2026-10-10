<div align="center">

# new-api-pool

**集成「号池管理」一级菜单的 new-api 硬分支**

[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)
![Status](https://img.shields.io/badge/status-self--hosted_alpha-orange)
![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go)
![Node](https://img.shields.io/badge/Bun-React-pink?logo=bun)

[简体中文](#简介-zh) · [架构总览](docs-pool/01-overview/architecture.md) · [上手手册](docs-pool/02-development/setup.md) · [API 文档](docs-pool/07-reference/pool-api.md)

</div>

---

## 简介 (zh)

`new-api-pool` 是 [QuantumNous/new-api](https://github.com/QuantumNous/new-api) 的硬分支（hard fork），在它的 LLM Gateway 内核之上**深度集成了一个完整的号池（Key Pool）管理系统**——把「上游账号 → API Key → 渠道（Channel）→ 路由（Ability）」这条链路的全部环节做成一级菜单可视化运营，并接入 Telegram 巡检告警、自动注册编排、计费 / 利润看板。

**和原版 new-api 的区别**：

| 维度 | 上游 new-api | 本 fork (`new-api-pool`) |
|---|---|---|
| 上游 Key 管理 | 渠道一张表，手工填 | ★ 一级菜单"号池管理"，覆盖账号/配方/作业/巡检/利润 |
| 上游账号注册 | 不涉及 | ★ Recipe + Job 编排（19 条内置配方），半自动 + 可扩展全自动 |
| Telegram 告警 | 不涉及 | ★ 内置巡检 cron + 触发模型推送 |
| 一键绑渠道 | 手动同步 | ★ 号池账号录入即自动建/绑 channel + abilities |
| 文档 | 多语言 README | ★ `docs-pool/` 下 24 篇工程文档 + `deploy/` 生产蓝图 |

> ⚠️ 这是**自维护的硬分支**，不计划与上游同步合并。需要上游最新功能请直接用 [QuantumNous/new-api](https://github.com/QuantumNous/new-api)。
> 反过来说：本 fork 一切围绕「号池运营」打磨，对个人 / 小团队自营 LLM 中转站友好。

---

## 60 秒上手

```bash
git clone <THIS_REPO> new-api-pool
cd new-api-pool

# 1. 编译（要求 Go 1.26+ + Bun 1.x）
cd web && bun install && bun run build && cd ..
go build -o new-api-macos .          # 或 GOOS=linux GOARCH=amd64 go build -o new-api-linux-x64

# 2. 启动
./new-api-macos --port 3000          # 或者 ./start.command（macOS 双击启动）

# 3. 浏览器
open http://localhost:3000
# 首次访问会跳到初始化向导，自己设管理员账号（≤12 字符）和密码（≥8 字符）
# 系统没有内置默认账号，别去找 root/123456
```

更详细：[`docs-pool/02-development/setup.md`](docs-pool/02-development/setup.md) / [`docs-pool/04-usage/admin-quickstart.md`](docs-pool/04-usage/admin-quickstart.md)

---

## 截图速览

> （TODO：贴号池管理 5 个子页 + 巡检告警截图。开源前没准备截图，PR 欢迎）

| 一级菜单 | 子页 | 功能 |
|---|---|---|
| 🏊 **号池管理** | 总览 | 健康分、按 Provider 分组的账号 / 余额 / 用量 |
|  | 账号 | CRUD 上游 Key，明文显示，一键建/绑 Channel |
|  | 配方 & 作业 | 19 条内置 Recipe，入队 → 录入结果 → 自动入号池 |
|  | 利润 | 收入 / 估算上游成本 / 毛利分组分析 |
|  | 巡检告警 | Telegram 推送、手动巡检、告警历史 |

---

## 文档地图

| 路径 | 内容 |
|---|---|
| [`docs-pool/01-overview/`](docs-pool/01-overview/) | 架构 / 仓库结构 / 术语表 |
| [`docs-pool/02-development/`](docs-pool/02-development/) | 本地开发环境、加 Recipe 教程、编码规范 |
| [`docs-pool/03-deployment/`](docs-pool/03-deployment/) | 本地部署 / 生产部署 |
| [`docs-pool/04-usage/`](docs-pool/04-usage/) | 管理员 5 分钟快速入门、号池子菜单功能详解、19 条 Recipe 完整步骤 |
| [`docs-pool/05-architecture/`](docs-pool/05-architecture/) | ★ 数据模型、路由 / abilities 机制、Pool Pipeline、ADR |
| [`docs-pool/06-operations/`](docs-pool/06-operations/) | 巡检告警 / 备份恢复 / 排错索引 |
| [`docs-pool/07-reference/`](docs-pool/07-reference/) | API 参考、Changelog、踩坑大全、进度路线图 |
| [`docs-pool/08-security.md`](docs-pool/08-security.md) | 安全合规要点 |
| [`docs-pool/09-contributing.md`](docs-pool/09-contributing.md) | 贡献指南 |
| [`deploy/`](deploy/) | 生产蓝图：12 周路线、Compose、Caddy、监控、Runbook、合规模板 |

---

## 协议与归属

- **协议**：AGPL-3.0（继承自上游 [QuantumNous/new-api](https://github.com/QuantumNous/new-api/blob/main/LICENSE)）。
- **核心 Gateway 内核**：99% 来自上游 new-api，全部署名归属 [@QuantumNous](https://github.com/QuantumNous) 及其贡献者。
- **本 fork 新增**：`controller/pool.go`、`router/pool-router.go`、`model/pool_*.go`、`service/pool_*.go`、`web/src/pages/Pool/*`、`docs-pool/`、`deploy/`。
- 上游原版多语言 README 已保留为 [`README.upstream.md`](README.upstream.md)、[`README.zh_CN.md`](README.zh_CN.md) 等。

⚠️ **AGPL-3.0 关键约束**：如你修改本仓库代码并对**外部网络用户**提供服务（包括 SaaS / 中转站），**必须**把你的修改后源码以 AGPL-3.0 形式公开。这是分发本项目的硬性约束。

---

## 致谢

- [QuantumNous/new-api](https://github.com/QuantumNous/new-api) — 没有上游就没有本 fork。
- [songquanpeng/one-api](https://github.com/songquanpeng/one-api) — 一切的源头。
- [mail.tm](https://mail.tm) — 一次性邮箱基础设施。
- [5sim.net](https://5sim.net) — 真实手机号池服务。
- 所有给 issue 出 bug 的同学。
