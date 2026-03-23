#!/bin/bash
# =============================================================================
# HomeClaw 依赖卸载脚本
# 卸载内容：Docker、Docker Compose、系统基础软件包、时间同步配置、仓库配置
# 对应安装脚本：install_deps.sh
# =============================================================================

set -uo pipefail

# ===================== 颜色日志 =====================
log() {
    local level="$1"; local message="$2"
    local ts; ts=$(date "+%Y-%m-%d %H:%M:%S")
    local color="\033[34m"
    case $level in
        SUCCESS) color="\033[32m" ;;
        ERROR)   color="\033[31m" ;;
        WARN)    color="\033[33m" ;;
    esac
    echo -e "${color}${ts} [${level}] ${message}\033[0m" >&2
}

# ===================== 权限检查 =====================
check_root() {
    if [[ $(id -u) -ne 0 ]]; then
        log "ERROR" "必须使用 root 权限执行本脚本（sudo -i）"
        exit 1
    fi
}

# ===================== 等待 dpkg/apt 锁 =====================
wait_for_dpkg_lock() {
    local max_wait=120
    local waited=0
    local check_interval=5
    local lock_files=(
        "/var/lib/dpkg/lock-frontend"
        "/var/lib/dpkg/lock"
        "/var/cache/apt/archives/lock"
    )
    while true; do
        local locked=false
        for lock_file in "${lock_files[@]}"; do
            if [[ -f "$lock_file" ]] && fuser "$lock_file" >/dev/null 2>&1; then
                locked=true; break
            fi
        done
        [[ "$locked" == "false" ]] && return 0
        if [[ $waited -ge $max_wait ]]; then
            log "ERROR" "等待 dpkg/apt 锁超时（${max_wait}s）"
            return 1
        fi
        [[ $waited -eq 0 ]] && log "WARN" "dpkg/apt 被占用，等待释放..."
        sleep $check_interval
        ((waited += check_interval))
    done
}

