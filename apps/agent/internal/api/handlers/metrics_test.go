package handlers

import (
	"testing"
	"time"

	"github.com/mcsm/agent/internal/link"
)

// linkedSink drives a sink to the state it would be in having received one
// snapshot. Reaching through the real Sink calls rather than building a State by
// hand keeps the test honest about what the sink actually records.
//
// The sink stamps arrival with its own clock, so age is simulated by asking
// tickFields about a later "now" rather than by mutating state behind its back.
func linkedSink(t *testing.T, serverID string, tps float64) *link.MemorySink {
	t.Helper()

	sink := link.NewMemorySink()
	sink.OnConnect(serverID, link.Hello{ModVersion: "1.0.2"})
	sink.OnSnapshot(serverID, link.Snapshot{TPS: link.TPS{M1: tps, M5: tps, M15: tps}})
	return sink
}

// A server whose helper mod is reporting puts its tick rate on the stream, keyed
// by when the snapshot arrived so the browser can tell a new reading from a
// restatement of the old one.
func TestTickFieldsReportsAFreshSnapshot(t *testing.T) {
	sink := linkedSink(t, "srv1", 19.98)

	tps, seq, ok := tickFields(sink, "srv1", time.Now())
	if !ok {
		t.Fatal("a snapshot that just arrived was not reported")
	}
	if tps != 19.98 {
		t.Errorf("tps %v, want 19.98", tps)
	}
	if seq == 0 {
		t.Error("tick_seq is 0 after a snapshot; it must count the ones received")
	}
}

// The dedupe key must not move on its own, and must move for every snapshot.
// Stable, or the browser appends a point on each 2s frame and draws a staircase
// at seven times the resolution the mod reports at. Distinct, or a reading is
// silently dropped.
//
// The back-to-back case is the one that rules out a timestamp: two snapshots
// within a millisecond of each other are indistinguishable by arrival time.
func TestTickFieldsKeyIsStablePerSnapshotAndDistinctBetween(t *testing.T) {
	sink := linkedSink(t, "srv1", 20)

	_, first, _ := tickFields(sink, "srv1", time.Now())
	_, second, _ := tickFields(sink, "srv1", time.Now().Add(2*time.Second))
	if first != second {
		t.Errorf("tick_seq changed between reads of one snapshot: %d then %d", first, second)
	}

	sink.OnSnapshot("srv1", link.Snapshot{TPS: link.TPS{M1: 19.4}})
	_, third, _ := tickFields(sink, "srv1", time.Now())
	if third == first {
		t.Error("tick_seq did not change when a new snapshot arrived")
	}

	sink.OnSnapshot("srv1", link.Snapshot{TPS: link.TPS{M1: 19.1}})
	_, fourth, _ := tickFields(sink, "srv1", time.Now())
	if fourth == third {
		t.Error("two snapshots in the same millisecond shared a key; one would be dropped")
	}
}

// Past the staleness bound the last reading stops being worth graphing as if it
// were current — the browser drops the series rather than holding a flat line
// from a mod that stopped talking.
func TestTickFieldsDropsAStaleSnapshot(t *testing.T) {
	sink := linkedSink(t, "srv1", 19.98)

	if _, _, ok := tickFields(sink, "srv1", time.Now().Add(tickStaleAfter-time.Second)); !ok {
		t.Error("a snapshot just inside the staleness bound was dropped")
	}
	if _, _, ok := tickFields(sink, "srv1", time.Now().Add(tickStaleAfter+time.Second)); ok {
		t.Error("a snapshot past the staleness bound was still reported")
	}
}

// A reconnect is not a reason to blank the graph: the last snapshot is seconds
// old and perfectly good. This mirrors the call vitals.go makes for the tiles.
func TestTickFieldsSurvivesADisconnect(t *testing.T) {
	sink := linkedSink(t, "srv1", 19.7)
	sink.OnDisconnect("srv1")

	tps, _, ok := tickFields(sink, "srv1", time.Now())
	if !ok {
		t.Fatal("tick data was dropped on disconnect; a brief reconnect must not blank the graph")
	}
	if tps != 19.7 {
		t.Errorf("tps %v, want the retained 19.7", tps)
	}
}

// The three ways there is simply nothing to say. Each must leave the frame
// exactly as it was before tick data existed, so a vanilla server's dashboard is
// untouched.
func TestTickFieldsAbsentWithoutData(t *testing.T) {
	t.Run("nil sink", func(t *testing.T) {
		if _, _, ok := tickFields(nil, "srv1", time.Now()); ok {
			t.Error("reported tick data with no sink at all")
		}
	})

	t.Run("unknown server", func(t *testing.T) {
		sink := linkedSink(t, "srv1", 20)
		if _, _, ok := tickFields(sink, "other", time.Now()); ok {
			t.Error("reported tick data for a server that never linked")
		}
	})

	t.Run("linked but never reported", func(t *testing.T) {
		// The window between a mod completing its handshake and its first
		// snapshot landing: connected, but with nothing yet to plot.
		sink := link.NewMemorySink()
		sink.OnConnect("srv1", link.Hello{ModVersion: "1.0.2"})
		if _, _, ok := tickFields(sink, "srv1", time.Now()); ok {
			t.Error("reported tick data before any snapshot arrived")
		}
	})
}
