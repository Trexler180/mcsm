package process

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

type Manager struct {
	serverRoot string

	mu        sync.RWMutex
	instances map[string]*Instance
	dirs      map[string]string

	rosterMu sync.Mutex
	roster   map[string]rosterCache
}

// rosterCache memoises the expensive part of AllPlayers (reading every
// playerdata .dat plus the state files) keyed by a fingerprint of the relevant
// files' mtimes. It is rebuilt only when roster membership or op/whitelist/ban
// state changes — so the 5s online poll no longer re-reads the world each time.
type rosterCache struct {
	fingerprint string
	offline     []Player
	state       playerState
}

func NewManager(serverRoot string) *Manager {
	return &Manager{
		serverRoot: serverRoot,
		instances:  make(map[string]*Instance),
		dirs:       make(map[string]string),
		roster:     make(map[string]rosterCache),
	}
}

// Reattach adopts Minecraft servers that kept running across an agent restart.
// For each persisted run state whose process is still alive and rooted in the
// recorded directory, it rebuilds an Instance and resumes monitoring; dead or
// unidentifiable entries (incl. PID reuse) are cleaned up. No-op on platforms
// without detached spawning (Windows/local dev).
func (m *Manager) Reattach() {
	if !supportsReattach {
		return
	}
	for _, st := range listRunStates(m.serverRoot) {
		if !processAlive(st.PID) || !processMatchesDir(st.PID, st.Directory) {
			clearRunState(m.serverRoot, st.ID)
			continue
		}
		inst, err := reattachInstance(m.serverRoot, st)
		if err != nil {
			log.Printf("reattach %s: %v", st.ID, err)
			clearRunState(m.serverRoot, st.ID)
			continue
		}
		m.mu.Lock()
		m.instances[st.ID] = inst
		m.dirs[st.ID] = st.Directory
		m.mu.Unlock()
		log.Printf("reattached to running server %s (pid %d)", st.ID, st.PID)
	}
}

func (m *Manager) Start(id string, cfg StartConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if inst, ok := m.instances[id]; ok {
		// offline/crashed/startup_failure all mean the process is dead and the
		// instance is just a stale record we can replace with a fresh start.
		s := inst.statusInfo().Status
		if s != StatusOffline && s != StatusCrashed && s != StatusStartupFailure {
			return fmt.Errorf("server already running")
		}
	}

	inst := newInstance(m.serverRoot, id, cfg)
	if err := inst.start(); err != nil {
		return err
	}
	m.instances[id] = inst
	m.dirs[id] = cfg.Directory
	return nil
}

func (m *Manager) Stop(id string, graceful bool, timeout time.Duration) error {
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("server not found")
	}
	return inst.stop(graceful, timeout)
}

func (m *Manager) Kill(id string) error {
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("server not found")
	}
	return inst.kill()
}

func (m *Manager) Restart(id string) error {
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("server not found")
	}
	cfg := inst.Config
	if err := inst.stop(true, defaultStopTimeout); err != nil {
		return fmt.Errorf("restart: stop failed: %w", err)
	}
	// stop() can return nil without the process having finalized (kill's exit
	// wait is bounded). Starting a second JVM on the same port/world would be
	// worse than failing the restart, so require the old process to be gone.
	if !inst.exited() {
		return fmt.Errorf("restart: server did not exit in time")
	}
	m.removeInstance(id, inst)
	return m.Start(id, cfg)
}

// removeInstance drops id from the instance map only if it still maps to inst.
// The identity check keeps Restart from deleting a fresh instance that a
// concurrent Start registered after our stop; the loser of that race then gets
// "server already running" from Start instead of orphaning the new process.
func (m *Manager) removeInstance(id string, inst *Instance) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.instances[id]; ok && cur == inst {
		delete(m.instances, id)
	}
}

func (m *Manager) Status(id string) StatusInfo {
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok {
		return StatusInfo{ID: id, Status: StatusOffline}
	}
	return inst.statusInfo()
}

