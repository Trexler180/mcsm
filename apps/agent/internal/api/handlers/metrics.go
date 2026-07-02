package handlers

import (
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"
	"github.com/mcsm/agent/internal/metrics"
	"github.com/mcsm/agent/internal/process"
)

type MetricsHandlers struct {
	mgr       *process.Manager
	collector *metrics.Collector
}

func NewMetricsHandlers(mgr *process.Manager, collector *metrics.Collector) *MetricsHandlers {
	return &MetricsHandlers{mgr: mgr, collector: collector}
}

// Stats returns a one-shot snapshot of a server's runtime stats plus its live
// online-player roster, for the panel's history sampler. Unlike ServerMetrics
// (a WebSocket stream for a watching browser), this costs one request and reads
// only passively-tracked state — no console command is sent.
func (h *MetricsHandlers) Stats(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	info := h.mgr.Status(id)

	out := map[string]any{"status": info.Status}
	if info.PID > 0 {
		if stats, err := h.collector.Process(int32(info.PID)); err == nil {
			out["cpu_percent"] = stats.CPUPct
			out["ram_used_mb"] = stats.RAMMb
		}
	}
	if host, err := h.collector.Host("/"); err == nil {
		out["ram_total_mb"] = host.RAMTotalMb
	}

	players := h.mgr.Players(id)
	list := make([]map[string]string, 0, len(players))
	for _, p := range players {
		if !p.Online {
			continue
		}
		list = append(list, map[string]string{"name": p.Name, "uuid": p.UUID})
	}
	out["players"] = list

	writeJSON(w, http.StatusOK, out)
}

func (h *MetricsHandlers) ServerMetrics(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	ctx := r.Context()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			info := h.mgr.Status(id)
			stats, err := h.collector.Process(int32(info.PID))
			if err != nil {
				continue
			}
			// Frontend graphs RAM as a fraction of host total, so include it
			// alongside the per-process CPU/RAM figures.
			var ramTotalMb uint64
			if host, err := h.collector.Host("/"); err == nil {
				ramTotalMb = host.RAMTotalMb
			}
			if err := wsjson.Write(ctx, conn, map[string]any{
				"type": "metrics",
				"data": map[string]any{
					"cpu_percent":  stats.CPUPct,
					"ram_used_mb":  stats.RAMMb,
					"ram_total_mb": ramTotalMb,
					"net_rx_bps":   stats.NetRxBps,
					"net_tx_bps":   stats.NetTxBps,
				},
			}); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (h *MetricsHandlers) HostMetrics(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	ctx := r.Context()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			stats, err := h.collector.Host("/")
			if err != nil {
				continue
			}
			if err := wsjson.Write(ctx, conn, map[string]any{
				"type": "host",
				"data": stats,
			}); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}
