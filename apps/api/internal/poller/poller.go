// Package poller keeps the panel's view of each node and server fresh by
// asking the agents on a fixed interval, and turns what changed into status
// updates, alerts and live events.
//
// The agent is the authority on how a run ended — it saw the process exit —
// so the poller trusts its exit cause rather than inferring a crash from the
// panel's own records. And a node that stops answering makes its servers
// "unknown", never "crashed": not being able to see a server is not the same
// as the server being down.
package poller

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/events"
	"github.com/mcsm/api/internal/notify"
	"github.com/mcsm/api/internal/store"
)

const (
	pollInterval  = 15 * time.Second
	perCallBudget = 5 * time.Second
	// nodeFreshWindow mirrors the overview's notion of a live node: not heard
	// from within this window means offline. A deploy that restarts an agent
	// takes a few seconds, well inside it, so it changes nothing.
	nodeFreshWindow = 45 * time.Second
	// maxConcurrentNodes bounds how many nodes are polled at once.
	maxConcurrentNodes = 8
)

// Poller holds what persists between sweeps.
type Poller struct {
	store  *store.Store
	engine *notify.Engine
	bus    *events.Bus

	denials  *denialTracker
	sampler  *sampler
	sessions *sessionTracker

	mu      sync.Mutex
	bootIDs map[string]string // node id -> last seen agent boot id
	stateMu sync.Mutex
	tick    int
}

// New builds a poller. engine and bus may be nil.
func New(s *store.Store, engine *notify.Engine, bus *events.Bus) *Poller {
	return &Poller{
		store: s, engine: engine, bus: bus,
		denials:  newDenialTracker(),
		sampler:  newSampler(),
		sessions: newSessionTracker(s),
		bootIDs:  map[string]string{},
	}
}

// Run blocks until ctx is done. Spawn it in its own goroutine.
func Run(ctx context.Context, s *store.Store, engine *notify.Engine, bus *events.Bus) {
	New(s, engine, bus).Run(ctx)
}

