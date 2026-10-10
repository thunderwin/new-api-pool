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
| `tokwork.com` + `www.tokwork.com` | Worker 自定义域 | Worker `tokwork-homepage` | 橙云 | 宣传站（两条都是自定义域，同源同内容） |

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

> `ops/` 在**仓库里**（`<REPO>/ops/`）。早前它在仓库外层，
> 导致别人克隆公开仓库后照着本文找不到脚本 —— 2026-10-10 已移入仓库，
> 并加了 [`ops/README.md`](../../ops/README.md) 说明它与 `deploy/` 蓝图的分工。
> 早期 DigitalOcean 试机的 cloud-init 已删除（那台机器早已销毁，留着只会误导）。

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

### 上游计费：能拿到什么、拿不到什么（2026-10-10 实测）

apimaster 本身也是 new-api/one-api 类网关，所以带**标准计费接口**。

**单次请求：只有 token，没有金额。** 文生图响应里带一个 `usage` 对象：

```json
{"input_tokens": 3, "output_tokens": 1056,
 "output_tokens_details": {"image_tokens": 1056, "reasoning_tokens": 0, "text_tokens": 0},
 "total_tokens": 1059}
```

（`gpt-image-2` 出 1024×1024 一张 = 1056 个 image token。）

**金额：只能查累计，没有逐笔明细。**

| 接口 | 返回 |
|---|---|
| `GET /v1/dashboard/billing/subscription` | `soft_limit_usd` / `hard_limit_usd` 均为 `100000000`（**占位值，不是真实余额**） |
| `GET /v1/dashboard/billing/usage?start_date=YYYY-MM-DD&end_date=YYYY-MM-DD` | `{"object":"list","total_usage": ...}` |

实测：生成一张 1024×1024 `gpt-image-2`，`total_usage` 增量 = **0.7876**。

> ⚠️ **单位待核实**：one-api 系该接口的 `total_usage` 惯例是**美分**
> （即 ≈ $0.0079/张），但**无法从 API 本身确认**，要以 apimaster 后台账单页为准。

**对利润核算的影响**：上游只给「token 数」+「按日累计金额」，
**拿不到逐笔成本**。逐笔要算只能调前调后各查一次取差值。
而本系统自己的 `logs` 表是**逐笔**记录用户侧 `quota` 的 ——
两边粒度不对等，所以「号池管理 → 利润」页目前用的是估算系数
`PoolUpstreamCostRatio`（默认 0.6），**那是拍脑袋的值，不是真实上游账单**。
要真实毛利，得改成按上面的接口拉日累计再对账。

#### 补充（同日实测）：逐笔成本其实拿得到 —— 但要用账号 access token

apimaster **本身也是 new-api**（`GET /api/status` 返回的结构与本项目一致）。
它有一整套管理接口，但用的是**账号 access token**，不是 `sk-` 中转 key：

| 接口 | 用 `sk-` key | 说明 |
|---|---|---|
| `/v1/dashboard/billing/usage?start_date=&end_date=` | ✅ 200 | 按日累计 |
| `/api/log/?p=0&page_size=N` | ❌ `invalid access token` | **逐笔日志**（含每笔 quota） |
| `/api/user/self` | ❌ 同上 | 账户已用额度 |

**做法**：去 apimaster 后台「个人设置 → 生成访问令牌」拿一个 access token，
之后 `/api/log/` 就能**逐笔**拉上游消耗，和本项目 `logs` 表的用户侧 `quota`
按时间+模型配对，即可算出**真实毛利**（两边没有共同的 request id，只能按时间窗配对，有噪声）。

> **更正**：`/api/log/` 是**管理员**接口，普通用户账号会返回
> `Unauthorized, insufficient privileges`。用户级的是这两个（从面板的网络请求里抓到）：
>
> | 接口 | 用途 |
> |---|---|
> | `GET /api/log/self?p=1&page_size=100&start_timestamp=&end_timestamp=` | **逐笔明细** |
> | `GET /api/log/self/stat?...` | 汇总（`{quota, rpm, tpm}`） |
>
> 都需要 `Authorization: Bearer <账号 access token>`。
>
> 逐笔记录里最有用的字段：`model_name` / `prompt_tokens` / `completion_tokens` /
> `quota` / `created_at` / `request_id` / `content`，以及 `other`（JSON）里的
> 计费明细 —— `ch_input_price`、`model_price`、`group_ratio`、
> **`fallback_triggered`（是否降级到更贵的上游，正是"价格浮动"的开关）**。

