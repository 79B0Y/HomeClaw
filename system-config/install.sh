#!/bin/bash
# =============================================================================
# HomeClaw 依赖安装脚本（独立版）
# 提取自: install.sh / install_system_services.sh / common.sh
# 包含：系统依赖、Docker、Docker Compose 安装
# =============================================================================

set -euo pipefail

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
    local max_wait=300
    local waited=0
    local check_interval=5
    local lock_files=(
        "/var/lib/dpkg/lock-frontend"
        "/var/lib/dpkg/lock"
        "/var/cache/apt/archives/lock"
    )

    while true; do
        local locked=false
        local lock_info=""
        for lock_file in "${lock_files[@]}"; do
            if [[ -f "$lock_file" ]] && fuser "$lock_file" >/dev/null 2>&1; then
                locked=true
                local pid; pid=$(fuser "$lock_file" 2>/dev/null | awk '{print $1}')
                local proc; proc=$(ps -p "$pid" -o comm= 2>/dev/null || echo "未知进程")
                lock_info="$lock_file (PID: $pid, 进程: $proc)"
                break
            fi
        done

        [[ "$locked" == "false" ]] && return 0

        if [[ $waited -ge $max_wait ]]; then
            log "ERROR" "等待 dpkg/apt 锁超时（${max_wait}s）: $lock_info"
            return 1
        fi

        [[ $waited -eq 0 ]] && log "WARN" "dpkg/apt 被占用，等待释放: $lock_info"
        sleep $check_interval
        ((waited += check_interval))
        [[ $((waited % 30)) -eq 0 ]] && log "INFO" "仍在等待锁释放... ($waited/${max_wait}s)"
    done
}

# ===================== 带重试的命令执行 =====================
retry_command() {
    local cmd="$1"; local desc="$2"
    local max_retries=3; local attempt=1
    while [[ $attempt -le $max_retries ]]; do
        log "INFO" "${desc}（尝试 ${attempt}/${max_retries}）"
        if eval "$cmd"; then return 0; fi
        ((attempt++))
        sleep $((attempt * 2))
    done
    log "ERROR" "${desc} 超出最大重试次数"
    return 1
}

# ===================== 系统发行版 & 代号检测 =====================
detect_distro() {
    [[ -f /etc/os-release ]] && { . /etc/os-release; echo "$ID"; return; }
    [[ -f /etc/debian_version ]] && echo "debian" && return
    [[ -f /etc/redhat-release ]] && echo "rhel" && return
    echo "unknown"
}

detect_codename() {
    if [[ -f /etc/os-release ]]; then
        . /etc/os-release
        if [[ -n "${VERSION_CODENAME:-}" ]]; then echo "$VERSION_CODENAME"; return; fi
        if [[ -n "${UBUNTU_CODENAME:-}" ]];  then echo "$UBUNTU_CODENAME";  return; fi
        case "${VERSION_ID:-}" in
            20.04) echo "focal"  ;;
            22.04) echo "jammy"  ;;
            24.04) echo "noble"  ;;
            *)     echo "jammy"  ;;
        esac
    else
        echo "jammy"
    fi
}

# ===================== 网络连通性检查 =====================
check_network() {
    local url="$1"; local timeout="${2:-5}"
    curl -fsSL --connect-timeout "$timeout" --max-time "$timeout" "$url" &>/dev/null
}

# ===================== 系统时区设置 =====================
set_timezone() {
    log "INFO" "设置系统时区为 Etc/UTC"
    if command -v timedatectl >/dev/null 2>&1 && timedatectl set-timezone Etc/UTC; then
        log "SUCCESS" "时区已设置为 Etc/UTC（timedatectl）"
        return 0
    fi
    if [[ -f /usr/share/zoneinfo/Etc/UTC ]]; then
        ln -sf /usr/share/zoneinfo/Etc/UTC /etc/localtime
        echo "Etc/UTC" > /etc/timezone
        log "SUCCESS" "时区已设置为 Etc/UTC（符号链接）"
        return 0
    fi
    log "ERROR" "时区设置失败"
    return 1
}

