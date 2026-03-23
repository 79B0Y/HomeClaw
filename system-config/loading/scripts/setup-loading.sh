#!/bin/bash

# LinknLink 超轻量级文本显示一键安装脚本

set -e

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info() {
    echo -e "${GREEN}[INFO]${NC} $*"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $*"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $*"
}

# 检查是否为 root
if [ "$EUID" -ne 0 ]; then
    log_error "This script must be run as root"
    exit 1
fi

log_info "Starting LinknLink Loading Display (Text Mode) installation..."

# 1. 创建安装目录
log_info "Creating installation directories..."
mkdir -p /var/log

# 2. 复制脚本文件
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

log_info "Copying scripts..."
cp "$SCRIPT_DIR/scripts/show-loading.sh" /usr/local/bin/show-loading
chmod +x /usr/local/bin/show-loading

# 3. 安装 Systemd 服务
log_info "Installing systemd service..."
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
StandardOutput=journal
StandardError=journal

# 环境变量配置
Environment="TTY_DEVICE=/dev/tty1"
Environment="REFRESH_INTERVAL=5"
Environment="ENABLE_COLOR=true"

[Install]
WantedBy=multi-user.target
EOF

# 4. 启用并启动服务
log_info "Enabling and starting service..."
systemctl daemon-reload
systemctl enable loading.service

# 5. 创建日志文件
touch /var/log/loading.log
chmod 644 /var/log/loading.log

log_info "Installation completed successfully!"
log_info ""
log_info "Next steps:"
log_info "1. Start the service: sudo systemctl start loading.service"
log_info "2. Check status: sudo systemctl status loading.service"
log_info "3. View logs: sudo journalctl -u loading.service -f"
log_info ""
log_info "To hide system logs (optional):"
log_info "  sudo bash $SCRIPT_DIR/scripts/hide-logs.sh"
log_info ""
log_info "Configuration:"
log_info "- Service: /etc/systemd/system/loading.service"
log_info "- Logs: /var/log/loading.log"
log_info ""
log_info "To customize, edit /etc/systemd/system/loading.service and restart:"
log_info "  sudo systemctl restart loading.service"