### 上游成本 ↔ 下游收入 的配对（`ops/reconcile-upstream.py`）

**两边没有共同的 request id**：各跳各生成。实测同一笔调用：

```
本系统：202610100341176 369241458268d9d6uUejzAJq    tokens 3/1650
上游：  202610101176... 516005558268d9d6ejqe9RrL    tokens 3/1650
        └─ 时间戳前缀相同（同一秒）        └─ token 数完全一致
```

**配对键 = (模型, prompt_tokens, completion_tokens, 秒级时间戳 ±5s)**。
token 数两边是同一个值（都是上游返回的 usage），同模型下几乎唯一，叠加时间窗后实践可 1:1。

`ops/reconcile-upstream.py` 实现了这个配对并输出真实毛利：

```bash
python3 /opt/new-api-pool/reconcile-upstream.py --hours 24
python3 /opt/new-api-pool/reconcile-upstream.py --since 2026-10-10 --until 2026-10-11
```

首次实测（2026-10-10）：下游 2 笔全部配对成功、0 笔未配对；上游 5 笔里有 3 笔未被认领
（那 3 笔是直接打上游、没走中转的测试调用 —— 判断正确）。

**两边 `quota_per_unit` 都是 500000**，所以 USD = `quota / 500000`，可直接比。
实测某一笔：上游成本 `3938/500000 = $0.007876`，下游收入 `123863/500000 = $0.247726`
→ 毛利 96.8%、加价 31.5x。

> ⚠️ 加价 31.5x 是因为**图片模型还没配价格**，系统走了兜底 `model_ratio = 37.5`。
> 也就是说当前不是"赚太少"，而是**定价严重偏高**。要对外卖必须先按
> `reconcile-upstream.py` 跑出来的真实成本定价。

### 下游客户能看到什么成本信息（2026-10-10 实测）

**API 响应里没有金额**——只有 `usage`（token 数），响应头也没有任何计费字段：

```
返回体顶层: ['created', 'data', 'usage']
响应头    : 无 quota / cost / billing
（但本系统自己的 logs 表逐笔记了 quota，例：79313 = $0.158626）
```

**但客户有 3 条路能查到自己的花费，都用同一把 `sk-` key：**

| 方式 | 命令 | 返回 |
|---|---|---|
| 逐笔明细 | `GET /api/log/token?p=0&page_size=N` + `Authorization: Bearer sk-xxx` | `[{model_name, quota, prompt_tokens, completion_tokens, ...}]`，`quota/500000` = USD |
| 累计花费 | `GET /dashboard/billing/usage`（或 `/v1/...`） | `{"object":"list","total_usage":56.5278}`（**美分**） |
| 额度上限 | `GET /dashboard/billing/subscription` | `{"soft_limit_usd":...,"hard_limit_usd":...}` |

外加面板 `/console/log` 页有「花费」列（登录可见）。

对应路由（`router/api-router.go` / `router/dashboard.go`）：

```go
logRoute.GET("/token", middleware.TokenAuthReadOnly(), controller.GetLogByKey)   // 用 sk- key
apiRouter.GET("/dashboard/billing/subscription", middleware.TokenAuth(), ...)
```

> 结论：**"响应里看不到成本"成立，但"客户查不到成本"不成立** —— 事后可查，只是不在响应那一刻。
> 若要让响应直接带金额，需要改 fork 代码（流式 SSE 里夹带金额尤其麻烦），
> 而业界惯例是：响应只给 token，成本走面板 + billing 接口。

### 上游定价同步（`ops/sync-upstream-pricing.py`，2026-10-10）

**上游定价是公开可读的**，但有两个接口，作用完全不同：

| 接口 | 内容 | 是否可用作定价基准 |
|---|---|---|
| `GET /api/pricing` | 79 个模型的**官方标价**（`model_ratio` / `model_price` / 相对倍率） | ❌ **会定高几十倍** |
| `GET /api/public/marketplace?model=X` | **每个渠道的真实售价**（`user_price`、`media_pricing.billing_prices` 按规格） | ✅ 真实成本 |

