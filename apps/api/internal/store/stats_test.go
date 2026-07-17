package store

import (
	"context"
	"testing"
	"time"
)

// TestRollupKeepsHistoryPastPrune is the retention contract: raw samples roll
// up into hourly rows, raw gets pruned, and the hourly history still answers.
func TestRollupKeepsHistoryPastPrune(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	srv := seedServer(t, s)

	// One old hour of samples (well past raw retention) + one recent sample.
	oldHour := time.Now().Add(-40 * 24 * time.Hour).Truncate(time.Hour)
	for i := 0; i < 4; i++ {
		ts := oldHour.Add(time.Duration(i*15) * time.Minute)
		if err := s.InsertServerMetric(ctx, srv.ID, ts, float64(10*(i+1)), int64(1000+i*100), 8192, i); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	if err := s.InsertServerMetric(ctx, srv.ID, now, 50, 2000, 8192, 5); err != nil {
		t.Fatal(err)
	}

	if err := s.RollupServerMetricsHourly(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneServerMetrics(ctx, now.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// Raw history no longer has the old hour…
	raw, err := s.ServerMetricsHistory(ctx, srv.ID, oldHour.Add(-time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 1 {
		t.Fatalf("raw rows after prune = %d, want just the recent one", len(raw))
	}

	// …but hourly history does: avg cpu (10+20+30+40)/4=25, peak players 3.
	hourly, err := s.ServerMetricsHistoryHourly(ctx, srv.ID, oldHour.Add(-time.Hour), 3600)
	if err != nil {
		t.Fatal(err)
	}
	if len(hourly) != 1 {
		t.Fatalf("hourly rows = %d, want 1", len(hourly))
	}
	p := hourly[0]
	if p.TS != oldHour.Unix()/3600*3600 {
		t.Fatalf("hourly ts = %d, want hour start %d", p.TS, oldHour.Unix()/3600*3600)
	}
	if p.CPUPercent != 25 || p.Players != 3 || p.RAMTotalMB != 8192 {
		t.Fatalf("hourly point = %+v, want cpu 25, players 3, ram_total 8192", p)
	}

	// A second rollup after the prune must not clobber the preserved hour
	// (its raw rows are gone; REPLACE only touches hours that still have raw).
	if err := s.RollupServerMetricsHourly(ctx, now); err != nil {
		t.Fatal(err)
	}
	hourly2, _ := s.ServerMetricsHistoryHourly(ctx, srv.ID, oldHour.Add(-time.Hour), 3600)
	if len(hourly2) != 1 || hourly2[0].CPUPercent != 25 {
		t.Fatalf("idempotent rollup broke preserved history: %+v", hourly2)
	}

	// Oldest data anchor sees the rolled-up hour.
	oldest, err := s.ServerMetricsDataSince(ctx, srv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if oldest.Unix() != oldHour.Unix()/3600*3600 {
		t.Fatalf("data since = %v, want %v", oldest, oldHour)
	}
}

func TestStatsAggregates(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	srv := seedServer(t, s)

	now := time.Now().UTC().Truncate(time.Minute)
	since := now.Add(-7 * 24 * time.Hour)

	// Alex: two closed sessions (2h + 1h). Steve: one session still open,
	// started 30m ago. A stale visitor outside the window shouldn't count.
	mkSession := func(name string, start time.Time, dur time.Duration) {
		id, err := s.StartPlayerSession(ctx, srv.ID, name, "", start)
		if err != nil {
			t.Fatal(err)
		}
		if dur > 0 {
			if err := s.EndPlayerSession(ctx, id, start.Add(dur)); err != nil {
				t.Fatal(err)
			}
		}
	}
	mkSession("Alex", now.Add(-48*time.Hour), 2*time.Hour)
	mkSession("alex", now.Add(-24*time.Hour), time.Hour) // case variant merges
	mkSession("Steve", now.Add(-30*time.Minute), 0)      // open
	mkSession("Old", now.Add(-30*24*time.Hour), time.Hour)

	sum, err := s.StatsSummary(ctx, srv.ID, since, now)
	if err != nil {
		t.Fatal(err)
	}
	if sum.UniquePlayers != 2 {
		t.Fatalf("unique = %d, want 2", sum.UniquePlayers)
	}
	if sum.TotalJoins != 3 {
		t.Fatalf("joins = %d, want 3", sum.TotalJoins)
	}
	wantPlay := int64((2*time.Hour + time.Hour + 30*time.Minute) / time.Second)
	if sum.PlaytimeSeconds != wantPlay {
		t.Fatalf("playtime = %d, want %d", sum.PlaytimeSeconds, wantPlay)
	}
	if sum.LongestSessionSeconds != int64((2*time.Hour)/time.Second) || sum.LongestSessionPlayer != "Alex" {
		t.Fatalf("longest = %d by %q, want 7200 by Alex", sum.LongestSessionSeconds, sum.LongestSessionPlayer)
	}

	top, err := s.TopPlayers(ctx, srv.ID, since, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 2 {
		t.Fatalf("top players = %d, want 2", len(top))
	}
	if lower := top[0]; lower.PlaytimeSeconds != int64((3*time.Hour)/time.Second) || lower.Joins != 2 {
		t.Fatalf("top[0] = %+v, want 3h over 2 joins", top[0])
	}
	if !top[1].Online || top[1].Name != "Steve" {
		t.Fatalf("top[1] = %+v, want Steve online", top[1])
	}

	// Metrics for peak/uptime/heatmap/daily: two rolled-up hours yesterday
	// (peaks 4 then 8) plus a raw sample now (peak 2).
	h1 := now.Add(-26 * time.Hour).Truncate(time.Hour)
	for i := 0; i < 30; i++ {
		if err := s.InsertServerMetric(ctx, srv.ID, h1.Add(time.Duration(i)*time.Minute), 20, 1024, 8192, 4); err != nil {
			t.Fatal(err)
		}
		if err := s.InsertServerMetric(ctx, srv.ID, h1.Add(time.Hour+time.Duration(i)*time.Minute), 20, 1024, 8192, 8); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RollupServerMetricsHourly(ctx, h1.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertServerMetric(ctx, srv.ID, now, 10, 1024, 8192, 2); err != nil {
		t.Fatal(err)
	}

	sum, err = s.StatsSummary(ctx, srv.ID, since, now)
	if err != nil {
		t.Fatal(err)
	}
	if sum.PeakPlayers != 8 {
		t.Fatalf("peak = %d, want 8", sum.PeakPlayers)
	}
	if sum.UptimeSeconds != 61*60 {
		t.Fatalf("uptime = %d, want %d (60 rolled + 1 raw samples)", sum.UptimeSeconds, 61*60)
	}

	cells, err := s.ActivityHeatmap(ctx, srv.ID, since, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 2 {
		t.Fatalf("heatmap cells = %d, want 2", len(cells))
	}

	daily, err := s.DailyStats(ctx, srv.ID, since, now, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(daily) == 0 {
		t.Fatal("daily stats empty")
	}
	// The two rolled-up hours may straddle a local midnight, so only require
	// that the peak day carries at least its own half hour of uptime.
	var sawPeak bool
	for _, d := range daily {
		if d.PeakPlayers == 8 && d.UptimeSeconds >= 30*60 {
			sawPeak = true
		}
	}
	if !sawPeak {
		t.Fatalf("daily series missing the rolled-up day: %+v", daily)
	}

	// All-time anchor includes the earliest session (30 days ago).
	oldest, err := s.StatsDataSince(ctx, srv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := now.Add(-30 * 24 * time.Hour); oldest.After(want.Add(time.Minute)) {
		t.Fatalf("data since = %v, want ≤ %v", oldest, want)
	}
}
