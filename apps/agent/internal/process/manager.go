package process

import (
	"fmt"
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
		s := inst.statusInfo().Status
		if s != StatusOffline && s != StatusCrashed {
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

func (m *Manager) Players(id string) []Player {
	m.mu.RLock()
	inst, ok := m.instances[id]
	m.mu.RUnlock()
	if !ok {
		return nil
	}
	return inst.Players()
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

func (m *Manager) GetDir(id string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	dir, ok := m.dirs[id]
	return dir, ok
}
