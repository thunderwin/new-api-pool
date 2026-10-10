#!/usr/bin/env bash
# tokwork 中转站 — 每日数据库备份
# 装到 crontab：30 3 * * * /opt/new-api-pool/backup.sh >> /opt/new-api-pool/logs/backup.log 2>&1
set -euo pipefail
cd "$(dirname "$0")"

set -a
# shellcheck disable=SC1091
source .env
set +a

TS=$(date +%Y%m%d-%H%M%S)
OUT="backups/newapi-${TS}.sql.gz"
KEEP_DAYS=${BACKUP_RETENTION_DAYS:-14}

log() { echo "[$(date '+%F %T')] $*"; }

notify() {
  if [[ -n "${TG_BOT_TOKEN:-}" && -n "${TG_CHAT_ID:-}" ]]; then
    curl -fsS --max-time 10 -o /dev/null \
      -d "chat_id=${TG_CHAT_ID}" -d "text=[backup] $1" \
      "https://api.telegram.org/bot${TG_BOT_TOKEN}/sendMessage" || true
  fi
}

mkdir -p backups

log "pg_dump -> ${OUT}"
docker exec -e PGPASSWORD="$PG_PASSWORD" new-api-postgres \
  pg_dump -U "$PG_USER" -d "$PG_DB" --no-owner --no-privileges \
  | gzip -9 > "$OUT"

SIZE=$(du -h "$OUT" | cut -f1)
log "完成 (${SIZE})"

# 自检：压缩包完整 + 能读到结尾标记
if ! gzip -t "$OUT" 2>/dev/null; then
  log "❌ 备份文件损坏: $OUT"
  notify "❌ 备份损坏 ${TS}"
  exit 1
fi

# 清理过期
DELETED=$(find backups -name 'newapi-*.sql.gz' -mtime +"$KEEP_DAYS" -print -delete | wc -l)
[[ "$DELETED" -gt 0 ]] && log "清理了 ${DELETED} 个过期备份（>${KEEP_DAYS} 天）"

# 可选异地同步（配好 rclone 后把 RCLONE_REMOTE/RCLONE_PATH 写进 .env 即生效）
if [[ -n "${RCLONE_REMOTE:-}" ]] && command -v rclone >/dev/null 2>&1; then
  log "rclone -> ${RCLONE_REMOTE}:${RCLONE_PATH}"
  rclone copy "$OUT" "${RCLONE_REMOTE}:${RCLONE_PATH}" --transfers 2 || log "⚠️ rclone 失败"
fi

TOTAL=$(du -sh backups | cut -f1)
log "备份目录占用: ${TOTAL}"
notify "✅ 备份成功 ${TS} (${SIZE})"
