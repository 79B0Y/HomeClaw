# LinknLink Loading 快速参考

## 安装

```bash
# 一键安装
sudo bash scripts/setup-loading.sh

# 启动服务
sudo systemctl start loading.service

# 查看状态
sudo systemctl status loading.service
```

## 常用命令

```bash
# 查看状态
sudo systemctl status loading.service

# 启动/停止/重启
sudo systemctl start loading.service
sudo systemctl stop loading.service
sudo systemctl restart loading.service

# 查看实时日志
sudo journalctl -u loading.service -f

# 查看最近 50 行日志
sudo journalctl -u loading.service -n 50

# 启用/禁用开机自启
sudo systemctl enable loading.service
sudo systemctl disable loading.service

# 卸载
sudo bash scripts/uninstall-loading.sh
```

## 配置修改

编辑 `/etc/systemd/system/loading.service`，修改 `Environment` 部分：

```ini
# TTY 设备
Environment="TTY_DEVICE=/dev/tty1"

# 刷新间隔（秒）
Environment="REFRESH_INTERVAL=5"

# 启用彩色输出
Environment="ENABLE_COLOR=true"
```

修改后重启服务：

```bash
sudo systemctl daemon-reload
sudo systemctl restart loading.service
```

## 调试

### 手动运行脚本

```bash
# 直接运行脚本
sudo /usr/local/bin/show-loading
```

### 查看日志

```bash
# 查看系统日志
sudo journalctl -u loading.service -f

# 查看应用日志
tail -f /var/log/loading.log
```

## 故障排除

| 问题 | 解决方案 |
|------|---------|
| 显示不出现 | 检查设备：`ls -la /dev/tty1`；查看日志：`sudo journalctl -u loading.service -f` |
| IP 显示错误 | 检查网络：`hostname -I`；查看接口：`ip addr show` |
| 彩色输出不工作 | 禁用彩色：编辑服务文件，设置 `ENABLE_COLOR=false` |

## 快速配置

### 更改刷新间隔

```bash
sudo sed -i 's/REFRESH_INTERVAL=5/REFRESH_INTERVAL=10/' /etc/systemd/system/loading.service
sudo systemctl daemon-reload
sudo systemctl restart loading.service
```

### 禁用彩色输出

```bash
sudo sed -i 's/ENABLE_COLOR=true/ENABLE_COLOR=false/' /etc/systemd/system/loading.service
sudo systemctl daemon-reload
sudo systemctl restart loading.service
```

### 屏蔽启动日志

```bash
# 屏蔽内核日志
sudo bash scripts/hide-logs.sh

# 禁用 TTY 登录
sudo bash scripts/disable-getty.sh

# 重启系统
sudo reboot
```

## 资源占用

- **内存**: <5MB
- **CPU**: <0.5%
- **启动时间**: <1 秒
- **磁盘占用**: <1MB
- **依赖项**: 0（零依赖）

## 与其他方案对比

| 特性 | loading-text | loading | Kiosk |
|------|---------|---------|-------|
| 内存占用 | <5MB | 10-20MB | 300-500MB |
| CPU占用 | <0.5% | <1% | 5-10% |
| 启动时间 | <1s | 1-2s | 15-20s |
| 依赖项 | 0 | 3 | 10+ |
| 磁盘占用 | <1MB | ~5MB | ~200MB |
| 显示效果 | 纯文本 | 图形+文字 | 完整 Web |

## 许可证

MIT License