差别有多大：`gpt-image-2` 在 `/api/pricing` 里标 `model_price = $0.25`，
实际每次只扣 **$0.0075**（33 倍差）。因为 APIMaster 有一层**折扣**，
折扣率逐模型不同（`gpt-5.6-sol` 官方输入价 $5/M → 实付 $0.223268/M，约 95.5% off）。

> ⚠️ 本 fork 自带的「上游定价同步」（`/api/ratio_sync/fetch`，见 `controller/ratio_sync.go`）
> 读的正是 `/api/pricing`。**对 APIMaster 这个上游直接用会把报价定高 33 倍**，
> 所以另写了 `ops/sync-upstream-pricing.py`，改从 marketplace 取真实售价。

**多渠道价差**：79 个模型里 **57 个**存在多渠道价差
（`gpt-image-2`：$0.0075 ↔ $0.325，4~5 条渠道，**43 倍**）。
APIMaster 便宜优先，所以**常态成本 = 最低价**。

**单位换算**（两边 `QuotaPerUnit` 都是 500000）：

- marketplace 的 `user_price` **单位随模型类型变化**，必须区分：
  - 带 `media_pricing` → **按张**（`unit: "image"`）
  - 不带 → **$/百万输入 token**（核验：`gpt-5.6-sol` 官方输入价 $5/M ↔ `model_ratio` 2.5）
- 本系统 `$/M = 2 × model_ratio`，所以 `our_model_ratio = upstream_$/M / 2`
- 相对倍率（`completion_ratio` / `cache_ratio` / `image_ratio`）**不加价**，原样照搬

**已知的两处取舍**（脚本已标注）：

1. **16 个图片模型有 1K/2K/4K 多档价** —— 已通过新增 `ImageSizePrice` 解决（见下）
2. **9 个分段/表达式计费模型**（`billing_mode = tiered_expr`，如 `gpt-5.6-sol`）
   无法用单一 ratio/price 表达 —— 已改为同步表达式本体（见下）

用法：

```bash
python3 ops/sync-upstream-pricing.py                    # dry-run，只打印
python3 ops/sync-upstream-pricing.py --markup 1.25      # 加价 25%
python3 ops/sync-upstream-pricing.py --markup 1.25 --apply
```

### 新增：`ImageSizePrice` —— 图片按输出规格定价（2026-10-10 本 fork 新增）

**问题**：本系统一个模型只有一个固定价，但上游对同一模型的不同尺寸收不同的钱
（apimaster 的 `media_pricing.billing_prices` 给了 0.5K/1K/2K/4K 四档）。
只按一个价收，客户传大尺寸就亏、传小尺寸就贵。把尺寸拆成不同模型名（用 model_mapping）
**防不住**——客户调 `-1k` 却传 `size: 2048x2048` 时，照样按 1K 收。

**做法**（改动很小，集中在 4 个文件）：

| 文件 | 改动 |
|---|---|
| `setting/ratio_setting/image_size_price.go` | **新增**。`{模型: {规格: 单价USD}}` 的存储 + `MatchImageSizeVariant(size, prices)` 按长边归类 |
| `model/option.go` | 注册 option `ImageSizePrice`（读 + 写两处） |
| `controller/option.go` | 把 `ImageSizePrice` 加进可写键列表 |
| `relay/helper/price.go` | `ModelPriceHelper` 里：若模型配了尺寸价，就用请求体的 `size` 覆盖固定价 |

规格按**长边像素**归类：`<=640 → 0.5K`、`<=1280 → 1K`、`<=2560 → 2K`、`更大 → 4K`；
该档没配价就向下找最接近的可用档；`size` 为空/`auto`/解析不出来，则用价表最低档。

**实测**（`gpt-image-2-fd`，同一模型两种尺寸）：

```
size=1024x1024  →  quota 11719 = $0.023438   （1K 价 ×1.25）
size=4096x4096  →  quota 28125 = $0.056250   （4K 价 ×1.25）
```

> ⚠️ 上游的「规格名」不全是尺寸：`midjourney-v8.2` 是 `fast/relax/turbo`、
> `doubao-seedream-5-0-pro` 是 `Image Output / High-Resolution Image Output`。
> 这些匹配不上尺寸档位，会兜底到价表最低档 —— 对 MJ 这类有自己的
> relay 路径的模型影响有限，但**大尺寸输出仍有薄利风险**，必要时改回单一固定价。

