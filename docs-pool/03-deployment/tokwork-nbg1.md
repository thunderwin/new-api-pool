# tokwork 生产部署（Hetzner NBG1 + Cloudflare Pages）

> **本文描述的是 2026-10-09 真实上线的那一套**，不是 `deploy/` 下的通用蓝图。
> 两者关系：`deploy/` 是"可以怎么搭"的蓝图；本文是"实际搭成了什么样"的记录。
> 全部事实于 **2026-10-09** 在线上核实。

---

## 1. 为什么选 Hetzner 纽伦堡

上游供应商 `apimaster.ai` / `api.apimaster.ai` 的实测归属（2026-10-09）：

| 项目 | 值 |
|---|---|
| IP | `188.245.245.213` |
| 反向解析 | `static.213.245.245.188.clients.your-server.de` |
| whois netname | `CLOUD-NBG1` |
| 归属 | AS24940 Hetzner Online GmbH，德国 |
| 位置 | 纽伦堡（NBG1），巴伐利亚州 |

`CLOUD-NBG1` 是 Hetzner Cloud **纽伦堡机房**专用 netname。中转站放在同一个机房，
到上游的 RTT 从跨洲的 ~200ms 降到机房内网级别。

## 2. 服务器规格

> 已核实 2026-10-09（Hetzner 控制台）。

| 项目 | 值 |
|---|---|
| 厂商 / 机房 | Hetzner Cloud / Nuremberg `nbg1` |
| 机型 | `CPX12`（1 vCPU / 2 GB RAM / 40 GB NVMe） |
| 价格 | **$13.49 / 月**（不含税，20 TB 月流量） |
| IPv4 | `2.31.8.21` |
| IPv6 | `2a01:4f8:1c1f:87c2::/64` |
| 主机名 | `ubuntu-2gb-nbg1-1` |
| 系统 | Ubuntu 26.04.1 LTS (x86_64) |
| 登录方式 | SSH 密钥 `tokwork-deploy`（禁用密码） |

**没选更便宜的 CX23（2 vCPU/4 GB/$6.49）**：Cost-Optimized 系列在 NBG1 当时
显示 `Not available`（Hetzner 官网整条线也是售罄状态），控制台里那一行不可选。
2026-10-09 在 NBG1 能买到的最便宜的 2 核 4G 是 CPX22（$22.99）。

## 3. 架构：前端上 Cloudflare，后端留在服务器

```
                    浏览器
                       │
        ┌──────────────┴──────────────┐
        ▼                             ▼
  admin.tokwork.com              api.tokwork.com
  (Cloudflare Pages)             (A → 2.31.8.21, 灰云)
  静态 React SPA                   │
        │                          ▼
        │ 跨域调 /api/*      ┌──────────────┐
        └───────────────────►│    Caddy     │ 自动 HTTPS
                             └──────┬───────┘
                                    ▼
                             ┌──────────────┐
                             │   new-api    │ :3000
                             └──┬────────┬──┘
                                ▼        ▼
                          Postgres 16  Redis 7
```

**分域部署一共踩了三个坑**（2026-10-10 全部修复）：

1. **`/api/*` 完全没有 CORS**（v0.5.1）：**上游没给 `/api/*` 挂 CORS**——
   `SetRelayRouter` 里的 `router.Use(middleware.CORS())` 挂的是全局链，而 gin 的
   中间件链是注册时快照，`SetApiRouter` 排在它之前执行，所以 `/api/*` 拿不到。
2. **登录态是 session cookie，不是 token**（v0.5.3）：
   `controller/user.go` 的 `setupLogin` 只写 session，响应体里没有 token；
   `middleware/auth.go` 的 `authHelper` 优先读 `session.Get("username")`。
   跨域 XHR 默认**不存也不带 cookie**，所以前端 axios 必须开 `withCredentials: true`，
   否则表现是「能登录、一刷新就掉登录，`/api/user/self` 401」。
3. **`*` 与凭证互斥**（v0.5.3）：凭证模式要求 `Access-Control-Allow-Origin`
   是**具体 Origin**，不能是 `*`。原来 `AllowAllOrigins = true` + `AllowCredentials = true`
   是无效组合。

> ⚠️ 订正：本文早前版本写过「前端不用 cookie（纯 header token），跨域直接可用」，
> **那是错的**。实际是 session cookie 鉴权，上面三条缺一不可。

**踩坑记录**：只测 `OPTIONS` 预检会得到假阳性——预检不匹配路由、落到 `NoRoute`，
而 NoRoute 的链在 `Use()` 之后重建过，所以带 CORS 头；真实 GET 却不带。
**验收必须测带 `Origin` 的真实请求**：

