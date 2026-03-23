// homeclaw-mgr — HomeClaw 系统依赖管理工具
//
// 命令：
//   install   [--image-file <f>] [--image-dir <d>]  安装所有系统依赖
//   uninstall                                         卸载系统依赖（保留 OpenSSH）
//   versions  [--json]                               显示所有服务已安装版本
//   latest    [--json]                               查询各服务最新可用版本
//   upgrade   [服务名...]                             升级指定服务（默认全部）
//
// 构建：
//   go build -o homeclaw-mgr .
//
// 使用：
//   sudo ./homeclaw-mgr install
//   sudo ./homeclaw-mgr uninstall
//   ./homeclaw-mgr versions
//   sudo ./homeclaw-mgr latest
//   sudo ./homeclaw-mgr upgrade docker nginx
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ============================================================
// 颜色 / 日志
// ============================================================

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
)

type logLevel string

const (
	lvlInfo    logLevel = "INFO"
	lvlSuccess logLevel = "SUCCESS"
	lvlWarn    logLevel = "WARN"
	lvlError   logLevel = "ERROR"
)

func logf(level logLevel, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	ts := time.Now().Format("2006-01-02 15:04:05")
	color := colorBlue
	switch level {
	case lvlSuccess:
		color = colorGreen
	case lvlWarn:
		color = colorYellow
	case lvlError:
		color = colorRed
	}
	fmt.Fprintf(os.Stderr, "%s%s [%s] %s%s\n", color, ts, level, msg, colorReset)
}

// ============================================================
// 服务定义
// ============================================================

// Service 描述一个受管理的系统服务 / apt 包。
type Service struct {
	Name             string   // 显示名称
	Pkg              string   // APT 包名（空字符串表示非 apt 管理的服务）
	Binary           string   // 可执行文件名（用于检测是否可用）
	VersionArgs      []string // 获取版本号的命令参数
	Protected        bool     // 受保护：禁止卸载（如 openssh-server）
	ConfirmUninstall bool     // 卸载前需要用户确认
	NonApt           bool     // 非 APT 包（通过 BinaryPath 文件是否存在判断安装状态）
	BinaryPath       string   // NonApt=true 时用于判断是否已安装的完整路径
}

// managedServices 是本工具管理的全部服务列表，顺序即安装/版本展示顺序。
var managedServices = []Service{
	// Docker 生态
	{Name: "docker",             Pkg: "docker-ce",             Binary: "docker",    VersionArgs: []string{"--version"}},
	{Name: "docker-cli",         Pkg: "docker-ce-cli",         Binary: ""},
	{Name: "containerd",         Pkg: "containerd.io",         Binary: "containerd", VersionArgs: []string{"--version"}},
	{Name: "docker-buildx",      Pkg: "docker-buildx-plugin",  Binary: ""},
	{Name: "docker-compose",     Pkg: "docker-compose-plugin", Binary: "docker",    VersionArgs: []string{"compose", "version"}},
	// Web 服务
	{Name: "nginx",              Pkg: "nginx",                 Binary: "nginx",     VersionArgs: []string{"-v"}},
	// 系统工具
	{Name: "net-tools",          Pkg: "net-tools",             Binary: "ifconfig"},
	{Name: "unzip",              Pkg: "unzip",                 Binary: "unzip",     VersionArgs: []string{"-v"}},
	{Name: "lsof",               Pkg: "lsof",                  Binary: "lsof"},
	{Name: "jq",                 Pkg: "jq",                    Binary: "jq",        VersionArgs: []string{"--version"}, ConfirmUninstall: true},
	{Name: "curl",               Pkg: "curl",                  Binary: "curl",      VersionArgs: []string{"--version"}, ConfirmUninstall: true},
	{Name: "wget",               Pkg: "wget",                  Binary: "wget",      VersionArgs: []string{"--version"}, ConfirmUninstall: true},
	{Name: "lsb-release",        Pkg: "lsb-release",           Binary: "lsb_release", VersionArgs: []string{"-a"}, ConfirmUninstall: true},
	// 系统安全 / 网络（受保护）
	{Name: "openssh-server",     Pkg: "openssh-server",        Binary: "sshd",      VersionArgs: []string{"-V"}, Protected: true},
	{Name: "gnupg",              Pkg: "gnupg",                 Binary: "gpg",       VersionArgs: []string{"--version"}, Protected: true},
	{Name: "ca-certificates",    Pkg: "ca-certificates",       Binary: "",          Protected: true},
	// 时间同步
	{Name: "systemd-timesyncd",  Pkg: "systemd-timesyncd",     Binary: ""},
	// 启动画面（非 apt 包，嵌入安装）
	{Name: "loading", NonApt: true, BinaryPath: "/usr/local/bin/show-loading"},
}

// ============================================================
// 辅助：执行命令
// ============================================================

// runOutput 执行命令并返回合并输出（stdout+stderr），忽略退出码。
func runOutput(name string, args ...string) string {
	out, _ := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out))
}