func (m *Manager) SendCommand(id, cmd string) error {
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("server not running")
	}
	return inst.sendCommand(cmd)
}

func (m *Manager) Subscribe(id string) (<-chan ConsoleEvent, func(), error) {
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok {
		return nil, nil, fmt.Errorf("server not running")
	}
	ch, unsub := inst.subscribe()
	return ch, unsub, nil
}

func (m *Manager) RegisterDir(id, dir string) {
	m.mu.Lock()
	m.dirs[id] = dir
	m.mu.Unlock()
}

// Unregister drops a server's tracked directory and instance. Called after a
// purge so a deleted server leaves no stale references behind.
func (m *Manager) Unregister(id string) {
	m.mu.Lock()
	delete(m.dirs, id)
	delete(m.instances, id)
	m.mu.Unlock()

	m.rosterMu.Lock()
	delete(m.roster, id)
	m.rosterMu.Unlock()

	// Drop any persisted runtime state so a purged server is never reattached.
	clearRunState(m.serverRoot, id)
}

func (m *Manager) Players(id string) []Player {
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok {
		return nil
	}
	return inst.Players()
}

func (m *Manager) RefreshPlayers(id string, timeout time.Duration) []Player {
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok {
		return nil
	}
	return inst.RefreshPlayers(timeout)
}

// AllPlayers returns the merged roster: players currently online (tracked live
// from the console) plus offline players read from the world's playerdata
// files, each stamped with op/whitelist/ban status. Online entries win — an
// offline .dat for someone currently online is dropped so each player appears
// once.
func (m *Manager) AllPlayers(id string) []Player {
	online := m.RefreshPlayers(id, playerRefreshTimeout)

	dir, ok := m.GetDir(id)
	if !ok {
		// No directory registered: we can only return the live roster.
		return online
	}

	offline, state := m.offlineRoster(id, dir)
	prefix, _ := bedrockPrefix(dir)

	offlineByName := make(map[string]Player, len(offline))
	for _, p := range offline {
		offlineByName[strings.ToLower(p.Name)] = p
	}

	onlineNames := make(map[string]struct{}, len(online))
	out := make([]Player, 0, len(online)+len(offline))
	for _, p := range online {
		key := strings.ToLower(p.Name)
		onlineNames[key] = struct{}{}
		if saved, ok := offlineByName[key]; ok {
			if p.UUID == "" {
				p.UUID = saved.UUID
			}
			if p.LastSeen.IsZero() {
				p.LastSeen = saved.LastSeen
			}
		}
		state.stamp(&p)
		// Stamp after the UUID backfill so a Floodgate UUID from the saved .dat
		// is available even when /list only gave us the (possibly prefixed) name.
		stampBedrock(&p, prefix)
		out = append(out, p)
	}

	for _, p := range offline {
		if _, on := onlineNames[strings.ToLower(p.Name)]; on {
			continue
		}
		out = append(out, p)
	}
	return out
}

// offlineRoster returns the cached offline roster + state for a server,
// rebuilding only when the underlying files have changed since last time.
func (m *Manager) offlineRoster(id, dir string) ([]Player, playerState) {
	fp := rosterFingerprint(dir)

	m.rosterMu.Lock()
	if c, ok := m.roster[id]; ok && c.fingerprint == fp {
		m.rosterMu.Unlock()
		return c.offline, c.state
	}
	m.rosterMu.Unlock()

	state := readServerState(dir)
	offline := buildOfflineRoster(dir, state)

	m.rosterMu.Lock()
	m.roster[id] = rosterCache{fingerprint: fp, offline: offline, state: state}
	m.rosterMu.Unlock()
	return offline, state
}

