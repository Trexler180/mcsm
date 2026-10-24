package lifecycle

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/operations"
	"github.com/mcsm/api/internal/store"
)

// installingAgent is a node whose start is still fetching the runtime: the
// start call blocks until the request is dropped.
type installingAgent struct {
	recordingAgent
	installing chan struct{}
	kill       error
}

func (a *installingAgent) StartServer(ctx context.Context, _ string, _ map[string]any) error {
	a.record("start")
	close(a.installing)
	<-ctx.Done()
	return ctx.Err()
}

func (a *installingAgent) KillServer(context.Context, string) error {
	a.record("kill")
	return a.kill
}

func waitOperation(t *testing.T, s *store.Store, id string) *store.Operation {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		op, err := s.GetOperation(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if op.Status != store.OpRunning {
			return op
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the operation never finished")
	return nil
}

// A start cancelled while the node installs the runtime tells the node to drop
// it, so the server is not launched after all, and is not recorded as a
// failed start.
func TestCancelledStartIsDroppedOnTheNode(t *testing.T) {
	a := &installingAgent{installing: make(chan struct{})}
	life, s, srv := testService(t, a)
	ctx := context.Background()

	op, err := life.Start(ctx, srv, operations.System)
	if err != nil {
		t.Fatal(err)
	}
	<-a.installing
	if !life.Operations().Cancel(op.ID) {
		t.Fatal("the start could not be cancelled")
	}
	if got := waitOperation(t, s, op.ID); got.Status != store.OpCancelled {
		t.Fatalf("operation %s, want cancelled", got.Status)
	}
	if got := a.seen(); got != "start,kill" {
		t.Fatalf("node calls %q, want the start dropped with a kill", got)
	}
	current, _ := s.GetServer(ctx, srv.ID)
	if current.Status != "offline" {
		t.Fatalf("status %q, want offline", current.Status)
	}
	entries, _ := s.ListAudit(ctx, srv.ID, 10)
	for _, e := range entries {
		if e.Action == "server.start_failed" {
			t.Fatal("a cancelled start was recorded as a failed one")
		}
	}
}

// A forced kill during a start cancels it, and an agent that answers it has no
// process (an older agent, for a start still installing) counts as killed.
func TestForcedKillDuringStartSucceeds(t *testing.T) {
	a := &installingAgent{installing: make(chan struct{}), kill: agent.ErrNoProcess}
	life, s, srv := testService(t, a)
	ctx := context.Background()

	op, err := life.Start(ctx, srv, operations.System)
	if err != nil {
		t.Fatal(err)
	}
	<-a.installing
	if err := life.Kill(ctx, srv, operations.System, false); err == nil {
		t.Fatal("an unforced kill went through a running start")
	}
	if err := life.Kill(ctx, srv, operations.System, true); err != nil {
		t.Fatalf("forced kill: %v", err)
	}
	if got := waitOperation(t, s, op.ID); got.Status != store.OpCancelled {
		t.Fatalf("operation %s, want cancelled", got.Status)
	}
	current, _ := s.GetServer(ctx, srv.ID)
	if current.Status != "offline" {
		t.Fatalf("status %q, want offline", current.Status)
	}
}

// Only a node that could not be reached is reported as unreachable; a node
// that answered with an error said why.
func TestDeleteSaysWhetherTheNodeWasReached(t *testing.T) {
	for _, tc := range []struct {
		name        string
		purge       error
		unreachable bool
	}{
		{"answered", errors.New("agent: delete files: in use"), false},
		{"unreachable", &url.Error{Op: "Delete", URL: "http://node", Err: errors.New("connection refused")}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			life, _, srv := testService(t, &recordingAgent{purge: tc.purge})
			err := life.Delete(context.Background(), srv, operations.System, DeleteOptions{Files: true})
			if err == nil {
				t.Fatal("a failed purge reported success")
			}
			if got := errors.Is(err, ErrNodeUnreachable); got != tc.unreachable {
				t.Fatalf("unreachable = %v for %v", got, err)
			}
		})
	}
}