// runQuiet 执行命令，成功返回 nil，失败返回 error（不打印输出）。
func runQuiet(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

// runVisible 执行命令，将 stdout/stderr 直接输出到终端。
func runVisible(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runShell 通过 bash -c 执行 shell 脚本片段（stdout/stderr 直达终端）。
func runShell(script string) error {
	cmd := exec.Command("bash", "-c", script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// ============================================================
// 辅助：apt / dpkg
// ============================================================

// isInstalled 判断 apt 包是否已安装。
func isInstalled(pkg string) bool {
	out := runOutput("dpkg-query", "-W", "-f=${db:Status-Abbrev}", pkg)
	return strings.HasPrefix(out, "ii")
}

// pkgInstalledVersion 返回已安装版本，未安装返回空串。
func pkgInstalledVersion(pkg string) string {
	if !isInstalled(pkg) {
		return ""
	}
	return runOutput("dpkg-query", "-W", "-f=${Version}", pkg)
}

// pkgCandidateVersion 从 apt-cache 返回候选（最新可用）版本。
func pkgCandidateVersion(pkg string) string {
	out := runOutput("apt-cache", "policy", pkg)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Candidate:") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "Candidate:"))
			if v == "(none)" {
				return ""
			}
			return v
		}
	}
	return ""
}

// waitDpkg 等待 dpkg/apt 锁释放，最多等 120 秒。
func waitDpkg() error {
	lockFiles := []string{
		"/var/lib/dpkg/lock-frontend",
		"/var/lib/dpkg/lock",
		"/var/cache/apt/archives/lock",
	}
	const maxWait = 120
	waited := 0
	for {
		locked := false
		for _, f := range lockFiles {
			if _, err := os.Stat(f); err == nil {
				if out := runOutput("fuser", f); out != "" {
					locked = true
					break
				}
			}
		}
		if !locked {
			return nil
		}
		if waited >= maxWait {
			return fmt.Errorf("等待 dpkg/apt 锁超时（%ds）", maxWait)
		}
		if waited == 0 {
			logf(lvlWarn, "dpkg/apt 被占用，等待释放...")
		}
		time.Sleep(5 * time.Second)
		waited += 5
	}
}

// aptUpdate 执行 apt-get update。
func aptUpdate() error {
	if err := waitDpkg(); err != nil {
		return err
	}
	return runVisible("apt-get", "update", "-qq")
}

// aptInstall 安装指定包（非交互模式，保留现有配置文件）。
func aptInstall(pkgs []string) error {
	if err := waitDpkg(); err != nil {
		return err
	}
	args := append([]string{
		"install", "-y", "-qq",
		"-o", "Dpkg::Options::=--force-confdef",
		"-o", "Dpkg::Options::=--force-confold",
	}, pkgs...)
	cmd := exec.Command("apt-get", args...)
	cmd.Env = append(os.Environ(),
		"DEBIAN_FRONTEND=noninteractive",
		"UCF_FORCE_CONFOLD=1",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// aptPurge 卸载指定包（purge）。
func aptPurge(pkgs []string) error {
	if err := waitDpkg(); err != nil {
		return err
	}
	args := append([]string{
		"purge", "-y", "-qq",
		"-o", "Dpkg::Options::=--force-confdef",
		"-o", "Dpkg::Options::=--force-confold",
	}, pkgs...)
	cmd := exec.Command("apt-get", args...)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ============================================================
// 辅助：通用
// ============================================================

// checkRoot 检查是否以 root 执行，否则退出。
func checkRoot() {
	if os.Geteuid() != 0 {
		logf(lvlError, "必须使用 root 权限执行（sudo）")
		os.Exit(1)
	}
}

// autoYes 为 true 时，confirm() 自动返回 true（供 API / --yes 模式使用）。
var autoYes bool

// confirm 在终端打印 [y/N] 提示，返回用户是否确认。
func confirm(msg string) bool {
	if autoYes {
		logf(lvlInfo, "[AUTO-YES] %s", msg)
		return true
	}
	fmt.Printf("%s[CONFIRM] %s [y/N] %s", colorYellow, msg, colorReset)
	sc := bufio.NewScanner(os.Stdin)
	sc.Scan()
	return strings.ToLower(strings.TrimSpace(sc.Text())) == "y"
}

// binaryExists 检查可执行文件是否在 PATH 中。
func binaryExists(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// binaryVersion 运行指定命令并返回第一行非空输出（用于展示版本）。
func binaryVersion(svc Service) string {
	if svc.Binary == "" || len(svc.VersionArgs) == 0 {
		return ""
	}
	if !binaryExists(svc.Binary) {
		return ""
	}
	out := runOutput(svc.Binary, svc.VersionArgs...)
	for _, line := range strings.Split(out, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// serviceInstalledVersion 返回服务的已安装版本字符串。
// 对于非 APT 包（NonApt=true），通过检查 BinaryPath 文件是否存在来判断，
// 返回固定版本 "1.0.0"；未安装返回空串。
func serviceInstalledVersion(svc Service) string {
	if svc.NonApt {
		if _, err := os.Stat(svc.BinaryPath); err == nil {
			return "1.0.0"
		}
		return ""
	}
	return pkgInstalledVersion(svc.Pkg)
}

// serviceCandidateVersion 返回服务的最新可用版本。
// 非 APT 包返回空串（无 apt 候选版本）。
func serviceCandidateVersion(svc Service) string {
	if svc.NonApt {
		return ""
	}
	return pkgCandidateVersion(svc.Pkg)
}

// ============================================================
// loading 启动画面服务（嵌入内容，自包含安装）
// ============================================================

// showLoadingScript 是 show-loading 的完整 shell 脚本内容（嵌入到二进制中）。
const showLoadingScript = `#!/bin/bash

# LinknLink Lightweight Text Display Script
# Zero dependencies, pure Bash implementation

set -e

# Configuration
TTY_DEVICE="${TTY_DEVICE:-/dev/tty1}"
REFRESH_INTERVAL="${REFRESH_INTERVAL:-5}"
ENABLE_COLOR="${ENABLE_COLOR:-true}"

# Color Definitions
if [ "$ENABLE_COLOR" = "true" ]; then
    RESET='\033[0m'
    BOLD='\033[1m'
    CYAN='\033[36m'
    GREEN='\033[32m'
else
    RESET=''
    BOLD=''
    CYAN=''
    GREEN=''
fi

log() {
    :
}

get_ip() {
    hostname -I | awk '{print $1}' | grep -oE '^[0-9.]+$' || echo "No IP"
}

get_hostname() {
    hostname
}

get_time() {
    date '+%Y-%m-%d %H:%M:%S'
}

generate_display() {
    local hostname="$1"
    local ip="$2"
    local time="$3"

    clear

    echo -e "${CYAN}╔════════════════════════════════════════════════════════════════════════════════╗${RESET}"
    echo -e "${CYAN}║${RESET}                                                                              ${CYAN}║${RESET}"
    echo -e "${CYAN}║${RESET}  ██╗     ██╗███╗   ██╗██╗  ██╗███╗   ██╗██╗     ██╗███╗   ██╗██╗  ██╗    ${CYAN}║${RESET}"
    echo -e "${CYAN}║${RESET}  ██║     ██║████╗  ██║██║ ██╔╝████╗  ██║██║     ██║████╗  ██║██║ ██╔╝    ${CYAN}║${RESET}"
    echo -e "${CYAN}║${RESET}  ██║     ██║██╔██╗ ██║█████╔╝ ██╔██╗ ██║██║     ██║██╔██╗ ██║█████╔╝     ${CYAN}║${RESET}"
    echo -e "${CYAN}║${RESET}  ██║     ██║██║╚██╗██║██╔═██╗ ██║╚██╗██║██║     ██║██║╚██╗██║██╔═██╗     ${CYAN}║${RESET}"
    echo -e "${CYAN}║${RESET}  ███████╗██║██║ ╚████║██║  ██╗██║ ╚████║███████╗██║██║ ╚████║██║  ██╗    ${CYAN}║${RESET}"
    echo -e "${CYAN}║${RESET}  ╚══════╝╚═╝╚═╝  ╚═══╝╚═╝  ╚═╝╚═╝  ╚═══╝╚══════╝╚═╝╚═╝  ╚═══╝╚═╝  ╚═╝    ${CYAN}║${RESET}"
    echo -e "${CYAN}║${RESET}                                                                              ${CYAN}║${RESET}"
    echo -e "${CYAN}║${RESET}                        ${BOLD}LinknLink HomeClaw${RESET}                     ${CYAN}║${RESET}"
    echo -e "${CYAN}║${RESET}                                                                              ${CYAN}║${RESET}"
    echo -e "${CYAN}╠════════════════════════════════════════════════════════════════════════════════╣${RESET}"
    echo -e "${CYAN}║${RESET}                                                                              ${CYAN}║${RESET}"
    echo -e "${CYAN}║${RESET}  ${GREEN}HomePage URL${RESET}: ${BOLD}http://$ip${RESET}$(printf '%*s' $((55 - ${#ip})) '')${CYAN}║${RESET}"
    echo -e "${CYAN}║${RESET}  ${GREEN}OpenClaw URL${RESET}: ${BOLD}https://$ip${RESET}$(printf '%*s' $((53 - ${#ip})) '')${CYAN}║${RESET}"
    echo -e "${CYAN}║${RESET}  ${GREEN}Last Update${RESET}: ${BOLD}$time${RESET}$(printf '%*s' $((62 - ${#time})) '')${CYAN}║${RESET}"
    echo -e "${CYAN}║${RESET}                                                                              ${CYAN}║${RESET}"
    echo -e "${CYAN}╚════════════════════════════════════════════════════════════════════════════════╝${RESET}"
}

display_to_tty() {
    local content="$1"
    if [ ! -e "$TTY_DEVICE" ]; then
        return 1
    fi
    echo "$content" > "$TTY_DEVICE" 2>/dev/null || return 1
}

trap '' INT TERM QUIT HUP

main() {
    local last_ip=""
    while true; do
        local current_ip=$(get_ip 2>/dev/null || echo "No IP")
        local current_hostname=$(get_hostname 2>/dev/null || echo "unknown")
        local current_time=$(get_time 2>/dev/null || echo "")
        if [ "$current_ip" != "$last_ip" ]; then
            local display=$(generate_display "$current_hostname" "$current_ip" "$current_time")
            display_to_tty "$display" 2>/dev/null || true
            last_ip="$current_ip"
        fi
        sleep "$REFRESH_INTERVAL"
    done
}

if [ "$1" = "--generate-only" ]; then
    generate_display "$(get_hostname)" "$(get_ip)" "$(get_time)" 2>/dev/null
    exit 0
fi

main 2>/dev/null &
wait
`

// loadingServiceUnit 是 loading.service 的 systemd 单元内容。
const loadingServiceUnit = `[Unit]
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

Environment="TTY_DEVICE=/dev/tty1"
Environment="REFRESH_INTERVAL=5"
Environment="ENABLE_COLOR=true"

[Install]
WantedBy=multi-user.target
`

const (
	loadingBinPath     = "/usr/local/bin/show-loading"
	loadingServicePath = "/etc/systemd/system/loading.service"
	loadingLogPath     = "/var/log/loading.log"
)

// installLoading 将 show-loading 脚本和 systemd 单元文件写入系统并启用服务。
func installLoading() error {
	logf(lvlInfo, "===== 安装 Loading 启动画面服务 =====")

	// 写入 show-loading 脚本
	if err := os.WriteFile(loadingBinPath, []byte(showLoadingScript), 0755); err != nil {
		return fmt.Errorf("写入 show-loading 脚本失败: %w", err)
	}
	logf(lvlInfo, "show-loading 脚本已写入：%s", loadingBinPath)

	// 写入 systemd 单元文件
	if err := os.WriteFile(loadingServicePath, []byte(loadingServiceUnit), 0644); err != nil {
		return fmt.Errorf("写入 loading.service 失败: %w", err)
	}
	logf(lvlInfo, "loading.service 已写入：%s", loadingServicePath)

	// 创建日志文件
	f, err := os.OpenFile(loadingLogPath, os.O_CREATE|os.O_APPEND, 0644)
	if err == nil {
		f.Close()
	}

	// 重载 systemd 并启用服务
	runQuiet("systemctl", "daemon-reload")
	if err := runQuiet("systemctl", "enable", "loading.service"); err != nil {
		return fmt.Errorf("启用 loading.service 失败: %w", err)
	}

	// 屏蔽内核日志，防止启动日志冲掉画面
	logf(lvlInfo, "屏蔽内核日志（kernel.printk）...")
	runQuiet("sysctl", "-w", "kernel.printk=0 0 0 0")
	sysctlConf := "/etc/sysctl.conf"
	if data, err := os.ReadFile(sysctlConf); err == nil {
		content := string(data)
		if !strings.Contains(content, "kernel.printk") {
			f2, err2 := os.OpenFile(sysctlConf, os.O_APPEND|os.O_WRONLY, 0644)
			if err2 == nil {
				fmt.Fprintln(f2, "kernel.printk = 0 0 0 0")
				f2.Close()
			}
		}
	}
	runQuiet("sysctl", "-p")

	// 禁用 getty@tty1，让 loading 独占 tty1
	logf(lvlInfo, "禁用 getty@tty1.service，让 loading 独占 TTY1...")
	runQuiet("systemctl", "mask", "getty@tty1.service")
	runQuiet("systemctl", "stop", "getty@tty1.service")

	runQuiet("systemctl", "restart", "loading.service")

	logf(lvlSuccess, "Loading 启动画面服务安装完成（开机自启已启用，TTY1 独占）")
	return nil
}

// uninstallLoading 停止并卸载 loading 启动画面服务，删除相关文件，恢复 getty 和内核日志。
func uninstallLoading() {
	logf(lvlInfo, "===== 卸载 Loading 启动画面服务 =====")

	runQuiet("systemctl", "stop", "loading.service")
	runQuiet("systemctl", "disable", "loading.service")

	for _, path := range []string{loadingServicePath, loadingBinPath, loadingLogPath} {
		if _, err := os.Stat(path); err == nil {
			os.Remove(path)
			logf(lvlInfo, "已删除：%s", path)
		}
	}

	runQuiet("systemctl", "daemon-reload")

	// 恢复 getty@tty1
	logf(lvlInfo, "恢复 getty@tty1.service...")
	runQuiet("systemctl", "unmask", "getty@tty1.service")
	runQuiet("systemctl", "start", "getty@tty1.service")

	// 移除 kernel.printk 覆盖（恢复系统默认）
	sysctlConf := "/etc/sysctl.conf"
	if data, err := os.ReadFile(sysctlConf); err == nil {
		var lines []string
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "kernel.printk") {
				lines = append(lines, line)
			}
		}
		os.WriteFile(sysctlConf, []byte(strings.Join(lines, "\n")), 0644)
	}
	runQuiet("sysctl", "-p")

	logf(lvlSuccess, "Loading 启动画面服务已卸载，TTY1 已恢复为登录终端")
}

// ============================================================
// dashboard 服务安装
// ============================================================

const (
	dashboardBinPath     = "/usr/local/bin/dashboard"
	dashboardInstallDir  = "/usr/local/share/homeclaw/dashboard"
	dashboardServicePath = "/etc/systemd/system/dashboard.service"
)

const dashboardServiceUnit = `[Unit]
Description=HomeClaw Dashboard
After=network.target

[Service]
Type=simple
WorkingDirectory=/usr/local/share/homeclaw/dashboard
ExecStart=/usr/local/bin/dashboard
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
`

// installDashboard 将 dashboard 可执行文件和 HTML 资源部署到系统目录，
// 注册并启用 systemd 服务，使其在 ARM64 Linux 上可以开机自启。
func installDashboard() error {
	logf(lvlInfo, "===== 安装 Dashboard 服务 =====")

	// 在 homeclaw-mgr 同级或上级目录中寻找 dashboard 可执行文件
	selfPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("获取当前可执行路径失败: %w", err)
	}
	selfDir := filepath.Dir(selfPath)

	candidates := []string{
		filepath.Join(selfDir, "..", "dashboard", "dashboard-service"),
		filepath.Join(selfDir, "..", "dashboard", "dashboard-linux-arm64"),
		filepath.Join(selfDir, "dashboard-service"),
		filepath.Join(selfDir, "dashboard-linux-arm64"),
	}
	dashboardSrc := ""
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			dashboardSrc = c
			break
		}
	}
	if dashboardSrc == "" {
		return fmt.Errorf("未找到 dashboard 可执行文件（已查找路径：%s/../dashboard/）", selfDir)
	}
	logf(lvlInfo, "找到 dashboard 可执行文件：%s", dashboardSrc)

	// 创建安装目录
	if err := os.MkdirAll(dashboardInstallDir, 0755); err != nil {
		return fmt.Errorf("创建安装目录失败: %w", err)
	}

	// 复制可执行文件
	srcData, err := os.ReadFile(dashboardSrc)
	if err != nil {
		return fmt.Errorf("读取 dashboard 二进制失败: %w", err)
	}
	if err := os.WriteFile(dashboardBinPath, srcData, 0755); err != nil {
		return fmt.Errorf("写入 dashboard 二进制失败: %w", err)
	}
	logf(lvlInfo, "dashboard 已安装至：%s", dashboardBinPath)

	// 复制 HTML 资源文件
	dashboardSrcDir := filepath.Join(selfDir, "..", "dashboard")
	htmlFiles := []string{"home.html", "login.html", "billing.html", "topup.html", "usage.html"}
	for _, html := range htmlFiles {
		srcFile := filepath.Join(dashboardSrcDir, html)
		if data, err := os.ReadFile(srcFile); err == nil {
			dest := filepath.Join(dashboardInstallDir, html)
			if err2 := os.WriteFile(dest, data, 0644); err2 == nil {
				logf(lvlInfo, "已复制：%s → %s", html, dest)
			}
		}
	}

	// 写入 systemd 服务文件
	if err := os.WriteFile(dashboardServicePath, []byte(dashboardServiceUnit), 0644); err != nil {
		return fmt.Errorf("写入 dashboard.service 失败: %w", err)
	}
	logf(lvlInfo, "dashboard.service 已写入：%s", dashboardServicePath)

	// 重载 systemd 并启用+启动服务
	runQuiet("systemctl", "daemon-reload")
	if err := runQuiet("systemctl", "enable", "--now", "dashboard.service"); err != nil {
		return fmt.Errorf("启用 dashboard.service 失败: %w", err)
	}

	logf(lvlSuccess, "Dashboard 服务安装完成（端口 9090，开机自启已启用）")
	return nil
}

// uninstallDashboard 停止并卸载 dashboard 服务，删除相关文件。
func uninstallDashboard() {
	logf(lvlInfo, "===== 卸载 Dashboard 服务 =====")
	runQuiet("systemctl", "stop", "dashboard.service")
	runQuiet("systemctl", "disable", "dashboard.service")
	for _, path := range []string{dashboardServicePath, dashboardBinPath} {
		if _, err := os.Stat(path); err == nil {
			os.Remove(path)
			logf(lvlInfo, "已删除：%s", path)
		}
	}
	os.RemoveAll(dashboardInstallDir)
	runQuiet("systemctl", "daemon-reload")
	logf(lvlSuccess, "Dashboard 服务已卸载")
}

// ============================================================
// install 子命令
// ============================================================

func detectDistro() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "unknown"
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "ID=") {
			return strings.Trim(strings.TrimPrefix(line, "ID="), `"'`)
		}
	}
	return "unknown"
}

