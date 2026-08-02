package poller

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/mcsm/api/internal/notify"
	"github.com/mcsm/api/internal/store"
)

// joinDenial mirrors the agent's process.JoinDenial. It is redeclared rather
// than imported because the API and the agent are separate modules that speak
// JSON, the same way ServerStats is.
type joinDenial struct {
	Name     string `json:"name"`
	UUID     string `json:"uuid"`
	Bedrock  bool   `json:"bedrock"`
	Reason   string `json:"reason"`
	FirstAt  int64  `json:"first_at"`
	Attempts int    `json:"attempts"`
}

// key identifies one denial record across polls. The agent keeps a record alive
// until the player is let in or it ages out, so identity has to survive repeat
// reports while still distinguishing the same person coming back later.
func (d joinDenial) key() string {
	return strings.ToLower(d.Name) + "@" + strconv.FormatInt(d.FirstAt, 10)
}

// denialTracker alerts once per rejected player.
//
// It holds the set of records already alerted on, intersected each sweep with
// what the agent still reports. That keeps it bounded (the agent caps its own
// list), makes it self-cleaning when a denial is resolved, and — unlike a
// high-water sequence number — leaves nothing stranded when an agent restarts or
// when a record in the middle of the list is whitelisted away.
type denialTracker struct {
	alerted map[string]map[string]struct{} // server id -> record keys
}

func newDenialTracker() *denialTracker {
	return &denialTracker{alerted: map[string]map[string]struct{}{}}
}

// observe emits an alert for each denial in this status payload that has not
// been alerted on yet.
func (t *denialTracker) observe(srv *store.Server, status map[string]any, engine *notify.Engine) {
	denials := parseDenials(status)
	if len(denials) == 0 {
		// Nobody is waiting: everything previously alerted on has been resolved or
		// aged out, so the server's ledger can go too.
		delete(t.alerted, srv.ID)
		return
	}

	seen := t.alerted[srv.ID]
	fresh := make(map[string]struct{}, len(denials))
	for _, d := range denials {
		if d.Name == "" {
			continue
		}
		k := d.key()
		fresh[k] = struct{}{}
		if _, ok := seen[k]; ok {
			continue
		}
		engine.Emit(notify.PlayerJoinDenied(srv.ID, srv.Name, d.Name, d.UUID, d.Bedrock, d.Attempts))
	}
	t.alerted[srv.ID] = fresh
}

// parseDenials pulls the typed list out of the generically-decoded status map.
func parseDenials(status map[string]any) []joinDenial {
	raw, ok := status["join_denied"]
	if !ok || raw == nil {
		return nil
	}
	// The status payload arrives as generic JSON, so re-marshal the one field we
	// want rather than hand-walking map[string]any.
	blob, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var denials []joinDenial
	if err := json.Unmarshal(blob, &denials); err != nil {
		return nil
	}
	return denials
}

// reap drops ledgers for servers that no longer exist, so the map does not grow
// for the life of the process as servers are deleted.
func (t *denialTracker) reap(live map[string]struct{}) {
	for id := range t.alerted {
		if _, ok := live[id]; !ok {
			delete(t.alerted, id)
		}
	}
}