# ===================== 系统时间同步 =====================
enable_time_sync() {
    log "INFO" "配置自动时间同步（systemd-timesyncd）"

    if ! dpkg -s systemd-timesyncd >/dev/null 2>&1; then
        wait_for_dpkg_lock || return 1
        DEBIAN_FRONTEND=noninteractive UCF_FORCE_CONFOLD=1 \
            apt-get install -y -qq systemd-timesyncd \
            -o Dpkg::Options::="--force-confdef" \
            -o Dpkg::Options::="--force-confold" || {
            log "ERROR" "systemd-timesyncd 安装失败"; return 1
        }
    fi

    cat > /etc/systemd/timesyncd.conf <<EOF
[Time]
NTP=ntp.aliyun.com ntp1.aliyun.com ntp2.aliyun.com ntp3.aliyun.com
FallbackNTP=ntp.ubuntu.com 0.ubuntu.pool.ntp.org 1.ubuntu.pool.ntp.org
RootDistanceMaxSec=5
EOF

    systemctl unmask systemd-timesyncd >/dev/null 2>&1 || true
    systemctl enable --now systemd-timesyncd
    timedatectl set-ntp true >/dev/null 2>&1 || true
    systemctl restart systemd-timesyncd

    log "SUCCESS" "时间同步服务已启动：$(date)"
}

# ===================== 基础软件包安装 =====================
# 来源：install_system_services.sh → install_base_packages()
# 注：mysql-server、mysql-client、mosquitto 已在原脚本中注释，保持一致
install_base_packages() {
    log "INFO" "===== 安装系统基础软件包 ====="

    wait_for_dpkg_lock || return 1
    retry_command "apt-get update -qq" "更新软件源"

    local packages=(
        "net-tools"        # 网络诊断工具（ifconfig 等）
        "openssh-server"   # SSH 服务
        "unzip"            # 解压工具
        "jq"               # JSON 解析
        "wget"             # 文件下载
        "gnupg"            # GPG 密钥管理（apt 仓库签名验证）
        "ca-certificates"  # CA 根证书（HTTPS）
        "lsof"             # 端口/文件占用查看
        "curl"             # HTTP 客户端（Docker 安装依赖）
        "lsb-release"      # 发行版信息（Docker 仓库配置依赖）
        "nginx"            # Web 服务器 / 反向代理
    )

    wait_for_dpkg_lock || return 1

    log "INFO" "安装: ${packages[*]}"
    DEBIAN_FRONTEND=noninteractive UCF_FORCE_CONFOLD=1 \
        apt-get install -y -qq "${packages[@]}" --no-install-recommends \
        -o Dpkg::Options::="--force-confdef" \
        -o Dpkg::Options::="--force-confold" \
    && log "SUCCESS" "基础软件包安装完成" \
    || { log "ERROR" "基础软件包安装失败"; return 1; }
}

# ===================== Docker 安装 - apt 方式（阿里云镜像）=====================
# 来源：common.sh → install_docker_via_apt()
install_docker_via_apt() {
    local codename="$1"
    log "INFO" "通过 apt 安装 Docker（阿里云镜像源）..."

    # 前置依赖
    DEBIAN_FRONTEND=noninteractive UCF_FORCE_CONFOLD=1 \
        apt-get install -y -qq ca-certificates curl gnupg lsb-release \
        -o Dpkg::Options::="--force-confdef" \
        -o Dpkg::Options::="--force-confold" || true

    # GPG 密钥
    install -m 0755 -d /etc/apt/keyrings 2>/dev/null || mkdir -p /etc/apt/keyrings
    local gpg_url="https://mirrors.aliyun.com/docker-ce/linux/ubuntu/gpg"
    if ! curl -fsSL "$gpg_url" -o /etc/apt/keyrings/docker.asc 2>/dev/null; then
        log "WARN" "阿里云 GPG 失败，改用官方源"
        curl -fsSL "https://download.docker.com/linux/ubuntu/gpg" \
            -o /etc/apt/keyrings/docker.asc || { log "ERROR" "GPG 密钥获取失败"; return 1; }
    fi
    chmod a+r /etc/apt/keyrings/docker.asc

    # 仓库配置
    local arch; arch=$(dpkg --print-architecture)
    local repo_url="https://mirrors.aliyun.com/docker-ce/linux/ubuntu"
    echo "deb [arch=${arch} signed-by=/etc/apt/keyrings/docker.asc] ${repo_url} ${codename} stable" \
        > /etc/apt/sources.list.d/docker.list

    # 更新并安装
    if ! DEBIAN_FRONTEND=noninteractive apt-get update -qq; then
        log "WARN" "阿里云仓库更新失败，改用官方源"
        repo_url="https://download.docker.com/linux/ubuntu"
        echo "deb [arch=${arch} signed-by=/etc/apt/keyrings/docker.asc] ${repo_url} ${codename} stable" \
            > /etc/apt/sources.list.d/docker.list
        DEBIAN_FRONTEND=noninteractive apt-get update -qq \
            || { log "ERROR" "仓库更新失败"; return 1; }
    fi

    wait_for_dpkg_lock || return 1

    DEBIAN_FRONTEND=noninteractive UCF_FORCE_CONFOLD=1 \
        apt-get install -y -qq \
        docker-ce \
        docker-ce-cli \
        containerd.io \
        docker-buildx-plugin \
        docker-compose-plugin \
        -o Dpkg::Options::="--force-confdef" \
        -o Dpkg::Options::="--force-confold" \
    && { log "SUCCESS" "Docker（apt）安装完成"; return 0; } \
    || { log "ERROR" "Docker（apt）安装失败"; return 1; }
}

