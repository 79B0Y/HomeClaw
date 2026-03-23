package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	configFile = "/etc/xray-config.json"
	service    = "xray-proxy"
	listenAddr = ":8080"
)

// ── 工具函数 ──────────────────────────────────────────────────────────────────

func run(cmdStr string, timeout time.Duration) (stdout, stderr string, exitCode int) {
	ctx := exec.Command("sh", "-c", cmdStr)
	out, err := ctx.Output()
	stdout = strings.TrimSpace(string(out))
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
			exitCode = ee.ExitCode()
		} else {
			stderr = err.Error()
			exitCode = 1
		}
	}
	return
}

func readConfig() map[string]interface{} {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return cfg
}

func writeConfig(cfg map[string]interface{}) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configFile, data, 0644)
}

func getSSServer(cfg map[string]interface{}) map[string]interface{} {
	outbounds, ok := cfg["outbounds"].([]interface{})
	if !ok || len(outbounds) == 0 {
		return nil
	}
	ob, ok := outbounds[0].(map[string]interface{})
	if !ok {
		return nil
	}
	settings, ok := ob["settings"].(map[string]interface{})
	if !ok {
		return nil
	}
	servers, ok := settings["servers"].([]interface{})
	if !ok || len(servers) == 0 {
		return nil
	}
	srv, ok := servers[0].(map[string]interface{})
	if !ok {
		return nil
	}
	return srv
}

func resolveDOH(domain string) string {
	out, _, rc := run(fmt.Sprintf(
		"curl -s --max-time 5 'https://dns.alidns.com/resolve?name=%s&type=A'", domain), 10*time.Second)
	if rc != 0 {
		return ""
	}
	re := regexp.MustCompile(`"data":"(\d+\.\d+\.\d+\.\d+)"`)
	matches := re.FindAllStringSubmatch(out, -1)
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1][1]
}

