#!/usr/bin/env bash
# 在服务器上生成本地 .env（密钥永不出这台机器）
set -euo pipefail
cd "$(dirname "$0")"

if [[ -f .env && "${1:-}" != "--force" ]]; then
  echo ".env 已存在，加 --force 覆盖"
  exit 0
fi

rand() { openssl rand -base64 32 | tr -d '/+=' | head -c 40; }

cat > .env <<EOF
# tokwork 中转站 — 生产环境变量（chmod 600，绝不进 Git）

# ─── 域名 ───
API_DOMAIN=${API_DOMAIN:-api.tokwork.com}
# 营销站 + 客户门户（Cloudflare Worker）。后端只做 API，网页路径会 301 跳到这里。
SITE_DOMAIN=${SITE_DOMAIN:-www.tokwork.com}
# 管理后台 SPA（Cloudflare Pages），只给 root 用；自动进 CORS 凭证白名单。
ADMIN_DOMAIN=${ADMIN_DOMAIN:-admin.tokwork.com}
# 其它要带 Cookie 跨域调 /api/* 的 Origin（逗号分隔）
TRUSTED_ORIGINS=${TRUSTED_ORIGINS:-https://tokwork.com}
ACME_EMAIL=${ACME_EMAIL:-info@yijianspace.com}

# ─── 数据库 ───
PG_USER=newapi
PG_DB=newapi
PG_PASSWORD=$(rand)

# ─── Redis ───
REDIS_PASSWORD=$(rand)

# ─── 应用密钥（改了会导致历史 Key 解密失败 / 用户被踢下线）───
SESSION_SECRET=$(rand)
CRYPTO_SECRET=$(rand)
EOF

chmod 600 .env
echo "已生成 .env（chmod 600）"
echo "⚠️  CRYPTO_SECRET 请另存到密码管理器，丢了所有上游 Key 作废"
