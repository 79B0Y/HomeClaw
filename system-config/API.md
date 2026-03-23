# HomeClaw 系统管理工具 API 文档

## 概览

`homeclaw-mgr` 是 HomeClaw 的系统依赖管理工具，支持命令行和 HTTP REST API 两种使用方式。

- **服务地址**：`http://192.168.1.101:18080`
- **部署路径**：`/home/cat/homeclaw/system-config/`
- **目标平台**：ARM64 Linux（Ubuntu 20.04）
- **日志文件**：`/home/cat/homeclaw/system-config/api.log`

---

## 受保护的服务

以下服务永远不会被卸载或修改，保障服务器可远程访问：

| 服务 | 说明 |
|------|------|
| `openssh-server` | SSH 远程登录，禁止卸载 |
| `gnupg` | 系统 GPG 基础组件 |
| `ca-certificates` | HTTPS 根证书 |

---

## 接口列表

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/health` | 健康检查 |
| GET | `/api/v1/versions` | 查询所有服务已安装版本 |
| GET | `/api/v1/latest` | 查询所有服务最新可用版本 |
| POST | `/api/v1/install` | 异步安装所有系统依赖 |
| POST | `/api/v1/uninstall` | 异步卸载系统依赖 |
| POST | `/api/v1/upgrade` | 异步升级指定服务 |
| GET | `/api/v1/tasks` | 列出所有异步任务 |
| GET | `/api/v1/tasks/{id}` | 查询指定任务的状态和日志 |

> **注意**：install / uninstall / upgrade 均为异步接口，调用后立即返回 `task_id`，通过 `/api/v1/tasks/{id}` 轮询结果。

---

## 接口详情

### GET /api/v1/health

健康检查，无需鉴权，可用于探活。

```bash
curl http://192.168.1.101:18080/api/v1/health
```

**响应示例**
```json
{
  "status": "ok",
  "version": "1.0.0",
  "time": "2026-03-23T11:13:09Z"
}
```

---

### GET /api/v1/versions

返回所有受管服务的当前已安装版本。

```bash
curl http://192.168.1.101:18080/api/v1/versions
```

**响应示例**
```json
[
  {
    "name": "docker",
    "package": "docker-ce",
    "protected": false,
    "installed_version": "5:28.1.1-1~ubuntu.20.04~focal",
    "binary_version": "Docker version 28.1.1, build 4eba377",
    "status": "已安装"
  },
  {
    "name": "openssh-server",
    "package": "openssh-server",
    "protected": true,
    "installed_version": "1:8.2p1-4ubuntu0.13",
    "status": "已安装"
  }
]
```

**字段说明**

| 字段 | 说明 |
|------|------|
| `name` | 服务名称 |
| `package` | APT 包名 |
| `protected` | 是否受保护（true 则不可卸载） |
| `installed_version` | 已安装版本，空字符串表示未安装 |
| `binary_version` | 可执行文件的版本输出（部分服务有） |
| `status` | `已安装` / `未安装` |

---

### GET /api/v1/latest

查询所有服务在 APT 仓库中的最新可用版本，并与已安装版本对比。

```bash
# 直接用缓存（快，不执行 apt-get update）
curl http://192.168.1.101:18080/api/v1/latest?update=false

# 先执行 apt-get update 再查询（慢，数据最新）
curl http://192.168.1.101:18080/api/v1/latest
curl http://192.168.1.101:18080/api/v1/latest?update=true
```

**参数**

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `update` | `true` | 是否先执行 `apt-get update` |

**响应示例**
```json
[
  {
    "name": "docker",
    "package": "docker-ce",
    "installed_version": "5:28.1.1-1~ubuntu.20.04~focal",
    "latest_version": "5:28.1.1-1~ubuntu.20.04~focal",
    "up_to_date": true,
    "protected": false
  },
  {
    "name": "nginx",
    "package": "nginx",
    "installed_version": "1.18.0-0ubuntu1.7",
    "latest_version": "1.20.0-0ubuntu1",
    "up_to_date": false,
    "protected": false
  }
]
```

**字段说明**

| 字段 | 说明 |
|------|------|
| `installed_version` | 当前已安装版本，空表示未安装 |
| `latest_version` | APT 仓库中的最新候选版本 |
| `up_to_date` | `true` = 已是最新；`false` = 可升级 |

---

### POST /api/v1/install

异步安装所有系统依赖（Docker、Nginx、基础工具等）。已安装的包会跳过，幂等安全。

```bash
# 不加载本地镜像
curl -X POST http://192.168.1.101:18080/api/v1/install

