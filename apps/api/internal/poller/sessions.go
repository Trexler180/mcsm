package poller

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/store"
)

// sessionTracker turns roster snapshots into player_sessions rows: a player
// appearing opens a session, disappearing closes it, and a server going
// offline (or unreachable) closes everything. Resolution is the sampler
// cadence (~one minute), which is plenty for playtime accounting.
type sessionTracker struct {
	s *store.Store
	// open sessions per server: lowercased player name -> session id.
	open map[string]map[string]string
	// seeded marks servers whose open sessions were loaded from the DB, so an
	// API restart adopts still-valid sessions instead of double-opening them.
	seeded map[string]bool
}

func newSessionTracker(s *store.Store) *sessionTracker {
	return &sessionTracker{s: s, open: map[string]map[string]string{}, seeded: map[string]bool{}}
}

// observe processes one sampler snapshot. stats == nil means the server is not
// online (stopped, crashed, or its agent is unreachable).
func (t *sessionTracker) observe(ctx context.Context, srv *store.Server, stats *agent.ServerStats) {
	now := time.Now()

	if stats == nil {
		// Whatever we thought was open is over. This also covers rows left open
		// by a previous API run for a server that is down now.
		if len(t.open[srv.ID]) > 0 || !t.seeded[srv.ID] {
			if err := t.s.EndAllPlayerSessions(ctx, srv.ID, now); err != nil {
				log.Printf("sessions: end all %s: %v", srv.ID, err)
				return
			}
		}
		delete(t.open, srv.ID)
		t.seeded[srv.ID] = true
		return
	}

	cur := t.open[srv.ID]
	if cur == nil {
		cur = map[string]string{}
		t.open[srv.ID] = cur
	}
	if !t.seeded[srv.ID] {
		// First online observation since boot: adopt sessions a previous API run
		// left open. Players still on the server keep their original started_at;
		// the diff below closes the rest.
		if rows, err := t.s.OpenPlayerSessions(ctx, srv.ID); err == nil {
			for _, ps := range rows {
				cur[strings.ToLower(ps.PlayerName)] = ps.ID
			}
			t.seeded[srv.ID] = true
		}
	}

	online := make(map[string]agent.PlayerRef, len(stats.Players))
	for _, p := range stats.Players {
		if p.Name == "" {
			continue
		}
		online[strings.ToLower(p.Name)] = p
	}

	// Joins: online now, no open session.
	for key, p := range online {
		if _, ok := cur[key]; ok {
			continue
		}
		id, err := t.s.StartPlayerSession(ctx, srv.ID, p.Name, p.UUID, now)
		if err != nil {
			log.Printf("sessions: start %s/%s: %v", srv.ID, p.Name, err)
			continue
		}
		cur[key] = id
	}

	// Leaves: open session, not online anymore.
	for key, id := range cur {
		if _, ok := online[key]; ok {
			continue
		}
		if err := t.s.EndPlayerSession(ctx, id, now); err != nil {
			log.Printf("sessions: end %s: %v", id, err)
			continue
		}
		delete(cur, key)
	}
}