func detectCodename() string {
	data, _ := os.ReadFile("/etc/os-release")
	m := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		if idx := strings.IndexByte(line, '='); idx > 0 {
			m[line[:idx]] = strings.Trim(line[idx+1:], `"'`)
		}
	}
	for _, k := range []string{"VERSION_CODENAME", "UBUNTU_CODENAME"} {
		if v, ok := m[k]; ok && v != "" {
			return v
		}
	}
	switch m["VERSION_ID"] {
	case "20.04":
		return "focal"
	case "22.04":
		return "jammy"
	case "24.04":
		return "noble"
	}
	return "jammy"
}

// setTimezone 将系统时区设为 Etc/UTC，并先备份原始 /etc/localtime。
func setTimezone() error {
	logf(lvlInfo, "设置系统时区为 Etc/UTC")
	// 备份（供 uninstall 时恢复）
	if _, err := os.Stat("/etc/localtime"); err == nil {
		if _, err2 := os.Stat("/etc/localtime.bak"); os.IsNotExist(err2) {
			if data, err3 := os.ReadFile("/etc/localtime"); err3 == nil {
				os.WriteFile("/etc/localtime.bak", data, 0644)
			}
		}
	}
	if err := runQuiet("timedatectl", "set-timezone", "Etc/UTC"); err == nil {
		logf(lvlSuccess, "时区已设置为 Etc/UTC（timedatectl）")
		return nil
	}
	// fallback: 符号链接
	os.Remove("/etc/localtime")
	if err := os.Symlink("/usr/share/zoneinfo/Etc/UTC", "/etc/localtime"); err != nil {
		return err
	}
	return os.WriteFile("/etc/timezone", []byte("Etc/UTC\n"), 0644)
}

