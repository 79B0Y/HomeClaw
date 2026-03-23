package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
)

// ── Config ────────────────────────────────────────────────────────────────────

const (
	listenAddr      = ":9090"
	proxyManagerURL = "http://localhost:8080"
	sysMgrURL       = "http://localhost:18080"
)

// hostIP is the machine's outbound LAN IP, detected once at startup.
// Used to rewrite "localhost" URLs so the browser can actually reach them.
var hostIP = detectHostIP()

func detectHostIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "localhost"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

// ServiceDef describes a managed system service.
type ServiceDef struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Unit         string `json:"-"` // systemd unit name
	Version      string `json:"version"`
	WebURL       string `json:"web_url"`
	Description  string `json:"description"`
	Color        string `json:"color"`
	InstallCmd   string `json:"-"` // shell command to install
	UninstallCmd string `json:"-"` // shell command to uninstall
}

var knownServices = []ServiceDef{
	{
		ID:           "home-assistant",
		Name:         "Home Assistant",
		Unit:         "home-assistant.service",
		Version:      "v2024.1.2",
		WebURL:       "http://localhost:8123",
		Description:  "Smart home automation platform",
		Color:        "#3ddc84",
		InstallCmd:   "pip3 install homeassistant && systemctl enable home-assistant",
		UninstallCmd: "systemctl disable home-assistant && pip3 uninstall -y homeassistant",
	},
	{
		ID:           "openclaw",
		Name:         "OpenClaw",
		Unit:         "openclaw.service",
		Version:      "v1.4.2-stable",
		WebURL:       "http://localhost:8765",
		Description:  "Home intelligence engine",
		Color:        "#4a9eff",
		InstallCmd:   "systemctl enable openclaw",
		UninstallCmd: "systemctl disable openclaw",
	},
	{
		ID:           "proxy-manager",
		Name:         "Proxy Manager",
		Unit:         "xray-proxy.service",
		Version:      xrayVersion(),
		WebURL:       "http://localhost:8080",
		Description:  "Network proxy service",
		Color:        "#a855f7",
		InstallCmd:   "bash -c \"$(curl -L https://github.com/XTLS/Xray-install/raw/main/install-release.sh)\" @ install && systemctl enable xray-proxy",
		UninstallCmd: "systemctl disable xray-proxy && bash -c \"$(curl -L https://github.com/XTLS/Xray-install/raw/main/install-release.sh)\" @ remove",
	},
	{
		ID:           "addon-manager",
		Name:         "HA Addons",
		Unit:         "addon-manager.service",
		Version:      "v5.3.1",
		WebURL:       "http://localhost:7080",
		Description:  "Home Assistant addon manager",
		Color:        "#f5a623",
		InstallCmd:   "systemctl enable addon-manager",
		UninstallCmd: "systemctl disable addon-manager",
	},
}

// ── Activity log ──────────────────────────────────────────────────────────────

