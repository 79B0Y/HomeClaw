#!/bin/bash

# LinknLink 超轻量级文本显示卸载脚本

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

log_info "Starting LinknLink Loading Display (Text Mode) uninstallation..."

# 1. 停止并禁用服务
if systemctl is-active --quiet loading.service; then
    log_info "Stopping loading service..."
    systemctl stop loading.service
fi

if systemctl is-enabled --quiet loading.service 2>/dev/null; then
    log_info "Disabling loading service..."
    systemctl disable loading.service
fi

# 2. 删除服务文件
if [ -f /etc/systemd/system/loading.service ]; then
    log_info "Removing service file..."
    rm -f /etc/systemd/system/loading.service
    systemctl daemon-reload
fi

# 3. 删除脚本
if [ -f /usr/local/bin/show-loading ]; then
    log_info "Removing script..."
    rm -f /usr/local/bin/show-loading
fi

# 4. 删除日志文件（可选）
read -p "Remove log file /var/log/loading.log? (y/N): " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    log_info "Removing log file..."
    rm -f /var/log/loading.log
else
    log_info "Keeping log file"
fi

log_info "Uninstallation completed!"
