package process

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type Manager struct {
	mu        sync.RWMutex
	instances map[string]*Instance
	dirs      map[string]string
}

func NewManager() *Manager {
	return &Manager{
		instances: make(map[string]*Instance),
		dirs:      make(map[string]string),
	}
}

func (m *Manager) Start(id string, cfg StartConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if inst, ok := m.instances[id]; ok {
		// offline/crashed/mod_conflict all mean the process is dead and the
		// instance is just a stale record we can replace with a fresh start.
		s := inst.statusInfo().Status
		if s != StatusOffline && s != StatusCrashed && s != StatusModConflict {
			return fmt.Errorf("server already running")
		}
	}

	inst := newInstance(id, cfg)
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
	_ = inst.stop(true, 30*time.Second)

	m.mu.Lock()
	delete(m.instances, id)
	m.mu.Unlock()

	return m.Start(id, cfg)
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
// files. Online entries win — an offline .dat for someone currently online is
// dropped so each player appears once.
func (m *Manager) AllPlayers(id string) []Player {
	online := m.RefreshPlayers(id, 750*time.Millisecond)

	var offline []Player
	if dir, ok := m.GetDir(id); ok {
		offline, _ = OfflinePlayers(dir)
	}

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
	for _, p := range m.RefreshPlayers(id, 750*time.Millisecond) {
		if strings.EqualFold(p.Name, d.Name) {
			d.Online = true
			break
		}
	}
	return d, nil
}

// StopAll gracefully stops every running instance, in parallel, bounded by
// timeout per instance. Called on agent shutdown so MC children aren't orphaned.
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

func (m *Manager) GetDir(id string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	dir, ok := m.dirs[id]
	return dir, ok
}