// enableTimeSync 安装并配置 systemd-timesyncd（使用阿里云 NTP）。
func enableTimeSync() error {
	logf(lvlInfo, "配置自动时间同步（systemd-timesyncd）")
	if !isInstalled("systemd-timesyncd") {
		if err := aptInstall([]string{"systemd-timesyncd"}); err != nil {
			return fmt.Errorf("systemd-timesyncd 安装失败: %w", err)
		}
	}
	// 备份原配置
	conf := "/etc/systemd/timesyncd.conf"
	if _, err := os.Stat(conf); err == nil {
		if _, err2 := os.Stat(conf + ".bak"); os.IsNotExist(err2) {
			data, _ := os.ReadFile(conf)
			os.WriteFile(conf+".bak", data, 0644)
		}
	}
	newConf := `[Time]
NTP=ntp.aliyun.com ntp1.aliyun.com ntp2.aliyun.com ntp3.aliyun.com
FallbackNTP=ntp.ubuntu.com 0.ubuntu.pool.ntp.org 1.ubuntu.pool.ntp.org
RootDistanceMaxSec=5
`
	if err := os.WriteFile(conf, []byte(newConf), 0644); err != nil {
		return err
	}
	runQuiet("systemctl", "unmask", "systemd-timesyncd")
	runQuiet("systemctl", "enable", "--now", "systemd-timesyncd")
	runQuiet("timedatectl", "set-ntp", "true")
	runQuiet("systemctl", "restart", "systemd-timesyncd")
	logf(lvlSuccess, "时间同步服务已启动：%s", time.Now().Format(time.RFC3339))
	return nil
}

