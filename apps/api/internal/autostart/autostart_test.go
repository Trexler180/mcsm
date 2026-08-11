package autostart

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mcsm/api/internal/store"
)

type fakeStore struct {
	servers  []*store.Server
	node     *store.Node
	mu       sync.Mutex
	statuses map[string]string
	actions  []string
	events   []string
}

func (f *fakeStore) ListServers(context.Context) ([]*store.Server, error) { return f.servers, nil }
func (f *fakeStore) GetNode(context.Context, string) (*store.Node, error) { return f.node, nil }
func (f *fakeStore) UpdateServerStatus(_ context.Context, id, status string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses[id] = status
	return nil
}
func (f *fakeStore) InsertLogEvent(_ context.Context, id, _, message, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, id+":"+message)
	return nil
}
func (f *fakeStore) LogAction(_ context.Context, _, id, action, _ string, _ any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, id+":"+action)
	return nil
}

type fakeClient struct {
	status    string
	statusErr error
	startErr  error
	mu        sync.Mutex
	starts    []string
	configs   []map[string]any
}

func (f *fakeClient) GetStatus(context.Context, string) (map[string]any, error) {
	return map[string]any{"status": f.status}, f.statusErr
}
func (f *fakeClient) StartServer(_ context.Context, id string, cfg map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, id)
	f.configs = append(f.configs, cfg)
	return f.startErr
}

func testConfig() runConfig {
	return runConfig{
		availabilityWindow: time.Second,
		retryDelay:         time.Millisecond,
		statusTimeout:      time.Second,
		startTimeout:       time.Second,
	}
}

func TestRunStartsOnlyOfflineOptedInServers(t *testing.T) {
	s := &fakeStore{
		servers: []*store.Server{
			{ID: "auto", NodeID: "node", Name: "Auto", AutoStart: true, DirectoryPath: "auto-dir", Platform: "paper", MCVersion: "1.21", RAMMbMin: 1024, RAMMbMax: 2048},
			{ID: "manual", NodeID: "node", Name: "Manual", AutoStart: false},
		},
		node:     &store.Node{ID: "node"},
		statuses: map[string]string{},
	}
	c := &fakeClient{status: "offline"}

	run(context.Background(), s, func(*store.Node) agentClient { return c }, testConfig())

	if len(c.starts) != 1 || c.starts[0] != "auto" {
		t.Fatalf("starts = %v, want [auto]", c.starts)
	}
	if c.configs[0]["directory"] != "auto-dir" {
		t.Fatalf("start config did not use shared builder: %#v", c.configs[0])
	}
	if s.statuses["auto"] != "starting" {
		t.Fatalf("status = %q, want starting", s.statuses["auto"])
	}
}

func TestRunDoesNotRestartAlreadyOnlineServer(t *testing.T) {
	s := &fakeStore{
		servers:  []*store.Server{{ID: "online", NodeID: "node", Name: "Online", AutoStart: true}},
		node:     &store.Node{ID: "node"},
		statuses: map[string]string{},
	}
	c := &fakeClient{status: "online"}

	run(context.Background(), s, func(*store.Node) agentClient { return c }, testConfig())

	if len(c.starts) != 0 {
		t.Fatalf("already-online server was started again: %v", c.starts)
	}
	if s.statuses["online"] != "online" {
		t.Fatalf("status = %q, want online", s.statuses["online"])
	}
}

func TestRunRecordsAgentStartFailureAndRestoresOfflineStatus(t *testing.T) {
	s := &fakeStore{
		servers:  []*store.Server{{ID: "broken", NodeID: "node", Name: "Broken", AutoStart: true}},
		node:     &store.Node{ID: "node"},
		statuses: map[string]string{},
	}
	c := &fakeClient{status: "offline", startErr: errors.New("boom")}

	run(context.Background(), s, func(*store.Node) agentClient { return c }, testConfig())

	if s.statuses["broken"] != "offline" {
		t.Fatalf("status = %q, want offline", s.statuses["broken"])
	}
	if len(s.actions) != 1 || s.actions[0] != "broken:server.autostart_failed" {
		t.Fatalf("actions = %v", s.actions)
	}
	if len(s.events) != 1 {
		t.Fatalf("events = %v", s.events)
	}
}
