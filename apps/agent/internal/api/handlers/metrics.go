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
