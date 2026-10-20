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

## ⚠️ 部署信息（改代码前必读）

> 这一节是给**未来的自己/协作者**看的。搞错账号是这套部署最容易翻车的地方。

### 生产环境在哪

| 部分 | 位置 |
|---|---|
| 后端（new-api + Postgres + Redis + Caddy） | **Hetzner Cloud**，纽伦堡 `nbg1`，`2.31.8.21` |
| 客户 API | `https://api.tokwork.com`（灰云直连 Hetzner，避免 Cloudflare 100s 超时掐断流式响应） |
| 管理面板（React SPA） | `https://admin.tokwork.com` → **Cloudflare Pages** 项目 `tokwork-panel` |
| 宣传站 | `www.tokwork.com` → Cloudflare Worker `tokwork-homepage`（apex `tokwork.com` 2026-10-20 核实**没挂上、打不开**，见 tokwork-nbg1.md §4） |
| 异地备份 | **Cloudflare R2** 桶 `tokwork-backup`（每日 `pg_dump` 自动上传） |

详细部署记录（含资源预算、构建链路、踩坑、排错速查）：
[`docs-pool/03-deployment/tokwork-nbg1.md`](docs-pool/03-deployment/tokwork-nbg1.md)

### 对客户暴露的入口（2026-10-20 核实）

| 用途 | 地址 | 谁托管 |
|---|---|---|
| 宣传站 + 账号入口（登录/注册/找回密码） | `https://www.tokwork.com` | Worker `tokwork-homepage` |
| **客户门户**：概览 / API Key / 调用历史 / 账户 | `https://www.tokwork.com/console` | 同一套 Next.js 应用 |
| 文档 / 价格 / 状态 | `https://www.tokwork.com/docs` · `/pricing` · `/status` | 同上 |
| 管理面板（**只给 root 用**，非 root 账号会被送回 `www.tokwork.com/console`） | `https://admin.tokwork.com` | Pages `tokwork-panel`（new-api 自带 SPA） |
| API | `https://api.tokwork.com/v1`（网页路径 301 到 www，不到 admin） | Hetzner（灰云直连） |

> `console.tokwork.com` 已于 2026-10-20 **整个下线**（Worker 自定义域与 DNS 都删了），不再做 301。

**客户看的一切都是我们自己写的**（2026-10-10 定的方向）：门户是营销站这套 Next.js
应用里的 `/console/**`，不再用 new-api 自带的界面。机制与取舍见
[`docs-pool/03-deployment/tokwork-nbg1.md`](docs-pool/03-deployment/tokwork-nbg1.md) §8.6.9。

### 当前在售（2026-10-10 核实）

- **1 个渠道**：apimaster（`base_url=https://apimaster.ai`，type=1）。
- **在售 77 个模型**，`GET /v1/models` 是唯一权威列表：
  | 计费方式 | 数量 | 例子 |
  |---|---|---|
  | 按 token（对话 / embedding / 音频转写） | 46 | `deepseek-flash`、`claude-sonnet-5`、`text-embedding-3-small`、`bge-m3`、`gpt-transcribe` |
  | 分段表达式（长上下文阶梯价） | 9 | `gpt-5.6-sol`、`gpt-6-*`、`MiniMax-M3` |
  | 按次（图片 / 视频） | 22 | `gpt-image-2-fd`（按规格）、`seedance-2.0`、`kling-v3-*`、`midjourney-*` |
- **上游有 79 个，我们去掉 2 个**（都不适合卖）：
  - `apimaster-freemodel`：上游 marketplace 里没有它的售价（只有官方 ratio=0），
    卖它会被本系统的兜底 ratio（37.5 → $75/M）定成天价。要卖的话得先给一个明确价（比如 0 = 免费）。
  - `jev-latest`：上游只支持 `typesafe` 端点（`POST /v1/systemone`），**不是 OpenAI 兼容**，
    走 `/v1/chat/completions` 会 400。
- 定价：`sync-upstream-pricing.py` 每日 04:00 按上游**最低价 ×1.25** 自动同步
  （图片模型另有按输出规格的价表 `ImageSizePrice`）。
- 控制台报价页与后台 admin 页是**同一份 SPA**，差别只在登录账号的角色。

> ⚠️ **已知缺口：视频模型的按次计费只能按最低档收。**
> 视频的价表档位是 `480P/720P/1080P`（图片是 1K/2K/4K），而计费时的
> `MatchImageSizeVariant` 是按"图片长边像素"归档的，对不上 → 一律落到该模型的最低价。
> 接视频正式售卖前要再改一轮（读视频请求里的分辨率/时长参数）。

### Cloudflare 账号（**别搞错**）

本项目所有 Cloudflare 资源（tokwork.com 的 zone、Pages 项目、R2 桶、Worker）
都在这个账号下：

```
Cloudflare Account ID: 28fa4860b37a3bb670d098eb493b781e
```

**陷阱**：CLI 的默认登录身份**不是**这个账号。
`wrangler` / `cf` 默认登录的是 `Info@yijianspace.com's Account`（`372be715…`），
拿它去部署会把资源建到错误的账号里 —— 表现为自定义域名绑不上、跨账号 CNAME 出怪问题
（2026-10-10 实际踩过：Pages 项目建到了 `372be715…`，导致 `admin.tokwork.com`
出现 308 死循环）。

**正确做法**：部署前显式指定账号与 token。

```bash
cd web
CLOUDFLARE_API_TOKEN=<token> \
CLOUDFLARE_ACCOUNT_ID=28fa4860b37a3bb670d098eb493b781e \
  wrangler pages deploy dist --project-name tokwork-panel
```

前端已放 `web/wrangler.jsonc` 声明项目名与产物目录（Pages 的配置文件**不支持**
`account_id` 字段，账号只能走环境变量）。

### 其他容易忘的

- 前端构建必须注入 `VITE_REACT_APP_SERVER_URL=https://api.tokwork.com`，
  否则 `web/src/helpers/api.js` 的 axios baseURL 会退化成相对路径。
- 后端**不在服务器上编译**：本地交叉编译 linux/amd64 单二进制再传上去。
- `main.go` 里有 `//go:embed web/dist`，**编后端之前必须先构建前端**。

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