```bash
# ✅ 正确验收方式（2026-10-10 修复后实测结果）
curl -s -D - -o /dev/null -H "Origin: https://admin.tokwork.com" \
  https://api.tokwork.com/api/status | grep -i access-control-allow-origin
# → access-control-allow-origin: https://admin.tokwork.com   （回显具体值，不是 *）
```

`middleware/cors.go` 现在的策略（v0.5.3 起）：

| Origin | `Access-Control-Allow-Origin` | `Allow-Credentials` | 谁在用 |
|---|---|---|---|
| 命中 `FRONTEND_BASE_URL` / `TRUSTED_ORIGINS` | 回显该 Origin | `true` | 本项目的面板（要带 cookie） |
| 其它 | `*` | 不带 | 第三方网页客户端调 `/v1/*`（自带 Key，不带 cookie） |

必须区分的原因：回显任意 Origin + 允许凭证 = 任意网站都能拿受害者的登录 Cookie
读接口响应（开个页面就能把 `/api/token/` 里的密钥读走）。而如果把所有 Origin 都收紧，
第三方网页客户端又调不了 `/v1/*`。

前端构建时必须注入 `VITE_REACT_APP_SERVER_URL=https://api.tokwork.com`，
否则 axios 的 baseURL 会退化成相对路径（见 `web/src/helpers/api.js`）。

**`api.tokwork.com` 只做 API，不带界面**（2026-10-10 变更）：

Go 二进制里 `//go:embed web/dist` 仍然嵌着整套前端，但后端设了
`FRONTEND_BASE_URL=https://admin.tokwork.com` 之后就不再兜底服务它了：

| 请求 | 行为 |
|---|---|
| `https://api.tokwork.com/` `/login` `/console` … | `301` → `https://admin.tokwork.com/<同路径>` |
| `https://api.tokwork.com/api/*`、`/v1/*` | 原样返回，**不跳转** |
| 未注册的 `/api/*`、`/v1/*` | JSON `404`（不会把 API 客户端引到 HTML 站点） |

> 已核实 2026-10-10 线上实测。改动见
> [`../07-reference/changelog.md`](../07-reference/changelog.md) v0.5.2。

**`api.tokwork.com` 必须是灰云（仅 DNS）**：橙云会被 Cloudflare 的 100 秒
HTTP 超时掐断 LLM 长流式响应（524）。

## 4. 域名与解析

> 已核实 2026-10-09。

| 域名 | 类型 | 目标 | 代理 | 用途 |
|---|---|---|---|---|
| `api.tokwork.com` | A | `2.31.8.21` | **仅 DNS（灰云）** | **只做 API**：客户调 `/v1/*`，面板跨域调 `/api/*`；网页路径 301 跳 admin |
| `admin.tokwork.com` | CNAME | `tokwork-admin.pages.dev` | **仅 DNS（灰云）** | 后台面板（Cloudflare Pages）——唯一的界面入口 |
| `tokwork.com` / `www` | — | 仍指向 `47.245.87.159` | — | 旧站，**本次未动** |

`tokwork.com` 这个 zone 在**另一个 Cloudflare 账号**里，与本项目使用的
`Info@yijianspace.com's Account`（Pages 项目所在账号）不同。

> ✅ **2026-10-10 已解决**：项目的 Cloudflare 资源统一在
> **`28fa4860b37a3bb670d098eb493b781e`** 这个账号
> （tokwork.com 的 zone、Pages 项目、`tokwork*` 系列 R2 桶、宣传站 Worker）。
>
> 迁移过程：新建 Pages 项目 `tokwork-panel`（不能用原名 `tokwork-admin`——
> `*.pages.dev` 子域全局唯一，被旧账号里那个项目占着），部署同一份 `dist`，
> 再把 `admin.tokwork.com` 的 CNAME 从 `tokwork-admin.pages.dev` 改到
> `tokwork-panel.pages.dev`。同账号后自定义域名一次激活（status `active`），
> 且带了 `zone_tag` —— 这正是之前跨账号时缺失的东西。
>
> **踩过的坑（记下来免得再犯）**：`wrangler` / `cf` 的默认 OAuth 身份是
> `Info@yijianspace.com`，它能访问的是 `372be715…`，**不是**本项目要用的账号。
> 用它部署会把资源建错账号。正确做法是显式传
> `CLOUDFLARE_ACCOUNT_ID=28fa4860…` + 一个该账号下的 API token
> （见仓库 README 的「部署信息」一节）。