func jsonResponse(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// ── 订阅解析 ──────────────────────────────────────────────────────────────────

type Node struct {
	Name     string `json:"name"`
	Server   string `json:"server"`
	Port     int    `json:"port"`
	Password string `json:"password"`
	Method   string `json:"method"`
}

var skipKeywords = []string{"流量", "时间", "网址", "套餐", "到期", "剩余", "过期", "最新"}

func containsSkip(s string) bool {
	for _, kw := range skipKeywords {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

func parseSSAJSON(raw string) []Node {
	var arr []map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return nil
	}
	var nodes []Node
	for _, n := range arr {
		name, _ := n["remarks"].(string)
		if containsSkip(name) {
			continue
		}
		server, _ := n["server"].(string)
		password, _ := n["password"].(string)
		method, _ := n["method"].(string)
		if method == "" {
			method = "chacha20-ietf-poly1305"
		}
		var port int
		switch v := n["server_port"].(type) {
		case float64:
			port = int(v)
		case string:
			port, _ = strconv.Atoi(v)
		}
		if server != "" && port > 0 && password != "" {
			nodes = append(nodes, Node{Name: name, Server: server, Port: port, Password: password, Method: method})
		}
	}
	return nodes
}

func parseClashYAML(raw string) []Node {
	var nodes []Node

	// 找 proxies: 块
	blockRe := regexp.MustCompile(`(?ms)^proxies:\s*\n(.*?)(?:^[a-zA-Z][\w-]*\s*:|\z)`)
	block := raw
	if m := blockRe.FindStringSubmatch(raw); m != nil {
		block = m[1]
	}

	// JSON inline: - {"name":"...","type":"ss",...}
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		entry := strings.TrimSpace(line[2:])
		if strings.HasPrefix(entry, "{") {
			var n map[string]interface{}
			if err := json.Unmarshal([]byte(entry), &n); err != nil {
				continue
			}
			typ, _ := n["type"].(string)
			if strings.ToLower(typ) != "ss" && strings.ToLower(typ) != "shadowsocks" {
				continue
			}
			name, _ := n["name"].(string)
			if containsSkip(name) {
				continue
			}
			server, _ := n["server"].(string)
			cipher, _ := n["cipher"].(string)
			password, _ := n["password"].(string)
			var port int
			switch v := n["port"].(type) {
			case float64:
				port = int(v)
			}
			if server != "" && port > 0 && password != "" {
				nodes = append(nodes, Node{Name: name, Server: server, Port: port, Password: password, Method: cipher})
			}
			continue
		}

		// YAML flow: - {name: ..., type: ss, ...}
		typRe := regexp.MustCompile(`\btype:\s*["\']?(\S+?)["\']?(?:,|\})`)
		tm := typRe.FindStringSubmatch(entry)
		if tm == nil {
			continue
		}
		typ := strings.ToLower(tm[1])
		if typ != "ss" && typ != "shadowsocks" {
			continue
		}
		gf := func(key string) string {
			re := regexp.MustCompile(`\b` + key + `:\s*["']?([^,"'\}]+)["']?`)
			m := re.FindStringSubmatch(entry)
			if m == nil {
				return ""
			}
			return strings.TrimSpace(m[1])
		}
		name := gf("name")
		if containsSkip(name) {
			continue
		}
		server := gf("server")
		portStr := gf("port")
		password := gf("password")
		cipher := gf("cipher")
		port, _ := strconv.Atoi(portStr)
		if server != "" && port > 0 && password != "" && cipher != "" {
			nodes = append(nodes, Node{Name: name, Server: server, Port: port, Password: password, Method: cipher})
		}
	}

	if len(nodes) > 0 {
		return nodes
	}

	// 多行展开格式
	entryRe := regexp.MustCompile(`(?m)\n(?:\s{0,4}-\s)`)
	entries := entryRe.Split(block, -1)
	for _, e := range entries {
		typM := regexp.MustCompile(`type:\s*(\S+)`).FindStringSubmatch(e)
		if typM == nil || (strings.ToLower(typM[1]) != "ss" && strings.ToLower(typM[1]) != "shadowsocks") {
			continue
		}
		get := func(key string) string {
			re := regexp.MustCompile(`(?m)` + key + `:\s*["']?([^"'\n]+)["']?`)
			m := re.FindStringSubmatch(e)
			if m == nil {
				return ""
			}
			return strings.TrimSpace(m[1])
		}
		name := get("name")
		if containsSkip(name) {
			continue
		}
		server := get("server")
		portStr := get("port")
		password := get("password")
		cipher := get("cipher")
		port, _ := strconv.Atoi(portStr)
		if server != "" && port > 0 && password != "" && cipher != "" {
			nodes = append(nodes, Node{Name: name, Server: server, Port: port, Password: password, Method: cipher})
		}
	}
	return nodes
}

// ── 节点测试 ──────────────────────────────────────────────────────────────────

func testNodeConnectivity(addr string, port int, password, method string) (outIP string, latencyMS int) {
	testPort := 19990
	cfg := map[string]interface{}{
		"log": map[string]interface{}{"loglevel": "none"},
		"inbounds": []interface{}{
			map[string]interface{}{
				"port":     testPort,
				"protocol": "socks",
				"settings": map[string]interface{}{"auth": "noauth"},
			},
		},
		"outbounds": []interface{}{
			map[string]interface{}{
				"protocol": "shadowsocks",
				"settings": map[string]interface{}{
					"servers": []interface{}{
						map[string]interface{}{
							"address":  addr,
							"port":     port,
							"method":   method,
							"password": password,
						},
					},
				},
				"tag": "proxy",
			},
		},
	}

	tmp := fmt.Sprintf("/tmp/xray-test-%d.json", testPort)
	data, _ := json.Marshal(cfg)
	os.WriteFile(tmp, data, 0644)
	defer os.Remove(tmp)

	proc := exec.Command("/usr/local/bin/xray", "run", "-c", tmp)
	proc.Stdout = nil
	proc.Stderr = nil
	if err := proc.Start(); err != nil {
		return "", 0
	}
	defer func() {
		proc.Process.Kill()
		proc.Wait()
	}()

	time.Sleep(2 * time.Second)

	t0 := time.Now()
	out, _, rc := run(fmt.Sprintf(
		"curl -s --max-time 8 --proxy socks5://127.0.0.1:%d https://api.ipify.org", testPort),
		12*time.Second)
	latencyMS = int(time.Since(t0).Milliseconds())

	if rc == 0 && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out), latencyMS
	}
	return "", 0
}

