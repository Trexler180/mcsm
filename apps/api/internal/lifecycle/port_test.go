package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/mcsm/api/internal/operations"
	"github.com/mcsm/api/internal/store"
)

// Two servers may share a port; what's refused is starting one while the
// other is running on it, and nothing reaches the node when it is.
func TestStartRefusesAPortAnotherRunningServerHolds(t *testing.T) {
	ctx := context.Background()
	a := &recordingAgent{}
	svc, st, srv := testService(t, a)
	twin, err := st.CreateServer(ctx, &store.Server{NodeID: srv.NodeID, OwnerID: srv.OwnerID, Name: "Twin", Platform: "paper",
		MCVersion: "1.21.4", DirectoryPath: "servers/twin", JavaBinary: "java", Port: srv.Port, RAMMbMin: 512, RAMMbMax: 1024})
	if err != nil {
		t.Fatalf("a second server on the same port should be allowed: %v", err)
	}
	if err := st.UpdateServerStatus(ctx, twin.ID, "online"); err != nil {
		t.Fatal(err)
	}

	_, err = svc.Start(ctx, srv, operations.Actor{})
	var busy *PortInUseError
	if !errors.As(err, &busy) || busy.Other != "Twin" || busy.Port != srv.Port {
		t.Fatalf("Start = %v, want PortInUseError naming Twin", err)
	}
	if err := svc.Restart(ctx, srv, operations.Actor{}); !errors.As(err, &busy) {
		t.Fatalf("Restart = %v, want PortInUseError", err)
	}
	if got := a.seen(); got != "" {
		t.Fatalf("the node was asked to %q", got)
	}

	// Once the twin stops, the start goes through.
	if err := st.UpdateServerStatus(ctx, twin.ID, "offline"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Start(ctx, srv, operations.Actor{}); err != nil {
		t.Fatalf("Start after the twin stopped = %v", err)
	}
}