> 已核实 2026-10-10：`admin` 这条是**灰云**（解析结果是 CNAME 链直接到
> `172.66.44.81 / 172.66.47.175`，不是 Cloudflare 代理 IP `104.21.x`）。
> 代价是用不了 Cloudflare Access（Access 需要橙云）。**待定**：要不要为 Access
> 把它改回橙云，需要先解决跨账号 Pages 自定义域名在橙云下的 308 死循环。

**橙云 CNAME 到跨账号 Pages 项目踩过的坑**（2026-10-09）：刚加完 10 几分钟内，
`https://admin.tokwork.com/` 返回 `308 → /` 自跳死循环。原因是 Pages 自定义域名
在边缘的注册还没传播完。等了几分钟后自行恢复，现为 200。

## 5. 后端部署物

服务器路径 `/opt/new-api-pool/`，由 `ops/` 下的脚本生成：

| 文件 | 来源 |
|---|---|
| `new-api` | 本地交叉编译的 linux/amd64 二进制（约 67 MB） |
| `Dockerfile` | `ops/deploy/Dockerfile` —— 只 `COPY` 二进制，服务器不做编译 |
| `docker-compose.yml` | `ops/deploy/docker-compose.yml` |
| `Caddyfile` | `ops/deploy/Caddyfile` |
| `gen-env.sh` | `ops/deploy/gen-env.sh` —— 在服务器本地生成 `.env`，密钥不出机器 |
| `backup.sh` | `ops/deploy/backup.sh` |
| `.env` | 由 `gen-env.sh` 生成，`chmod 600` |

### 构建链路

**服务器只有 1 vCPU，不做编译。** 流程：

```bash
# 1. 前端（本机）
cd web && bun install
VITE_REACT_APP_SERVER_URL="https://api.tokwork.com" DISABLE_ESLINT_PLUGIN='true' bun run build

# 2. 后端（本机交叉编译）
cd .. && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-s -w" -o new-api .

# 3. 传上去，服务器上 docker build 只做 COPY（秒级）
scp new-api root@2.31.8.21:/opt/new-api-pool/
ssh root@2.31.8.21 'cd /opt/new-api-pool && docker compose build && docker compose up -d'
```

> 注意 `main.go` 里有 `//go:embed web/dist`，所以**编译后端之前必须先构建前端**，
> 否则 `go build` 会失败。二进制里嵌的这套 SPA 仍然可用作兜底面板
> （`https://api.tokwork.com/` 返回 200）。

## 6. 容器与资源预算

> 已核实 2026-10-09（`docker compose ps` / `docker stats`）。

| 容器 | 镜像 | mem_limit | cpus | 实测占用 |
|---|---|---|---|---|
| `new-api-app` | `tokwork/new-api:local` | 576M | 0.9 | ~18 MB |
| `new-api-postgres` | `postgres:16-alpine` | 512M | 0.8 | ~34 MB |
| `new-api-redis` | `redis:7-alpine` | 160M | 0.3 | ~4 MB |
| `new-api-caddy` | `caddy:2-alpine` | 96M | 0.3 | ~12 MB |

合计上限约 1.31 GB，实测仅用 ~68 MB；宿主机 `free` 显示可用约 1.3 GB。
另配 **2 GB swap**（`vm.swappiness=10`）作为兜底。

Postgres 关键参数：`shared_buffers=128MB`、`max_connections=80`、`work_mem=4MB`。
`new-api` 设了 `GOMEMLIMIT=460MiB`，让 Go GC 在 cgroup 限制内更积极回收。

## 7. 备份

> 已核实 2026-10-10：三层全部到位，异地同步实测通过。

| 层 | 内容 | 状态 |
|---|---|---|
| `pg_dump` 每日全量 | `/opt/new-api-pool/backups/newapi-<时间戳>.sql.gz` | ✅ cron `30 3 * * *`，保留 14 天，落地即做 `gzip -t` 自检 |
| rclone → Cloudflare R2 | 桶 `tokwork-backup`，`RCLONE_REMOTE=r2` / `RCLONE_PATH=tokwork-backup` | ✅ 已配，随每日 cron 自动上传 |
| Hetzner 付费磁盘备份（+20%，约 +$2.70/月） | 整机快照，7 槽轮转 | ✅ 已开（2026-10-10） |

**三层各防什么**：Hetzner 快照防整机故障；`pg_dump` 防数据逻辑损坏（误删 / 应用 bug 写坏）；
R2 异地防"Hetzner 账号级事故"。缺任何一层都留有整类盲区。