// installBasePackages 安装系统基础软件包。
func installBasePackages() error {
	logf(lvlInfo, "安装系统基础软件包")
	pkgs := []string{
		"net-tools", "openssh-server", "unzip", "jq", "wget",
		"gnupg", "ca-certificates", "lsof", "curl", "lsb-release", "nginx",
	}
	if err := aptUpdate(); err != nil {
		return fmt.Errorf("apt-get update 失败: %w", err)
	}
	if err := aptInstall(pkgs); err != nil {
		return fmt.Errorf("基础包安装失败: %w", err)
	}
	logf(lvlSuccess, "基础软件包安装完成")
	return nil
}

// installDockerViaApt 通过 apt 安装 Docker（优先阿里云，失败时降级官方源）。
func installDockerViaApt(codename string) error {
	logf(lvlInfo, "通过 apt 安装 Docker（codename=%s）", codename)
	aptInstall([]string{"ca-certificates", "curl", "gnupg", "lsb-release"})

	// GPG 密钥
	os.MkdirAll("/etc/apt/keyrings", 0755)
	gpgOK := false
	for _, url := range []string{
		"https://mirrors.aliyun.com/docker-ce/linux/ubuntu/gpg",
		"https://download.docker.com/linux/ubuntu/gpg",
	} {
		if err := runVisible("curl", "-fsSL", url, "-o", "/etc/apt/keyrings/docker.asc"); err == nil {
			gpgOK = true
			break
		}
		logf(lvlWarn, "GPG 源 %s 失败，尝试备用", url)
	}
	if !gpgOK {
		return fmt.Errorf("GPG 密钥获取失败")
	}
	os.Chmod("/etc/apt/keyrings/docker.asc", 0644)

	arch := runOutput("dpkg", "--print-architecture")
	for _, repoURL := range []string{
		"https://mirrors.aliyun.com/docker-ce/linux/ubuntu",
		"https://download.docker.com/linux/ubuntu",
	} {
		line := fmt.Sprintf("deb [arch=%s signed-by=/etc/apt/keyrings/docker.asc] %s %s stable\n",
			arch, repoURL, codename)
		os.WriteFile("/etc/apt/sources.list.d/docker.list", []byte(line), 0644)
		if err := aptUpdate(); err == nil {
			break
		}
		logf(lvlWarn, "仓库 %s 更新失败，尝试备用源", repoURL)
	}

	pkgs := []string{
		"docker-ce", "docker-ce-cli", "containerd.io",
		"docker-buildx-plugin", "docker-compose-plugin",
	}
	if err := aptInstall(pkgs); err != nil {
		return fmt.Errorf("Docker apt 安装失败: %w", err)
	}
	logf(lvlSuccess, "Docker（apt）安装完成")
	return nil
}

// installDockerViaScript 使用官方脚本安装 Docker（备选方案）。
func installDockerViaScript() error {
	logf(lvlInfo, "尝试官方安装脚本（--mirror Aliyun）")
	return runShell("curl -fsSL https://get.docker.com | bash -s docker --mirror Aliyun")
}

// installDocker 安装 Docker 及 Docker Compose v2。
func installDocker() error {
	if binaryExists("docker") {
		logf(lvlInfo, "Docker 已安装：%s", runOutput("docker", "--version"))
		runQuiet("systemctl", "enable", "--now", "docker")
		return nil
	}
	logf(lvlWarn, "未检测到 Docker，开始安装...")
	distro := detectDistro()
	codename := detectCodename()
	logf(lvlInfo, "系统：%s / %s", distro, codename)

	var err error
	if distro == "ubuntu" || distro == "debian" {
		err = installDockerViaApt(codename)
	}
	if err != nil {
		logf(lvlWarn, "apt 方式失败（%v），尝试官方脚本", err)
		err = installDockerViaScript()
	}
	if err != nil {
		return fmt.Errorf("Docker 安装失败: %w", err)
	}

	runQuiet("systemctl", "enable", "--now", "docker")
	time.Sleep(2 * time.Second)
	logf(lvlSuccess, "Docker 已启动：%s", runOutput("docker", "--version"))
	if v := runOutput("docker", "compose", "version"); v != "" {
		logf(lvlSuccess, "Docker Compose v2：%s", v)
	}
	return nil
}

func loadSingleImage(path string) {
	logf(lvlInfo, "加载镜像：%s", path)
	if err := runVisible("docker", "load", "-i", path); err != nil {
		logf(lvlError, "镜像加载失败：%s - %v", path, err)
	} else {
		logf(lvlSuccess, "镜像加载成功：%s", path)
	}
}

func loadImagesFromDir(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		logf(lvlError, "读取目录失败：%s", dir)
		return
	}
	found := false
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, ".tar") || strings.HasSuffix(n, ".tar.gz") {
			loadSingleImage(filepath.Join(dir, n))
			found = true
		}
	}
	if !found {
		logf(lvlInfo, "目录中未找到镜像文件（.tar/.tar.gz）：%s", dir)
	}
}