// ── API 处理器 ────────────────────────────────────────────────────────────────

func handleStatus(w http.ResponseWriter, r *http.Request) {
	_, _, rc := run("systemctl is-active "+service, 5*time.Second)
	active := rc == 0
	cfg := readConfig()
	var node map[string]interface{}
	if cfg != nil {
		node = getSSServer(cfg)
	}
	outIP, _, _ := run("curl -s --max-time 6 --proxy http://127.0.0.1:1081 https://api.ipify.org", 8*time.Second)
	jsonResponse(w, 200, map[string]interface{}{
		"active":      active,
		"node":        node,
		"outbound_ip": strings.TrimSpace(outIP),
	})
}

func handleService(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	parts := strings.Split(r.URL.Path, "/")
	action := parts[len(parts)-1]
	if action != "start" && action != "stop" && action != "restart" {
		jsonResponse(w, 400, map[string]interface{}{"ok": false, "msg": "invalid action"})
		return
	}
	_, errStr, rc := run("systemctl "+action+" "+service, 10*time.Second)
	jsonResponse(w, 200, map[string]interface{}{"ok": rc == 0, "msg": errStr})
}

func handleSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var body map[string]string
	json.NewDecoder(r.Body).Decode(&body)
	url := strings.TrimSpace(body["url"])
	if url == "" {
		jsonResponse(w, 400, map[string]interface{}{"ok": false, "msg": "URL 为空"})
		return
	}

	out, errStr, rc := run(fmt.Sprintf("curl -sL --max-time 15 '%s'", url), 20*time.Second)
	if rc != 0 {
		jsonResponse(w, 400, map[string]interface{}{"ok": false, "msg": "下载失败: " + errStr})
		return
	}

	// 过滤注释行
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "#") {
			lines = append(lines, l)
		}
	}
	raw := strings.TrimSpace(strings.Join(lines, "\n"))

	var nodes []Node
	var fmt_ string
	if strings.HasPrefix(raw, "[") {
		nodes = parseSSAJSON(raw)
		fmt_ = "SSA JSON"
	} else if strings.Contains(raw, "proxies:") || strings.Contains(raw, "type: ss") || strings.Contains(raw, "type: shadowsocks") {
		nodes = parseClashYAML(raw)
		fmt_ = "Clash YAML"
	} else {
		nodes = parseSSAJSON(raw)
		if len(nodes) == 0 {
			nodes = parseClashYAML(raw)
		}
		fmt_ = "auto"
	}

	if len(nodes) == 0 {
		preview := raw
		if len(preview) > 120 {
			preview = preview[:120]
		}
		preview = strings.ReplaceAll(preview, "\n", " ")
		jsonResponse(w, 400, map[string]interface{}{
			"ok":  false,
			"msg": fmt.Sprintf("未解析到节点，格式：%s，内容：%s", fmt_, preview),
		})
		return
	}
	jsonResponse(w, 200, map[string]interface{}{
		"ok":    true,
		"count": len(nodes),
		"nodes": nodes,
		"fmt":   fmt_,
	})
}

func handleTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var data map[string]interface{}
	json.NewDecoder(r.Body).Decode(&data)
	addr, _ := data["server"].(string)
	method, _ := data["method"].(string)
	if method == "" {
		method = "chacha20-ietf-poly1305"
	}
	password, _ := data["password"].(string)
	var port int
	switch v := data["port"].(type) {
	case float64:
		port = int(v)
	}

	realAddr := addr
	ipRe := regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`)
	if addr != "" && !ipRe.MatchString(addr) {
		if ip := resolveDOH(addr); ip != "" {
			realAddr = ip
		}
	}

	outIP, latency := testNodeConnectivity(realAddr, port, password, method)
	jsonResponse(w, 200, map[string]interface{}{
		"ok":        outIP != "",
		"ip":        outIP,
		"latency":   latency,
		"real_addr": realAddr,
	})
}

func handleApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var data map[string]interface{}
	json.NewDecoder(r.Body).Decode(&data)
	addr, _ := data["server"].(string)
	method, _ := data["method"].(string)
	if method == "" {
		method = "chacha20-ietf-poly1305"
	}
	password, _ := data["password"].(string)
	var port int
	switch v := data["port"].(type) {
	case float64:
		port = int(v)
	}

	realAddr := addr
	ipRe := regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`)
	if addr != "" && !ipRe.MatchString(addr) {
		if ip := resolveDOH(addr); ip != "" {
			realAddr = ip
		}
	}

	cfg := readConfig()
	if cfg == nil {
		jsonResponse(w, 500, map[string]interface{}{"ok": false, "msg": "读取配置文件失败"})
		return
	}

	outbounds, _ := cfg["outbounds"].([]interface{})
	if outbounds == nil {
		outbounds = []interface{}{}
	}

	var ssOutbound map[string]interface{}
	for _, ob := range outbounds {
		o, ok := ob.(map[string]interface{})
		if !ok {
			continue
		}
		if o["protocol"] == "shadowsocks" {
			ssOutbound = o
			break
		}
	}
	if ssOutbound == nil {
		ssOutbound = map[string]interface{}{
			"protocol": "shadowsocks",
			"settings": map[string]interface{}{"servers": []interface{}{map[string]interface{}{}}},
			"tag":      "proxy",
		}
		outbounds = append([]interface{}{ssOutbound}, outbounds...)
		cfg["outbounds"] = outbounds
	}

	settings, _ := ssOutbound["settings"].(map[string]interface{})
	if settings == nil {
		settings = map[string]interface{}{}
		ssOutbound["settings"] = settings
	}
	settings["servers"] = []interface{}{
		map[string]interface{}{
			"address":  realAddr,
			"port":     port,
			"method":   method,
			"password": password,
		},
	}

	if err := writeConfig(cfg); err != nil {
		jsonResponse(w, 500, map[string]interface{}{"ok": false, "msg": err.Error()})
		return
	}
	_, errStr, rc := run("systemctl restart "+service, 10*time.Second)
	jsonResponse(w, 200, map[string]interface{}{
		"ok":        rc == 0,
		"msg":       errStr,
		"real_addr": realAddr,
	})
}

func handleSettings(w http.ResponseWriter, r *http.Request) {
	cfg := readConfig()
	if cfg == nil {
		jsonResponse(w, 500, map[string]interface{}{"ok": false, "msg": "读取配置失败"})
		return
	}

	if r.Method == http.MethodGet {
		socksPort, httpPort := 1080, 1081
		if inbounds, ok := cfg["inbounds"].([]interface{}); ok {
			for _, ib := range inbounds {
				i, ok := ib.(map[string]interface{})
				if !ok {
					continue
				}
				p := int(i["port"].(float64))
				switch i["protocol"] {
				case "socks":
					socksPort = p
				case "http":
					httpPort = p
				}
			}
		}
		_, routing := cfg["routing"]
		jsonResponse(w, 200, map[string]interface{}{
			"ok":         true,
			"socks_port": socksPort,
			"http_port":  httpPort,
			"routing":    routing,
		})
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}

	var data map[string]interface{}
	json.NewDecoder(r.Body).Decode(&data)
	socksPort := 1080
	httpPort := 1081
	if v, ok := data["socks_port"].(float64); ok {
		socksPort = int(v)
	}
	if v, ok := data["http_port"].(float64); ok {
		httpPort = int(v)
	}
	routing, _ := data["routing"].(bool)

	if inbounds, ok := cfg["inbounds"].([]interface{}); ok {
		for _, ib := range inbounds {
			i, ok := ib.(map[string]interface{})
			if !ok {
				continue
			}
			switch i["protocol"] {
			case "socks":
				i["port"] = socksPort
			case "http":
				i["port"] = httpPort
			}
		}
	}

	if routing {
		cfg["routing"] = map[string]interface{}{
			"domainStrategy": "IPIfNonMatch",
			"rules": []interface{}{
				map[string]interface{}{"type": "field", "outboundTag": "direct", "domain": []string{"geosite:cn"}},
				map[string]interface{}{"type": "field", "outboundTag": "direct", "ip": []string{"geoip:cn", "geoip:private"}},
			},
		}
		outbounds, _ := cfg["outbounds"].([]interface{})
		hasDirect := false
		for _, ob := range outbounds {
			o, ok := ob.(map[string]interface{})
			if ok && o["tag"] == "direct" {
				hasDirect = true
				break
			}
		}
		if !hasDirect {
			cfg["outbounds"] = append(outbounds, map[string]interface{}{"protocol": "freedom", "tag": "direct"})
		}
	} else {
		delete(cfg, "routing")
	}

	if err := writeConfig(cfg); err != nil {
		jsonResponse(w, 500, map[string]interface{}{"ok": false, "msg": err.Error()})
		return
	}
	_, errStr, rc := run("systemctl restart "+service, 10*time.Second)
	jsonResponse(w, 200, map[string]interface{}{"ok": rc == 0, "msg": errStr})
}