# ===================== 交互确认 =====================
confirm() {
    local msg="$1"
    read -r -p "$(echo -e "\033[33m[CONFIRM] ${msg} [y/N] \033[0m")" ans
    [[ "${ans,,}" == "y" ]]
}

# =============================================================================
# 卸载步骤
# =============================================================================

# ----- 1. 停止并卸载 Docker -----
uninstall_docker() {
    log "INFO" "===== 步骤 1/6：卸载 Docker ====="

    # 停止所有运行中的容器
    if command -v docker &>/dev/null && docker info &>/dev/null 2>&1; then
        local running
        running=$(docker ps -q 2>/dev/null || true)
        if [[ -n "$running" ]]; then
            log "WARN" "检测到运行中的容器，正在停止..."
            docker stop $running 2>/dev/null || true
        fi

        # 可选：删除所有容器、镜像、卷、网络
        if confirm "是否删除所有 Docker 容器、镜像、卷和网络？（不可恢复）"; then
            log "INFO" "清理 Docker 资源..."
            docker rm -f $(docker ps -aq 2>/dev/null) 2>/dev/null || true
            docker rmi -f $(docker images -q 2>/dev/null)  2>/dev/null || true
            docker volume rm $(docker volume ls -q 2>/dev/null) 2>/dev/null || true
            docker network prune -f 2>/dev/null || true
            log "SUCCESS" "Docker 资源已清理"
        else
            log "INFO" "跳过 Docker 资源清理"
        fi
    else
        log "INFO" "Docker 未运行或未安装，跳过资源清理"
    fi

    # 停止并禁用服务
    for svc in docker containerd; do
        if systemctl list-units --full -all 2>/dev/null | grep -q "${svc}.service"; then
            log "INFO" "停止服务：$svc"
            systemctl stop    "$svc" 2>/dev/null || true
            systemctl disable "$svc" 2>/dev/null || true
        fi
    done

    wait_for_dpkg_lock || return 1

    # 卸载所有 Docker 相关包
    local docker_pkgs=(
        docker-ce
        docker-ce-cli
        containerd.io
        docker-buildx-plugin
        docker-compose-plugin
        docker-ce-rootless-extras
        docker-compose           # v1 兼容包
        docker.io                # Ubuntu 默认源版本（如有）
        docker-doc
        podman-docker
    )

    local installed_docker=()
    for pkg in "${docker_pkgs[@]}"; do
        dpkg -s "$pkg" &>/dev/null && installed_docker+=("$pkg")
    done

    if [[ ${#installed_docker[@]} -gt 0 ]]; then
        log "INFO" "卸载 Docker 包：${installed_docker[*]}"
        DEBIAN_FRONTEND=noninteractive apt-get purge -y -qq "${installed_docker[@]}" \
            -o Dpkg::Options::="--force-confdef" \
            -o Dpkg::Options::="--force-confold" \
        && log "SUCCESS" "Docker 包卸载完成" \
        || log "WARN" "部分 Docker 包卸载失败"
    else
        log "INFO" "未检测到已安装的 Docker 包，跳过"
    fi

    # 删除 Docker 数据目录
    if confirm "是否删除 Docker 数据目录（/var/lib/docker、/var/lib/containerd）？（不可恢复）"; then
        local docker_dirs=(
            /var/lib/docker
            /var/lib/containerd
            /etc/docker
            /run/docker
            /run/docker.sock
            /run/containerd
        )
        for d in "${docker_dirs[@]}"; do
            if [[ -e "$d" ]]; then
                rm -rf "$d"
                log "INFO" "已删除：$d"
            fi
        done
        log "SUCCESS" "Docker 数据目录已清理"
    else
        log "INFO" "跳过 Docker 数据目录删除"
    fi

    # 删除 Docker socket 和二进制残留
    rm -f /usr/bin/docker /usr/bin/dockerd /usr/bin/docker-compose 2>/dev/null || true
    # 删除 docker 用户组（可选，不强制）
    if getent group docker &>/dev/null; then
        groupdel docker 2>/dev/null || log "WARN" "docker 用户组删除失败（可能仍有用户属于该组）"
    fi

    log "SUCCESS" "Docker 卸载完成"
}

# ----- 2. 删除 Docker apt 仓库配置 -----
remove_docker_repo() {
    log "INFO" "===== 步骤 2/6：删除 Docker 仓库配置 ====="

    local files=(
        /etc/apt/sources.list.d/docker.list
        /etc/apt/keyrings/docker.asc
        /etc/apt/keyrings/docker.gpg
        /usr/share/keyrings/docker-archive-keyring.gpg
    )
    for f in "${files[@]}"; do
        if [[ -f "$f" ]]; then
            rm -f "$f"
            log "INFO" "已删除：$f"
        fi
    done

    wait_for_dpkg_lock || return 1
    apt-get update -qq 2>/dev/null || true
    log "SUCCESS" "Docker 仓库配置已清理"
}

# ----- 3. 卸载基础软件包 -----
uninstall_base_packages() {
    log "INFO" "===== 步骤 3/6：卸载基础软件包 ====="

    # 以下包由 install_deps.sh 安装，但部分（gnupg、ca-certificates、curl）
    # 是系统关键依赖，默认跳过，除非用户明确确认
    local safe_to_remove=(
        net-tools
        unzip
        lsof
        nginx
    )

    local confirm_before_remove=(
        jq
        wget
        curl
        lsb-release
    )

    local critical=(
        gnupg             # 系统 GPG 基础组件，不卸载
        ca-certificates   # 系统 HTTPS 根证书，不卸载
        openssh-server    # 远程登录依赖，不卸载
    )

    # 安全卸载组
    local to_remove=()
    for pkg in "${safe_to_remove[@]}"; do
        dpkg -s "$pkg" &>/dev/null && to_remove+=("$pkg")
    done

    if [[ ${#to_remove[@]} -gt 0 ]]; then
        log "INFO" "卸载以下软件包：${to_remove[*]}"
        wait_for_dpkg_lock || return 1
        DEBIAN_FRONTEND=noninteractive apt-get purge -y -qq "${to_remove[@]}" \
            -o Dpkg::Options::="--force-confdef" \
            -o Dpkg::Options::="--force-confold" \
        && log "SUCCESS" "软件包已卸载：${to_remove[*]}" \
        || log "WARN" "部分软件包卸载失败"
    fi

    # 需二次确认的组
    local confirm_remove=()
    for pkg in "${confirm_before_remove[@]}"; do
        if dpkg -s "$pkg" &>/dev/null; then
            if confirm "是否卸载 ${pkg}？"; then
                confirm_remove+=("$pkg")
            else
                log "INFO" "跳过：$pkg"
            fi
        fi
    done

    if [[ ${#confirm_remove[@]} -gt 0 ]]; then
        wait_for_dpkg_lock || return 1
        DEBIAN_FRONTEND=noninteractive apt-get purge -y -qq "${confirm_remove[@]}" \
            -o Dpkg::Options::="--force-confdef" \
            -o Dpkg::Options::="--force-confold" \
        && log "SUCCESS" "已卸载：${confirm_remove[*]}" \
        || log "WARN" "部分包卸载失败"
    fi

    # 提示保留的关键包
    log "INFO" "以下关键系统包已保留（不卸载）：${critical[*]}"

    # 清理孤立依赖
    wait_for_dpkg_lock || return 1
    DEBIAN_FRONTEND=noninteractive apt-get autoremove -y -qq \
        -o Dpkg::Options::="--force-confdef" \
        -o Dpkg::Options::="--force-confold" 2>/dev/null || true
    apt-get clean 2>/dev/null || true

    log "SUCCESS" "基础软件包卸载完成"
}

# ----- 4. 恢复时区配置 -----
restore_timezone() {
    log "INFO" "===== 步骤 4/6：恢复时区配置 ====="

    # 恢复备份（如有）
    if [[ -f /etc/localtime.bak ]]; then
        mv /etc/localtime.bak /etc/localtime
        log "SUCCESS" "已从备份恢复 /etc/localtime"
    else
        log "INFO" "无时区备份，保持当前设置（Etc/UTC）不变"
    fi
}

# ----- 5. 还原时间同步配置 -----
restore_time_sync() {
    log "INFO" "===== 步骤 5/6：还原时间同步配置 ====="

    local conf="/etc/systemd/timesyncd.conf"
    local bak="${conf}.bak"

    if [[ -f "$bak" ]]; then
        mv "$bak" "$conf"
        log "SUCCESS" "已从备份恢复 $conf"
    else
        # 写回系统默认配置
        cat > "$conf" <<EOF
# /etc/systemd/timesyncd.conf (restored to default)
[Time]
#NTP=
#FallbackNTP=ntp.ubuntu.com
#RootDistanceMaxSec=5
#PollIntervalMinSec=32
#PollIntervalMaxSec=2048
EOF
        log "INFO" "已写回 timesyncd 默认配置"
    fi

    systemctl restart systemd-timesyncd 2>/dev/null || true
    log "SUCCESS" "时间同步配置已还原"
}

# ----- 6. 清理 logrotate 定时器覆盖 -----
restore_logrotate() {
    log "INFO" "===== 步骤 6/6：还原 logrotate 配置 ====="

    local override_dir="/etc/systemd/system/logrotate.timer.d"
    local override_conf="${override_dir}/override.conf"

    if [[ -f "$override_conf" ]]; then
        rm -f "$override_conf"
        log "INFO" "已删除 logrotate timer 覆盖配置：$override_conf"
        # 删除空目录
        rmdir "$override_dir" 2>/dev/null || true
    fi

    # 恢复 rsyslog logrotate 备份
    local rsyslog_bak="/etc/logrotate.d/rsyslog.bak"
    if [[ -f "$rsyslog_bak" ]]; then
        mv "$rsyslog_bak" /etc/logrotate.d/rsyslog
        log "INFO" "已从备份恢复 /etc/logrotate.d/rsyslog"
    fi

    systemctl daemon-reload 2>/dev/null || true
    systemctl restart logrotate.timer 2>/dev/null || true
    log "SUCCESS" "logrotate 配置已还原"
}

# =============================================================================
# 主流程
# =============================================================================
main() {
    log "INFO" "=========================================="
    log "INFO" "   HomeClaw 依赖卸载脚本"
    log "INFO" "=========================================="
    log "WARN" "此操作将卸载 Docker 及相关系统软件包"
    log "WARN" "请确保已备份重要数据和容器"
    echo ""

    check_root

    if ! confirm "确认要开始卸载所有 HomeClaw 依赖吗？"; then
        log "INFO" "用户取消，退出"
        exit 0
    fi

    uninstall_docker
    remove_docker_repo
    uninstall_base_packages
    restore_timezone
    restore_time_sync
    restore_logrotate

    log "SUCCESS" "=========================================="
    log "SUCCESS" "   HomeClaw 依赖卸载完成"
    log "SUCCESS" "   建议重启系统以确保所有变更生效"
    log "SUCCESS" "   sudo reboot"
    log "SUCCESS" "=========================================="
}

main "$@"