func cmdInstall(args []string) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	imageFile := fs.String("image-file", "", "单个本地镜像文件（.tar 或 .tar.gz）")
	imageDir := fs.String("image-dir", "", "本地镜像目录（加载所有 .tar/.tar.gz）")
	fs.Usage = func() {
		fmt.Println("用法: homeclaw-mgr install [--image-file <f>] [--image-dir <d>]")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	logf(lvlInfo, "==========================================")
	logf(lvlInfo, "   HomeClaw 依赖安装")
	logf(lvlInfo, "==========================================")
	checkRoot()

	logf(lvlInfo, "步骤 1/7：设置系统时区...")
	if err := setTimezone(); err != nil {
		logf(lvlWarn, "时区设置失败（非致命）：%v", err)
	}

	logf(lvlInfo, "步骤 2/7：配置时间同步...")
	if err := enableTimeSync(); err != nil {
		logf(lvlWarn, "时间同步配置失败（非致命）：%v", err)
	}

	logf(lvlInfo, "步骤 3/7：安装 jq（前置依赖）...")
	if !isInstalled("jq") {
		if err := aptInstall([]string{"jq"}); err != nil {
			logf(lvlError, "jq 安装失败，终止")
			os.Exit(1)
		}
	}

	logf(lvlInfo, "步骤 4/7：安装系统基础软件包...")
	if err := installBasePackages(); err != nil {
		logf(lvlError, "基础包安装失败，终止：%v", err)
		os.Exit(1)
	}

	logf(lvlInfo, "步骤 5/7：安装 Docker 及 Docker Compose v2...")
	if err := installDocker(); err != nil {
		logf(lvlError, "Docker 安装失败，终止：%v", err)
		os.Exit(1)
	}

	logf(lvlInfo, "步骤 6/8：安装 Loading 启动画面服务...")
	if err := installLoading(); err != nil {
		logf(lvlWarn, "Loading 安装失败（非致命）：%v", err)
	}

	logf(lvlInfo, "步骤 7/8：安装 Dashboard 服务（ARM64 开机自启）...")
	if err := installDashboard(); err != nil {
		logf(lvlWarn, "Dashboard 安装失败（非致命）：%v", err)
	}

	logf(lvlInfo, "步骤 8/8：加载本地镜像...")
	switch {
	case *imageFile != "":
		loadSingleImage(*imageFile)
	case *imageDir != "":
		loadImagesFromDir(*imageDir)
	default:
		logf(lvlInfo, "未指定 --image-file 或 --image-dir，跳过镜像加载")
	}

	logf(lvlSuccess, "==========================================")
	logf(lvlSuccess, "   所有依赖安装完成")
	logf(lvlSuccess, "   Docker:         %s", runOutput("docker", "--version"))
	logf(lvlSuccess, "   Docker Compose: %s", runOutput("docker", "compose", "version"))
	loadingStatus := "未安装"
	if _, err := os.Stat(loadingBinPath); err == nil {
		loadingStatus = "已安装"
	}
	logf(lvlSuccess, "   Loading 服务:   %s", loadingStatus)
	dashboardStatus := "未安装"
	if _, err := os.Stat(dashboardBinPath); err == nil {
		dashboardStatus = "已安装（端口 9090）"
	}
	logf(lvlSuccess, "   Dashboard 服务: %s", dashboardStatus)
	logf(lvlSuccess, "==========================================")
}

// ============================================================
// uninstall 子命令
// ============================================================

func uninstallDocker() {
	logf(lvlInfo, "===== 步骤 1/5：停止并卸载 Docker =====")

	if binaryExists("docker") && runQuiet("docker", "info") == nil {
		if running := runOutput("docker", "ps", "-q"); running != "" {
			logf(lvlWarn, "检测到运行中的容器，正在停止...")
			runShell("docker stop $(docker ps -q 2>/dev/null) 2>/dev/null || true")
		}
		if confirm("是否删除所有 Docker 容器、镜像、卷和网络？（不可恢复）") {
			runShell("docker rm -f $(docker ps -aq 2>/dev/null) 2>/dev/null || true")
			runShell("docker rmi -f $(docker images -q 2>/dev/null) 2>/dev/null || true")
			runShell("docker volume rm $(docker volume ls -q 2>/dev/null) 2>/dev/null || true")
			runVisible("docker", "network", "prune", "-f")
			logf(lvlSuccess, "Docker 资源已清理")
		}
	}

	for _, svc := range []string{"docker", "containerd"} {
		runQuiet("systemctl", "stop", svc)
		runQuiet("systemctl", "disable", svc)
	}

	candidatePkgs := []string{
		"docker-ce", "docker-ce-cli", "containerd.io",
		"docker-buildx-plugin", "docker-compose-plugin",
		"docker-ce-rootless-extras", "docker-compose",
		"docker.io", "docker-doc", "podman-docker",
	}
	var toRemove []string
	for _, pkg := range candidatePkgs {
		if isInstalled(pkg) {
			toRemove = append(toRemove, pkg)
		}
	}
	if len(toRemove) > 0 {
		logf(lvlInfo, "卸载 Docker 包：%s", strings.Join(toRemove, " "))
		if err := aptPurge(toRemove); err != nil {
			logf(lvlWarn, "部分 Docker 包卸载失败：%v", err)
		} else {
			logf(lvlSuccess, "Docker 包卸载完成")
		}
	} else {
		logf(lvlInfo, "未检测到已安装的 Docker 包，跳过")
	}

	if confirm("是否删除 Docker 数据目录？（/var/lib/docker 等，不可恢复）") {
		for _, d := range []string{
			"/var/lib/docker", "/var/lib/containerd",
			"/etc/docker", "/run/docker",
			"/run/docker.sock", "/run/containerd",
		} {
			if _, err := os.Stat(d); err == nil {
				os.RemoveAll(d)
				logf(lvlInfo, "已删除：%s", d)
			}
		}
		logf(lvlSuccess, "Docker 数据目录已清理")
	}

	for _, f := range []string{"/usr/bin/docker", "/usr/bin/dockerd", "/usr/bin/docker-compose"} {
		os.Remove(f)
	}
	runQuiet("groupdel", "docker")
	logf(lvlSuccess, "Docker 卸载完成")
}

func removeDockerRepo() {
	logf(lvlInfo, "===== 步骤 2/5：删除 Docker 仓库配置 =====")
	for _, f := range []string{
		"/etc/apt/sources.list.d/docker.list",
		"/etc/apt/keyrings/docker.asc",
		"/etc/apt/keyrings/docker.gpg",
		"/usr/share/keyrings/docker-archive-keyring.gpg",
	} {
		if _, err := os.Stat(f); err == nil {
			os.Remove(f)
			logf(lvlInfo, "已删除：%s", f)
		}
	}
	aptUpdate()
	logf(lvlSuccess, "Docker 仓库配置已清理")
}

func uninstallBasePackages() {
	logf(lvlInfo, "===== 步骤 3/5：卸载基础软件包 =====")

	// 直接卸载（不询问）
	var safeRemove []string
	for _, pkg := range []string{"net-tools", "unzip", "lsof", "nginx"} {
		if isInstalled(pkg) {
			safeRemove = append(safeRemove, pkg)
		}
	}
	if len(safeRemove) > 0 {
		logf(lvlInfo, "卸载：%s", strings.Join(safeRemove, " "))
		if err := aptPurge(safeRemove); err != nil {
			logf(lvlWarn, "部分软件包卸载失败：%v", err)
		}
	}

	// 需要确认才卸载
	var confirmRemove []string
	for _, pkg := range []string{"jq", "wget", "curl", "lsb-release", "systemd-timesyncd"} {
		if isInstalled(pkg) && confirm(fmt.Sprintf("是否卸载 %s？", pkg)) {
			confirmRemove = append(confirmRemove, pkg)
		}
	}
	if len(confirmRemove) > 0 {
		aptPurge(confirmRemove)
	}

	// 受保护包，永远不卸载
	logf(lvlInfo, "以下关键包已保留（不卸载）：openssh-server, gnupg, ca-certificates")

	waitDpkg()
	cmd := exec.Command("apt-get", "autoremove", "-y", "-qq")
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()
	runQuiet("apt-get", "clean")
	logf(lvlSuccess, "基础软件包卸载完成")
}

func restoreTimezone() {
	logf(lvlInfo, "===== 还原时区配置 =====")
	if _, err := os.Stat("/etc/localtime.bak"); err == nil {
		os.Rename("/etc/localtime.bak", "/etc/localtime")
		logf(lvlSuccess, "已从备份恢复 /etc/localtime")
	} else {
		logf(lvlInfo, "无时区备份，保持当前时区不变")
	}
}