func handleLog(w http.ResponseWriter, r *http.Request) {
	out, _, _ := run(fmt.Sprintf("journalctl -u %s -n 80 --no-pager --output=short", service), 10*time.Second)
	jsonResponse(w, 200, map[string]interface{}{"log": out})
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	html := filepath.Join(filepath.Dir(os.Args[0]), "xray-panel.html")
	// 开发时兼容：与 main.go 同目录
	if _, err := os.Stat(html); os.IsNotExist(err) {
		html = filepath.Join(filepath.Dir(os.Args[0]), "proxy-manager", "xray-panel.html")
	}
	http.ServeFile(w, r, html)
}

// ── 安装 / 卸载 ──────────────────────────────────────────────────────────────

func colorPrint(color, prefix, msg string) {
	codes := map[string]string{"red": "\033[0;31m", "green": "\033[0;32m", "yellow": "\033[1;33m", "blue": "\033[0;34m"}
	reset := "\033[0m"
	c := codes[color]
	fmt.Printf("%s[%s]%s  %s\n", c, prefix, reset, msg)
}

func info(msg string)    { colorPrint("blue", "INFO", msg) }
func success(msg string) { colorPrint("green", "OK", msg) }
func warn(msg string)    { colorPrint("yellow", "WARN", msg) }
func fatal(msg string)   { colorPrint("red", "ERROR", msg); os.Exit(1) }

func confirm(prompt string) bool {
	fmt.Printf("%s (y/N): ", prompt)
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	return strings.ToLower(strings.TrimSpace(line)) == "y"
}

func runCmd(cmd string) (string, string, int) {
	return run(cmd, 60*time.Second)
}

// ── 异步任务状态 ──────────────────────────────────────────────────────────────

type taskState struct {
	mu      sync.Mutex
	running bool
	done    bool
	ok      bool
	log     strings.Builder
}

var installState = &taskState{}
var uninstallState = &taskState{}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func (t *taskState) write(line string) {
	clean := ansiRe.ReplaceAllString(line, "")
	t.mu.Lock()
	t.log.WriteString(clean + "\n")
	t.mu.Unlock()
	fmt.Println(line)
}

func (t *taskState) getLog() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.log.String()
}

// ── 安装核心逻辑 ──────────────────────────────────────────────────────────────
// viaHTTP=true 时，跳过重启 xray-panel（自身进程已在服务，重启会端口冲突）