# ===================== Docker 安装 - 官方脚本（备选）=====================
# 来源：common.sh → install_docker_via_script()
install_docker_via_script() {
    log "INFO" "尝试官方安装脚本（--mirror Aliyun）..."
    if check_network "https://get.docker.com" 10; then
        curl -fsSL https://get.docker.com | bash -s docker --mirror Aliyun \
            && { log "SUCCESS" "Docker（官方脚本）安装完成"; return 0; }
    fi
    log "ERROR" "官方脚本安装失败"
    return 1
}

# ===================== Docker 主安装入口 =====================
# 来源：common.sh → install_docker()
# 安装内容：docker-ce, docker-ce-cli, containerd.io,
#            docker-buildx-plugin, docker-compose-plugin（即 docker compose v2）
install_docker() {
    if command -v docker &>/dev/null; then
        log "INFO" "Docker 已安装：$(docker --version)"
        # 确保服务运行
        systemctl enable --now docker 2>/dev/null || true
        return 0
    fi

    log "WARN" "未检测到 Docker，开始安装..."
    local distro; distro=$(detect_distro)
    local codename; codename=$(detect_codename)
    log "INFO" "系统：$distro / $codename"

    local ok=false
    if [[ "$distro" == "ubuntu" || "$distro" == "debian" ]]; then
        install_docker_via_apt "$codename" && ok=true \
            || { log "WARN" "apt 方式失败，尝试官方脚本..."; install_docker_via_script && ok=true; }
    else
        install_docker_via_script && ok=true
    fi

    [[ "$ok" == "false" ]] && { log "ERROR" "Docker 安装失败"; return 1; }

    # 启动服务
    log "INFO" "启动 Docker 服务..."
    systemctl enable --now docker 2>/dev/null || service docker start 2>/dev/null || true
    sleep 2

    if systemctl is-active --quiet docker || pgrep dockerd &>/dev/null; then
        log "SUCCESS" "Docker 服务已启动：$(docker --version)"
    else
        log "WARN" "Docker 服务可能未正常启动，请手动检查: systemctl status docker"
    fi

    # 验证 docker compose v2（随 docker-compose-plugin 一起安装）
    if docker compose version &>/dev/null; then
        log "SUCCESS" "Docker Compose v2 可用：$(docker compose version)"
    else
        log "WARN" "docker compose 插件未检测到，可能需要单独安装"
    fi
}

# ===================== jq 检查（其他脚本的前置依赖）=====================
check_jq() {
    if ! command -v jq &>/dev/null; then
        log "WARN" "jq 未安装，正在安装..."
        retry_command \
            "DEBIAN_FRONTEND=noninteractive UCF_FORCE_CONFOLD=1 apt-get update && \
             DEBIAN_FRONTEND=noninteractive UCF_FORCE_CONFOLD=1 apt-get install -y jq \
             -o Dpkg::Options::=\"--force-confdef\" -o Dpkg::Options::=\"--force-confold\"" \
            "安装 jq" \
        && log "SUCCESS" "jq 安装完成" \
        || { log "ERROR" "jq 安装失败"; return 1; }
    fi
}

# ===================== 本地镜像加载 =====================
# 来源：load_image.sh + common.sh → load_image()
# 支持 -f <单个文件> 或 -d <目录> 两种方式，互斥
load_image() {
    local image_file="$1"

    if [[ ! -f "$image_file" ]]; then
        log "ERROR" "镜像文件不存在：$image_file"
        return 1
    fi

    # 从 manifest.json 提取镜像名
    local image_info
    image_info=$(tar -xOf "$image_file" manifest.json 2>/dev/null \
        | jq -r '.[0].RepoTags[0]' 2>/dev/null) || {
        log "ERROR" "无法读取镜像信息：$image_file"
        return 1
    }

    if [[ -z "$image_info" || "$image_info" == "null" ]]; then
        log "ERROR" "镜像文件格式错误或缺少标签：$image_file"
        return 1
    fi

    # 已存在则跳过
    if docker image inspect "$image_info" &>/dev/null; then
        log "INFO" "镜像已存在，跳过加载：$image_info"
        return 0
    fi

    log "INFO" "正在加载镜像：$image_info"
    if docker load -i "$image_file" &>/dev/null; then
        log "SUCCESS" "镜像加载成功：$image_info"
        return 0
    else
        log "ERROR" "镜像加载失败：$image_file"
        return 1
    fi
}

