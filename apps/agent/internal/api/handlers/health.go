package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

var startTime = time.Now()

func Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": "0.1.0",
	})
}

func Info(w http.ResponseWriter, r *http.Request) {
	hostname, _ := os.Hostname()

	vm, _ := mem.VirtualMemory()
	du, _ := disk.Usage("/")
	hi, _ := host.Info()

	var uptime uint64
	if hi != nil {
		uptime = hi.Uptime
	}

	var memTotal, diskTotal uint64
	if vm != nil {
		memTotal = vm.Total / 1024 / 1024
	}
	if du != nil {
		diskTotal = du.Total / 1024 / 1024 / 1024
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"hostname":        hostname,
		"os":              runtime.GOOS,
		"arch":            runtime.GOARCH,
		"memory_mb":       memTotal,
		"disk_gb":         diskTotal,
		"cpu_cores":       runtime.NumCPU(),
		"uptime_seconds":  uptime,
		"agent_uptime_sec": int(time.Since(startTime).Seconds()),
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
