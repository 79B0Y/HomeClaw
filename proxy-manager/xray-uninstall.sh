#!/bin/bash
# =============================================================================
# Xray 代理 + Web 管理面板 卸载脚本
# =============================================================================

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

info()    { echo -e "${BLUE}[INFO]${NC}  $*"; }
success() { echo -e "${GREEN}[OK]${NC}    $*"; }
warn()    { echo -e "${YELLOW}[WARN]${NC}  $*"; }

[ "$(id -u)" -eq 0 ] || { echo -e "${RED}[ERROR]${NC} 请使用 root 权限运行：sudo bash $0"; exit 1; }

PANEL_DIR="$(cd "$(dirname "$0")" && pwd)"

echo ""
echo "=============================================="
echo "   Xray 代理 + Web 管理面板 卸载向导"
echo "=============================================="
echo ""
echo "  将要删除："
echo "  - systemd 服务：xray-proxy、xray-panel"
echo "  - Xray 二进制：/usr/local/bin/xray"
echo "  - Geo 数据文件：geoip.dat、geosite.dat"
echo "  - Xray 配置：/etc/xray-config.json"
echo "  - 面板文件：$PANEL_DIR（可选）"
echo ""
read -rp "确认卸载？(y/N): " CONFIRM
[[ "${CONFIRM,,}" == "y" ]] || { echo "已取消。"; exit 0; }

# =============================================================================
# 停止并删除 systemd 服务
# =============================================================================
echo ""
info "停止并删除 systemd 服务..."

for svc in xray-proxy xray-panel; do
    if systemctl is-active --quiet "$svc" 2>/dev/null; then
        systemctl stop "$svc"
        success "已停止 $svc"
    fi
    if systemctl is-enabled --quiet "$svc" 2>/dev/null; then
        systemctl disable "$svc"
        success "已禁用 $svc"
    fi
    if [ -f "/etc/systemd/system/${svc}.service" ]; then
        rm -f "/etc/systemd/system/${svc}.service"
        success "已删除 /etc/systemd/system/${svc}.service"
    fi
done

systemctl daemon-reload
success "systemd 已刷新"

# =============================================================================
# 删除 Xray 文件
# =============================================================================
echo ""
info "删除 Xray 二进制和配置..."

for f in /usr/local/bin/xray /usr/local/bin/geoip.dat /usr/local/bin/geosite.dat; do
    if [ -f "$f" ]; then
        rm -f "$f"
        success "已删除 $f"
    fi
done

if [ -f /etc/xray-config.json ]; then
    rm -f /etc/xray-config.json
    success "已删除 /etc/xray-config.json"
fi

# =============================================================================
# 可选：删除面板文件
# =============================================================================
echo ""
read -rp "是否同时删除面板目录 $PANEL_DIR？(y/N): " DEL_PANEL
if [[ "${DEL_PANEL,,}" == "y" ]]; then
    rm -rf "$PANEL_DIR"
    success "已删除 $PANEL_DIR"
else
    info "保留面板目录 $PANEL_DIR"
fi

# =============================================================================
# 可选：卸载 Flask
# =============================================================================
echo ""
read -rp "是否卸载 python3-flask？(y/N): " DEL_FLASK
if [[ "${DEL_FLASK,,}" == "y" ]]; then
    apt-get remove -y -qq python3-flask && success "已卸载 python3-flask"
fi

# =============================================================================
# 完成
# =============================================================================
echo ""
echo "=============================================="
echo -e "${GREEN}           卸载完成！${NC}"
echo "=============================================="
echo ""