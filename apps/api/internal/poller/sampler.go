package poller

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/notify"
	"github.com/mcsm/api/internal/store"
)

const (
	// sampleEvery spaces resource samples: one every N poll ticks (15s each),
	// so ~one row per minute per running server.
	sampleEvery = 4
	// metricsRetention bounds how much history the panel keeps.
	metricsRetention = 30 * 24 * time.Hour
	// pruneEvery spaces retention sweeps in sample rounds (~hourly).
	pruneEvery = 60

	// Sustained-load alert: a sample is "high" when CPU or memory crosses its
	// threshold; highSampleCount consecutive high samples (~10 minutes) raise
	// one alert, re-armed only after the server drops back under the line.
	cpuHighPct      = 90.0
	memHighPct      = 90.0
	highSampleCount = 10
)

// sampler carries the in-memory streak state for sustained-load alerts. State
// resets on API restart, which at worst delays a re-alert by highSampleCount
// samples — acceptable for a warning signal.
type sampler struct {
	highStreak map[string]int
	alerted    map[string]bool
	rounds     int
}

func newSampler() *sampler {
	return &sampler{highStreak: map[string]int{}, alerted: map[string]bool{}}
}

// sampleAll records one resource sample for every running server and updates
// the sustained-load streaks. onStats, when non-nil, receives each server's
// snapshot (used by the player-session tracker so both features share the one
// agent call per server).
func (sm *sampler) sampleAll(ctx context.Context, s *store.Store, engine *notify.Engine, onStats func(srv *store.Server, stats *agent.ServerStats)) {
	servers, err := s.ListServers(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	nodes := map[string]*store.Node{}
	for _, srv := range servers {
		if srv.Status != "online" {
			// Not running: clear any streak so a restart starts fresh.
			delete(sm.highStreak, srv.ID)
			delete(sm.alerted, srv.ID)
			if onStats != nil {
				onStats(srv, nil)
			}
			continue
		}
		node, ok := nodes[srv.NodeID]
		if !ok {
			n, err := s.GetNode(ctx, srv.NodeID)
			if err != nil {
				continue
			}
			nodes[srv.NodeID] = n
			node = n
		}
		c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)

		callCtx, cancel := context.WithTimeout(ctx, perCallBudget)
		stats, err := c.GetServerStats(callCtx, srv.ID)
		cancel()
		if err != nil {
			if onStats != nil {
				onStats(srv, nil)
			}
			continue
		}

		// Helper-mod vitals ride on the stats response, avoiding a second agent
		// round trip per running server. When the mod isn't linked the row is
		// written with NULL vitals, which history queries skip rather than
		// averaging as zero.
		var tps, msptAvg, msptP95 *float64
		if stats.Vitals != nil && stats.Vitals.Linked && stats.Vitals.TPS != nil && stats.Vitals.MSPT != nil {
			tps = &stats.Vitals.TPS.M1
			msptAvg = &stats.Vitals.MSPT.Avg
			msptP95 = &stats.Vitals.MSPT.P95
		}

		if err := s.InsertServerMetric(ctx, srv.ID, now,
			stats.CPUPercent, stats.RAMUsedMB, stats.RAMTotalMB, len(stats.Players),
			tps, msptAvg, msptP95); err != nil {
			log.Printf("sampler: insert metric %s: %v", srv.ID, err)
		}

		sm.trackLoad(srv, stats, engine)
		if onStats != nil {
			onStats(srv, stats)
		}
	}

	sm.rounds++
	// Roll raw samples up into the forever-kept hourly table, then prune raw
	// rows past retention — in that order, so pruning only ever drops
	// resolution, never history. The first round also runs it so a fresh boot
	// (or first deploy of the rollup feature) backfills without waiting an hour.
	if sm.rounds == 1 || sm.rounds%pruneEvery == 0 {
		if err := s.RollupServerMetricsHourly(ctx, now); err != nil {
			log.Printf("sampler: rollup metrics: %v", err)
		} else if err := s.PruneServerMetrics(ctx, now.Add(-metricsRetention)); err != nil {
			log.Printf("sampler: prune metrics: %v", err)
		}
	}
}

// trackLoad updates a server's consecutive-high-sample streak and emits one
// sustained-load alert per excursion above the thresholds.
func (sm *sampler) trackLoad(srv *store.Server, stats *agent.ServerStats, engine *notify.Engine) {
	// Memory pressure is measured against the server's own RAM cap when one is
	// configured (the JVM heap limit), else against host RAM as a fallback.
	memLimit := int64(srv.RAMMbMax)
	if memLimit <= 0 {
		memLimit = stats.RAMTotalMB
	}
	var memPct float64
	if memLimit > 0 {
		memPct = float64(stats.RAMUsedMB) / float64(memLimit) * 100
	}

	var reason string
	switch {
	case stats.CPUPercent >= cpuHighPct:
		reason = fmt.Sprintf("CPU at %.0f%%", stats.CPUPercent)
	case memPct >= memHighPct:
		reason = fmt.Sprintf("memory at %.0f%% of its %d MB limit", memPct, memLimit)
	}

	if reason == "" {
		sm.highStreak[srv.ID] = 0
		sm.alerted[srv.ID] = false
		return
	}

	sm.highStreak[srv.ID]++
	if sm.highStreak[srv.ID] >= highSampleCount && !sm.alerted[srv.ID] {
		sm.alerted[srv.ID] = true
		mins := highSampleCount * sampleEvery * int(pollInterval/time.Second) / 60
		engine.Emit(notify.ServerPerformance(srv.ID, srv.Name,
			fmt.Sprintf("%s for the last ~%d minutes.", reason, mins)))
	}
}
