#!/bin/bash
# new-api-pool 一键启动脚本 - 双击即可运行
#
# 本 binary 是 fork 的自编译产物（含号池管理一级菜单 + 5 个子模块）。
# 重新编译（在本目录跑）：
#   (cd web && bun install && bun run build)
#   go build -o new-api-macos .

set -e
cd "$(dirname "$0")"

BIN="new-api-macos"

echo "============================================"
echo "  new-api-pool 一键启动"
echo "  工作目录: $(pwd)"
echo "============================================"

if [ ! -f "$BIN" ]; then
    echo ""
    echo "❌ 未找到 $BIN"
    echo "请先编译："
    echo "  (cd web && bun install && bun run build)"
    echo "  go build -o new-api-macos ."
    exit 1
fi

CURRENT_SIZE=$(stat -f%z "$BIN" 2>/dev/null || echo 0)
SIZE_MB=$((CURRENT_SIZE / 1024 / 1024))
if [ "$CURRENT_SIZE" -lt 50000000 ]; then
    echo "❌ binary 体积异常 (${SIZE_MB}MB)，可能不完整。请重新编译。"
    exit 1
fi
echo "[1/3] binary OK (${SIZE_MB}MB) — 自编译 fork 版本"

echo ""
echo "[2/3] 设置执行权限..."
chmod +x "$BIN"
xattr -d com.apple.quarantine "$BIN" 2>/dev/null || true

echo ""
echo "[3/3] 启动 new-api，监听 http://localhost:3000"
echo "首次启动：浏览器会进入初始化向导，请自己设置管理员账号（≤12 字符）和密码（≥8 字符）"
echo "号池管理入口：登录后侧边栏「号池管理」一级菜单"
echo "数据保存在: $(pwd)/data"
echo ""
echo "按 Ctrl+C 停止服务"
echo "============================================"
echo ""

( sleep 5 && open "http://localhost:3000" ) &

mkdir -p data logs
exec ./"$BIN" --port 3000 --log-dir ./logs
