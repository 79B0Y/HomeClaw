# 屏蔽启动日志配置指南

## 问题

启动时会显示大量系统日志，影响显示效果。

## 解决方案

### 方案 1：屏蔽内核日志（推荐）

编辑 `/boot/extlinux/extlinux.conf` 或 `/boot/grub/grub.cfg`：

#### 对于 extlinux（Rockchip 设备）

```bash
sudo nano /boot/extlinux/extlinux.conf
```

找到 `append` 行，添加以下参数：

```
quiet splash loglevel=0 console=tty3 rd.systemd.show_status=false
```

完整示例：

```
label Linux
  kernel /Image
  fdt /dtb
  append quiet splash loglevel=0 console=tty3 rd.systemd.show_status=false root=/dev/mmcblk0p2 rw
```

#### 对于 GRUB（x86_64 设备）

```bash
sudo nano /etc/default/grub
```

修改 `GRUB_CMDLINE_LINUX_DEFAULT` 行：

```
GRUB_CMDLINE_LINUX_DEFAULT="quiet splash loglevel=0 console=tty3 rd.systemd.show_status=false"
```

然后更新 GRUB：

```bash
sudo update-grub
```

### 方案 2：屏蔽 systemd 日志

编辑 `/etc/systemd/system/loading.service`：

```ini
[Service]
StandardOutput=null
StandardError=null
```

### 方案 3：重定向到虚拟终端

编辑 `/etc/systemd/system/loading.service`：

```ini
[Service]
StandardOutput=tty
StandardError=tty
TTYPath=/dev/tty3
```

### 方案 4：禁用 Plymouth 启动画面

```bash
sudo systemctl disable plymouth.service
```

## 完整配置步骤

### 1. 修改内核启动参数

```bash
# 对于 extlinux
sudo nano /boot/extlinux/extlinux.conf

# 或对于 GRUB
sudo nano /etc/default/grub
sudo update-grub
```

### 2. 修改 systemd 服务

```bash
sudo nano /etc/systemd/system/loading.service
```

添加以下配置：

```ini
[Service]
StandardOutput=null
StandardError=null
```

### 3. 重启系统

```bash
sudo reboot
```

## 参数说明

| 参数 | 说明 |
|------|------|
| `quiet` | 屏蔽大部分启动信息 |
| `splash` | 显示启动画面 |
| `loglevel=0` | 仅显示紧急信息 |
| `console=tty3` | 将日志输出到 tty3（不影响 tty1） |
| `rd.systemd.show_status=false` | 屏蔽 systemd 启动状态 |

## 验证配置

### 查看当前内核参数

```bash
cat /proc/cmdline
```

### 查看日志级别

```bash
cat /proc/sys/kernel/printk
```

### 手动设置日志级别

```bash
# 临时设置（重启后失效）
sudo dmesg -n 0

# 查看当前级别
dmesg -n
```

## 故障排除

### 如果屏幕完全黑屏

1. 移除 `loglevel=0` 参数
2. 改为 `loglevel=3`
3. 重启系统

### 如果看不到启动画面

1. 检查 Plymouth 是否启用：`sudo systemctl status plymouth.service`
2. 启用 Plymouth：`sudo systemctl enable plymouth.service`

### 如果日志仍然显示

1. 检查内核参数是否生效：`cat /proc/cmdline`
2. 检查 systemd 配置：`systemctl cat loading.service`
3. 查看日志：`sudo journalctl -u loading.service -f`

## 最终效果

配置完成后，启动时应该只显示：

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

✓ 服务运行中 (按 Ctrl+C 停止)
```

无任何系统日志干扰。