// PlayerDetail parses one player's .dat file and resolves their name (from
// usercache.json) and online status (from the live roster).
func (m *Manager) PlayerDetail(id, uuid string) (*PlayerDetail, error) {
	dir, ok := m.GetDir(id)
	if !ok {
		return nil, fmt.Errorf("server directory not registered")
	}
	d, err := ReadPlayerDetail(dir, uuid)
	if err != nil {
		return nil, err
	}
	if name := usercache(dir)[strings.ToLower(uuid)]; name != "" {
		d.Name = name
	} else {
		d.Name = uuid
	}
	for _, p := range m.RefreshPlayers(id, playerRefreshTimeout) {
		if strings.EqualFold(p.Name, d.Name) {
			d.Online = true
			break
		}
	}

	// Stamp op/whitelist/ban status and attach lifetime stats.
	state := readServerState(dir)
	key := strings.ToLower(d.Name)
	_, d.Op = state.ops[key]
	_, d.Whitelisted = state.whitelist[key]
	if b, ok := state.banned[key]; ok {
		d.Banned = true
		d.BanReason = b.Reason
	}
	if isBedrockUUID(uuid) {
		d.Bedrock = true
	} else if prefix, _ := bedrockPrefix(dir); prefix != "" && strings.HasPrefix(d.Name, prefix) {
		d.Bedrock = true
	}
	d.Stats = readPlayerStats(dir, uuid)

	return d, nil
}

// DetachAll stops tracking every running server WITHOUT stopping the processes,
// so an agent restart or upgrade leaves Minecraft servers running for the next
// agent to reattach to. This is the default on graceful shutdown.
func (m *Manager) DetachAll() {
	m.mu.RLock()
	insts := make([]*Instance, 0, len(m.instances))
	for _, inst := range m.instances {
		insts = append(insts, inst)
	}
	m.mu.RUnlock()
	for _, inst := range insts {
		inst.detach()
	}
}

// StopAll gracefully stops every running instance, in parallel, bounded by
// timeout per instance. Used on shutdown only when the operator opts in to
// stopping servers with the agent (AGENT_STOP_SERVERS_ON_EXIT=1).
func (m *Manager) StopAll(timeout time.Duration) {
	m.mu.RLock()
	insts := make([]*Instance, 0, len(m.instances))
	for _, inst := range m.instances {
		insts = append(insts, inst)
	}
	m.mu.RUnlock()

	var wg sync.WaitGroup
	for _, inst := range insts {
		wg.Add(1)
		go func(i *Instance) {
			defer wg.Done()
			_ = i.stop(true, timeout)
		}(inst)
	}
	wg.Wait()
}

// Conflict returns the detected Fabric mod conflict for a server, or nil.
func (m *Manager) Conflict(id string) *ModConflict {
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok {
		return nil
	}
	return inst.Conflict()
}

// DisableConflictMods renames the jars whose loader mod id is in ids to
// "<name>.disabled" in the server's mods dir, then clears the stored conflict.
// Returns the disabled filenames.
func (m *Manager) DisableConflictMods(id string, ids []string) ([]string, error) {
	dir, ok := m.GetDir(id)
	if !ok {
		return nil, fmt.Errorf("server directory unknown; register or start it first")
	}
	disabled, err := disableModsByID(dir, ids)
	if err != nil {
		return nil, err
	}
	m.mu.RLock()
	inst := m.instances[id]
	m.mu.RUnlock()
	if inst != nil {
		inst.ClearConflict()
	}
	return disabled, nil
}

// GeyserInfo reports whether a server has the Geyser/Floodgate Bedrock bridge
// installed and its effective username prefix, so the UI can surface Bedrock
// support even before any Bedrock player has joined.
func (m *Manager) GeyserInfo(id string) (GeyserInfo, error) {
	dir, ok := m.GetDir(id)
	if !ok {
		return GeyserInfo{}, fmt.Errorf("server directory not registered")
	}
	return detectGeyser(dir), nil
}

func (m *Manager) GetDir(id string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	dir, ok := m.dirs[id]
	return dir, ok
}