func restoreTimeSync() {
	logf(lvlInfo, "===== 步骤 4/5：还原时间同步配置 =====")
	conf := "/etc/systemd/timesyncd.conf"
	bak := conf + ".bak"
	if _, err := os.Stat(bak); err == nil {
		os.Rename(bak, conf)
		logf(lvlSuccess, "已从备份恢复 %s", conf)
	} else {
		defaultConf := `# /etc/systemd/timesyncd.conf (restored to default)
[Time]
#NTP=
#FallbackNTP=ntp.ubuntu.com
#RootDistanceMaxSec=5
#PollIntervalMinSec=32
#PollIntervalMaxSec=2048
`
		os.WriteFile(conf, []byte(defaultConf), 0644)
		logf(lvlInfo, "已写回 timesyncd 默认配置")
	}
	runQuiet("systemctl", "restart", "systemd-timesyncd")
	logf(lvlSuccess, "时间同步配置已还原")
}

func restoreLogrotate() {
	logf(lvlInfo, "===== 步骤 5/5：还原 logrotate 配置 =====")
	overrideConf := "/etc/systemd/system/logrotate.timer.d/override.conf"
	if _, err := os.Stat(overrideConf); err == nil {
		os.Remove(overrideConf)
		os.Remove(filepath.Dir(overrideConf))
		logf(lvlInfo, "已删除 logrotate timer 覆盖配置")
	}
	bak := "/etc/logrotate.d/rsyslog.bak"
	if _, err := os.Stat(bak); err == nil {
		os.Rename(bak, "/etc/logrotate.d/rsyslog")
		logf(lvlInfo, "已从备份恢复 /etc/logrotate.d/rsyslog")
	}
	runQuiet("systemctl", "daemon-reload")
	runQuiet("systemctl", "restart", "logrotate.timer")
	logf(lvlSuccess, "logrotate 配置已还原")
}

func cmdUninstall(args []string) {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	yes := fs.Bool("yes", false, "跳过所有交互确认（API / 自动化模式）")
	fs.Usage = func() {
		fmt.Println("用法: homeclaw-mgr uninstall [--yes]")
		fs.PrintDefaults()
	}
	fs.Parse(args)
	if *yes {
		autoYes = true
	}

	logf(lvlInfo, "==========================================")
	logf(lvlInfo, "   HomeClaw 依赖卸载")
	logf(lvlInfo, "==========================================")
	logf(lvlWarn, "此操作将卸载 Docker 及相关软件包")
	logf(lvlWarn, "以下包将被保留：openssh-server, gnupg, ca-certificates")
	fmt.Println()
	checkRoot()

	if !confirm("确认开始卸载所有 HomeClaw 依赖？") {
		logf(lvlInfo, "用户取消，退出")
		return
	}

	uninstallDocker()
	removeDockerRepo()
	uninstallBasePackages()
	restoreTimezone()
	restoreTimeSync()
	restoreLogrotate()
	uninstallLoading()

	logf(lvlSuccess, "==========================================")
	logf(lvlSuccess, "   HomeClaw 依赖卸载完成")
	logf(lvlSuccess, "   建议重启系统：sudo reboot")
	logf(lvlSuccess, "==========================================")
}

// ============================================================
// versions 子命令
// ============================================================

// ServiceVersionInfo 是单个服务的版本信息，供 JSON 输出和表格展示使用。
type ServiceVersionInfo struct {
	Name          string `json:"name"`
	Package       string `json:"package"`
	Protected     bool   `json:"protected"`
	Installed     string `json:"installed_version"`
	BinaryVersion string `json:"binary_version,omitempty"`
	Status        string `json:"status"`
}

func cmdVersions(args []string) {
	fs := flag.NewFlagSet("versions", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "以 JSON 格式输出")
	fs.Bool("j", false, "以 JSON 格式输出（简写）")
	fs.Parse(args)
	if fs.Lookup("j").Value.String() == "true" {
		*jsonOut = true
	}

	var results []ServiceVersionInfo
	for _, svc := range managedServices {
		installed := serviceInstalledVersion(svc)
		binVer := binaryVersion(svc)
		status := "未安装"
		if installed != "" {
			status = "已安装"
		}
		results = append(results, ServiceVersionInfo{
			Name:          svc.Name,
			Package:       svc.Pkg,
			Protected:     svc.Protected,
			Installed:     installed,
			BinaryVersion: binVer,
			Status:        status,
		})
	}

	if *jsonOut {
		data, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(data))
		return
	}

	fmt.Printf("\n%s%-25s %-35s %-18s %-6s%s\n",
		colorBold, "服务名", "APT 包", "已安装版本", "保护", colorReset)
	fmt.Println(strings.Repeat("─", 90))
	for _, r := range results {
		ver := colorRed + "未安装" + colorReset
		if r.Installed != "" {
			ver = colorGreen + r.Installed + colorReset
		}
		prot := ""
		if r.Protected {
			prot = colorCyan + "✓" + colorReset
		}
		fmt.Printf("%-25s %-35s %-28s %-6s\n", r.Name, r.Package, ver, prot)
		if r.BinaryVersion != "" {
			fmt.Printf("  %s└ %s%s\n", colorCyan, r.BinaryVersion, colorReset)
		}
	}
	fmt.Println()
}

// ============================================================
// latest 子命令
// ============================================================

// ServiceLatestInfo 是单个服务的版本对比信息。
type ServiceLatestInfo struct {
	Name      string `json:"name"`
	Package   string `json:"package"`
	Installed string `json:"installed_version"`
	Latest    string `json:"latest_version"`
	UpToDate  bool   `json:"up_to_date"`
	Protected bool   `json:"protected"`
}

func cmdLatest(args []string) {
	fs := flag.NewFlagSet("latest", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "以 JSON 格式输出")
	noUpdate := fs.Bool("no-update", false, "跳过 apt-get update（更快但数据可能过期）")
	fs.Parse(args)

	if !*noUpdate {
		logf(lvlInfo, "正在更新软件源（apt-get update）...")
		if err := aptUpdate(); err != nil {
			logf(lvlWarn, "apt-get update 失败，版本信息可能不是最新：%v", err)
		}
	}

	var results []ServiceLatestInfo
	for _, svc := range managedServices {
		installed := serviceInstalledVersion(svc)
		latest := serviceCandidateVersion(svc)
		upToDate := installed != "" && latest != "" && installed == latest
		if svc.NonApt && installed != "" {
			upToDate = true // 非 apt 包，视为始终最新
		}
		results = append(results, ServiceLatestInfo{
			Name:      svc.Name,
			Package:   svc.Pkg,
			Installed: installed,
			Latest:    latest,
			UpToDate:  upToDate,
			Protected: svc.Protected,
		})
	}

	if *jsonOut {
		data, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(data))
		return
	}

	fmt.Printf("\n%s%-25s %-22s %-30s %-10s%s\n",
		colorBold, "服务名", "已安装版本", "最新可用版本", "状态", colorReset)
	fmt.Println(strings.Repeat("─", 92))
	for _, r := range results {
		status := colorYellow + "未安装" + colorReset
		if r.Installed != "" {
			if r.UpToDate {
				status = colorGreen + "最新" + colorReset
			} else if r.Latest != "" {
				status = colorYellow + "可升级" + colorReset
			}
		}
		fmt.Printf("%-25s %-22s %-30s %s\n", r.Name, r.Installed, r.Latest, status)
	}
	fmt.Println()
}

