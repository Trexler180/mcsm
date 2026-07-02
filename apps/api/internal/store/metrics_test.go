package store

import (
	"context"
	"testing"
	"time"
)

func TestServerMetricsInsertHistoryPrune(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	srv := seedServer(t, s)

	base := time.Now().Truncate(time.Minute)
	// Two minutes of samples 30s apart: four rows, two per minute-bucket.
	for i, cpu := range []float64{10, 30, 50, 70} {
		ts := base.Add(time.Duration(i*30) * time.Second)
		if err := s.InsertServerMetric(ctx, srv.ID, ts, cpu, int64(1000+i), 8192, i); err != nil {
			t.Fatal(err)
		}
	}

	// Raw (no bucketing) returns all four.
	raw, err := s.ServerMetricsHistory(ctx, srv.ID, base.Add(-time.Minute), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 4 {
		t.Fatalf("raw samples = %d, want 4", len(raw))
	}

	// 60s buckets average pairs: (10,30)->20 and (50,70)->60.
	bucketed, err := s.ServerMetricsHistory(ctx, srv.ID, base.Add(-time.Minute), 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(bucketed) != 2 {
		t.Fatalf("bucketed points = %d, want 2", len(bucketed))
	}
	if bucketed[0].CPUPercent != 20 || bucketed[1].CPUPercent != 60 {
		t.Fatalf("bucket averages = %v, %v; want 20, 60", bucketed[0].CPUPercent, bucketed[1].CPUPercent)
	}
	if bucketed[1].Players != 3 {
		t.Fatalf("bucket max players = %d, want 3", bucketed[1].Players)
	}

	// Same-second re-insert is ignored, not an error.
	if err := s.InsertServerMetric(ctx, srv.ID, base, 99, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	raw2, _ := s.ServerMetricsHistory(ctx, srv.ID, base.Add(-time.Minute), 0)
	if len(raw2) != 4 || raw2[0].CPUPercent != 10 {
		t.Fatalf("re-insert should be a no-op; got %d rows, first cpu %v", len(raw2), raw2[0].CPUPercent)
	}

	// Prune removes everything before the cutoff.
	if err := s.PruneServerMetrics(ctx, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	left, _ := s.ServerMetricsHistory(ctx, srv.ID, base.Add(-time.Hour), 0)
	if len(left) != 2 {
		t.Fatalf("after prune = %d rows, want 2", len(left))
	}
}