func doInstall(selfPath, panelDir string, w func(string), viaHTTP bool) error {
	deviceIP, _, _ := runCmd("hostname -I | awk '{print $1}'")

	w("[INFO]  面板目录：" + panelDir)
	w("[INFO]  面板地址：http://" + strings.TrimSpace(deviceIP) + ":8080")

	// 步骤 1：安装依赖
	w("[INFO]  步骤 1/4：安装依赖（unzip / curl）...")
	if _, _, rc := runCmd("apt-get update -qq && apt-get install -y -qq unzip curl"); rc != 0 {
		return fmt.Errorf("依赖安装失败")
	}
	w("[OK]    依赖安装完成")

	// 步骤 2：下载安装 Xray
	if _, _, rc := runCmd("command -v xray"); rc == 0 {
		ver, _, _ := runCmd("xray version 2>&1 | head -1")
		w("[INFO]  步骤 2/4：Xray 已安装（" + ver + "），跳过")
	} else {
		w("[INFO]  步骤 2/4：下载 Xray ARM64...")
		const xrayVer = "v25.2.21"
		const fileName = "Xray-linux-arm64-v8a.zip"
		urls := []string{
			"https://github.com/XTLS/Xray-core/releases/download/" + xrayVer + "/" + fileName,
			"https://ghfast.top/https://github.com/XTLS/Xray-core/releases/download/" + xrayVer + "/" + fileName,
			"https://gh-proxy.com/https://github.com/XTLS/Xray-core/releases/download/" + xrayVer + "/" + fileName,
			"https://mirror.ghproxy.com/https://github.com/XTLS/Xray-core/releases/download/" + xrayVer + "/" + fileName,
		}
		downloaded := false
		for _, url := range urls {
			w("[INFO]  尝试：" + url)
			_, _, rc := runCmd(fmt.Sprintf("curl -L --max-time 60 -o /tmp/xray.zip '%s'", url))
			if rc == 0 {
				out, _, _ := runCmd("file /tmp/xray.zip 2>/dev/null")
				if strings.Contains(strings.ToLower(out), "zip") || strings.Contains(strings.ToLower(out), "archive") {
					w("[OK]    下载成功")
					downloaded = true
					break
				}
			}
			w("[WARN]  下载失败，尝试下一个源...")
			os.Remove("/tmp/xray.zip")
		}
		if !downloaded {
			return fmt.Errorf("所有源均失败，请手动下载 %s 放到 /tmp/xray.zip 后重跑", fileName)
		}
		os.MkdirAll("/tmp/xray-core", 0755)
		runCmd("unzip -o /tmp/xray.zip -d /tmp/xray-core > /dev/null")
		runCmd("cp /tmp/xray-core/xray /usr/local/bin/xray && chmod +x /usr/local/bin/xray")
		for _, dat := range []string{"geoip.dat", "geosite.dat"} {
			if _, err := os.Stat("/tmp/xray-core/" + dat); err == nil {
				runCmd("cp /tmp/xray-core/" + dat + " /usr/local/bin/")
				w("[OK]    已安装 " + dat)
			}
		}
		ver, _, _ := runCmd("xray version 2>&1 | head -1")
		w("[OK]    Xray 安装成功：" + ver)
	}

	// 步骤 3：写入初始 Xray 配置
	w("[INFO]  步骤 3/4：写入初始 Xray 配置...")
	if _, err := os.Stat("/etc/xray-config.json"); os.IsNotExist(err) {
		initCfg := `{
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
}`
		os.WriteFile("/etc/xray-config.json", []byte(initCfg), 0644)
		w("[OK]    初始配置已写入（节点请在 Web 面板配置）")
	} else {
		w("[INFO]  已存在 /etc/xray-config.json，跳过覆盖")
	}

	proxyService := `[Unit]
Description=Xray Proxy Service
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/xray run -c /etc/xray-config.json
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
`
	os.WriteFile("/etc/systemd/system/xray-proxy.service", []byte(proxyService), 0644)
	runCmd("systemctl daemon-reload && systemctl enable xray-proxy && systemctl restart xray-proxy")
	time.Sleep(1 * time.Second)
	if _, _, rc := runCmd("systemctl is-active xray-proxy"); rc == 0 {
		w("[OK]    xray-proxy 服务已启动")
	} else {
		w("[WARN]  xray-proxy 启动异常（初始配置无节点，属正常），配置节点后会自动恢复")
	}

	// 步骤 4：注册面板服务
	w("[INFO]  步骤 4/4：注册 Web 管理面板服务（Go 版本）...")
	panelService := fmt.Sprintf(`[Unit]
Description=Xray Web Panel (Go)
After=network.target

[Service]
Type=simple
WorkingDirectory=%s
ExecStart=%s
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
`, panelDir, selfPath)

	os.WriteFile("/etc/systemd/system/xray-panel.service", []byte(panelService), 0644)
	if viaHTTP {
		// 当前进程已在 8080 提供服务，只注册+启用，不重启（避免端口冲突）
		runCmd("systemctl daemon-reload && systemctl enable xray-panel")
		w("[OK]    xray-panel 服务已注册（当前进程即为面板，系统重启后由 systemd 托管）")
	} else {
		runCmd("systemctl daemon-reload && systemctl enable xray-panel && systemctl restart xray-panel")
		time.Sleep(2 * time.Second)
		if _, _, rc := runCmd("systemctl is-active xray-panel"); rc == 0 {
			w("[OK]    xray-panel 服务已启动（端口 8080）")
		} else {
			return fmt.Errorf("面板启动失败，请运行：journalctl -u xray-panel -n 30")
		}
	}

	os.RemoveAll("/tmp/xray.zip")
	os.RemoveAll("/tmp/xray-core")
	w("[OK]    安装完成！访问 http://" + strings.TrimSpace(deviceIP) + ":8080")
	return nil
}

