package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/mcsm/agent/internal/isolation"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

var startTime = time.Now()

// BootID identifies this agent process. It changes on every restart, so the
// panel can tell when anything it cached about the agent is stale.
var BootID = func() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}()

// DiskPath is where disk usage is measured: the server root, where worlds and
// backups live, rather than whatever filesystem holds "/". Set from main.
var DiskPath = "/"

// Capabilities name the agent features the panel may rely on. The panel checks
// this list rather than the version, so an older agent degrades gracefully.
var Capabilities = []string{"batch_status", "exit_cause", "persistent_registry", "server_logs", "world_jobs", "isolation"}

func init() {
	// Prime the CPU sampler: cpu.Percent(0, ...) reports usage since the
	// previous call, so the first un-primed call would always return 0.
	_, _ = cpu.Percent(0, false)
}

// Version identifies the running build (git revision or "dev"). Set once from
// main at startup; read-only afterwards.
var Version = "dev"

func Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": Version,
	})
}

func Info(w http.ResponseWriter, r *http.Request) {
	hostname, _ := os.Hostname()

	vm, _ := mem.VirtualMemory()
	du, _ := disk.Usage(DiskPath)
	hi, _ := host.Info()

	var uptime uint64
	if hi != nil {
		uptime = hi.Uptime
	}

	var memTotal, memUsed, diskTotal, diskUsed uint64
	if vm != nil {
		memTotal = vm.Total / 1024 / 1024
		memUsed = vm.Used / 1024 / 1024
	}
	if du != nil {
		diskTotal = du.Total / 1024 / 1024 / 1024
		diskUsed = du.Used / 1024 / 1024 / 1024
	}

	// Usage since the previous call (the panel polls on a fixed interval, so
	// this averages over roughly one poll window).
	var cpuPct float64
	if pcts, err := cpu.Percent(0, false); err == nil && len(pcts) > 0 {
		cpuPct = pcts[0]
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"hostname":         hostname,
		"os":               runtime.GOOS,
		"arch":             runtime.GOARCH,
		"memory_mb":        memTotal,
		"mem_used_mb":      memUsed,
		"disk_gb":          diskTotal,
		"disk_used_gb":     diskUsed,
		"cpu_cores":        runtime.NumCPU(),
		"cpu_pct":          cpuPct,
		"uptime_seconds":   uptime,
		"agent_uptime_sec": int(time.Since(startTime).Seconds()),
		"version":          Version,
		"boot_id":          BootID,
		"capabilities":     Capabilities,
		"isolation":        isolation.Current(),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