### 分段表达式同步（原为跳过，现已支持）

本 fork 有同款表达式引擎（`pkg/billingexpr`，变量 `p` / `c` / `len` / `cr` / `cc` / `cc1h`），
与 apimaster 的表达式语法一致，所以直接照搬 `billing_mode` + `billing_expr`，
并**只把价格系数放大、阈值不动**（正则只匹配 `变量 * 数字`，不会误伤 `len <= 272000`）：

```
原: len <= 272000 ? tier("standard", p * 2   + c * 12  + cr * 0.2  + cc * 2.5  ) : ...
新: len <= 272000 ? tier("standard", p * 2.5 + c * 15  + cr * 0.25 + cc * 3.125) : ...
```

9 个分段模型全部同步（写入 `billing_setting.billing_mode` / `billing_setting.billing_expr`）。

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

## 8.6 客户门户（P0，2026-10-10 上线）

> ⚠️ **本节记录的是 P0 那一版（把 new-api 自带 SPA 当客户门户）。它已经被
> 「自研门户」取代** —— 2026-10-10 晚些时候产品改了方向：客户看到的一切都自己写，
> 就在营销站那套 Next.js 应用里。**现状请看本节末尾的「§8.6.9 自研门户（最终形态）」**，
> 前面这些保留下来是因为它们解释了当时的取舍，而且 P0 做的两件事（价格页按规格显示、
> 客户域名）仍然是现状的一部分。

### 结论先行：**不需要新起项目，也不需要改营销站的 admin**

查证后发现三件事，直接推翻了「另起一个静态门户」和「改造营销站 admin」两条路：

1. **客户本来就看不到复杂后台**。new-api 按角色切侧边栏
   （`controller/user.go` 的 `calculateUserPermissions` / `generateDefaultSidebarConfig`）：
   普通用户 `admin: false`，**完全看不到渠道 / 号池 / 系统设置 / 用户管理**。
2. **「模型 + 价格」页是现成的**：`web/src/components/table/model-pricing/`，
   挂在顶栏（`HeaderNavModules.pricing`），而且 **`requireAuth: false` 默认免登录可见**。
3. **侧边栏是纯配置**（`SidebarModulesAdmin` / `HeaderNavModules` 两个 option），
   改它不用动代码。

营销站的 `/admin` 是 **Payload CMS 的内容管理后台**（Pages/Posts/Media/ContactRequests
的 CRUD），不是门户；把它开门给客户等于暴露内容表，做不出「只显示我的 Key 和价格」。

### P0 实际做的四件事

| # | 内容 | 类型 |
|---|---|---|
| 1 | `/api/pricing` 暴露 `image_size_price`（`model/pricing.go` 的 `Pricing` 结构 + `updatePricing`） | 代码 |
| 2 | 价格页显示**起价 + 按规格明细**（`web/src/helpers/utils.jsx` 的 `getModelPriceItems`） | 代码 |
| 3 | 侧边栏瘦身：关掉客户用不到的 `console.midjourney`（其余保留） | 配置 |
| 4 | 客户域名 `console.tokwork.com` → 同一个 Pages 项目 `tokwork-panel` | 基建 |

**为什么要做 1 和 2**：上游对同一模型的不同尺寸收费不同（`gpt-image-2-fd`
1K $0.0234 / 4K $0.0563，差 2.4 倍）。价格页只显示兜底固定价的话，客户按 1K 下单、
传 4K 被多扣，**必然产生争议**。修完实测：

```
gpt-image-2-fd   起价 $0.023 / 次
                 按规格  1K $0.023 · 2K $0.028 · 4K $0.056
gpt-image-2      模型价格 $0.009 / 次        ← 单档模型不变
```

### 客户域名的两个必备配置

1. **DNS**：`console` CNAME → `tokwork-panel.pages.dev`，**仅 DNS（灰云）**
   —— 和 `admin` 一致。Pages 自定义域在**同账号**下才能激活（这次带了 `zone_tag`）。
2. **CORS 白名单**：`console.tokwork.com` 必须进后端的可信 Origin，否则客户
   **登录成功但刷新就掉线**（跨域 XHR 不带 cookie）。

```bash
# .env
TRUSTED_ORIGINS=https://console.tokwork.com,https://www.tokwork.com,https://tokwork.com
```

