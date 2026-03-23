#!/bin/bash
# disable-getty.sh - 禁用 TTY1 登录，让 loading 独占
# 用法: sudo bash disable-getty.sh

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }

[[ $EUID -ne 0 ]] && log_error "请使用 root 权限运行此脚本"

log_info "============================================"
log_info "       禁用 TTY1 登录"
log_info "============================================"

# 1. 禁用 getty@tty1.service
log_info "禁用 getty@tty1.service..."
systemctl mask getty@tty1.service
systemctl stop getty@tty1.service 2>/dev/null || true

# 2. 确保 loading 服务在 getty 之前启动
log_info "更新 loading 服务配置..."
cat > /etc/systemd/system/loading.service << 'EOF'
[Unit]
Description=LinknLink Loading Display Service (Text Mode)
After=network-online.target
Wants=network-online.target
Before=getty@tty1.service

[Service]
Type=simple
ExecStart=/usr/local/bin/show-loading
Restart=always
RestartSec=5
StandardOutput=null
StandardError=null

# 环境变量配置
Environment="TTY_DEVICE=/dev/tty1"
Environment="REFRESH_INTERVAL=5"
Environment="ENABLE_COLOR=true"

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable loading.service

log_info "============================================"
log_info "       配置完成"
log_info "============================================"
log_info "已做的修改："
log_info "  ✓ 禁用 getty@tty1.service"
log_info "  ✓ loading 服务配置已更新"
log_info ""
log_info "后续步骤："
log_info "  1. 重启服务：sudo systemctl restart loading.service"
log_info "  2. 重启系统：sudo reboot"
log_info ""
log_info "恢复方法（如果需要登录）："
log_info "  1. 启用 getty：sudo systemctl unmask getty@tty1.service"
log_info "  2. 启动 getty：sudo systemctl start getty@tty1.service"
log_info "============================================"