# 加载单个本地镜像文件
curl -X POST http://192.168.1.101:18080/api/v1/install \
  -H "Content-Type: application/json" \
  -d '{"image_file": "/opt/images/myapp.tar"}'

# 加载目录下所有镜像
curl -X POST http://192.168.1.101:18080/api/v1/install \
  -H "Content-Type: application/json" \
  -d '{"image_dir": "/opt/images"}'
```

**请求体**（均为可选）

| 字段 | 类型 | 说明 |
|------|------|------|
| `image_file` | string | 单个本地 Docker 镜像文件路径（.tar / .tar.gz） |
| `image_dir` | string | 镜像目录，加载其中所有 .tar / .tar.gz 文件 |

**响应示例**（HTTP 202）
```json
{
  "task_id": "task-000001",
  "status": "running",
  "message": "安装任务已启动，请通过 /api/v1/tasks/task-000001 查询进度"
}
```

**安装步骤顺序**
1. 设置系统时区（Etc/UTC）
2. 配置时间同步（systemd-timesyncd，阿里云 NTP）
3. 安装 jq（前置依赖）
4. 安装系统基础软件包
5. 安装 Docker 及 Docker Compose v2
6. 加载本地镜像（可选）

---

### POST /api/v1/uninstall

异步卸载系统依赖。`openssh-server`、`gnupg`、`ca-certificates` 永远不会被卸载。

```bash
curl -X POST http://192.168.1.101:18080/api/v1/uninstall
```

**请求体**（可选）

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `yes` | bool | `true` | API 调用默认自动确认所有提示 |

**响应示例**（HTTP 202）
```json
{
  "task_id": "task-000003",
  "status": "running",
  "message": "卸载任务已启动，请通过 /api/v1/tasks/task-000003 查询进度"
}
```

**卸载内容**

| 类别 | 操作 |
|------|------|
| Docker 容器/镜像/卷 | 停止并清理 |
| Docker 相关包 | `docker-ce`、`docker-ce-cli`、`containerd.io` 等 |
| Docker apt 仓库配置 | `/etc/apt/sources.list.d/docker.list` 等 |
| 基础工具 | `net-tools`、`nginx`、`unzip`、`lsof`（直接卸载） |
| 工具类包 | `jq`、`wget`、`curl` 等（自动确认卸载） |
| 时区/时间同步 | 还原为系统默认配置 |
| **受保护包** | **openssh-server、gnupg、ca-certificates —— 不卸载** |

---

### POST /api/v1/upgrade

异步升级指定服务（或全部可升级服务）。

```bash
# 升级全部可升级的服务
curl -X POST http://192.168.1.101:18080/api/v1/upgrade

# 升级指定服务
curl -X POST http://192.168.1.101:18080/api/v1/upgrade \
  -H "Content-Type: application/json" \
  -d '{"services": ["docker", "nginx"]}'
