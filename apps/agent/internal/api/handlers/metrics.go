package handlers

import (
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"
	"github.com/mcsm/agent/internal/link"
	"github.com/mcsm/agent/internal/metrics"
	"github.com/mcsm/agent/internal/process"
)

type MetricsHandlers struct {
	mgr       *process.Manager
	collector *metrics.Collector
	// sink is the helper mod's latest state, used to put tick rate on the same
	// stream as CPU and RAM. Nil is valid and simply means no tick fields.
	sink *link.MemorySink
}

func NewMetricsHandlers(mgr *process.Manager, collector *metrics.Collector, sink *link.MemorySink) *MetricsHandlers {
	return &MetricsHandlers{mgr: mgr, collector: collector, sink: sink}
}

// tickStaleAfter is four heartbeats — the same deadline link.idleTimeout uses to
// evict a session that has gone silent, and the same one the panel calls stale.
// Past it the last snapshot is no longer worth graphing as if it were current.
const tickStaleAfter = 60 * time.Second

// tickFields reports the helper mod's tick rate for a server, if there is one
// recent enough to plot.
//
// seq counts snapshots received, and is what lets the browser tell a genuinely
// new reading from the same one restated: the metrics stream ticks every 2s
// while the mod reports every 15s, so most frames carry a repeat. Graphing those
// repeats would draw a staircase that implies a resolution the data does not
// have. A counter rather than SnapshotAt because two snapshots landing in one
// millisecond would share a timestamp, and one would be dropped in silence.
//
// Deliberately not gated on Connected. A brief reconnect leaves a snapshot that
// is seconds old and perfectly good, and blanking the graph for it would be
// noisier than the reconnect — the same call vitals.go makes.
func tickFields(sink *link.MemorySink, serverID string, now time.Time) (tps float64, seq uint64, ok bool) {
	if sink == nil {
		return 0, 0, false
	}
	st, found := sink.State(serverID)
	if !found || !st.SnapshotSeen {
		return 0, 0, false
	}
	if now.Sub(st.SnapshotAt) > tickStaleAfter {
		return 0, 0, false
	}
	return st.Snapshot.TPS.M1, st.SnapshotSeq, true
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

	// Fold helper-mod vitals into the same one-shot payload. The API history
	// sampler used to make a second HTTP request per running server for this
	// in-memory state; co-locating it halves that minute-by-minute agent traffic.
	if h.sink != nil {
		if st, ok := h.sink.State(id); ok {
			vitals := map[string]any{"linked": st.Connected}
			if st.SnapshotSeen {
				vitals["tps"] = st.Snapshot.TPS
				vitals["mspt"] = st.Snapshot.MSPT
			}
			out["vitals"] = vitals
		}
	}

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
			data := map[string]any{
				"cpu_percent":  stats.CPUPct,
				"ram_used_mb":  stats.RAMMb,
				"ram_total_mb": ramTotalMb,
				"net_rx_bps":   stats.NetRxBps,
				"net_tx_bps":   stats.NetTxBps,
			}
			// Only present when the helper mod is reporting, so a vanilla
			// server's frame stays byte-for-byte what it has always been and its
			// dashboard keeps the layout it has always had.
			if tps, seq, ok := tickFields(h.sink, id, time.Now()); ok {
				data["tps"] = tps
				data["tick_seq"] = seq
			}
			if err := wsjson.Write(ctx, conn, map[string]any{
				"type": "metrics",
				"data": data,
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