// ── 卸载核心逻辑 ──────────────────────────────────────────────────────────────
// viaHTTP=true 时，跳过停止 xray-panel（避免杀死自身），最后再退出进程

func doUninstall(w func(string), viaHTTP bool) error {
	w("[INFO]  停止并删除 systemd 服务...")
	for _, svc := range []string{"xray-proxy", "xray-panel"} {
		// HTTP 模式下跳过停止 xray-panel（自身进程），改为最后退出
		if viaHTTP && svc == "xray-panel" {
			if _, _, rc := runCmd("systemctl is-enabled " + svc); rc == 0 {
				runCmd("systemctl disable " + svc)
				w("[OK]    已禁用 " + svc)
			}
			svcFile := "/etc/systemd/system/" + svc + ".service"
			if _, err := os.Stat(svcFile); err == nil {
				os.Remove(svcFile)
				w("[OK]    已删除 " + svcFile)
			}
			continue
		}
		if _, _, rc := runCmd("systemctl is-active " + svc); rc == 0 {
			runCmd("systemctl stop " + svc)
			w("[OK]    已停止 " + svc)
		}
		if _, _, rc := runCmd("systemctl is-enabled " + svc); rc == 0 {
			runCmd("systemctl disable " + svc)
			w("[OK]    已禁用 " + svc)
		}
		svcFile := "/etc/systemd/system/" + svc + ".service"
		if _, err := os.Stat(svcFile); err == nil {
			os.Remove(svcFile)
			w("[OK]    已删除 " + svcFile)
		}
	}
	runCmd("systemctl daemon-reload")
	w("[OK]    systemd 已刷新")

	w("[INFO]  删除 Xray 二进制和配置...")
	for _, f := range []string{"/usr/local/bin/xray", "/usr/local/bin/geoip.dat", "/usr/local/bin/geosite.dat"} {
		if _, err := os.Stat(f); err == nil {
			os.Remove(f)
			w("[OK]    已删除 " + f)
		}
	}
	if _, err := os.Stat("/etc/xray-config.json"); err == nil {
		os.Remove("/etc/xray-config.json")
		w("[OK]    已删除 /etc/xray-config.json")
	}

	w("[OK]    卸载完成！")
	return nil
}

// ── CLI 入口 ──────────────────────────────────────────────────────────────────

func runInstall() {
	if os.Getuid() != 0 {
		fatal("请使用 root 权限运行：sudo ./proxy-manager install")
	}
	selfPath, _ := filepath.Abs(os.Args[0])
	panelDir := filepath.Dir(selfPath)

	fmt.Println()
	fmt.Println("==============================================")
	fmt.Println("   Xray 代理 + Web 管理面板 一键安装 (Go)")
	fmt.Println("==============================================")
	fmt.Println()
	if !confirm("确认开始安装？") {
		fmt.Println("已取消。")
		return
	}
	fmt.Println()
	if err := doInstall(selfPath, panelDir, func(s string) { fmt.Println(s) }, false); err != nil {
		fatal(err.Error())
	}
}

func runUninstall() {
	if os.Getuid() != 0 {
		fatal("请使用 root 权限运行：sudo ./proxy-manager uninstall")
	}
	fmt.Println()
	fmt.Println("==============================================")
	fmt.Println("   Xray 代理 + Web 管理面板 卸载向导")
	fmt.Println("==============================================")
	fmt.Println()
	fmt.Println("  将要删除：")
	fmt.Println("  - systemd 服务：xray-proxy、xray-panel")
	fmt.Println("  - Xray 二进制：/usr/local/bin/xray")
	fmt.Println("  - Geo 数据文件：geoip.dat、geosite.dat")
	fmt.Println("  - Xray 配置：/etc/xray-config.json")
	fmt.Println()
	if !confirm("确认卸载？") {
		fmt.Println("已取消。")
		return
	}
	fmt.Println()
	if err := doUninstall(func(s string) { fmt.Println(s) }, false); err != nil {
		fatal(err.Error())
	}
}

