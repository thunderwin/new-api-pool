#!/usr/bin/env bash
# 交互式配置 rclone → Cloudflare R2，并把 RCLONE_REMOTE / RCLONE_PATH 写进 .env
# 用法（在服务器上）：bash /opt/new-api-pool/setup-rclone.sh
#   —— 用 read -s 读密钥，不落 shell history，也不经过任何第三方
set -euo pipefail
cd "$(dirname "$0")"

CF_ACCOUNT_ID="${CF_ACCOUNT_ID:-372be71521f180dfa777274859e1c325}"
BUCKET="${BUCKET:-tokwork-backup}"
REMOTE_NAME="${REMOTE_NAME:-r2}"

echo "目标：Cloudflare R2  账号 ${CF_ACCOUNT_ID:0:8}…  桶 ${BUCKET}"
echo
read -r -p "R2 Access Key ID: " AK
read -r -s -p "R2 Secret Access Key: " SK
echo
echo

if [[ -z "$AK" || -z "$SK" ]]; then
	echo "密钥为空，中止" >&2
	exit 1
fi

mkdir -p /root/.config/rclone
umask 077
cat > /root/.config/rclone/rclone.conf <<EOF
[$REMOTE_NAME]
type = s3
provider = Cloudflare
access_key_id = $AK
secret_access_key = $SK
endpoint = https://$CF_ACCOUNT_ID.r2.cloudflarestorage.com
acl = private
# ⚠️ 必须加：R2 的 token 通常是「桶级 + 对象读写」，没有账号级的 CreateBucket 权限。
# 不加这个，rclone 会去"检查目标桶是否存在"，HEAD 失败后尝试 CreateBucket → 403
# （报错形式很误导：failed to prepare upload: operation error S3: CreateBucket ... AccessDenied）
no_check_bucket = true
EOF
chmod 600 /root/.config/rclone/rclone.conf

# 把远端登记进 .env（backup.sh 检测到就自动异地同步）
if grep -q '^RCLONE_REMOTE=' .env 2>/dev/null; then
	sed -i "s|^RCLONE_REMOTE=.*|RCLONE_REMOTE=$REMOTE_NAME|" .env
else
	echo "RCLONE_REMOTE=$REMOTE_NAME" >> .env
fi
if grep -q '^RCLONE_PATH=' .env 2>/dev/null; then
	sed -i "s|^RCLONE_PATH=.*|RCLONE_PATH=$BUCKET|" .env
else
	echo "RCLONE_PATH=$BUCKET" >> .env
fi

echo "已写入 /root/.config/rclone/rclone.conf 与 .env"
echo "自检中…"
# 注意：不能 `rclone lsd r2:`（列所有桶需要账号级权限，桶级 token 一定 403）。
# 要按具体桶测。
if rclone lsd "$REMOTE_NAME:$BUCKET" >/dev/null 2>&1; then
	echo "✅ 连通 R2（桶 $BUCKET）"
else
	echo "❌ 连不通，请检查 Access Key / Secret / 桶名" >&2
	rclone lsd "$REMOTE_NAME:$BUCKET" 2>&1 | head -5 || true
	exit 1
fi