营销站那两个域名是 2026-10-10 因为「营销站登录框」加进来的（见上面的 SSO 一节）——
登录请求是从 `www.tokwork.com` 发起的，白名单里没有它，预检就过不去。

> ⚠️ **踩过的坑**：光把 `TRUSTED_ORIGINS` 写进 `.env` **不生效**——
> `ops/deploy/docker-compose.yml` 的 `environment:` 必须显式透传它，否则变量进不了容器。
> 症状极具迷惑性：`/api/*` 预检仍返回 200（拿到的是 `*`），但浏览器因为凭证模式
> 不接受 `*` 而静默失败。已在 compose 里补上并加了注释。

实测的 Origin 行为（2026-10-10，加完营销站域名后复测）：

| Origin | Allow-Origin | Allow-Credentials |
|---|---|---|
| `https://admin.tokwork.com` | 回显该 Origin | `true` |
| `https://console.tokwork.com` | 回显该 Origin | `true` |
| `https://www.tokwork.com` | 回显该 Origin | `true` |
| `https://tokwork.com` | 回显该 Origin | `true` |
| `https://evil.example.com` | `*` | 不带 |

### 两个门户的分工

| 域名 | 谁用 | 说明 |
|---|---|---|
| `console.tokwork.com` | **客户** | 模型价格页（免登录）+ 令牌/日志/充值/个人设置 |
| `admin.tokwork.com` | **你自己** | 同一个 Pages 项目的同一个 SPA，但用 admin 账号登录，能看到渠道/号池 |

> 两个域名指向**同一个 Pages 项目**，界面是同一份代码 —— 差别只在登录的账号角色。
> 所以别指望靠域名隔离权限，真正的隔离是 new-api 的角色控制。

### P1：控制台对齐 Kumo 设计系统（2026-10-10 完成）

营销站（www）用的是 Cloudflare 的 **Kumo**；控制台（console/admin）是 Semi UI。
两边要像同一个产品，但**没有重写组件**，走的是「把 Semi 的 CSS 变量映射到 Kumo 的语义值」。

改了哪些文件：

| 文件 | 改了什么 |
|---|---|
| `web/src/index.css` | 新增 `设计令牌：对齐 Cloudflare Kumo` 一节：品牌蓝调色板（`--semi-blue-0..9`，亮/暗两套）、圆角刻度（xs 2 / sm 4 / md 6 / lg 8）、`--semi-color-border` 8%→10%、字体换系统栈；另加 `.tk-card` / `.tk-tile` / `.tk-card--selected` 三个表面类 |
| `web/src/index.css` | 第 986 行那段硬编码 `border-radius: 10px !important` → `var(--semi-border-radius-medium)`（6px） |
| `web/src/components/table/model-pricing/…` | 模型广场的卡片/图标块/骨架屏/详情头：`rounded-2xl`(16px)+`shadow-md` → `tk-card`/`tk-tile`（1px 描边 + 8px 圆角，Kumo 不用投影分层次） |
| `…/layout/header/PricingVendorIntro(.Skeleton).jsx` | 供应商介绍条从「蓝色渐变封面图 + 白字」改成中性表面（**`public/cover-4.webp` 不再被引用**） |
| `web/src/components/auth/LoginForm.jsx`、`RegisterForm.jsx` | 登录/注册卡 `!rounded-2xl` → `tk-card`；按钮 `!rounded-full` → `!rounded-lg` |

**没做的（以及为什么）**：

- **没有改中性色底**。Semi 暗色 `--semi-color-bg-0` 是 `rgb(22,22,26)`，Kumo 的暗色画布接近纯黑
  （`oklch(10%)`）。整体换底色会牵动 1000+ 行既有样式，收益只是"更像 Cloudflare"，风险不对称，**刻意保留**。
- **没有换成 Kumo 的 React 组件**。控制台是 Semi 生态（Form/Table/Modal 深度绑定），换组件库等于重写。

### P2：文档入口（2026-10-10 完成）

客户在控制台点顶栏「文档」，落到**我们自己的文档**：

```
general_setting.docs_link = https://www.tokwork.com/docs
```

落库方式（管理员令牌，`/api/option/` 的通用写接口）：

