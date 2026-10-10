#!/usr/bin/env bash
# tokwork 中转站 — Hetzner 服务器初始化（Ubuntu 26.04, 1 vCPU / 2 GB）
# 用法：ssh root@2.31.8.21 'bash -s' < ops/hetzner-prep.sh
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

log() { echo "[prep $(date '+%F %T')] $*"; }

# ── 1. swap 2G（1.9G 物理内存，swap 是兜底保险）──
if [ ! -f /swapfile ]; then
  log "创建 2G swap"
  fallocate -l 2G /swapfile
  chmod 600 /swapfile
  mkswap /swapfile >/dev/null
  swapon /swapfile
  grep -q '^/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
else
  log "swap 已存在，跳过"
fi

cat > /etc/sysctl.d/99-tokwork.conf <<'EOF'
vm.swappiness = 10
vm.overcommit_memory = 1
EOF
sysctl --system >/dev/null

# ── 2. 基础软件 ──
log "apt update + 安装基础包"
apt-get update -qq
apt-get install -y -qq \
  ca-certificates curl git jq htop \
  ufw fail2ban rclone sqlite3 postgresql-client unzip >/dev/null

# ── 3. Docker（不走 Docker Desktop 源，用官方脚本）──
if ! command -v docker >/dev/null 2>&1; then
  log "安装 Docker"
  curl -fsSL https://get.docker.com | sh >/dev/null
fi
systemctl enable --now docker >/dev/null 2>&1 || true

# 日志滚动，防止 journal/json-file 吃满 /var/lib/docker
mkdir -p /etc/docker
cat > /etc/docker/daemon.json <<'EOF'
{
  "log-driver": "json-file",
  "log-opts": { "max-size": "50m", "max-file": "3" }
}
EOF
systemctl restart docker

# ── 4. 防火墙：只放 22 / 80 / 443 ──
log "配置 ufw"
ufw allow 22/tcp >/dev/null
ufw allow 80/tcp >/dev/null
ufw allow 443/tcp >/dev/null
ufw allow 443/udp >/dev/null
ufw --force enable >/dev/null

# ── 5. 目录 ──
mkdir -p /opt/new-api-pool/{data,logs,backups,caddy}

# ── 6. 报告 ──
log "完成。当前状态："
echo "--- swap ---"; swapon --show || true
echo "--- docker ---"; docker --version
echo "--- compose ---"; docker compose version
echo "--- ufw ---"; ufw status | head -8
echo "--- 磁盘 ---"; df -h / | tail -1
echo "--- 内存 ---"; free -m | head -2
