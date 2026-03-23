# LinknLink 轻量级启动画面方案

> 基于纯 Bash 的超轻量级显示方案，零依赖，资源占用极低。

## 方案概述

这是一个轻量级的启动画面显示方案，使用纯 Bash 实现，直接输出到 TTY 设备，支持自动网络检测和彩色显示。

### 与其他方案对比

| 特性 | loading-text (纯文本) | loading (图形) | Kiosk |
|------|---------------|----------------|-------|
| 内存占用 | <5MB | 10-20MB | 300-500MB |
| CPU占用 | <0.5% | <1% | 5-10% |
| 启动时间 | <1s | 1-2s | 15-20s |
| 依赖项 | 0 | 3 | 10+ |
| 磁盘占用 | <1MB | ~5MB | ~200MB |
| 显示效果 | 纯文本 | 图形+文字 | 完整 Web |

### 核心特性

- ✅ 显示 ASCII 艺术 Logo
- ✅ 实时显示设备 IP 地址和主机名
- ✅ 极低资源占用（<5MB 内存）
- ✅ 零依赖（纯 Bash 实现）
- ✅ 开机自启
- ✅ 自动网络变化检测
- ✅ 支持彩色输出

## 快速开始

### 一键安装（推荐）

```bash
sudo bash scripts/setup-loading.sh
```

脚本会自动：
1. 创建安装目录
2. 复制脚本文件
3. 安装 Systemd 服务
4. 启用开机自启

### 查看状态

```bash
sudo systemctl status loading.service
sudo journalctl -u loading.service -f
```

## 目录结构

```
loading/
├── README.md                    # 本文件
├── QUICKREF.md                  # 快速参考
├── DEPLOYMENT.md                # 详细部署指南
├── HIDE_LOGS.md                 # 屏蔽启动日志指南
├── scripts/
│   ├── setup-loading.sh        # 一键安装脚本
│   ├── show-loading.sh         # 主显示脚本
│   ├── uninstall-loading.sh    # 卸载脚本
│   ├── disable-getty.sh        # 禁用 TTY 登录脚本
│   └── hide-logs.sh            # 屏蔽启动日志脚本
└── systemd/
    └── loading.service         # Systemd 服务配置
```

## 配置说明

编辑 `/etc/systemd/system/loading.service`，修改 `Environment` 部分：

```ini
# TTY 设备
Environment="TTY_DEVICE=/dev/tty1"

# 刷新间隔（秒）
Environment="REFRESH_INTERVAL=5"

# 启用彩色输出
Environment="ENABLE_COLOR=true"
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

显示内容包括：
- ASCII 艺术 Logo（顶部）
- 设备主机名（底部）
- 设备 IP 地址（底部）
- 最后更新时间（底部）

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

# 手动测试
sudo /usr/local/bin/show-loading

# 卸载
sudo bash scripts/uninstall-loading.sh
```

## 故障排除

### 显示不出现

```bash
# 检查 TTY 设备
ls -la /dev/tty1

# 检查服务状态
sudo systemctl status loading.service

# 查看日志
sudo journalctl -u loading.service -n 50
```

### IP 显示错误

```bash
# 检查网络配置
hostname -I
ip addr show

# 查看日志
sudo journalctl -u loading.service -f
```

### 彩色输出不工作

```bash
# 禁用彩色输出
sudo sed -i 's/ENABLE_COLOR=true/ENABLE_COLOR=false/' /etc/systemd/system/loading.service
sudo systemctl daemon-reload
sudo systemctl restart loading.service
```

## 优势

- **超轻量级** - 相比 Kiosk 节省 99% 内存
- **快速启动** - 启动时间 <1 秒
- **零依赖** - 纯 Bash 实现，无外部依赖
- **易于定制** - 支持自定义配置
- **可靠稳定** - 完整的错误处理和日志

## 适用场景

✅ **适合使用：**
- 资源受限的嵌入式设备
- 需要快速启动的场景
- 需要显示设备信息
- 简单的启动画面

❌ **不适合使用：**
- 需要完整 Web 应用
- 需要复杂交互
- 需要实时数据更新
- 需要图形显示

## 屏蔽启动日志

启动时会显示系统日志？查看 `HIDE_LOGS.md` 了解如何屏蔽。

**快速方案：**

```bash
# 屏蔽内核日志
sudo bash scripts/hide-logs.sh

# 禁用 TTY 登录
sudo bash scripts/disable-getty.sh

# 重启系统
sudo reboot
```

详见 `HIDE_LOGS.md`。

## 许可证

MIT License