```bash
TOKEN=$(cat /root/newapi-admin-token.txt)
curl -X PUT http://127.0.0.1:3000/api/option/ \
  -H "Authorization: Bearer $TOKEN" -H "New-API-User: 1" \
  -H 'Content-Type: application/json' \
  -d '{"key":"general_setting.docs_link","value":"https://www.tokwork.com/docs"}'
# 核实：curl -s https://api.tokwork.com/api/status | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["docs_link"])'
```

> 原来的默认值是上游的 `https://docs.newapi.pro` —— 那个站讲的是 new-api 本身，
> 不是我们的服务条款、模型和价格，对客户没有用。

### 客户从营销站登录进控制台（SSO 交接，2026-10-10 完成）

> ⚠️ **已被自研门户取代** —— `?sso_user=` 那套 URL 传参在门户搬到营销站之后就删掉了
> （见 §8.6.9）。下面保留的是当时的实现与踩坑记录，其中"三件事"（CORS 白名单、
> CSP、身份交接）里的前两条**现在依然成立**。

**用户的诉求**：营销站上有个登录框，登完直接进控制台，不要再登一次。

实现的链路（每一环都实测过）：

```
www.tokwork.com/login
  → 浏览器直接 POST https://api.tokwork.com/api/user/login （credentials:'include'）
     后端下发 session cookie，host-only 落在 api.tokwork.com
     ✅ 跨域但不跨站（同为 tokwork.com），SameSite=Strict 不会拦
  → 跳 https://console.tokwork.com/console?sso_user=<id>
  → 控制台 index.html 的引导脚本在**任何 module 之前**把 {id} 写进 localStorage，
     然后用 history.replaceState 抹掉地址栏参数
  → 控制台按正常流程请求 /api/user/self，直接就是登录态
```

**为什么必须带 `sso_user`**：控制台把用户 id 存在自己域下的 localStorage，
每个请求都带 `New-API-User` 头，后端要求它和 session 里的用户一致
（`middleware/auth.go` 的 `authHelper`，缺头/不匹配一律 401）。
营销站跨域写不了 console 域的 localStorage，所以只能用 URL 传一次；同时
`getUserIdFromLocalStorage()` 是在 axios 实例**创建时**求值的，写晚了就是
`New-API-User: -1` → 401 →「登录成功、跳过去又被踢回登录页」。所以写在 `index.html` 里。

**要同时满足的三件事**（少一件都会失败，且症状都很像）：

1. 后端 CORS 白名单要有营销站的两个域名（见下面 §"客户域名的两个必备配置"）；
2. 营销站的 CSP `connect-src` 要放行 `https://api.tokwork.com`
   （漏了会报 `Refused to connect`，页面上只显示"连不上网关"）；
3. 控制台 `index.html` 的引导脚本要在 bundle 之前跑。

### 一条只有跨域才会遇到的坑：尾斜杠 301 会丢 CORS 头

gin 对没注册的路径做 301 去斜杠重定向，而**重定向发生在中间件链之前**，
响应里没有 `Access-Control-Allow-Origin`。同源部署感觉不到，跨域部署则一律失败。

实测过（2026-10-10）三条路径会 301：

```
/api/data/self/   → 客户「数据看板」
/api/log/self/    → 客户「使用日志」
/api/mj/self/     → midjourney 日志（本部署已关掉该模块）
```

**处置**：前端改成不带尾斜杠（`useDashboardData.js`、`useUsageLogsData.jsx`、
`useMjLogsData.js`），后端同时注册带尾斜杠的**别名**兜住还没刷新到新包的浏览器
（`router/api-router.go`，三处，带注释）。

> 顺带修掉同一个症状的白屏根因：`loadQuotaData()` 原来只有 `finally` 没有 `catch`，
> 请求失败就是 unhandled rejection，看板直接白屏。现在失败返回空数组，页面显示「无数据」。

**验收**：

```bash
for p in "/api/data/self/" "/api/log/self/" "/api/mj/self/"; do
  printf "%-20s " "$p"
  curl -s -o /dev/null -w "%{http_code}\n" -H "Origin: https://console.tokwork.com" \
    "https://api.tokwork.com$p"
done
# 必须都是 401（走到了鉴权中间件），不能是 301
```

### 8.6.9 自研门户（最终形态，2026-10-10）