// ── HTTP API：安装 / 卸载 ──────────────────────────────────────────────────────

func handleInstall(w http.ResponseWriter, r *http.Request) {
	// GET：查询安装进度
	if r.Method == http.MethodGet {
		installState.mu.Lock()
		resp := map[string]interface{}{
			"running": installState.running,
			"done":    installState.done,
			"ok":      installState.ok,
			"log":     installState.log.String(),
		}
		installState.mu.Unlock()
		jsonResponse(w, 200, resp)
		return
	}

	// POST：触发安装
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	installState.mu.Lock()
	if installState.running {
		installState.mu.Unlock()
		jsonResponse(w, 409, map[string]interface{}{"ok": false, "msg": "安装正在进行中"})
		return
	}
	installState.running = true
	installState.done = false
	installState.ok = false
	installState.log.Reset()
	installState.mu.Unlock()

	selfPath, _ := filepath.Abs(os.Args[0])
	panelDir := filepath.Dir(selfPath)

	go func() {
		err := doInstall(selfPath, panelDir, func(line string) { installState.write(line) }, true)
		installState.mu.Lock()
		installState.running = false
		installState.done = true
		installState.ok = err == nil
		if err != nil {
			installState.log.WriteString("[ERROR] " + err.Error() + "\n")
		}
		installState.mu.Unlock()
	}()

	jsonResponse(w, 200, map[string]interface{}{"ok": true, "msg": "安装已启动，通过 GET /api/install 查看进度"})
}

func handleUninstall(w http.ResponseWriter, r *http.Request) {
	// GET：查询卸载进度
	if r.Method == http.MethodGet {
		uninstallState.mu.Lock()
		resp := map[string]interface{}{
			"running": uninstallState.running,
			"done":    uninstallState.done,
			"ok":      uninstallState.ok,
			"log":     uninstallState.log.String(),
		}
		uninstallState.mu.Unlock()
		jsonResponse(w, 200, resp)
		return
	}

	// POST：触发卸载
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	uninstallState.mu.Lock()
	if uninstallState.running {
		uninstallState.mu.Unlock()
		jsonResponse(w, 409, map[string]interface{}{"ok": false, "msg": "卸载正在进行中"})
		return
	}
	uninstallState.running = true
	uninstallState.done = false
	uninstallState.ok = false
	uninstallState.log.Reset()
	uninstallState.mu.Unlock()

	go func() {
		err := doUninstall(func(line string) { uninstallState.write(line) }, true)
		uninstallState.mu.Lock()
		uninstallState.running = false
		uninstallState.done = true
		uninstallState.ok = err == nil
		if err != nil {
			uninstallState.log.WriteString("[ERROR] " + err.Error() + "\n")
		}
		uninstallState.mu.Unlock()
		// HTTP 模式：清理完成后延迟退出，让客户端有机会拿到最终状态
		time.Sleep(2 * time.Second)
		os.Exit(0)
	}()

	jsonResponse(w, 200, map[string]interface{}{"ok": true, "msg": "卸载已启动，通过 GET /api/uninstall 查看进度"})
}

// ── 主程序 ────────────────────────────────────────────────────────────────────

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "install":
			runInstall()
			return
		case "uninstall":
			runUninstall()
			return
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", handleStatus)
	mux.HandleFunc("/api/service/", handleService)
	mux.HandleFunc("/api/subscribe", handleSubscribe)
	mux.HandleFunc("/api/test", handleTest)
	mux.HandleFunc("/api/apply", handleApply)
	mux.HandleFunc("/api/settings", handleSettings)
	mux.HandleFunc("/api/log", handleLog)
	mux.HandleFunc("/api/install", handleInstall)
	mux.HandleFunc("/api/uninstall", handleUninstall)
	mux.HandleFunc("/", handleIndex)

	fmt.Println("==================================================")
	fmt.Println("  Xray 管理面板 (Go)")
	fmt.Println("  访问地址：http://0.0.0.0" + listenAddr)
	fmt.Println("==================================================")
	log.Fatal(http.ListenAndServe(listenAddr, mux))
}