```

**请求体**

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `services` | []string | `[]`（全部） | 要升级的服务名列表，空则升级全部 |
| `yes` | bool | `true` | 自动确认升级 |

**服务名对照表**

| 服务名 | 对应包 |
|--------|--------|
| `docker` | docker-ce |
| `docker-cli` | docker-ce-cli |
| `containerd` | containerd.io |
| `docker-buildx` | docker-buildx-plugin |
| `docker-compose` | docker-compose-plugin |
| `nginx` | nginx |
| `jq` | jq |
| `curl` | curl |
| `wget` | wget |

**响应示例**（HTTP 202）
```json
{
  "task_id": "task-000004",
  "status": "running",
  "message": "升级任务已启动，请通过 /api/v1/tasks/task-000004 查询进度"
}
```

---

### GET /api/v1/tasks

列出所有异步任务，按创建时间降序。

```bash
curl http://192.168.1.101:18080/api/v1/tasks
```

**响应示例**
```json
[
  {
    "id": "task-000002",
    "command": "install",
    "args": [],
    "status": "success",
    "created_at": "2026-03-23T11:27:06Z",
    "finished_at": "2026-03-23T11:27:32Z",
    "exit_code": 0,
    "output": "..."
  },
  {
    "id": "task-000001",
    "command": "upgrade",
    "args": ["--yes", "nginx"],
    "status": "success",
    "created_at": "2026-03-23T11:25:20Z",
    "finished_at": "2026-03-23T11:25:31Z",
    "exit_code": 0,
    "output": "..."
  }
]
```

**任务状态说明**

| status | 说明 |
|--------|------|
| `running` | 任务执行中 |
| `success` | 执行成功（exit_code = 0） |
| `failed` | 执行失败（exit_code ≠ 0） |

---

### GET /api/v1/tasks/{id}

查询指定任务的详情及完整日志输出。

```bash
curl http://192.168.1.101:18080/api/v1/tasks/task-000001
```

**响应示例**
```json
{
  "id": "task-000001",
  "command": "upgrade",
  "args": ["--yes", "nginx"],
  "status": "success",
  "created_at": "2026-03-23T11:25:20.123Z",
  "finished_at": "2026-03-23T11:25:31.456Z",
  "exit_code": 0,
  "output": "2026-03-23 11:25:20 [INFO] 更新软件源...\n2026-03-23 11:25:31 [INFO] nginx 已是最新版本（1.18.0）\n..."
}
```

**错误响应**（HTTP 404）
```json
{
  "error": "任务不存在：task-999999"
}
```

> **提示**：任务数据保存在内存中，服务重启后历史任务会清空。

---

## 典型使用流程

### 全新安装依赖

```bash
# 1. 触发安装
TASK=$(curl -s -X POST http://192.168.1.101:18080/api/v1/install \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['task_id'])")

# 2. 轮询直到完成
while true; do
  STATUS=$(curl -s http://192.168.1.101:18080/api/v1/tasks/$TASK \
    | python3 -c "import sys,json; print(json.load(sys.stdin)['status'])")
  echo "$(date): $STATUS"
  [ "$STATUS" != "running" ] && break
  sleep 5
done

# 3. 查看完整日志
curl -s http://192.168.1.101:18080/api/v1/tasks/$TASK \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['output'])"
```

### 检查并升级所有服务

```bash
# 查看哪些服务可升级
curl -s "http://192.168.1.101:18080/api/v1/latest?update=false" \
  | python3 -c "
import sys, json
for s in json.load(sys.stdin):
    if not s['up_to_date'] and s['installed_version']:
        print(f\"{s['name']}: {s['installed_version']} → {s['latest_version']}\")
"

# 升级全部
curl -X POST http://192.168.1.101:18080/api/v1/upgrade
```

---

## 服务管理

### 启动 API 服务器

```bash
# 标准启动（仅本机访问）
sudo ./homeclaw-mgr api

# 对外暴露（局域网可访问）
sudo ./homeclaw-mgr api --host 0.0.0.0 --port 18080

# 后台运行
sudo nohup ./homeclaw-mgr api --host 0.0.0.0 --port 18080 \
  </dev/null >api.log 2>&1 &
```

### 查看运行状态

```bash
# 查看进程
pgrep -a homeclaw-mgr

# 查看实时日志
tail -f /home/cat/homeclaw/system-config/api.log
```

### 停止服务

```bash
sudo pkill -f "homeclaw-mgr api"
```

---

## 命令行模式（不启动 API）

直接在服务器上运行：

```bash
cd /home/cat/homeclaw/system-config

sudo ./homeclaw-mgr install                        # 安装
sudo ./homeclaw-mgr install --image-dir /opt/imgs  # 安装并加载镜像
sudo ./homeclaw-mgr uninstall                      # 卸载（交互确认）
sudo ./homeclaw-mgr uninstall --yes                # 卸载（自动确认）
     ./homeclaw-mgr versions                       # 查看已安装版本
     ./homeclaw-mgr versions --json                # JSON 格式
sudo ./homeclaw-mgr latest                         # 查看最新版本
sudo ./homeclaw-mgr upgrade                        # 升级全部
sudo ./homeclaw-mgr upgrade docker nginx           # 升级指定服务
```

---

## 源码重新构建

```bash
cd /Users/boyao/Documents/linknlink/HomeClaw/system-config

# 本地 macOS 构建（用于开发调试）
go build -o homeclaw-mgr .

# 交叉编译 ARM64 Linux 静态二进制（用于部署）
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o homeclaw-mgr-linux-arm64 .

# 部署到远程主机
scp homeclaw-mgr-linux-arm64 cat@192.168.1.101:/home/cat/homeclaw/system-config/homeclaw-mgr
```

---

## 错误码参考

| HTTP 状态码 | 含义 |
|-------------|------|
| 200 | 成功 |
| 202 | 已接受（异步任务已创建） |
| 404 | 任务不存在 |
| 405 | HTTP 方法不允许 |

任务执行失败时，`status` 为 `failed`，`exit_code` 非 0，详细原因见 `output` 字段。
