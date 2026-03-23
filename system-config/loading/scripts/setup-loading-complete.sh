#!/bin/bash
# setup-loading-complete.sh - 完整的 loading 配置脚本
# 用法: sudo bash setup-loading-complete.sh
#
# 依赖文件：
#   - loading/scripts/show-loading.sh（必需）
#
# 此脚本会自动执行以下操作：
#   1. 安装 loading 服务
#   2. 屏蔽系统日志
#   3. 禁用 TTY1 登录
#   4. 启动 loading 服务
#
# 注意：必须在 iSGBox 项目根目录运行此脚本

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
log_info "   LinknLink Loading2 完整配置"
log_info "============================================"

# 获取脚本目录
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ============================================
# 1. 基础安装
# ============================================
log_info ""
log_info "步骤 1/4: 基础安装..."
log_info "创建安装目录..."
mkdir -p /var/log

log_info "复制脚本文件..."
SHOW_LOADING2="$SCRIPT_DIR/show-loading.sh"
if [ ! -f "$SHOW_LOADING2" ]; then
    log_error "找不到 $SHOW_LOADING2，请确保脚本在正确的位置"
fi
cp "$SHOW_LOADING2" /usr/local/bin/show-loading
chmod +x /usr/local/bin/show-loading

log_info "安装 systemd 服务..."
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

touch /var/log/loading.log
chmod 644 /var/log/loading.log

log_info "✓ 基础安装完成"

# ============================================
# 2. 屏蔽系统日志
# ============================================
log_info ""
log_info "步骤 2/4: 屏蔽系统日志..."

log_info "临时屏蔽内核日志..."
sysctl -w kernel.printk="0 0 0 0" 2>/dev/null || true

log_info "配置永久屏蔽..."
SYSCTL_CONF="/etc/sysctl.conf"

# 备份
if [ ! -f "${SYSCTL_CONF}.bak" ]; then
    cp "$SYSCTL_CONF" "${SYSCTL_CONF}.bak"
    log_info "已备份到 ${SYSCTL_CONF}.bak"
fi

# 处理注释和未注释的情况
if grep -q "kernel.printk" "$SYSCTL_CONF"; then
    log_info "更新 kernel.printk 配置..."
    sed -i 's/^#*kernel.printk.*/kernel.printk = 0 0 0 0/' "$SYSCTL_CONF"
else
    log_info "添加 kernel.printk 配置..."
    echo "kernel.printk = 0 0 0 0" >> "$SYSCTL_CONF"
fi

# 应用配置
sysctl -p 2>/dev/null || true

log_info "✓ 系统日志屏蔽完成"

# ============================================
# 3. 禁用 TTY1 登录
# ============================================
log_info ""
log_info "步骤 3/4: 禁用 TTY1 登录..."

log_info "禁用 getty@tty1.service..."
systemctl mask getty@tty1.service
systemctl stop getty@tty1.service 2>/dev/null || true

log_info "✓ TTY1 登录禁用完成"

# ============================================
# 4. 启动服务
# ============================================
log_info ""
log_info "步骤 4/4: 启动服务..."

log_info "重启 loading 服务..."
systemctl restart loading.service
sleep 2

log_info "检查服务状态..."
if systemctl is-active --quiet loading.service; then
    log_info "✓ loading 服务运行正常"
else
    log_warn "⚠ loading 服务可能有问题，请检查"
fi

# ============================================
# 完成
# ============================================
log_info ""
log_info "============================================"
log_info "   配置完成！"
log_info "============================================"
log_info ""
log_info "已完成的配置："
log_info "  ✓ loading 服务已安装并启动"
log_info "  ✓ 系统日志已屏蔽"
log_info "  ✓ TTY1 登录已禁用"
log_info "  ✓ loading 独占 TTY1"
log_info ""
log_info "后续步骤："
log_info "  1. 重启系统：sudo reboot"
log_info "  2. 重启后应该只显示 loading 页面"
log_info ""
log_info "恢复方法（如果需要登录）："
log_info "  1. 启用 getty："
log_info "     sudo systemctl unmask getty@tty1.service"
log_info "     sudo systemctl start getty@tty1.service"
log_info ""
log_info "查看日志："
log_info "  sudo journalctl -u loading.service -f"
log_info "============================================"