func (p *Poller) Run(ctx context.Context) {
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	// First sweep immediately so the UI is correct on boot.
	p.Sweep(ctx)
	for {
		select {
		case <-t.C:
			p.Sweep(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// Sweep polls every node once, concurrently, and samples resource history on
// a slower cadence.
func (p *Poller) Sweep(ctx context.Context) {
	nodes, err := p.store.ListNodes(ctx)
	if err != nil {
		return
	}
	servers, err := p.store.ListServers(ctx)
	if err != nil {
		return
	}
	byNode := map[string][]*store.Server{}
	live := make(map[string]struct{}, len(servers))
	for _, srv := range servers {
		byNode[srv.NodeID] = append(byNode[srv.NodeID], srv)
		live[srv.ID] = struct{}{}
	}

	sem := make(chan struct{}, maxConcurrentNodes)
	var wg sync.WaitGroup
	for _, node := range nodes {
		node := node
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			p.pollNode(ctx, node, byNode[node.ID])
		}()
	}
	wg.Wait()

	p.stateMu.Lock()
	p.denials.reap(live)
	sample := p.tick%sampleEvery == 0
	p.tick++
	p.stateMu.Unlock()

	if sample {
		p.sampler.sampleAll(ctx, p.store, p.engine, p.bus, p.observeSessions)
	}
}

func (p *Poller) observeSessions(ctx context.Context, srv *store.Server, stats *agent.ServerStats) {
	joined, left := p.sessions.observe(ctx, srv, stats)
	if p.bus != nil && (len(joined) > 0 || len(left) > 0) {
		online := 0
		if stats != nil {
			online = len(stats.Players)
		}
		p.bus.Publish(events.Event{Type: events.ServerPlayers, ServerID: srv.ID, Data: map[string]any{
			"joined": joined, "left": left, "online": online,
		}})
	}
}

func (p *Poller) pollNode(ctx context.Context, node *store.Node, servers []*store.Server) {
	wasOnline := node.LastSeen != nil && time.Since(*node.LastSeen) < nodeFreshWindow
	c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)

	callCtx, cancel := context.WithTimeout(ctx, perCallBudget)
	info, err := c.Info(callCtx)
	cancel()
	if err != nil {
		if wasOnline {
			p.engine.Emit(notify.NodeOffline(node.ID, node.Name))
			p.publishNode(node, false, nil)
		} else {
			// Unreachable for longer than a restart takes: the panel can no
			// longer vouch for what its servers are doing.
			for _, srv := range servers {
				p.markUnknown(ctx, srv)
			}
		}
		return
	}

	hb := store.NodeHeartbeat{
		MemoryMb:      intFromInfo(info, "memory_mb"),
		DiskGb:        intFromInfo(info, "disk_gb"),
		CPUCores:      intFromInfo(info, "cpu_cores"),
		MemUsedMb:     intFromInfo(info, "mem_used_mb"),
		DiskUsedGb:    intFromInfo(info, "disk_used_gb"),
		CPUPct:        floatFromInfo(info, "cpu_pct"),
		UptimeSeconds: int64FromInfo(info, "uptime_seconds"),
		OS:            strFromInfo(info, "os"),
		Arch:          strFromInfo(info, "arch"),
		AgentVersion:  strFromInfo(info, "version"),
	}
	if iso, ok := info["isolation"]; ok && iso != nil {
		if raw, err := json.Marshal(iso); err == nil {
			hb.Isolation = raw
		}
	}
	if err := p.store.UpdateNodeHeartbeat(ctx, node.ID, hb); err != nil {
		log.Printf("poller: update node heartbeat %s: %v", node.ID, err)
	}
	// A new boot id means the agent restarted: forget which directories this
	// process told it about, so the next request re-sends them if needed.
	if boot := strFromInfo(info, "boot_id"); boot != nil {
		p.mu.Lock()
		prev, seen := p.bootIDs[node.ID]
		p.bootIDs[node.ID] = *boot
		p.mu.Unlock()
		if seen && prev != *boot {
			agent.ForgetRegistrations(c.BaseURL)
		}
	}
	if !wasOnline {
		p.engine.Emit(notify.NodeOnline(node.ID, node.Name))
	}
	p.publishNode(node, true, &hb)

	callCtx, cancel = context.WithTimeout(ctx, perCallBudget)
	statuses, err := c.AllStatus(callCtx)
	cancel()
	if errors.Is(err, agent.ErrNotSupported) {
		statuses, err = legacyStatuses(ctx, c, servers)
	}
	if err != nil {
		return // the node answered /info but not this; try again next sweep
	}
	for _, srv := range servers {
		st, ok := statuses[srv.ID]
		if !ok {
			// The agent has no run of this server since it started.
			st = agent.AgentStatus{ID: srv.ID, Status: "offline"}
		}
		p.apply(ctx, srv, st)
	}
}

// legacyStatuses asks an agent that predates the batch endpoint one server at
// a time.
func legacyStatuses(ctx context.Context, c *agent.Client, servers []*store.Server) (map[string]agent.AgentStatus, error) {
	out := map[string]agent.AgentStatus{}
	for _, srv := range servers {
		callCtx, cancel := context.WithTimeout(ctx, perCallBudget)
		raw, err := c.GetStatus(callCtx, srv.ID)
		cancel()
		if err != nil {
			return nil, err
		}
		blob, _ := json.Marshal(raw)
		var st agent.AgentStatus
		_ = json.Unmarshal(blob, &st)
		out[srv.ID] = st
	}
	return out, nil
}

// markUnknown records that a server's node has gone quiet.
func (p *Poller) markUnknown(ctx context.Context, srv *store.Server) {
	switch srv.Status {
	case "unknown", "offline", "crashed", "startup_failure":
		return
	}
	if err := p.store.UpdateServerStatusUnreachable(ctx, srv.ID); err != nil {
		log.Printf("poller: mark %s unknown: %v", srv.ID, err)
		return
	}
	p.publishStatus(srv.ID, "unknown", agent.AgentStatus{})
}

// apply reconciles one server's recorded status with what its agent reports.
func (p *Poller) apply(ctx context.Context, srv *store.Server, st agent.AgentStatus) {
	p.stateMu.Lock()
	p.denials.observeRaw(srv, st.JoinDenied, p.engine)
	p.stateMu.Unlock()

	desired := st.Status
	if desired == "" {
		return
	}
	// During a first start the record says "starting" while the agent is still
	// installing a runtime and has no process yet. The start operation owns
	// that state, and sets it back itself if the install fails.
	if srv.Status == "starting" && desired == "offline" && st.RunID == "" {
		return
	}
	if desired == srv.Status {
		return
	}
	prev := srv.Status
	running := prev == "online" || prev == "starting" || prev == "stopping" || prev == "unknown"

	switch desired {
	case "online":
		if prev == "unknown" {
			if resumed, err := p.store.ResumeUptime(ctx, srv.ID, st.StartedAt); err == nil && resumed {
				// The same process ran right through the outage.
				p.publishStatus(srv.ID, "online", st)
				return
			}
		}
		if err := p.store.UpdateServerStatus(ctx, srv.ID, "online"); err != nil {
			log.Printf("poller: update status %s -> online: %v", srv.ID, err)
			return
		}
		// A clean boot also means any stored mod conflict is resolved.
		if err := p.store.ResolveServerConflicts(ctx, srv.ID); err != nil {
			log.Printf("poller: resolve conflicts %s: %v", srv.ID, err)
		}
		if prev != "unknown" {
			p.engine.Emit(notify.ServerOnline(srv.ID, srv.Name))
		}

	case "crashed":
		if err := p.store.UpdateServerStatusCrash(ctx, srv.ID, "crashed"); err != nil {
			log.Printf("poller: update status %s -> crashed: %v", srv.ID, err)
			return
		}
		if running {
			msg := "The server crashed"
			if st.ExitCode != nil && *st.ExitCode != 0 {
				msg = "The server crashed (exit code " + itoa(*st.ExitCode) + ")"
			}
			p.store.LogAction(ctx, "", srv.ID, "server.crash", "", map[string]any{"detail": msg, "exit_code": st.ExitCode, "run_id": st.RunID})
			_ = p.store.InsertLogEvent(ctx, srv.ID, "error", msg, "poller")
			p.engine.Emit(notify.ServerCrash(srv.ID, srv.Name))
		}

	case "startup_failure":
		if err := p.store.UpdateServerStatus(ctx, srv.ID, "startup_failure"); err != nil {
			log.Printf("poller: update status %s -> startup_failure: %v", srv.ID, err)
			return
		}
		reason := conflictSummary(st.ModConflict)
		_ = p.store.InsertLogEvent(ctx, srv.ID, "error", "Startup failed: "+reason, "poller")
		p.engine.Emit(notify.ServerStartFailed(srv.ID, srv.Name, reason))

	case "offline":
		if err := p.store.UpdateServerStatus(ctx, srv.ID, "offline"); err != nil {
			log.Printf("poller: update status %s -> offline: %v", srv.ID, err)
			return
		}
		if running {
			if st.ExitCause == "exited" {
				_ = p.store.InsertLogEvent(ctx, srv.ID, "warn", "The server stopped itself (for example /stop in the game)", "poller")
			}
			p.engine.Emit(notify.ServerOffline(srv.ID, srv.Name))
		}

	default:
		if err := p.store.UpdateServerStatus(ctx, srv.ID, desired); err != nil {
			log.Printf("poller: update status %s -> %s: %v", srv.ID, desired, err)
			return
		}
	}
	p.publishStatus(srv.ID, desired, st)
}

func (p *Poller) publishStatus(serverID, status string, st agent.AgentStatus) {
	if p.bus == nil {
		return
	}
	data := map[string]any{"status": status}
	if st.ExitCause != "" {
		data["exit_cause"] = st.ExitCause
	}
	if st.ExitCode != nil {
		data["exit_code"] = *st.ExitCode
	}
	if st.RunID != "" {
		data["run_id"] = st.RunID
	}
	if !st.StartedAt.IsZero() {
		data["started_at"] = st.StartedAt
	}
	p.bus.Publish(events.Event{Type: events.ServerStatus, ServerID: serverID, Data: data})
}

func (p *Poller) publishNode(node *store.Node, online bool, hb *store.NodeHeartbeat) {
	if p.bus == nil {
		return
	}
	data := map[string]any{"online": online}
	if hb != nil {
		data["cpu_pct"], data["mem_used_mb"], data["memory_mb"] = hb.CPUPct, hb.MemUsedMb, hb.MemoryMb
		data["disk_used_gb"], data["disk_gb"] = hb.DiskUsedGb, hb.DiskGb
	}
	p.bus.Publish(events.Event{Type: events.NodeStatus, NodeID: node.ID, Data: data})
}

func conflictSummary(raw json.RawMessage) string {
	var mc struct {
		Summary string `json:"summary"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &mc) == nil && mc.Summary != "" {
		return mc.Summary
	}
	return "a mod or the Java version prevented the server from starting"
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func intFromInfo(info map[string]any, key string) *int {
	i := int64FromInfo(info, key)
	if i == nil {
		return nil
	}
	out := int(*i)
	return &out
}

func int64FromInfo(info map[string]any, key string) *int64 {
	f := floatFromInfo(info, key)
	if f == nil {
		return nil
	}
	out := int64(*f)
	return &out
}

func floatFromInfo(info map[string]any, key string) *float64 {
	v, ok := info[key]
	if !ok {
		return nil
	}
	var out float64
	switch n := v.(type) {
	case float64:
		out = n
	case int:
		out = float64(n)
	case int64:
		out = float64(n)
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return nil
		}
		out = f
	default:
		return nil
	}
	return &out
}

func strFromInfo(info map[string]any, key string) *string {
	s, ok := info[key].(string)
	if !ok || s == "" {
		return nil
	}
	return &s
}
