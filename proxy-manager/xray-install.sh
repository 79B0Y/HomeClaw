#!/bin/bash
# =============================================================================
# Xray 代理一键安装脚本（含 Web 管理面板）
# 系统环境：Ubuntu 20.04+ / ARM64 (aarch64)
#
# 使用前确保以下文件在同一目录：
#   xray-install.sh
#   xray-panel.html
#   xray-panel-api.py
#
# 节点管理统一在 Web 面板操作：http://设备IP:8080
# =============================================================================

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

info()    { echo -e "${BLUE}[INFO]${NC}  $*"; }
success() { echo -e "${GREEN}[OK]${NC}    $*"; }
warn()    { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error()   { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }

[ "$(id -u)" -eq 0 ] || error "请使用 root 权限运行：sudo bash $0"

PANEL_DIR="$(cd "$(dirname "$0")" && pwd)"

[ -f "$PANEL_DIR/xray-panel.html" ]    || error "未找到 $PANEL_DIR/xray-panel.html"
[ -f "$PANEL_DIR/xray-panel-api.py" ] || error "未找到 $PANEL_DIR/xray-panel-api.py"

DEVICE_IP=$(hostname -I | awk '{print $1}')

echo ""
echo "=============================================="
echo "   Xray 代理 + Web 管理面板 一键安装"
echo "=============================================="
echo ""
info "面板目录：$PANEL_DIR"
info "面板地址：http://${DEVICE_IP}:8080"
echo ""
read -rp "确认开始安装？(y/N): " CONFIRM
[[ "${CONFIRM,,}" == "y" ]] || { echo "已取消。"; exit 0; }

# =============================================================================
# 步骤 1：安装依赖
# =============================================================================
echo ""
info "步骤 1/4：安装依赖（unzip / curl / python3）..."
apt-get update -qq
apt-get install -y -qq unzip curl python3
success "依赖安装完成"

# =============================================================================
# 步骤 2：下载安装 Xray
# =============================================================================
echo ""
if command -v xray &>/dev/null; then
    info "步骤 2/4：Xray 已安装（$(xray version 2>&1 | head -1)），跳过"
else
    info "步骤 2/4：下载 Xray ARM64..."
    local_ver="v25.2.21"
    filename="Xray-linux-arm64-v8a.zip"
    urls=(
        "https://github.com/XTLS/Xray-core/releases/download/${local_ver}/${filename}"
        "https://ghfast.top/https://github.com/XTLS/Xray-core/releases/download/${local_ver}/${filename}"
        "https://gh-proxy.com/https://github.com/XTLS/Xray-core/releases/download/${local_ver}/${filename}"
        "https://mirror.ghproxy.com/https://github.com/XTLS/Xray-core/releases/download/${local_ver}/${filename}"
    )

    downloaded=0
    for url in "${urls[@]}"; do
        info "尝试：$url"
        if curl -L --progress-bar --max-time 60 -o /tmp/xray.zip "$url" 2>/dev/null; then
            if file /tmp/xray.zip 2>/dev/null | grep -q -i 'zip\|archive'; then
                success "下载成功"
                downloaded=1
                break
            else
                warn "文件异常，尝试下一个源..."
                rm -f /tmp/xray.zip
            fi
        else
            warn "下载失败，尝试下一个源..."
            rm -f /tmp/xray.zip
        fi
    done

    [ "$downloaded" -eq 1 ] || error "所有源均失败，请手动下载 ${filename} 放到 /tmp/xray.zip 后重跑"

    mkdir -p /tmp/xray-core
    unzip -o /tmp/xray.zip -d /tmp/xray-core > /dev/null
    cp /tmp/xray-core/xray /usr/local/bin/xray
    chmod +x /usr/local/bin/xray
    for dat in geoip.dat geosite.dat; do
        [ -f "/tmp/xray-core/$dat" ] \
            && cp "/tmp/xray-core/$dat" /usr/local/bin/ \
            && success "已安装 $dat"
    done
    success "Xray 安装成功：$(xray version 2>&1 | head -1)"
fi

# =============================================================================
# 步骤 3：写入初始 Xray 配置（占位，节点通过 Web 面板配置）
# =============================================================================
echo ""
info "步骤 3/4：写入初始 Xray 配置..."

if [ ! -f /etc/xray-config.json ]; then
    cat > /etc/xray-config.json << 'EOF'
{
  "log": {"loglevel": "warning"},
  "inbounds": [
    {
      "port": 1080, "listen": "0.0.0.0",
      "protocol": "socks",
      "settings": {"auth": "noauth", "udp": true},
      "tag": "socks"
    },
    {
      "port": 1081, "listen": "0.0.0.0",
      "protocol": "http",
      "settings": {},
      "tag": "http"
    }
  ],
  "outbounds": [
    {
      "protocol": "freedom",
      "tag": "direct"
    }
  ]
}
EOF
    success "初始配置已写入（节点请在 Web 面板配置）"
else
    info "已存在 /etc/xray-config.json，跳过覆盖"
fi

# 注册 xray-proxy 服务
cat > /etc/systemd/system/xray-proxy.service << 'EOF'
[Unit]
Description=Xray Proxy Service
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/xray run -c /etc/xray-config.json
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable xray-proxy
systemctl restart xray-proxy
sleep 1

if systemctl is-active --quiet xray-proxy; then
    success "xray-proxy 服务已启动"
else
    warn "xray-proxy 启动异常（初始配置无节点，属正常），配置节点后会自动恢复"
fi

# =============================================================================
# 步骤 4：安装 Web 管理面板
# =============================================================================
echo ""
info "步骤 4/4：安装 Web 管理面板..."

# 安装 Flask
apt-get install -y -qq python3-flask || {
    warn "apt 安装 flask 失败，尝试 pip3..."
    pip3 install flask 2>/dev/null || error "Flask 安装失败，请手动执行：sudo apt install python3-flask"
}
success "Flask 安装完成"

# 注册 xray-panel 服务
cat > /etc/systemd/system/xray-panel.service << EOF
[Unit]
Description=Xray Web Panel
After=network.target

[Service]
Type=simple
WorkingDirectory=${PANEL_DIR}
ExecStart=/usr/bin/python3 ${PANEL_DIR}/xray-panel-api.py
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable xray-panel
systemctl restart xray-panel
sleep 2

if systemctl is-active --quiet xray-panel; then
    success "xray-panel 服务已启动（端口 8080）"
else
    error "面板启动失败，请运行：journalctl -u xray-panel -n 30"
fi

# =============================================================================
# 完成
# =============================================================================
echo ""
echo "=============================================="
echo -e "${GREEN}           安装完成！${NC}"
echo "=============================================="
cat << USAGE

  打开浏览器访问 Web 管理面板：
  ➜  http://${DEVICE_IP}:8080

  在面板中：
  1. 右侧填入订阅链接，点击「拉取」获取节点
  2. 点击「全部测试」或「自动选优」选择节点
  3. 点击「应用」切换节点，左侧状态实时更新

  服务管理：
  sudo systemctl status  xray-proxy   # 代理状态
  sudo systemctl status  xray-panel   # 面板状态
  sudo systemctl restart xray-proxy
  sudo systemctl restart xray-panel

  卸载：
  sudo systemctl disable --now xray-proxy xray-panel
  sudo rm -f /etc/systemd/system/xray-proxy.service
  sudo rm -f /etc/systemd/system/xray-panel.service
  sudo rm -f /usr/local/bin/xray /usr/local/bin/geoip.dat /usr/local/bin/geosite.dat
  sudo rm -f /etc/xray-config.json

==============================================
USAGE

# 清理临时文件
rm -rf /tmp/xray.zip /tmp/xray-core 2>/dev/null || true