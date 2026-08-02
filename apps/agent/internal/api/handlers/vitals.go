package handlers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/agent/internal/link"
)

// VitalsHandlers serves the helper mod's live telemetry for a server.
//
// This is a read of in-memory state, not a proxy into the JVM: the mod pushes a
// snapshot every heartbeat and the sink keeps the newest one. Polling this
// endpoint therefore costs the Minecraft server nothing, no matter how many
// dashboards are open.
type VitalsHandlers struct {
	sink *link.MemorySink
}

func NewVitalsHandlers(sink *link.MemorySink) *VitalsHandlers {
	return &VitalsHandlers{sink: sink}
}

// Vitals reports the latest snapshot for a server.
//
// The response always answers two questions separately: is the mod connected
// right now (`linked`), and is there snapshot data at all. A brief reconnect
// keeps serving the last snapshot with linked=false rather than blanking the
// panel, and `snapshot_age_ms` lets the UI decide when data is too old to show.
func (h *VitalsHandlers) Vitals(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if h.sink == nil {
		writeJSON(w, http.StatusOK, map[string]any{"linked": false})
		return
	}

	st, ok := h.sink.State(id)
	if !ok || !st.SnapshotSeen {
		writeJSON(w, http.StatusOK, map[string]any{"linked": ok && st.Connected})
		return
	}

	snap := st.Snapshot
	writeJSON(w, http.StatusOK, map[string]any{
		"linked":          st.Connected,
		"mod_version":     st.Hello.ModVersion,
		"snapshot_age_ms": time.Since(st.SnapshotAt).Milliseconds(),
		"uptime_ms":       snap.UptimeMS,
		"tps":             snap.TPS,
		"mspt":            snap.MSPT,
		"heap":            snap.Heap,
		"chunks":          snap.Chunks.Loaded,
		"entities":        snap.Entities.Total,
		"players": map[string]any{
			"online": snap.Players.Online,
			"max":    snap.Players.Max,
		},
	})
}