### 7.1 R2 配置要点（踩过的坑）

R2 的 bucket 在 **`28fa4860b37a3bb670d098eb493b781e`** 这个 Cloudflare 账号里
（不是 CLI 认证的 `372be715…` 那个）。

token 类型：**Account API Token**，权限 **对象读和写**，**仅作用于 `tokwork-backup`**。

rclone 配置必须加两处，否则一定失败：

```ini
[r2]
type = s3
provider = Cloudflare
access_key_id = ...
secret_access_key = ...
endpoint = https://28fa4860b37a3bb670d098eb493b781e.r2.cloudflarestorage.com
acl = private
no_check_bucket = true   # ⚠️ 必需，见下
```

1. **`no_check_bucket = true` 是必需的**。桶级 token 没有 `CreateBucket` 权限，
   而 rclone 默认会"检查目标桶是否存在"，HEAD 失败后去建桶 → 403。
   报错形式极具误导性：
   `failed to prepare upload: operation error S3: CreateBucket ... AccessDenied`
   ——看起来像"没写权限"，其实是"没建桶权限"。
2. **自检不能写 `rclone lsd r2:`**（列所有桶要账号级权限，桶级 token 必然 403）。
   要按具体桶测：`rclone lsd r2:tokwork-backup`。

另外 Ubuntu 26.04 自带的 `rclone` 是 **1.60.1-DEV**（太老，对 R2 支持有问题，报错也不清晰）。
换成官方最新版后错误信息立刻变得可诊断：

```bash
curl -fsSL https://downloads.rclone.org/rclone-current-linux-amd64.zip -o /tmp/rclone.zip
cd /tmp && mkdir -p x && unzip -q -o rclone.zip -d x
install -m 0755 "$(find x -name rclone -type f | head -1)" /usr/local/bin/rclone
```

## 8. 运维脚本（`ops/`）

| 脚本 | 作用 |
|---|---|
| `ops/hetzner-prep.sh` | 服务器初始化：2G swap、基础包、Docker、ufw 只放 22/80/443、建目录 |
| `ops/deploy/Dockerfile` | 极简运行时镜像（debian:bookworm-slim + 二进制） |
| `ops/deploy/docker-compose.yml` | 四容器编排 + 健康检查 + 资源限制 |
| `ops/deploy/Caddyfile` | 只服务 API 域名，SSE 不缓冲，超时 600s |
| `ops/deploy/gen-env.sh` | 在服务器本地生成 `.env`（PG/Redis 密码、SESSION_SECRET、CRYPTO_SECRET） |
| `ops/deploy/backup.sh` | 每日 pg_dump + 自检 + 清理 + 可选 rclone |
| `ops/deploy/setup-rclone.sh` | 交互式配置 rclone → R2（`read -s` 读密钥，不落 shell history），写配置并做连通自检 |
| `ops/tokwork-bootstrap-test.yaml` | 早期 DigitalOcean 试机的 cloud-init，**已不用**（DO 那台已删） |

## 8.5 上游渠道：apimaster 文生图（2026-10-10 接入并实测）

> 已核实 2026-10-10：直连上游与经本系统中转**均返回 200**。

### 上游

| 项 | 值 |
|---|---|
| Base URL | `https://apimaster.ai` |
| 文生图接口 | `POST /v1/images/generations` |
| 可用 image 模型（10 个） | `gpt-image-2` `gpt-image-2-fd` `gpt-image-2.5-sunburst` `gpt-image-2.5-sunburst-fd` `gpt-image-2.5-flare` `gpt-image-2.5-flare-fd` `gemini-2.5-flash-image` `gemini-3-pro-image` `gemini-3.1-flash-image` `grok-imagine-image-2.0` |
| 上游 key | 存服务器 `/root/upstream-apimaster.key`（600） |

直连实测：`/v1/models` 200（78 个模型）；`gpt-image-2` 生成一张 1024×1024 约 38s，返回 `b64_json` + `url`。

### 本系统里的渠道

| 项 | 值 |
|---|---|
| 渠道 id | 1 |
| 名称 | `apimaster 文生图` |
| 类型 | OpenAI（`ChannelTypeOpenAI = 1`） |
| Base URL | `https://apimaster.ai` |
| 分组 | `default` |

**中继实测**：用系统令牌调 `https://api.tokwork.com/v1/images/generations`（model `gpt-image-2`）→
`200`，27s，3.89MB，`logs` 表里记了 `quota=79463`。

### 两个必踩的坑（都已解决，照做即可）

