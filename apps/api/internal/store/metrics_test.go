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
		if err := s.InsertServerMetric(ctx, srv.ID, ts, cpu, int64(1000+i), 8192, i, nil, nil, nil); err != nil {
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
	if err := s.InsertServerMetric(ctx, srv.ID, base, 99, 1, 1, 1, nil, nil, nil); err != nil {
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

func f64(v float64) *float64 { return &v }

// Vitals are optional per sample: a bucket mixing rows with and without mod
// data must average only the rows that have it, and a bucket with none at all
// must report nil rather than a fabricated zero.
func TestServerMetricsVitalsSkipNullSamples(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	srv := seedServer(t, s)

	base := time.Now().Truncate(time.Minute)
	// Bucket 0: one linked sample (tps 20) + one unlinked sample (NULL vitals).
	if err := s.InsertServerMetric(ctx, srv.ID, base, 10, 1000, 8192, 1,
		f64(20), f64(30), f64(45)); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertServerMetric(ctx, srv.ID, base.Add(30*time.Second), 10, 1000, 8192, 1,
		nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	// Bucket 1: no vitals at all.
	if err := s.InsertServerMetric(ctx, srv.ID, base.Add(time.Minute), 10, 1000, 8192, 1,
		nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	// Raw rows round-trip the pointers verbatim.
	raw, err := s.ServerMetricsHistory(ctx, srv.ID, base.Add(-time.Minute), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 3 {
		t.Fatalf("raw rows = %d, want 3", len(raw))
	}
	if raw[0].TPS == nil || *raw[0].TPS != 20 || raw[0].MSPTAvg == nil || *raw[0].MSPTAvg != 30 ||
		raw[0].MSPTP95 == nil || *raw[0].MSPTP95 != 45 {
		t.Fatalf("raw vitals not round-tripped: %+v", raw[0])
	}
	if raw[1].TPS != nil || raw[1].MSPTAvg != nil || raw[1].MSPTP95 != nil {
		t.Fatalf("unlinked row should have nil vitals: %+v", raw[1])
	}

	bucketed, err := s.ServerMetricsHistory(ctx, srv.ID, base.Add(-time.Minute), 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(bucketed) != 2 {
		t.Fatalf("bucketed points = %d, want 2", len(bucketed))
	}
	// The NULL row must not drag the average toward 0.
	if bucketed[0].TPS == nil || *bucketed[0].TPS != 20 {
		t.Fatalf("bucket tps = %v, want 20 (NULL sample must be skipped)", bucketed[0].TPS)
	}
	if bucketed[0].MSPTAvg == nil || *bucketed[0].MSPTAvg != 30 {
		t.Fatalf("bucket mspt_avg = %v, want 30", bucketed[0].MSPTAvg)
	}
	if bucketed[0].MSPTP95 == nil || *bucketed[0].MSPTP95 != 45 {
		t.Fatalf("bucket mspt_p95 = %v, want 45", bucketed[0].MSPTP95)
	}
	if bucketed[1].TPS != nil || bucketed[1].MSPTAvg != nil || bucketed[1].MSPTP95 != nil {
		t.Fatalf("vitals-free bucket should be nil, got %+v", bucketed[1])
	}
}

// The hourly rollup must carry vitals through with sample-count weighting, and
// a server that never had mod data must come back with nil pointers.
func TestVitalsHourlyRollupWeighting(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	srv := seedServer(t, s)
	other, err := s.CreateServer(ctx, &Server{
		NodeID:        srv.NodeID,
		OwnerID:       srv.OwnerID,
		Name:          "novitals",
		Platform:      "fabric",
		MCVersion:     "1.21.4",
		DirectoryPath: "servers/novitals",
		JavaBinary:    "java",
		Port:          25566,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Align to a 2-hour boundary so h1 and h2 share one bucket below.
	h1 := time.Unix(time.Now().Add(-40*24*time.Hour).Unix()/7200*7200, 0)
	h2 := h1.Add(time.Hour)

	// Hour 1: three vitals samples (tps 18/20/22 -> avg 20, min 18) + one
	// unlinked sample that must not count.
	for i, tps := range []float64{18, 20, 22} {
		if err := s.InsertServerMetric(ctx, srv.ID, h1.Add(time.Duration(i)*time.Minute),
			10, 1000, 8192, 1, f64(tps), f64(40), f64(60+float64(i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.InsertServerMetric(ctx, srv.ID, h1.Add(30*time.Minute),
		10, 1000, 8192, 1, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	// Hour 2: a single sample at tps 10, mspt_avg 100.
	if err := s.InsertServerMetric(ctx, srv.ID, h2, 10, 1000, 8192, 1,
		f64(10), f64(100), f64(150)); err != nil {
		t.Fatal(err)
	}
	// Other server: samples but never any vitals.
	if err := s.InsertServerMetric(ctx, other.ID, h1, 10, 1000, 8192, 1, nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	if err := s.RollupServerMetricsHourly(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Per-hour rollup values.
	var tpsAvg, tpsMin, msptAvg, p95Max *float64
	var vs int
	if err := s.db.QueryRowContext(ctx,
		`SELECT tps_avg, tps_min, mspt_avg, mspt_p95_max, vitals_samples
		 FROM server_metrics_hourly WHERE server_id = ? AND ts = ?`,
		srv.ID, h1.Unix()).Scan(&tpsAvg, &tpsMin, &msptAvg, &p95Max, &vs); err != nil {
		t.Fatal(err)
	}
	if vs != 3 {
		t.Fatalf("vitals_samples = %d, want 3 (NULL row excluded)", vs)
	}
	if tpsAvg == nil || *tpsAvg != 20 || tpsMin == nil || *tpsMin != 18 {
		t.Fatalf("tps_avg/tps_min = %v/%v, want 20/18", tpsAvg, tpsMin)
	}
	if msptAvg == nil || *msptAvg != 40 || p95Max == nil || *p95Max != 62 {
		t.Fatalf("mspt_avg/mspt_p95_max = %v/%v, want 40/62", msptAvg, p95Max)
	}

	// One 2-hour bucket: weighted mean of (20 x3) and (10 x1) = 17.5.
	pts, err := s.ServerMetricsHistoryHourly(ctx, srv.ID, h1.Add(-time.Hour), 2*3600)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 1 {
		t.Fatalf("hourly points = %d, want 1", len(pts))
	}
	if pts[0].TPS == nil || *pts[0].TPS != 17.5 {
		t.Fatalf("weighted tps = %v, want 17.5", pts[0].TPS)
	}
	if pts[0].MSPTAvg == nil || *pts[0].MSPTAvg != 55 {
		t.Fatalf("weighted mspt_avg = %v, want 55", pts[0].MSPTAvg)
	}
	if pts[0].MSPTP95 == nil || *pts[0].MSPTP95 != 150 {
		t.Fatalf("mspt_p95 max = %v, want 150", pts[0].MSPTP95)
	}

	// A server with zero vitals rows gets nils, not zeros.
	nvPts, err := s.ServerMetricsHistoryHourly(ctx, other.ID, h1.Add(-time.Hour), 3600)
	if err != nil {
		t.Fatal(err)
	}
	if len(nvPts) != 1 {
		t.Fatalf("no-vitals server points = %d, want 1", len(nvPts))
	}
	if nvPts[0].TPS != nil || nvPts[0].MSPTAvg != nil || nvPts[0].MSPTP95 != nil {
		t.Fatalf("no-vitals server should yield nil vitals, got %+v", nvPts[0])
	}
}
