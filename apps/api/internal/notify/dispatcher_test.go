package notify

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mcsm/api/internal/store"
)

// A webhook that hangs must not hold up deliveries to anyone else, and
// deliveries to one target keep their order.
func TestDispatcherDeliversTargetsInParallel(t *testing.T) {
	d := NewDispatcher(nil, nil, nil)
	release := make(chan struct{})
	var mu sync.Mutex
	var got []string
	d.send = func(_ context.Context, del *store.NotificationDelivery) {
		if del.TargetID == "slow" {
			<-release
		}
		mu.Lock()
		got = append(got, del.ID)
		mu.Unlock()
	}
	batch := []*store.NotificationDelivery{
		{ID: "s1", TargetKind: "webhook", TargetID: "slow"},
		{ID: "f1", TargetKind: "webhook", TargetID: "fast"},
		{ID: "f2", TargetKind: "webhook", TargetID: "fast"},
		{ID: "s2", TargetKind: "webhook", TargetID: "slow"},
	}
	done := make(chan struct{})
	go func() {
		d.deliverBatch(context.Background(), batch)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the fast target waited for the slow one")
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(release)
	<-done
	want := []string{"f1", "f2", "s1", "s2"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("delivery order %v, want %v", got, want)
		}
	}
}
