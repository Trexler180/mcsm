package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mcsm/api/internal/events"
	"github.com/mcsm/api/internal/store"
)

// Reconciling a server's mods with its disk lists every mod folder on the node,
// hashes the jars, looks them up on Modrinth and CurseForge and rewrites rows.
// That used to happen inside every GET /mods, so each Mods-tab load paid for
// it and concurrent loads raced each other's writes. Now the GET only reads;
// a rescan runs in the background when the last one is stale, at most one per
// server at a time, and POST /mods/rescan runs one on request.

// modRescanStaleAfter is how old the last rescan may be before a GET starts a
// new one in the background.
const modRescanStaleAfter = 60 * time.Second

// modRescanBudget bounds one rescan: listing, fingerprint lookups and the
// dependency-graph refresh together.
const modRescanBudget = 2 * time.Minute

type modRescans struct {
	mu       sync.Mutex
	inFlight map[string]chan struct{}
	last     map[string]time.Time
}

// modLocks serializes the changes to one server's mod jars and rows: an
// install, toggle, update or removal, and the rescan that reconciles rows with
// the disk. Without it a rescan that lists the folder while an install is
// between writing the jar and recording it adopts the jar as a second row for
// the same file, and one that lists it mid-rename prunes the row of a mod
// being turned off.
type modLocks struct {
	mu sync.Mutex
	m  map[string]*sync.Mutex
}

// lockMods holds the server's mod lock until the returned func is called.
func (h *ModHandlers) lockMods(serverID string) (unlock func()) {
	h.modLocks.mu.Lock()
	if h.modLocks.m == nil {
		h.modLocks.m = map[string]*sync.Mutex{}
	}
	l, ok := h.modLocks.m[serverID]
	if !ok {
		l = &sync.Mutex{}
		h.modLocks.m[serverID] = l
	}
	h.modLocks.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// rescanState is reported on GET /mods in X-Mods-Rescan.
const (
	rescanFresh   = "fresh"
	rescanStarted = "started"
	rescanRunning = "running"
)

// WithBus lets a finished rescan announce itself, so a live client can refetch.
func (h *ModHandlers) WithBus(bus *events.Bus) *ModHandlers {
	h.bus = bus
	return h
}

// startRescan begins a rescan unless one is running or the last is fresh
// (force skips the freshness check). The returned channel closes when the
// rescan that will cover this request finishes.
func (h *ModHandlers) startRescan(serverID string, force bool) (<-chan struct{}, string) {
	h.rescans.mu.Lock()
	defer h.rescans.mu.Unlock()
	if h.rescans.inFlight == nil {
		h.rescans.inFlight = map[string]chan struct{}{}
		h.rescans.last = map[string]time.Time{}
	}
	if ch, ok := h.rescans.inFlight[serverID]; ok {
		return ch, rescanRunning
	}
	if !force {
		if at, ok := h.rescans.last[serverID]; ok && time.Since(at) < modRescanStaleAfter {
			done := make(chan struct{})
			close(done)
			return done, rescanFresh
		}
	}
	done := make(chan struct{})
	h.rescans.inFlight[serverID] = done
	go h.rescan(serverID, done)
	return done, rescanStarted
}

func (h *ModHandlers) rescan(serverID string, done chan struct{}) {
	defer func() {
		h.rescans.mu.Lock()
		delete(h.rescans.inFlight, serverID)
		h.rescans.last[serverID] = time.Now()
		h.rescans.mu.Unlock()
		close(done)
	}()
	// Detached from any request: a closed tab must not leave a rescan half done.
	ctx, cancel := context.WithTimeout(context.Background(), modRescanBudget)
	defer cancel()

	h.reconcileLocked(ctx, serverID)
	mods, err := h.store.ListMods(ctx, serverID)
	if err != nil {
		slog.Warn("mods rescan: list", "server_id", serverID, "error", err)
		return
	}
	h.ensureDependencyGraph(ctx, serverID, mods)
	h.refreshTitles(ctx, mods)
	if h.bus != nil {
		h.bus.Publish(events.Event{Type: events.ServerUpdated, ServerID: serverID,
			Data: map[string]any{"what": "mods"}})
	}
}

// reconcileLocked reconciles under the server's mod lock, and not at all while
// an operation (a version change, an update run, a restore) is rewriting the
// server's files: those change jars and rows in steps a listing must not see
// half done. The next rescan picks up whatever they leave.
func (h *ModHandlers) reconcileLocked(ctx context.Context, serverID string) {
	defer h.lockMods(serverID)()
	if h.ops != nil {
		if err := h.ops.Guard(ctx, serverID); err != nil {
			return
		}
	}
	h.reconcileFromDisk(ctx, serverID)
}

// List returns the server's mods as the panel last saw them. When that view
// is stale it starts a rescan in the background and says so in X-Mods-Rescan;
// the result arrives on the next load, or as a server.updated event.
func (h *ModHandlers) List(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	_, state := h.startRescan(id, false)
	w.Header().Set("X-Mods-Rescan", state)
	h.writeModList(w, r, id)
}

// Rescan reconciles the server's mods with its disk now (joining a rescan
// already under way) and returns the result.
func (h *ModHandlers) Rescan(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	done, _ := h.startRescan(id, true)
	select {
	case <-done:
	case <-r.Context().Done():
		return
	}
	h.writeModList(w, r, id)
}

func (h *ModHandlers) writeModList(w http.ResponseWriter, r *http.Request, serverID string) {
	mods, err := h.store.ListMods(r.Context(), serverID)
	if err != nil {
		writeServerError(w, r, "list mods", err)
		return
	}
	if mods == nil {
		mods = []*store.InstalledMod{}
	}
	if err := h.annotateDependencies(r.Context(), serverID, mods); err != nil {
		writeServerError(w, r, "list mods: dependencies", err)
		return
	}
	writeJSON(w, http.StatusOK, mods)
}
