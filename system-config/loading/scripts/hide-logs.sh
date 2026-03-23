#!/bin/bash
# hide-logs.sh - 屏蔽系统日志输出（安全方案，不修改启动参数）
# 用法: sudo bash hide-logs.sh

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
log_info "       屏蔽系统日志配置"
log_info "============================================"

# 1. 临时屏蔽内核日志（立即生效）
log_info "临时屏蔽内核日志..."
sysctl -w kernel.printk="0 0 0 0" 2>/dev/null || true

# 2. 永久屏蔽内核日志（修改 sysctl.conf）
log_info "配置永久屏蔽..."

SYSCTL_CONF="/etc/sysctl.conf"

# 备份
if [ ! -f "${SYSCTL_CONF}.bak" ]; then
    cp "$SYSCTL_CONF" "${SYSCTL_CONF}.bak"
    log_info "已备份到 ${SYSCTL_CONF}.bak"
fi

# 检查是否已存在（包括注释的）
if grep -q "kernel.printk" "$SYSCTL_CONF"; then
    log_info "已存在 kernel.printk 配置，更新..."
    # 处理注释和未注释的情况
    sed -i 's/^#*kernel.printk.*/kernel.printk = 0 0 0 0/' "$SYSCTL_CONF"
else
    log_info "添加 kernel.printk 配置..."
    echo "kernel.printk = 0 0 0 0" >> "$SYSCTL_CONF"
fi

# 应用配置
sysctl -p 2>/dev/null || true

# 3. 修改 systemd 服务配置
log_info "修改 systemd 服务配置..."
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
log_info "已更新 systemd 服务配置"

log_info "============================================"
log_info "       配置完成"
log_info "============================================"
log_info "已做的修改："
log_info "  ✓ 临时屏蔽内核日志（立即生效）"
log_info "  ✓ 永久屏蔽内核日志（修改 sysctl.conf）"
log_info "  ✓ systemd 服务日志屏蔽"
log_info ""
log_info "注意：此方案不修改启动参数，更安全"
log_info ""
log_info "后续步骤："
log_info "  1. 重启服务：sudo systemctl restart loading.service"
log_info "  2. 检查效果：sudo systemctl status loading.service"
log_info ""
log_info "恢复方法（如果需要）："
log_info "  1. 恢复 sysctl.conf："
log_info "     sudo cp ${SYSCTL_CONF}.bak $SYSCTL_CONF"
log_info "     sudo sysctl -p"
log_info "============================================"
