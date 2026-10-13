package poller

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/events"
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
	// maxConcurrentSamples bounds how many servers are sampled at once.
	maxConcurrentSamples = 8

	// Sustained-load alert: a sample is "high" when CPU or memory crosses its
	// threshold; highSampleCount consecutive high samples (~10 minutes) raise
	// one alert, re-armed only after the server drops back under the line.
	cpuHighPct      = 90.0
	memHighPct      = 90.0
	highSampleCount = 10
	// A server averaging under this many ticks per second for
	// lowTPSSampleCount samples raises the same alert, with the reason given.
	tpsLow            = 18.0
	lowTPSSampleCount = 10
)

// sampler carries the in-memory streak state for sustained-load alerts. State
// resets on API restart, which at worst delays a re-alert by highSampleCount
// samples — acceptable for a warning signal.
type sampler struct {
	mu         sync.Mutex
	highStreak map[string]int
	lowTPS     map[string]int
	alerted    map[string]bool
	rounds     int
}

func newSampler() *sampler {
	return &sampler{highStreak: map[string]int{}, lowTPS: map[string]int{}, alerted: map[string]bool{}}
}

type sampleResult struct {
	srv   *store.Server
	stats *agent.ServerStats
}

// sampleAll records one resource sample for every running server, updates the
// sustained-load streaks, and publishes each sample on the event bus.
// onStats, when non-nil, receives each server's snapshot (used by the
// player-session tracker so both features share the one agent call per
// server). Agents are asked concurrently; results are processed in order.
func (sm *sampler) sampleAll(ctx context.Context, s *store.Store, engine *notify.Engine, bus *events.Bus,
	onStats func(ctx context.Context, srv *store.Server, stats *agent.ServerStats)) {
	servers, err := s.ListServers(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	nodes := map[string]*store.Node{}
	for _, srv := range servers {
		if _, ok := nodes[srv.NodeID]; !ok {
			if n, err := s.GetNode(ctx, srv.NodeID); err == nil {
				nodes[srv.NodeID] = n
			}
		}
	}

	results := make([]sampleResult, len(servers))
	sem := make(chan struct{}, maxConcurrentSamples)
	var wg sync.WaitGroup
	for i, srv := range servers {
		results[i].srv = srv
		node, ok := nodes[srv.NodeID]
		if srv.Status != "online" || !ok {
			continue
		}
		i, srv := i, srv
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
			callCtx, cancel := context.WithTimeout(ctx, perCallBudget)
			stats, err := c.GetServerStats(callCtx, srv.ID)
			cancel()
			if err == nil {
				results[i].stats = stats
			}
		}()
	}
	wg.Wait()

	for _, res := range results {
		srv, stats := res.srv, res.stats
		if stats == nil {
			sm.mu.Lock()
			delete(sm.highStreak, srv.ID)
			delete(sm.lowTPS, srv.ID)
			delete(sm.alerted, srv.ID)
			sm.mu.Unlock()
			if onStats != nil {
				onStats(ctx, srv, nil)
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
		if bus != nil {
			bus.Publish(events.Event{Type: events.ServerStats, ServerID: srv.ID, At: now, Data: map[string]any{
				"cpu_percent": stats.CPUPercent, "ram_used_mb": stats.RAMUsedMB, "ram_total_mb": stats.RAMTotalMB,
				"players": len(stats.Players), "tps": tps, "mspt_avg": msptAvg, "mspt_p95": msptP95,
			}})
		}

		sm.trackLoad(srv, stats, tps, engine)
		if onStats != nil {
			onStats(ctx, srv, stats)
		}
	}

	sm.mu.Lock()
	sm.rounds++
	rounds := sm.rounds
	sm.mu.Unlock()
	// Roll raw samples up into the forever-kept hourly table, then prune raw
	// rows past retention — in that order, so pruning only ever drops
	// resolution, never history. The first round also runs it so a fresh boot
	// (or first deploy of the rollup feature) backfills without waiting an hour.
	if rounds == 1 || rounds%pruneEvery == 0 {
		if err := s.RollupServerMetricsHourly(ctx, now); err != nil {
			log.Printf("sampler: rollup metrics: %v", err)
		} else if err := s.PruneServerMetrics(ctx, now.Add(-metricsRetention)); err != nil {
			log.Printf("sampler: prune metrics: %v", err)
		}
		if n, err := s.PruneHistory(ctx, now); err != nil {
			log.Printf("sampler: prune history: %v", err)
		} else if n > 0 {
			log.Printf("sampler: pruned %d expired history rows", n)
		}
	}
}

// trackLoad updates a server's consecutive-high-sample streaks and emits one
// sustained-load alert per excursion: CPU or memory above their thresholds, or
// ticks per second below tpsLow.
func (sm *sampler) trackLoad(srv *store.Server, stats *agent.ServerStats, tps *float64, engine *notify.Engine) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
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
	if tps != nil && *tps < tpsLow {
		sm.lowTPS[srv.ID]++
		if reason == "" && sm.lowTPS[srv.ID] >= lowTPSSampleCount {
			reason = fmt.Sprintf("running slowly at %.1f ticks per second", *tps)
		}
	} else {
		sm.lowTPS[srv.ID] = 0
	}

	if reason == "" {
		sm.highStreak[srv.ID] = 0
		sm.alerted[srv.ID] = false
		return
	}

	sm.highStreak[srv.ID]++
	if (sm.highStreak[srv.ID] >= highSampleCount || sm.lowTPS[srv.ID] >= lowTPSSampleCount) && !sm.alerted[srv.ID] {
		sm.alerted[srv.ID] = true
		mins := highSampleCount * sampleEvery * int(pollInterval/time.Second) / 60
		engine.Emit(notify.ServerPerformance(srv.ID, srv.Name,
			fmt.Sprintf("%s for the last ~%d minutes.", reason, mins)))
	}
}