// ============================================================
// upgrade 子命令
// ============================================================

func cmdUpgrade(args []string) {
	fs := flag.NewFlagSet("upgrade", flag.ExitOnError)
	yes := fs.Bool("yes", false, "跳过升级确认提示")
	fs.Usage = func() {
		fmt.Println("用法: homeclaw-mgr upgrade [--yes] [服务名...]")
		fmt.Println("  不指定服务名时升级所有可升级的非保护包。")
		fs.PrintDefaults()
	}
	fs.Parse(args)
	targets := fs.Args()

	checkRoot()
	logf(lvlInfo, "更新软件源...")
	if err := aptUpdate(); err != nil {
		logf(lvlWarn, "apt-get update 失败，继续尝试升级")
	}

	// 构建"包名 → Service"索引，方便按名称查找
	byName := make(map[string]Service)
	byPkg := make(map[string]Service)
	for _, svc := range managedServices {
		byName[svc.Name] = svc
		byPkg[svc.Pkg] = svc
	}

	type upgrade struct {
		svc   Service
		oldVer string
		newVer string
	}

	var pending []upgrade

	if len(targets) == 0 {
		// 升级所有可升级的非保护、非 NonApt 包
		for _, svc := range managedServices {
			if svc.Protected || svc.NonApt {
				continue
			}
			installed := pkgInstalledVersion(svc.Pkg)
			if installed == "" {
				continue
			}
			latest := pkgCandidateVersion(svc.Pkg)
			if latest != "" && installed != latest {
				pending = append(pending, upgrade{svc, installed, latest})
			}
		}
	} else {
		// 只升级指定的包
		for _, t := range targets {
			svc, ok := byName[t]
			if !ok {
				svc, ok = byPkg[t]
			}
			if !ok {
				logf(lvlWarn, "未知服务：%s，跳过", t)
				continue
			}
			if svc.Protected {
				logf(lvlWarn, "%s 是受保护的包，禁止升级", svc.Name)
				continue
			}
			if svc.NonApt {
				logf(lvlWarn, "%s 不是 APT 包，跳过升级", svc.Name)
				continue
			}
			installed := pkgInstalledVersion(svc.Pkg)
			if installed == "" {
				logf(lvlWarn, "%s 未安装，跳过", svc.Name)
				continue
			}
			latest := pkgCandidateVersion(svc.Pkg)
			if latest == "" || installed == latest {
				logf(lvlInfo, "%s 已是最新版本（%s）", svc.Name, installed)
				continue
			}
			pending = append(pending, upgrade{svc, installed, latest})
		}
	}

	if len(pending) == 0 {
		logf(lvlSuccess, "所有指定服务已是最新版本，无需升级")
		return
	}

	fmt.Printf("\n%s待升级列表：%s\n", colorBold, colorReset)
	var pkgsToUpgrade []string
	for _, u := range pending {
		fmt.Printf("  %-25s %s%s%s → %s%s%s\n",
			u.svc.Name,
			colorYellow, u.oldVer, colorReset,
			colorGreen, u.newVer, colorReset)
		pkgsToUpgrade = append(pkgsToUpgrade, u.svc.Pkg)
	}
	fmt.Println()

	if !*yes && !confirm(fmt.Sprintf("确认升级以上 %d 个包？", len(pkgsToUpgrade))) {
		logf(lvlInfo, "用户取消升级")
		return
	}

	if err := waitDpkg(); err != nil {
		logf(lvlError, "%v", err)
		os.Exit(1)
	}

	aptArgs := append([]string{
		"install", "--only-upgrade", "-y",
		"-o", "Dpkg::Options::=--force-confdef",
		"-o", "Dpkg::Options::=--force-confold",
	}, pkgsToUpgrade...)
	cmd := exec.Command("apt-get", aptArgs...)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		logf(lvlError, "升级失败：%v", err)
		os.Exit(1)
	}

	logf(lvlSuccess, "升级完成，最新版本：")
	for _, u := range pending {
		ver := pkgInstalledVersion(u.svc.Pkg)
		logf(lvlSuccess, "  %-25s %s", u.svc.Name, ver)
	}
}

// ============================================================
// 帮助
// ============================================================

func usage() {
	fmt.Printf(`%shomeclaw-mgr%s — HomeClaw 系统依赖管理工具

%s用法:%s
  homeclaw-mgr <命令> [选项]

%s命令:%s
  install              安装所有系统依赖（Docker、Nginx 等）
  uninstall [--yes]    卸载系统依赖（openssh-server / gnupg / ca-certificates 受保护）
  versions [--json]    显示所有服务的已安装版本
  latest   [--json]    查询各服务在 apt 中的最新可用版本
  upgrade  [服务名...]  升级指定服务；不指定服务名则升级全部可升级包
  api      [--port p] [--host h]  启动 HTTP REST API 服务器（默认 127.0.0.1:8080）

%s示例:%s
  sudo ./homeclaw-mgr install
  sudo ./homeclaw-mgr install --image-dir /opt/images
  sudo ./homeclaw-mgr uninstall
  sudo ./homeclaw-mgr uninstall --yes
  ./homeclaw-mgr versions
  ./homeclaw-mgr versions --json
  sudo ./homeclaw-mgr latest
  sudo ./homeclaw-mgr upgrade
  sudo ./homeclaw-mgr upgrade docker nginx
  sudo ./homeclaw-mgr upgrade --yes docker
  sudo ./homeclaw-mgr api
  sudo ./homeclaw-mgr api --port 9090 --host 0.0.0.0

%s构建:%s
  go build -o homeclaw-mgr .
  # 静态编译（Linux）：
  CGO_ENABLED=0 GOOS=linux go build -o homeclaw-mgr .

`, colorBold, colorReset,
		colorCyan, colorReset,
		colorCyan, colorReset,
		colorCyan, colorReset,
		colorCyan, colorReset)
}

// ============================================================
// main
// ============================================================

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	rest := os.Args[2:]

	switch cmd {
	case "install":
		cmdInstall(rest)
	case "uninstall":
		cmdUninstall(rest)
	case "versions":
		cmdVersions(rest)
	case "latest":
		cmdLatest(rest)
	case "upgrade":
		cmdUpgrade(rest)
	case "api":
		cmdAPI(rest)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知命令：%s\n\n", cmd)
		usage()
		os.Exit(1)
	}
}