type ActivityEvent struct {
	ID        int    `json:"id"`
	Icon      string `json:"icon"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
	Category  string `json:"category"` // info | success | warning | error
}

var (
	activityMu     sync.Mutex
	activityLog    []ActivityEvent
	activityNextID = 1
)

func logActivity(icon, message, category string) {
	activityMu.Lock()
	defer activityMu.Unlock()
	event := ActivityEvent{
		ID:        activityNextID,
		Icon:      icon,
		Message:   message,
		Timestamp: time.Now().Format(time.RFC3339),
		Category:  category,
	}
	activityNextID++
	activityLog = append([]ActivityEvent{event}, activityLog...)
	if len(activityLog) > 50 {
		activityLog = activityLog[:50]
	}
}

// ── systemctl helpers ─────────────────────────────────────────────────────────

// serviceInstalled checks whether a service is installed on the system.
// For proxy-manager it checks the xray binary; for others it checks the systemd unit file.
func serviceInstalled(id, unit string) bool {
	switch id {
	case "proxy-manager":
		for _, bin := range []string{"/usr/local/bin/xray", "/usr/bin/xray"} {
			if _, err := os.Stat(bin); err == nil {
				return true
			}
		}
		return false
	default:
		out, err := exec.Command("systemctl", "list-unit-files", "--no-legend", unit).Output()
		if err != nil {
			return false
		}
		return len(strings.TrimSpace(string(out))) > 0
	}
}

// serviceStatus queries the systemd unit status.
// Returns "running", "stopped", or "error".
func serviceStatus(unit string) string {
	out, err := exec.Command("systemctl", "is-active", unit).Output()
	if err != nil {
		// Not on systemd (e.g. macOS dev) — treat as unknown/stopped
		return "stopped"
	}
	switch strings.TrimSpace(string(out)) {
	case "active":
		return "running"
	case "inactive", "failed", "dead":
		return "stopped"
	default:
		return "stopped"
	}
}

// controlService runs systemctl start|stop|restart on the given unit.
func controlService(unit, action string) error {
	cmd := exec.Command("systemctl", action, unit)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s %s: %s", action, unit, strings.TrimSpace(string(out)))
	}
	return nil
}

// xrayVersion reads the installed xray binary version once at startup.
// Returns e.g. "25.2.21"; falls back to "unknown" if xray is not found.
func xrayVersion() string {
	for _, bin := range []string{"xray", "/usr/local/bin/xray", "/usr/bin/xray"} {
		out, err := exec.Command(bin, "version").Output()
		if err != nil {
			continue
		}
		// First line looks like: "Xray 25.2.21 (Xray, Penetrates Everything.) ..."
		line := strings.SplitN(string(out), "\n", 2)[0]
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			return parts[1]
		}
	}
	return "unknown"
}

// findService looks up a ServiceDef by its ID.
func findService(id string) (ServiceDef, bool) {
	for _, s := range knownServices {
		if s.ID == id {
			return s, true
		}
	}
	return ServiceDef{}, false
}

// ── Resource cache ────────────────────────────────────────────────────────────

// Sampling intervals — adjust to taste.
const (
	resourcePollInterval = 10 * time.Second // CPU + memory
	diskPollInterval     = 60 * time.Second // disk changes slowly
	servicePollInterval  = 15 * time.Second // service status via systemctl
)

type resourceSnapshot struct {
	CPUPercent     float64
	MemoryPercent  float64
	MemoryUsedMB   uint64
	MemoryTotalMB  uint64
	StoragePercent float64
	StorageUsedGB  float64
	StorageTotalGB float64
	CollectedAt    time.Time
}

type serviceSnapshot struct {
	Statuses    map[string]string // id → "running"|"stopped"
	Installed   map[string]bool   // id → installed on system
	CollectedAt time.Time
}

var (
	resMu    sync.RWMutex
	resCache resourceSnapshot

	svcMu    sync.RWMutex
	svcCache serviceSnapshot
)

// startPollers launches background goroutines that refresh caches on fixed intervals.
// An initial sample is taken synchronously so the first API response is never empty.
func startPollers() {
	// Initial samples (CPU uses a brief 200ms window just for startup).
	resCache = sampleResources(200 * time.Millisecond)
	svcCache = sampleServices()

	go func() {
		t := time.NewTicker(resourcePollInterval)
		dt := time.NewTicker(diskPollInterval)
		defer t.Stop()
		defer dt.Stop()
		for {
			select {
			case <-t.C:
				// cpu.Percent with interval=0 returns delta since last call — zero blocking.
				snap := sampleResources(0)
				resMu.Lock()
				// Preserve disk values on CPU/mem-only ticks.
				snap.StoragePercent = resCache.StoragePercent
				snap.StorageUsedGB = resCache.StorageUsedGB
				snap.StorageTotalGB = resCache.StorageTotalGB
				resCache = snap
				resMu.Unlock()
			case <-dt.C:
				diskStat, err := disk.Usage("/")
				if err == nil {
					resMu.Lock()
					resCache.StoragePercent = round2(diskStat.UsedPercent)
					resCache.StorageUsedGB = round2(float64(diskStat.Used) / 1e9)
					resCache.StorageTotalGB = round2(float64(diskStat.Total) / 1e9)
					resMu.Unlock()
				}
			}
		}
	}()

	go func() {
		t := time.NewTicker(servicePollInterval)
		defer t.Stop()
		for range t.C {
			snap := sampleServices()
			svcMu.Lock()
			svcCache = snap
			svcMu.Unlock()
		}
	}()

	// Proxy Manager state watcher — poll every 20s for node/IP changes.
	go func() {
		pollProxyManagerState() // initial baseline
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for range t.C {
			pollProxyManagerState()
		}
	}()
}

// sampleResources does a one-shot CPU+memory+disk read.
// cpuInterval=0 means non-blocking delta since last call (requires a prior call).
func sampleResources(cpuInterval time.Duration) resourceSnapshot {
	snap := resourceSnapshot{CollectedAt: time.Now()}

	if pcts, err := cpu.Percent(cpuInterval, false); err == nil && len(pcts) > 0 {
		snap.CPUPercent = round2(pcts[0])
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		snap.MemoryPercent = round2(vm.UsedPercent)
		snap.MemoryUsedMB = vm.Used / 1024 / 1024
		snap.MemoryTotalMB = vm.Total / 1024 / 1024
	}
	if d, err := disk.Usage("/"); err == nil {
		snap.StoragePercent = round2(d.UsedPercent)
		snap.StorageUsedGB = round2(float64(d.Used) / 1e9)
		snap.StorageTotalGB = round2(float64(d.Total) / 1e9)
	}
	return snap
}

func sampleServices() serviceSnapshot {
	statuses  := make(map[string]string, len(knownServices))
	installed := make(map[string]bool,   len(knownServices))
	for _, s := range knownServices {
		statuses[s.ID]  = serviceStatus(s.Unit)
		installed[s.ID] = serviceInstalled(s.ID, s.Unit)
	}
	return serviceSnapshot{Statuses: statuses, Installed: installed, CollectedAt: time.Now()}
}

// ── JSON helpers ──────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// ── Middleware ────────────────────────────────────────────────────────────────

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

// ── Handlers ──────────────────────────────────────────────────────────────────

// GET /api/overview
// Returns aggregated stats shown in the top stat cards.
func handleOverview(w http.ResponseWriter, r *http.Request) {
	svcMu.RLock()
	snap := svcCache
	svcMu.RUnlock()

	running := 0
	for _, st := range snap.Statuses {
		if st == "running" {
			running++
		}
	}

	type Overview struct {
		ServicesRunning int    `json:"services_running"`
		ServicesTotal   int    `json:"services_total"`
		UptimeSeconds   int64  `json:"uptime_seconds"`
		CollectedAt     string `json:"collected_at"`
	}

	writeJSON(w, http.StatusOK, Overview{
		ServicesRunning: running,
		ServicesTotal:   len(knownServices),
		UptimeSeconds:   getSystemUptimeSeconds(),
		CollectedAt:     snap.CollectedAt.Format(time.RFC3339),
	})
}

// GET /api/resources
// Returns cached system resource usage percentages (refreshed by background poller).
func handleResources(w http.ResponseWriter, r *http.Request) {
	resMu.RLock()
	snap := resCache
	resMu.RUnlock()

	type Resources struct {
		CPUPercent       float64 `json:"cpu_pct"`
		MemoryPercent    float64 `json:"mem_pct"`
		StoragePercent   float64 `json:"storage_pct"`
		MemoryUsedMB     uint64  `json:"mem_used_mb"`
		MemoryTotalMB    uint64  `json:"mem_total_mb"`
		StorageUsedGB    float64 `json:"storage_used_gb"`
		StorageTotalGB   float64 `json:"storage_total_gb"`
		TokensMonthlyPct float64 `json:"tokens_monthly_pct"`
		CollectedAt      string  `json:"collected_at"`
		TokensNote       string  `json:"tokens_note,omitempty"`
	}

	writeJSON(w, http.StatusOK, Resources{
		CPUPercent:       snap.CPUPercent,
		MemoryPercent:    snap.MemoryPercent,
		StoragePercent:   snap.StoragePercent,
		MemoryUsedMB:     snap.MemoryUsedMB,
		MemoryTotalMB:    snap.MemoryTotalMB,
		StorageUsedGB:    snap.StorageUsedGB,
		StorageTotalGB:   snap.StorageTotalGB,
		TokensMonthlyPct: 0,
		CollectedAt:      snap.CollectedAt.Format(time.RFC3339),
		TokensNote:       "Token data unavailable — token-service not configured",
	})
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}

// GET /api/services
// Returns the list of all services with their cached status.
func handleListServices(w http.ResponseWriter, r *http.Request) {
	svcMu.RLock()
	snap := svcCache
	svcMu.RUnlock()

	type ServiceInfo struct {
		ServiceDef
		Status    string `json:"status"`
		Installed bool   `json:"installed"`
	}

	result := make([]ServiceInfo, 0, len(knownServices))
	for _, s := range knownServices {
		result = append(result, ServiceInfo{
			ServiceDef: s,
			Status:     snap.Statuses[s.ID],
			Installed:  snap.Installed[s.ID],
		})
	}
	writeJSON(w, http.StatusOK, result)
}

// POST /api/services/{id}/start
// POST /api/services/{id}/stop
// POST /api/services/{id}/restart
func handleServiceAction(w http.ResponseWriter, r *http.Request) {
	// Path: /api/services/{id}/{action}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// parts: ["api", "services", "{id}", "{action}"]
	if len(parts) != 4 {
		writeError(w, http.StatusBadRequest, "invalid path")
		return
	}
	serviceID := parts[2]
	action := parts[3]

	if action != "start" && action != "stop" && action != "restart" {
		writeError(w, http.StatusBadRequest, "action must be start, stop, or restart")
		return
	}

	svc, ok := findService(serviceID)
	if !ok {
		writeError(w, http.StatusNotFound, "service not found: "+serviceID)
		return
	}

	// For proxy-manager, also forward control through its own API
	if serviceID == "proxy-manager" {
		if err := forwardProxyManagerAction(action); err != nil {
			log.Printf("proxy-manager API forward failed: %v (continuing with systemctl)", err)
		}
	} else {
		if err := controlService(svc.Unit, action); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			logActivity("❌", fmt.Sprintf("%s %s failed: %v", action, svc.Name, err), "error")
			return
		}
	}

	logActivity(actionIcon(action), fmt.Sprintf("%s %sed successfully", svc.Name, action), "success")
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "success",
		"message": fmt.Sprintf("Service %s %sed successfully", svc.Name, action),
	})
}

func actionIcon(action string) string {
	switch action {
	case "start":
		return "▶️"
	case "stop":
		return "⏹️"
	case "restart":
		return "🔄"
	}
	return "⚙️"
}

// GET /api/services/{id}/open
// Returns the web UI URL, with "localhost" replaced by the host's LAN IP
// so that browsers on other machines can actually reach the service.
func handleServiceOpen(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 4 {
		writeError(w, http.StatusBadRequest, "invalid path")
		return
	}
	serviceID := parts[2]

	svc, ok := findService(serviceID)
	if !ok {
		writeError(w, http.StatusNotFound, "service not found: "+serviceID)
		return
	}

	url := strings.ReplaceAll(svc.WebURL, "localhost", hostIP)
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// POST /api/services/{id}/install
func handleServiceInstall(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 4 {
		writeError(w, http.StatusBadRequest, "invalid path")
		return
	}
	svc, ok := findService(parts[2])
	if !ok {
		writeError(w, http.StatusNotFound, "service not found: "+parts[2])
		return
	}
	if svc.InstallCmd == "" {
		writeError(w, http.StatusNotImplemented, "no install command configured for "+svc.Name)
		return
	}
	out, err := exec.Command("bash", "-c", svc.InstallCmd).CombinedOutput()
	if err != nil {
		logActivity("❌", fmt.Sprintf("%s install failed: %v", svc.Name, err), "error")
		writeError(w, http.StatusInternalServerError, string(out))
		return
	}
	logActivity("📦", fmt.Sprintf("%s installed successfully", svc.Name), "success")
	writeJSON(w, http.StatusOK, map[string]string{"status": "success", "output": string(out)})
}

// POST /api/services/{id}/uninstall
func handleServiceUninstall(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 4 {
		writeError(w, http.StatusBadRequest, "invalid path")
		return
	}
	svc, ok := findService(parts[2])
	if !ok {
		writeError(w, http.StatusNotFound, "service not found: "+parts[2])
		return
	}
	if svc.UninstallCmd == "" {
		writeError(w, http.StatusNotImplemented, "no uninstall command configured for "+svc.Name)
		return
	}
	out, err := exec.Command("bash", "-c", svc.UninstallCmd).CombinedOutput()
	if err != nil {
		logActivity("❌", fmt.Sprintf("%s uninstall failed: %v", svc.Name, err), "error")
		writeError(w, http.StatusInternalServerError, string(out))
		return
	}
	logActivity("🗑️", fmt.Sprintf("%s uninstalled successfully", svc.Name), "success")
	writeJSON(w, http.StatusOK, map[string]string{"status": "success", "output": string(out)})
}

// GET /api/activity
// Returns recent activity events.
func handleActivity(w http.ResponseWriter, r *http.Request) {
	activityMu.Lock()
	events := make([]ActivityEvent, len(activityLog))
	copy(events, activityLog)
	activityMu.Unlock()
	writeJSON(w, http.StatusOK, events)
}

// ── Proxy Manager passthrough ─────────────────────────────────────────────────

// forwardProxyManagerAction calls the proxy-manager's own /api/service/{action}.
func forwardProxyManagerAction(action string) error {
	url := fmt.Sprintf("%s/api/service/%s", proxyManagerURL, action)
	resp, err := http.Post(url, "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("proxy-manager returned %d: %s", resp.StatusCode, body)
	}
	return nil
}

// GET /api/proxy/status
// Fetches live status from the proxy-manager service.
func handleProxyStatus(w http.ResponseWriter, r *http.Request) {
	resp, err := http.Get(proxyManagerURL + "/api/status")
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "proxy-manager unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

// ── Proxy Manager watcher ─────────────────────────────────────────────────────

type proxyNodeState struct {
	Active     bool
	Address    string
	Port       int
	OutboundIP string
}

var (
	pmStateMu   sync.Mutex
	pmLastState *proxyNodeState
)

// pollProxyManagerState fetches /api/status from the proxy manager and logs
// any meaningful changes (node switch, IP change, active toggle).
func pollProxyManagerState() {
	resp, err := http.Get(proxyManagerURL + "/api/status")
	if err != nil {
		return
	}
	defer resp.Body.Close()

	var s struct {
		Active bool `json:"active"`
		Node   struct {
			Address string `json:"address"`
			Port    int    `json:"port"`
		} `json:"node"`
		OutboundIP string `json:"outbound_ip"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return
	}

	pmStateMu.Lock()
	defer pmStateMu.Unlock()

	cur := &proxyNodeState{
		Active:     s.Active,
		Address:    s.Node.Address,
		Port:       s.Node.Port,
		OutboundIP: s.OutboundIP,
	}

	if pmLastState == nil {
		if s.Active && s.Node.Address != "" {
			logActivity("🌐", fmt.Sprintf("Proxy Manager active — node %s:%d, outbound IP %s",
				s.Node.Address, s.Node.Port, s.OutboundIP), "info")
		}
		pmLastState = cur
		return
	}

	prev := pmLastState

	if cur.Active != prev.Active {
		if cur.Active {
			logActivity("▶️", "Proxy Manager started — proxy is now active", "success")
		} else {
			logActivity("⏹️", "Proxy Manager stopped — proxy is inactive", "warning")
		}
	}
	if cur.Address != prev.Address && cur.Address != "" {
		logActivity("🔄", fmt.Sprintf("Proxy node switched: %s → %s", prev.Address, cur.Address), "info")
	} else if cur.Port != prev.Port && prev.Port != 0 {
		logActivity("🔌", fmt.Sprintf("Proxy node port changed: %s:%d → %s:%d",
			cur.Address, prev.Port, cur.Address, cur.Port), "info")
	}
	if cur.OutboundIP != prev.OutboundIP && prev.OutboundIP != "" {
		logActivity("🌍", fmt.Sprintf("Outbound IP changed: %s → %s", prev.OutboundIP, cur.OutboundIP), "info")
	}

	pmLastState = cur
}

