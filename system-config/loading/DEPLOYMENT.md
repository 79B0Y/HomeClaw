# LinknLink Loading 部署指南

## 概述

这是一个**超轻量级的纯文本启动画面显示方案**，零依赖，资源占用极低，启动快速。

**核心特性：**
- 显示 ASCII 艺术 Logo 和设备信息
- 实时显示设备 IP 地址和主机名
- 极低资源占用（<5MB 内存）
- 零依赖（纯 Bash 实现）
- 支持彩色输出
- 自动检测网络变化

## 快速开始（2 步）

### 1. 一键安装

```bash
cd iSGBox/loading
sudo bash scripts/setup-loading.sh
```

### 2. 启动服务

```bash
sudo systemctl start loading.service
```

## 安装详解

### 前置要求

- Ubuntu 20.04 / 22.04 Server
- Bash shell
- 网络连接

### 自动安装（推荐）

```bash
sudo bash scripts/setup-loading.sh
```

脚本会自动：
1. 创建安装目录
2. 复制脚本文件
3. 安装 Systemd 服务
4. 启用开机自启

### 手动安装

```bash
# 1. 创建目录
sudo mkdir -p /var/log

# 2. 复制脚本
sudo cp scripts/show-loading.sh /usr/local/bin/show-loading
sudo chmod +x /usr/local/bin/show-loading

# 3. 安装服务
sudo cp systemd/loading.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable loading.service

# 4. 启动服务
sudo systemctl start loading.service
```

## 配置

### 环境变量

编辑 `/etc/systemd/system/loading.service`：

```ini
[Service]
Environment="TTY_DEVICE=/dev/tty1"           # TTY 设备
Environment="REFRESH_INTERVAL=5"             # 刷新间隔（秒）
Environment="ENABLE_COLOR=true"              # 启用彩色输出
```

修改后重启：

```bash
sudo systemctl daemon-reload
sudo systemctl restart loading.service
```

### 常见配置

**更改刷新间隔：**

```bash
sudo sed -i 's/REFRESH_INTERVAL=5/REFRESH_INTERVAL=10/' /etc/systemd/system/loading.service
sudo systemctl daemon-reload
sudo systemctl restart loading.service
```

**禁用彩色输出：**

```bash
sudo sed -i 's/ENABLE_COLOR=true/ENABLE_COLOR=false/' /etc/systemd/system/loading.service
sudo systemctl daemon-reload
sudo systemctl restart loading.service
```

## 操作命令

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

# 禁用开机自启
sudo systemctl disable loading.service

# 启用开机自启
sudo systemctl enable loading.service
```

## 调试

### 手动运行脚本

```bash
# 直接运行脚本（按 Ctrl+C 退出）
sudo /usr/local/bin/show-loading
```

### 查看日志

```bash
# 查看系统日志
sudo journalctl -u loading.service -f

# 查看应用日志
tail -f /var/log/loading.log

# 查看最近的错误
sudo journalctl -u loading.service -p err
```

## 显示效果

```
╔════════════════════════════════════════════════════════════════════════════════╗
║                                                                                ║
║  ██╗     ██╗███╗   ██╗██╗  ██╗███╗   ██╗██╗     ██╗███╗   ██╗██╗  ██╗        ║
║  ██║     ██║████╗  ██║██║ ██╔╝████╗  ██║██║     ██║████╗  ██║██║ ██╔╝        ║
║  ██║     ██║██╔██╗ ██║█████╔╝ ██╔██╗ ██║██║     ██║██╔██╗ ██║█████╔╝         ║
║  ██║     ██║██║╚██╗██║██╔═██╗ ██║╚██╗██║██║     ██║██║╚██╗██║██╔═██╗         ║
║  ███████╗██║██║ ╚████║██║  ██╗██║ ╚████║███████╗██║██║ ╚████║██║  ██╗        ║
║  ╚══════╝╚═╝╚═╝  ╚═══╝╚═╝  ╚═╝╚═╝  ╚═══╝╚══════╝╚═╝╚═╝  ╚═══╝╚═╝  ╚═╝        ║
║                                                                                ║
║                        LinknLink iSGBox                                       ║
║                                                                                ║
╠════════════════════════════════════════════════════════════════════════════════╣
║                                                                                ║
║  Hostname: isgbox-001                                                          ║
║  IP Address: 192.168.1.100                                                     ║
║  Last Update: 2026-03-16 10:30:45                                              ║
║                                                                                ║
╚════════════════════════════════════════════════════════════════════════════════╝
```

## 故障排除

### IP 显示不正确

```bash
# 检查网络配置
hostname -I

# 查看网络接口
ip addr show

# 查看日志
sudo journalctl -u loading.service -f
```

### 显示不出现

```bash
# 检查 TTY 设备
ls -la /dev/tty1

# 检查服务状态
sudo systemctl status loading.service

# 查看日志
sudo journalctl -u loading.service -n 50
```

### 彩色输出不工作

```bash
# 禁用彩色输出
sudo sed -i 's/ENABLE_COLOR=true/ENABLE_COLOR=false/' /etc/systemd/system/loading.service
sudo systemctl daemon-reload
sudo systemctl restart loading.service
```

## 性能指标

| 指标 | 值 |
|------|-----|
| 内存占用 | <5MB |
| CPU占用 | <0.5% |
| 启动时间 | <1 秒 |
| 刷新间隔 | 5 秒（可配置） |
| 磁盘占用 | <1MB |
| 依赖项 | 0（零依赖） |

## 与其他方案对比

| 特性 | loading-text | loading | Kiosk |
|------|---------|---------|-------|
| 内存占用 | <5MB | 10-20MB | 300-500MB |
| CPU占用 | <0.5% | <1% | 5-10% |
| 启动时间 | <1s | 1-2s | 15-20s |
| 依赖项 | 0 | 3 | 10+ |
| 磁盘占用 | <1MB | ~5MB | ~200MB |
| 显示效果 | 纯文本 | 图形+文字 | 完整 Web |

## 卸载

```bash
sudo bash scripts/uninstall-loading.sh
```

脚本会提示是否删除日志文件。

## 许可证

MIT License

## 支持

如有问题，请查看：
- `README.md` - 完整文档
- `QUICKREF.md` - 快速参考
- 日志文件：`sudo journalctl -u loading.service -f`