load_images() {
    local image_file=""
    local image_dir=""

    # 解析 -f / -d 参数
    local OPTIND=1
    while getopts "f:d:" opt; do
        case $opt in
            f) image_file="$OPTARG" ;;
            d) image_dir="$OPTARG"  ;;
            *) log "ERROR" "用法：load_images [-f <镜像文件>] [-d <镜像目录>]"; return 1 ;;
        esac
    done

    # -f 和 -d 互斥
    if [[ -n "$image_file" && -n "$image_dir" ]]; then
        log "ERROR" "-f 和 -d 不能同时使用，请二选一"
        return 1
    fi

    if [[ -n "$image_file" ]]; then
        # 单文件模式
        [[ -f "$image_file" ]] || { log "ERROR" "镜像文件不存在：$image_file"; return 1; }
        log "INFO" "加载镜像文件：$image_file"
        load_image "$image_file"

    elif [[ -n "$image_dir" ]]; then
        # 目录模式：遍历所有 .tar / .tar.gz
        [[ -d "$image_dir" ]] || { log "ERROR" "镜像目录不存在：$image_dir"; return 1; }
        log "INFO" "从目录加载镜像：$image_dir"
        local found=0
        while IFS= read -r -d '' f; do
            log "INFO" "加载镜像文件：$f"
            load_image "$f"
            found=1
        done < <(find "$image_dir" -maxdepth 1 \( -name "*.tar" -o -name "*.tar.gz" \) -print0 2>/dev/null)
        if [[ $found -eq 0 ]]; then
            log "INFO" "目录中未找到镜像文件（.tar/.tar.gz）：$image_dir"
        fi

    else
        log "INFO" "未指定 -f 或 -d，跳过镜像加载"
    fi
}

# =============================================================================
# 主流程
# =============================================================================
usage() {
    echo "用法: bash install_deps.sh [-f <镜像文件>] [-d <镜像目录>]"
    echo ""
    echo "  -f <file>   加载单个本地镜像文件（.tar 或 .tar.gz）"
    echo "  -d <dir>    加载目录下所有本地镜像文件（.tar / .tar.gz）"
    echo "  （-f 和 -d 不可同时使用，均可省略）"
}

main() {
    # 解析顶层参数（传给 load_images）
    local image_args=()
    while [[ $# -gt 0 ]]; do
        case "$1" in
            -f) image_args+=("-f" "$2"); shift 2 ;;
            -d) image_args+=("-d" "$2"); shift 2 ;;
            -h|--help) usage; exit 0 ;;
            *) log "ERROR" "未知参数：$1"; usage; exit 1 ;;
        esac
    done

    log "INFO" "=========================================="
    log "INFO" "   HomeClaw 依赖安装脚本（独立版）"
    log "INFO" "=========================================="

    check_root

    log "INFO" "步骤 1/6：设置系统时区..."
    set_timezone || log "WARN" "时区设置失败（非致命）"

    log "INFO" "步骤 2/6：配置时间同步..."
    enable_time_sync || log "WARN" "时间同步配置失败（非致命）"

    log "INFO" "步骤 3/6：检查并安装 jq..."
    check_jq || { log "ERROR" "jq 安装失败，终止"; exit 1; }

    log "INFO" "步骤 4/6：安装系统基础软件包..."
    install_base_packages || { log "ERROR" "基础包安装失败，终止"; exit 1; }

    log "INFO" "步骤 5/6：安装 Docker 及 Docker Compose..."
    install_docker || { log "ERROR" "Docker 安装失败，终止"; exit 1; }

    log "INFO" "步骤 6/6：加载本地镜像..."
    load_images "${image_args[@]+"${image_args[@]}"}"

    log "SUCCESS" "=========================================="
    log "SUCCESS" "   所有依赖安装完成"
    log "SUCCESS" "   Docker:         $(docker --version 2>/dev/null || echo '未知')"
    log "SUCCESS" "   Docker Compose: $(docker compose version 2>/dev/null || echo '未知')"
    log "SUCCESS" "=========================================="
}

main "$@"