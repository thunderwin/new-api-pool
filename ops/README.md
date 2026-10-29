# `ops/` — 这台生产环境**实际在跑**的部署脚本

> ⚠️ 别和 [`../deploy/`](../deploy/) 搞混：
>
> | | 是什么 |
> |---|---|
> | [`deploy/`](../deploy/) | **通用蓝图**——"可以怎么搭"。12 周上线路线图 + 各种模板，里面的 IP / 域名 / 密钥全是占位符 |
> | `ops/`（本目录） | **实际部署**——tokwork 生产环境真正在用的那一套脚本 |
>
> 当前生产环境的完整记录（拓扑、规格、域名、踩坑）见
> [`../docs-pool/03-deployment/tokwork-nbg1.md`](../docs-pool/03-deployment/tokwork-nbg1.md)。

## 文件

| 文件 | 作用 |
|---|---|
| `hetzner-prep.sh` | 服务器初始化：2G swap、基础包、Docker、ufw 只放 22/80/443、建 `/opt/new-api-pool/` |
| `deploy/Dockerfile` | 极简运行时镜像（`debian:bookworm-slim` + 预编译二进制）。**服务器不做编译** |
| `deploy/docker-compose.yml` | 四容器编排（app / postgres / redis / caddy）+ 健康检查 + 针对 2 GB 内存的资源限制 |
| `deploy/Caddyfile` | 只服务 API 域名；SSE 不缓冲、超时 600s（否则长流式回答会被掐断） |
| `deploy/gen-env.sh` | **在服务器本地**生成 `.env`（PG/Redis 密码、`SESSION_SECRET`、`CRYPTO_SECRET`），密钥不出机器 |
| `deploy/backup.sh` | 每日 `pg_dump` → gzip → 完整性自检 → 清理过期 → 可选 rclone 上传 R2 |
| `deploy/setup-rclone.sh` | 交互式配置 rclone → Cloudflare R2（`read -s` 读密钥，不落 shell history），写完当场连通自检 |
| `deploy/set-capability-secret.sh` | 交互式设置任意 `CAPABILITY_*` 变量（默认 Bright Data key）；`read -rs` 读入后经 SSH stdin 传到服务器，原子更新 `.env` 并重建 `new-api` |
| `reconcile-capability-aisa.py` | 只读核对 AISA 每日实际扣费与中转站日志成本；密钥和数据库凭证只在容器内使用 |

## 完整流程

```bash
# 1. 新机器初始化（在服务器上，用 root 跑）
ssh root@<ip> 'bash -s' < ops/hetzner-prep.sh

# 2. 本地构建（服务器只有 1 核，故意不在上面编译）
cd web && bun install
VITE_REACT_APP_SERVER_URL="https://api.<domain>" DISABLE_ESLINT_PLUGIN='true' bun run build
cd .. && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-s -w" -o new-api-linux-amd64 .

# 3. 传到服务器
scp new-api-linux-amd64 ops/deploy/{Dockerfile,docker-compose.yml,Caddyfile,gen-env.sh,backup.sh,setup-rclone.sh,set-capability-secret.sh} \
    root@<ip>:/opt/new-api-pool/

# 4. 生成密钥 + 起服务
ssh root@<ip> 'cd /opt/new-api-pool && chmod +x *.sh && ./gen-env.sh && docker compose build && docker compose up -d'
```

## 三个必须知道的约束

1. **`main.go` 里有 `//go:embed web/dist`** —— 编后端之前**必须先构建前端**，否则 `go build` 直接失败。
2. **前端构建要注入 `VITE_REACT_APP_SERVER_URL`** —— 否则 `web/src/helpers/api.js` 的 axios baseURL
   会退化成相对路径，而前端托管在 Cloudflare、后端在服务器上，两边不同域。
3. **Capability 环境变量必须在 compose 显式透传** —— 例如
   `CAPABILITY_BRIGHTDATA_API_KEY`、`CAPABILITY_BRIGHTDATA_ZONE` 和
   `CAPABILITY_AISA_API_KEY` 只写进 `.env` 不会自动进入容器。生产使用：

   ```bash
   ./ops/deploy/set-capability-secret.sh
   ./ops/deploy/set-capability-secret.sh CAPABILITY_AISA_API_KEY
   ./ops/deploy/set-capability-secret.sh CAPABILITY_AISA_API_KEY --dry-run
   ./ops/deploy/set-capability-secret.sh --dry-run
   # 若 SSH 未在 ~/.ssh/config 绑定该主机：
   SSH_IDENTITY_FILE="$HOME/.ssh/id_ed25519_tokwork" ./ops/deploy/set-capability-secret.sh
   ```

   变量名只允许 `^CAPABILITY_[A-Z0-9_]+$`。不带变量名时保持 Bright Data 行为。
   API key 由交互输入提供；不要把它放进命令行参数、shell history 或提交到仓库。

## 改动本目录时

按 GEB 规矩，改脚本要**顺手改脚本内的注释**，并同步
[`../docs-pool/03-deployment/tokwork-nbg1.md`](../docs-pool/03-deployment/tokwork-nbg1.md) 里对应的说明。

## AISA Capability 对账

生产机从 `/opt/new-api-pool` 运行：

```bash
python3 /opt/new-api-pool/reconcile-capability-aisa.py \
  --compose-dir /opt/new-api-pool \
  --since 2026-10-29 --until 2026-10-29
```

脚本汇总 AISA `GET /v1/usage` 的 `charged_micros_usd` 与消费日志
`other.provider=aisa` 的 `usage_quantity`。AISA 是 UTC 日桶；差额阈值是
`max($0.05, 上游金额的 2%)`。标准输出只有日期、两个金额和差额，并把同样的
结果追加到 `logs/aisa-capability-reconcile.jsonl`。API 进程另有每日自动任务，
超阈值时通过现有 root 通知机制告警。
