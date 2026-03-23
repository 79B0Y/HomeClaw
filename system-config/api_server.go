// api_server.go — HomeClaw 管理工具 HTTP REST API 服务器
//
// 启动：
//   sudo ./homeclaw-mgr api [--port 8080] [--host 127.0.0.1]
//
// 端点一览：
//   GET  /api/v1/health              健康检查
//   GET  /api/v1/versions            查询所有服务已安装版本
//   GET  /api/v1/latest              查询所有服务最新可用版本（?update=true 先 apt-get update）
//   POST /api/v1/install             异步安装依赖
//   POST /api/v1/uninstall           异步卸载依赖（保留 openssh-server 等受保护包）
//   POST /api/v1/upgrade             异步升级服务
//   GET  /api/v1/tasks               列出所有任务
//   GET  /api/v1/tasks/{id}          查询指定任务状态及输出
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ============================================================
// 任务管理（Task）
// ============================================================

// TaskStatus 表示异步任务的执行状态。
type TaskStatus string

const (
	TaskRunning TaskStatus = "running"
	TaskSuccess TaskStatus = "success"
	TaskFailed  TaskStatus = "failed"
)

// Task 代表一次异步管理操作。
type Task struct {
	ID         string     `json:"id"`
	Command    string     `json:"command"`
	Args       []string   `json:"args"`
	Status     TaskStatus `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	ExitCode   int        `json:"exit_code"`
	Output     string     `json:"output"`
}

var (
	taskStore   sync.Map
	taskCounter uint64
)

// newTask 创建并注册一个新任务（状态 running）。
func newTask(command string, args []string) *Task {
	id := fmt.Sprintf("task-%06d", atomic.AddUint64(&taskCounter, 1))
	t := &Task{
		ID:        id,
		Command:   command,
		Args:      args,
		Status:    TaskRunning,
		CreatedAt: time.Now(),
	}
	taskStore.Store(id, t)
	return t
}

// getTask 按 ID 查找任务。
func getTask(id string) (*Task, bool) {
	v, ok := taskStore.Load(id)
	if !ok {
		return nil, false
	}
	return v.(*Task), true
}

// listTasks 返回所有任务，按创建时间降序。
func listTasks() []*Task {
	var tasks []*Task
	taskStore.Range(func(_, v any) bool {
		tasks = append(tasks, v.(*Task))
		return true
	})
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
	})
	return tasks
}

// finishTask 标记任务完成并写入输出。
func finishTask(t *Task, exitCode int, output string) {
	now := time.Now()
	t.FinishedAt = &now
	t.ExitCode = exitCode
	t.Output = output
	if exitCode == 0 {
		t.Status = TaskSuccess
	} else {
		t.Status = TaskFailed
	}
}

// runTaskAsync 在 goroutine 中以子进程方式执行本工具的指定子命令，
// 捕获 stdout+stderr 写入任务输出，完成后更新任务状态。
func runTaskAsync(t *Task, subCmd string, args []string) {
	self, err := os.Executable()
	if err != nil {
		self = os.Args[0]
	}

	cmdArgs := append([]string{subCmd}, args...)
	cmd := exec.Command(self, cmdArgs...)

	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(&buf, os.Stdout)
	cmd.Stderr = io.MultiWriter(&buf, os.Stderr)

	runErr := cmd.Run()
	exitCode := 0
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = 1
		}
	}
	finishTask(t, exitCode, buf.String())
}

// ============================================================
// 版本/最新版数据获取（直接调用，无需子进程）
// ============================================================

// versionsData 返回所有受管服务的已安装版本信息。
func versionsData() []ServiceVersionInfo {
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
	return results
}

// latestData 返回所有受管服务的最新可用版本信息。
// doUpdate=true 时先执行 apt-get update。
func latestData(doUpdate bool) []ServiceLatestInfo {
	if doUpdate {
		if err := aptUpdate(); err != nil {
			logf(lvlWarn, "API apt-get update 失败：%v", err)
		}
	}
	var results []ServiceLatestInfo
	for _, svc := range managedServices {
		installed := serviceInstalledVersion(svc)
		latest := serviceCandidateVersion(svc)
		upToDate := installed != "" && latest != "" && installed == latest
		if svc.NonApt && installed != "" {
			upToDate = true
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
	return results
}

// ============================================================
// JSON 响应工具
// ============================================================

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

type errResp struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errResp{Error: msg})
}

// ============================================================
// HTTP 处理器
// ============================================================

// GET /api/v1/health
func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": "1.0.0",
		"time":    time.Now().Format(time.RFC3339),
	})
}

// GET /api/v1/versions
func handleVersions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}
	writeJSON(w, http.StatusOK, versionsData())
}

// GET /api/v1/latest?update=true
func handleLatest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}
	// 默认 update=true；传 update=false 可跳过 apt-get update
	doUpdate := r.URL.Query().Get("update") != "false"
	writeJSON(w, http.StatusOK, latestData(doUpdate))
}

// POST /api/v1/install
// 请求体（可选）：{"image_file":"","image_dir":""}
func handleInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	var req struct {
		ImageFile string `json:"image_file"`
		ImageDir  string `json:"image_dir"`
	}
	decodeJSON(r, &req) // 参数可选，忽略解析错误

	var args []string
	if req.ImageFile != "" {
		args = append(args, "--image-file", req.ImageFile)
	}
	if req.ImageDir != "" {
		args = append(args, "--image-dir", req.ImageDir)
	}

	t := newTask("install", args)
	go runTaskAsync(t, "install", args)

	writeJSON(w, http.StatusAccepted, map[string]string{
		"task_id": t.ID,
		"status":  string(t.Status),
		"message": "安装任务已启动，请通过 /api/v1/tasks/" + t.ID + " 查询进度",
	})
}

// POST /api/v1/uninstall
// 请求体（可选）：{"yes":true}
// yes 默认为 true（API 模式下自动确认）
func handleUninstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	var req struct {
		Yes bool `json:"yes"`
	}
	req.Yes = true // API 调用默认自动确认
	decodeJSON(r, &req)

	args := []string{}
	if req.Yes {
		args = append(args, "--yes")
	}

	t := newTask("uninstall", args)
	go runTaskAsync(t, "uninstall", args)

	writeJSON(w, http.StatusAccepted, map[string]string{
		"task_id": t.ID,
		"status":  string(t.Status),
		"message": "卸载任务已启动，请通过 /api/v1/tasks/" + t.ID + " 查询进度",
	})
}

// POST /api/v1/upgrade
// 请求体（可选）：{"services":["docker","nginx"],"yes":true}
func handleUpgrade(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	var req struct {
		Services []string `json:"services"`
		Yes      bool     `json:"yes"`
	}
	req.Yes = true // 默认自动确认
	decodeJSON(r, &req)

	args := []string{"--yes"}
	args = append(args, req.Services...)

	t := newTask("upgrade", args)
	go runTaskAsync(t, "upgrade", args)

	writeJSON(w, http.StatusAccepted, map[string]string{
		"task_id": t.ID,
		"status":  string(t.Status),
		"message": "升级任务已启动，请通过 /api/v1/tasks/" + t.ID + " 查询进度",
	})
}

// GET /api/v1/tasks
func handleListTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}
	writeJSON(w, http.StatusOK, listTasks())
}

// GET /api/v1/tasks/{id}
func handleGetTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}
	// 从路径提取 task id：/api/v1/tasks/{id}
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/")
	id = strings.Trim(id, "/")
	if id == "" {
		handleListTasks(w, r)
		return
	}
	t, ok := getTask(id)
	if !ok {
		writeError(w, http.StatusNotFound, "任务不存在："+id)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// ============================================================
// 路由与服务器启动
// ============================================================

// corsMiddleware 为所有响应添加 CORS 头，允许浏览器跨端口访问。
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func newAPIRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/health", handleHealth)
	mux.HandleFunc("/api/v1/versions", handleVersions)
	mux.HandleFunc("/api/v1/latest", handleLatest)
	mux.HandleFunc("/api/v1/install", handleInstall)
	mux.HandleFunc("/api/v1/uninstall", handleUninstall)
	mux.HandleFunc("/api/v1/upgrade", handleUpgrade)
	mux.HandleFunc("/api/v1/tasks/", handleGetTask) // 带斜杠，匹配 /tasks/{id}
	mux.HandleFunc("/api/v1/tasks", handleListTasks)
	return corsMiddleware(mux)
}

// cmdAPI 解析参数并启动 HTTP API 服务器。
func cmdAPI(args []string) {
	fs := flag.NewFlagSet("api", flag.ExitOnError)
	port := fs.Int("port", 8080, "监听端口")
	host := fs.String("host", "127.0.0.1", "监听地址（0.0.0.0 则对外暴露，注意安全）")
	fs.Usage = func() {
		fmt.Println("用法: homeclaw-mgr api [--port <p>] [--host <h>]")
		fmt.Println()
		fmt.Println("启动 HTTP REST API 服务器，提供以下端点：")
		fmt.Println("  GET  /api/v1/health              健康检查")
		fmt.Println("  GET  /api/v1/versions            已安装版本（JSON）")
		fmt.Println("  GET  /api/v1/latest[?update=true] 最新可用版本（JSON）")
		fmt.Println("  POST /api/v1/install             异步安装依赖")
		fmt.Println("  POST /api/v1/uninstall           异步卸载依赖")
		fmt.Println("  POST /api/v1/upgrade             异步升级服务")
		fmt.Println("  GET  /api/v1/tasks               列出所有任务")
		fmt.Println("  GET  /api/v1/tasks/{id}          查询任务详情")
		fmt.Println()
		fs.PrintDefaults()
	}
	fs.Parse(args)

	addr := fmt.Sprintf("%s:%d", *host, *port)

	logf(lvlInfo, "==========================================")
	logf(lvlInfo, "   HomeClaw 管理 API 服务器")
	logf(lvlInfo, "==========================================")
	logf(lvlInfo, "监听地址：http://%s", addr)
	logf(lvlInfo, "")
	logf(lvlInfo, "可用端点：")
	logf(lvlInfo, "  GET  http://%s/api/v1/health", addr)
	logf(lvlInfo, "  GET  http://%s/api/v1/versions", addr)
	logf(lvlInfo, "  GET  http://%s/api/v1/latest", addr)
	logf(lvlInfo, "  POST http://%s/api/v1/install", addr)
	logf(lvlInfo, "  POST http://%s/api/v1/uninstall", addr)
	logf(lvlInfo, "  POST http://%s/api/v1/upgrade", addr)
	logf(lvlInfo, "  GET  http://%s/api/v1/tasks", addr)
	logf(lvlInfo, "  GET  http://%s/api/v1/tasks/{id}", addr)
	logf(lvlInfo, "")
	logf(lvlInfo, "示例：")
	logf(lvlInfo, "  curl http://%s/api/v1/health", addr)
	logf(lvlInfo, "  curl http://%s/api/v1/versions", addr)
	logf(lvlInfo, "  curl -X POST http://%s/api/v1/install", addr)
	logf(lvlInfo, "  curl http://%s/api/v1/tasks", addr)
	logf(lvlInfo, "==========================================")

	srv := &http.Server{
		Addr:         addr,
		Handler:      newAPIRouter(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 300 * time.Second, // 留足时间供同步查询操作（latest 需要 apt update）
		IdleTimeout:  60 * time.Second,
	}

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logf(lvlError, "API 服务器启动失败：%v", err)
		os.Exit(1)
	}
}