**1. 渠道的 Base URL 不要带 `/v1`。**
`relay/common/relay_utils.go` 的 `GetFullRequestURL(baseURL, requestURL, ...)` 是
`baseURL + requestURL` 直接拼接，而 requestURL 已经带 `/v1` 前缀。
写 `https://apimaster.ai/v1` 会拼成 `/v1/v1/images/generations`。

**2. 用户设置里的 `accept_unset_model_ratio_model` 必须为 `true`。**
否则调一个没配价格的模型会直接被拒：

```
模型 gpt-image-2 的价格未配置。请前往「系统设置 → 运营设置」开启自用模式，
或在「系统设置 → 分组与模型定价设置」中为该模型配置价格
```

检查的是 `relay/helper/price.go` 里的 `info.UserSetting.AcceptUnsetRatioModel`（**用户级**，
不是全局自用模式）。存 `users.setting` 这个 JSON 列：

```sql
UPDATE users SET setting='{"accept_unset_model_ratio_model": true}' WHERE role=100;
```

> ⚠️ 这只解决了"能不能调通"。**未配价格 = 计费按兜底 ratio 37.5 估算，对图像模型没有意义**。
> 要正式对外收费，得去「分组与模型定价设置」给这些 image 模型配真实价格
> （按次计费的图像模型走 `GetModelPrice`，见 `relay/helper/price.go` 的 `ModelPriceHelperPerCall`）。

### 运维用的管理员 API 令牌

管理员账号（`woothunder`）原本没有访问令牌，脚本调管理接口会 `Unauthorized`。
已生成一个 32 位令牌存 `/root/newapi-admin-token.txt`（600），等价于面板「个人设置 → 生成访问令牌」。

调管理接口必须同时带两个头，缺 `New-API-User` 会报 `Unauthorized, New-API-User header not provided`：

```bash
TOKEN=$(cat /root/newapi-admin-token.txt)
curl -X POST http://127.0.0.1:3000/api/channel/ \
  -H "Authorization: Bearer $TOKEN" -H "New-API-User: 1" \
  -H "Content-Type: application/json" -d @payload.json
```

**要吊销**：把 `users.access_token` 置空（`UPDATE users SET access_token=NULL WHERE role=100;`），
或在面板里重新生成一次覆盖。

---

## 9. 还没做（计划，不是现状）

- [ ] 确认管理员密码足够强（初始化时自设，系统无默认账号）；删除默认令牌
- [ ] **把 Pages 项目从 `372be715…` 迁到 `28fa4860…`**（同账号后 Access 才配得动）
- [ ] 配 Cloudflare Access 保护 `admin.tokwork.com`
- [ ] 决定是否把 `/api/*` 从 `api.tokwork.com` 上摘掉（Caddy 加路径规则）
- [ ] 宣传站（`tokwork.com` / `www`）—— 目前 apex 仍是旧站
- [ ] `CRYPTO_SECRET` 另存到密码管理器（**丢了所有上游 Key 解码失败**）

> 已完成（2026-10-10）：Hetzner 付费磁盘备份已开；rclone → R2 异地备份已配并实测通过。

## 10. 排错速查

| 现象 | 原因 / 处理 |
|---|---|
| `https://admin.tokwork.com/` 308 死循环 | Pages 自定义域名边缘注册未传播完，等 10 分钟；仍不行则把该记录改灰云 |
| 证书签发失败，报 `216.198.79.1` | 该域名有旧的 A 记录残留，等 DNS 缓存过期后 `docker compose restart caddy` |
| Caddy 报 `challenge failed` | 80 端口不通，或 DNS 还没指向本机 |
| 面板能打开但接口全 401 | 正常——需要先登录；`/v1/*` 要带 `Authorization: Bearer sk-...` |
| 面板打开但控制台报 `No 'Access-Control-Allow-Origin' header` | `/api/*` 缺 CORS。见 §3，确认 `SetApiRouter` 里有 `apiRouter.Use(middleware.CORS())`，改完必须重新编译后端二进制并重新部署 |
| 改动生效了但浏览器仍旧报错 | 先硬刷 `Cmd+Shift+R`；CORS 失败会被浏览器短暂记忆，无痕窗口可快速判定 |
| 号池总览 500 / panic `pool.go:70` | 该模块残留 SQLite 写法。见 changelog v0.5.5：`top_ups` 的列名是 `create_time`（不是 `created_time`），`group`/`key` 必须按方言引用（PG 双引号） |
| 号池配方列表重复（19 条变 57 条） | 同一根因的连带伤：查询报错被当成"不存在"→ 反复 Insert。v0.5.5 起启动会自愈去重 |