// getSystemUptimeSeconds reads uptime from /proc/uptime (Linux).
func getSystemUptimeSeconds() int64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	var secs float64
	if _, err := fmt.Sscanf(string(data), "%f", &secs); err != nil {
		return 0
	}
	return int64(secs)
}

// ── Health ────────────────────────────────────────────────────────────────────

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"time":   time.Now().Format(time.RFC3339),
	})
}

// ── Router ────────────────────────────────────────────────────────────────────

func newRouter() http.Handler {
	mux := http.NewServeMux()

	// ── Static pages ──────────────────────────────────────────────────────────
	// Serve HTML files from the same directory as the binary.
	serveHTML := func(filename string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, filename)
		}
	}
	mux.HandleFunc("/", serveHTML("home.html"))
	mux.HandleFunc("/home", serveHTML("home.html"))
	mux.HandleFunc("/login", serveHTML("login.html"))
	mux.HandleFunc("/billing", serveHTML("billing.html"))
	mux.HandleFunc("/topup", serveHTML("topup.html"))
	mux.HandleFunc("/usage", serveHTML("usage.html"))

	// ── API ───────────────────────────────────────────────────────────────────
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/api/overview", handleOverview)
	mux.HandleFunc("/api/resources", handleResources)
	mux.HandleFunc("/api/proxy/status", handleProxyStatus)
	mux.HandleFunc("/api/activity", handleActivity)

	// /api/services           → GET  list
	// /api/services/{id}/start|stop|restart → POST action
	// /api/services/{id}/open → GET  open URL
	mux.HandleFunc("/api/services", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		handleListServices(w, r)
	})

	mux.HandleFunc("/api/services/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 4 {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		action := parts[3]
		switch {
		case action == "open" && r.Method == http.MethodGet:
			handleServiceOpen(w, r)
		case action == "install" && r.Method == http.MethodPost:
			handleServiceInstall(w, r)
		case action == "uninstall" && r.Method == http.MethodPost:
			handleServiceUninstall(w, r)
		case (action == "start" || action == "stop" || action == "restart") && r.Method == http.MethodPost:
			handleServiceAction(w, r)
		default:
			writeError(w, http.StatusNotFound, "not found")
		}
	})

	// ── System-config mgr proxy: /api/sys/* → localhost:18080/api/v1/* ───────
	mux.HandleFunc("/api/sys/", handleSysMgrProxy)

	return corsMiddleware(mux)
}

// handleSysMgrProxy forwards /api/sys/{tail} to sysMgrURL/api/v1/{tail},
// preserving method, body, and query string.
func handleSysMgrProxy(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/api/sys")
	target := sysMgrURL + "/api/v1" + tail
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	req, err := http.NewRequest(r.Method, target, r.Body)
	if err != nil {
		writeError(w, http.StatusBadGateway, "proxy build request: "+err.Error())
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}

	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "sys-mgr unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	logActivity("🚀", "Dashboard service started", "info")

	// Start background pollers before accepting requests.
	startPollers()

	router := newRouter()
	log.Printf("Dashboard service listening on %s", listenAddr)
	if err := http.ListenAndServe(listenAddr, router); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