**产品决定**：客户看到的一切都由自己写，**不要 new-api 自带的那套界面**。
理由很直接 —— 客户只需要三件事（生成自己的 Key、看调用历史、看模型价格），
而这套 SPA 是给管理员用的重型后台：中文界面、Semi UI 的另一套设计语言、
还有一堆客户用不到的入口。既然都要调 API，不如按我们自己的设计系统写四页。

#### 现在的分工

| 域名 / 路径 | 是什么 | 谁用 |
|---|---|---|
| `www.tokwork.com/console` | **客户门户**：概览（余额/今日用量/Key 数） | 客户 |
| `www.tokwork.com/console/keys` | API Key 列表 / 新建 / 停用 / 删除 / 按需显示完整 key | 客户 |
| `www.tokwork.com/console/logs` | 调用历史（时间/模型/token/花费/耗时/request id + 筛选分页） | 客户 |
| `www.tokwork.com/console/account` | 账号信息 + 改密码 | 客户 |
| `www.tokwork.com/login · /register · /reset · /user/reset` | 登录 / 注册 / 找回密码 | 客户 |
| `console.tokwork.com/**` | **301 到 `www.tokwork.com/console/**`**（旧地址兜底） | — |
| `admin.tokwork.com` | 原来的 new-api SPA（Pages 项目 `tokwork-panel`），**只给自己用** | 老板 / 运维 |

门户和营销站是**同一套 Next.js 应用**（route group `(portal)`，自己的 root layout），
所以：一个 origin、一套 cookie、一份 CSP、一套设计令牌。代码在
`homepage/src/app/(portal)/console/**`，样式 `homepage/src/styles/portal.css`。

#### 后端加的一个接口：`GET /api/user/whoami`

**问题的根**：本项目所有 `/api/*` 都强制要求 `New-API-User: <id>` 头
（`middleware/auth.go` 的 `authHelper`）。而那个 id 只在 session cookie 里
（HttpOnly，JS 读不到），门户的第一个请求又必须先知道 id 才能拼头 —— 死循环。
P0 那版是靠"登录时把 id 塞进 URL（`?sso_user=`）"绕过去的，代价是一堆脆弱的约定。

**解法**（`controller/user.go` 的 `WhoAmI`，路由 `userRoute.GET("/whoami")`）：
只认 session cookie、不要那个头，返回 session 里已经认证过的身份。
门户启动时调用它拿 id，缓存到 localStorage，后续请求再带上头。

- 为什么安全：它不返回密钥、不放大权限（攻击者拿到的最多是"你自己是谁"）；
  写入类接口仍然要求头与 session 一致，那正是 authHelper 里"防以别人身份操作"的检查。
  session cookie 又是 `SameSite=Strict`，跨站请求根本带不上 cookie。
- 副作用：`?sso_user=` 与 `web/index.html` 里那段引导脚本**不再需要**（已删）。

**验收**（不带头也必须能认出身份，且其它接口不带头仍然 401）：

```bash
JAR=$(mktemp)
curl -s -c "$JAR" -o /dev/null -X POST https://api.tokwork.com/api/user/login \
  -H 'Content-Type: application/json' -d '{"username":"…","password":"…"}'
curl -s -b "$JAR" https://api.tokwork.com/api/user/whoami          # 200 + data.id
curl -s -o /dev/null -w '%{http_code}\n' -b "$JAR" \
  https://api.tokwork.com/api/user/self                            # 401（缺 New-API-User 头）
```

#### 面板外观相关的配置（都是 option，不是代码）

| option | 值 | 作用 |
|---|---|---|
| `SystemName` | `TokWork` | 页头站点名、浏览器标签标题 |
| `Logo` | `https://www.tokwork.com/logo.svg` | 页头 logo + favicon |
| `HeaderNavModules` | 五项全 `false` | 顶栏**不显示任何导航链接** |
| `Footer` | `© 2026 TokWork · 仅限授权客户使用` | 页脚换成我们自己的文字 |
| `ServerAddress` | `https://www.tokwork.com` | 密码重置邮件里的链接基址（拼成 `/user/reset?email=&token=`，正好落在营销站的页面上） |

另外 `web/src/context/Theme/index.jsx` 改成**只有亮色**（夜间模式下线）：
去掉主题切换按钮（`ActionButtons.jsx`）、挂载时清掉 `body[theme-mode]` 与 `html.dark`、
`index.css` 里声明 `color-scheme: light`。它们只对 admin SPA 有意义，客户门户本来就是亮色。

> 保留的：`admin.tokwork.com` 上那套 SPA 的页脚里有一行"设计与开发由 New API"的
> 外链署名。那是 AGENTS.md「规则 5」明确保护的上游署名，**不删**；
> 客户门户（自研的那四页）里没有任何上游标记，只有我们自己的 logo。

---

## 9. 还没做（计划，不是现状）

- [ ] 确认管理员密码足够强（初始化时自设，系统无默认账号）；删除默认令牌
- [ ] 配 Cloudflare Access 保护 `admin.tokwork.com`
- [ ] 决定是否把 `/api/*` 从 `api.tokwork.com` 上摘掉（Caddy 加路径规则）
- [ ] `CRYPTO_SECRET` 另存到密码管理器（**丢了所有上游 Key 解码失败**）
- [ ] 接文本模型渠道。**当前只接了 1 个渠道（apimaster 文生图），在售 10 个图片模型**；
      `GET /v1/chat/completions` 用任何文本模型都会返回 `503 model_not_found`
      （2026-10-10 实测，见 README 的「当前在售」一节）

> 已完成（2026-10-10）：Hetzner 付费磁盘备份已开；rclone → R2 异地备份已配并实测通过。
> 已完成（2026-10-10）：宣传站 apex 已从死掉的 `47.245.87.159` 切到 Worker
> `tokwork-homepage`（先删掉 apex 的 A 记录，再加 Worker 自定义域 ——
> Cloudflare 不允许自定义域与已有 DNS 记录冲突，会报
> `Hostname ... already has externally managed DNS records`）。
> 已完成：Pages 项目 `tokwork-panel` 已在 tokwork zone 所在账号
> `28fa4860…` 上（admin / console 两个域名都指向它，2026-10-10 实测可服务）。

## 10. 排错速查

| 现象 | 原因 / 处理 |
|---|---|
| `https://admin.tokwork.com/` 308 死循环 | Pages 自定义域名边缘注册未传播完，等 10 分钟；仍不行则把该记录改灰云 |
| 证书签发失败，报 `216.198.79.1` | 该域名有旧的 A 记录残留，等 DNS 缓存过期后 `docker compose restart caddy` |
| Caddy 报 `challenge failed` | 80 端口不通，或 DNS 还没指向本机 |
| 面板能打开但接口全 401 | 正常——需要先登录；`/v1/*` 要带 `Authorization: Bearer sk-...` |
| 面板打开但控制台报 `No 'Access-Control-Allow-Origin' header` | `/api/*` 缺 CORS。见 §3，确认 `SetApiRouter` 里有 `apiRouter.Use(middleware.CORS())`，改完必须重新编译后端二进制并重新部署 |
| 跨域下某个接口报 `No 'Access-Control-Allow-Origin'`，但 curl 直接请求是 200/401 | 大概率是**尾斜杠 301**：重定向响应不带 CORS 头。用户实测过 `/api/data/self/`、`/api/log/self/`、`/api/mj/self/`。见 §8.6「一条只有跨域才会遇到的坑」 |
| 营销站登录框点了报「Could not reach the gateway」 | 那是营销站 CSP 拦的（`connect-src` 缺 `https://api.tokwork.com`），不是网关故障。浏览器控制台会明说 `violates the Content Security Policy` |
| 营销站登录成功、跳到 console 又被踢回 `/login?expired=true` | `?sso_user=<id>` 没传过去，或控制台 `index.html` 的引导脚本没跑 —— 控制台会带 `New-API-User: -1` 请求，后端按「用户不匹配」401。见 §8.6 的 SSO 一节 |
| 改动生效了但浏览器仍旧报错 | 先硬刷 `Cmd+Shift+R`；CORS 失败会被浏览器短暂记忆，无痕窗口可快速判定 |
| 号池总览 500 / panic `pool.go:70` | 该模块残留 SQLite 写法。见 changelog v0.5.5：`top_ups` 的列名是 `create_time`（不是 `created_time`），`group`/`key` 必须按方言引用（PG 双引号） |
| 号池配方列表重复（19 条变 57 条） | 同一根因的连带伤：查询报错被当成"不存在"→ 反复 Insert。v0.5.5 起启动会自愈去重 |
