package process

import (
	"regexp"
	"strings"
	"time"
)

// A player turned away by the whitelist is invisible to every other signal the
// agent has: they never reach the roster, never appear in `/list`, and leave no
// playerdata behind. The only trace is one console line at the moment the server
// closes the connection. This file turns that line into a structured record so
// the panel can tell an operator "someone tried to join" and offer to let them
// in, instead of the operator finding out hours later from a Discord message.

// JoinDenial is one player's rejected attempt to join a server. Attempts counts
// how many times the same player was turned away inside the coalescing window —
// a rejected player almost always retries, and twelve identical alerts help
// nobody.
type JoinDenial struct {
	Name string `json:"name"`
	// UUID is the profile id the server rejected. Empty on servers that log no
	// id (offline mode, and some Paper builds); a whitelist entry can still be
	// written from the name alone in that case.
	UUID string `json:"uuid,omitempty"`
	// Bedrock marks a player who came in through Geyser/Floodgate, recognised by
	// the Floodgate UUID signature. Whitelisting them takes a different path
	// (see applyBedrockWhitelist), and the panel has to say which one it means.
	Bedrock bool `json:"bedrock,omitempty"`

	// Reason is the kick message the server logged, kept verbatim so a custom
	// whitelist message still reads sensibly in the alert.
	Reason string `json:"reason,omitempty"`

	// FirstAt doubles as this record's identity. The panel re-reads the whole
	// unresolved set on every poll and needs to tell "the same person still
	// waiting" from "they came back an hour later", and (name, FirstAt) says that
	// without a sequence counter — which would reset on an agent restart and
	// strand every later denial behind a cursor that can never be reached again.
	FirstAt  int64 `json:"first_at"`
	LastAt   int64 `json:"last_at"`
	Attempts int   `json:"attempts"`
}

const (
	// denialCoalesce is how long repeat attempts by the same player fold into one
	// record instead of creating a new one. Reconnect spam from a client that
	// auto-retries lands well inside this.
	denialCoalesce = 5 * time.Minute
	// denialTTL is how long an unresolved denial stays reportable. Past this it
	// is stale news, and a record nobody acted on should not resurface days later
	// because the panel restarted.
	denialTTL = 30 * time.Minute
	// denialCap bounds the per-server record set so a hostile or broken client
	// cycling names cannot grow it without limit.
	denialCap = 20
)

var (
	// The console prefix (`[12:34:56] [Server thread/INFO]: `) separates a line
	// the *server* emitted from one a *player* typed. Requiring it is what stops
	// a chat message quoting a kick line from being read as a real denial.
	logPayloadRe = regexp.MustCompile(`\]:\s+(.*)$`)

	// Two shapes carry a login rejection, and which one a server uses depends on
	// its version and fork rather than anything we can detect up front:
	//   Disconnecting <profile> (/addr): <reason>
	//   <profile> (/addr) lost connection: <reason>
	// The subject is matched lazily up to the first colon-*space*, which no
	// socket address contains, so `(/127.0.0.1:25565)` stays part of the subject.
	disconnectingRe   = regexp.MustCompile(`^Disconnecting (.*?): (.+)$`)
	lostConnectionRe  = regexp.MustCompile(`^(.*?) lost connection: (.+)$`)
	trailingAddressRe = regexp.MustCompile(`\s*\(/\S*\)\s*$`)

	// Vanilla renders the profile as `com.mojang.authlib.GameProfile@1a2b[id=…,
	// name=…,properties={},legacy=false]`; Paper often logs the bare name. Each
	// field is matched independently because the id may be `<null>` in offline
	// mode and field order is not something the log format guarantees.
	profileIDRe   = regexp.MustCompile(`\bid=([0-9a-fA-F]{8}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{12})\b`)
	profileNameRe = regexp.MustCompile(`\bname=([^,\]]+)`)

	// Vanilla says "white-listed", Paper says "whitelisted", and an operator can
	// replace either with their own wording — but the two defaults cover servers
	// that were never reconfigured, which is the case worth detecting.
	notWhitelistedRe = regexp.MustCompile(`(?i)\bnot\s+white[-\s_]?listed\b`)
)

// parseJoinDenial extracts the player a console line says was refused by the
// whitelist. ok is false for every other line, including kicks for other reasons
// (a ban is a separate signal with a separate remedy).
func parseJoinDenial(line string) (name, uuid string, reason string, ok bool) {
	m := logPayloadRe.FindStringSubmatch(line)
	if m == nil {
		return "", "", "", false
	}
	payload := strings.TrimSpace(m[1])

	var subject string
	if sm := disconnectingRe.FindStringSubmatch(payload); sm != nil {
		subject, reason = sm[1], sm[2]
	} else if sm := lostConnectionRe.FindStringSubmatch(payload); sm != nil {
		subject, reason = sm[1], sm[2]
	} else {
		return "", "", "", false
	}

	reason = strings.TrimSpace(reason)
	if !notWhitelistedRe.MatchString(reason) {
		return "", "", "", false
	}

	name, uuid = identityFromSubject(subject)
	if name == "" {
		return "", "", "", false
	}
	return name, uuid, reason, true
}

// identityFromSubject pulls a player name (and profile id, when the server
// logged one) out of the subject half of a disconnect line.
func identityFromSubject(subject string) (name, uuid string) {
	subject = trailingAddressRe.ReplaceAllString(strings.TrimSpace(subject), "")

	if m := profileIDRe.FindStringSubmatch(subject); m != nil {
		uuid = m[1]
	}
	if m := profileNameRe.FindStringSubmatch(subject); m != nil {
		name = strings.TrimSpace(m[1])
	} else if !strings.Contains(subject, "GameProfile") {
		// A bare name, as Paper logs it. Anything still carrying GameProfile
		// syntax we failed to parse is dropped rather than guessed at.
		name = subject
	}

	if !plausiblePlayerName(name) {
		return "", ""
	}
	return name, uuid
}

// plausiblePlayerName rejects anything that is not a username the panel could
// act on. It is looser than validName, which would refuse a Floodgate name's
// prefix, and much stricter than "non-empty": this string ends up in an alert
// and, if the operator accepts the prompt, in a whitelist write, so a griefer
// must not be able to put an arbitrary string in front of an admin by typing it
// in chat. ApplyPlayerAction re-applies the exact per-edition rules later; this
// is the parser's own bound.
//
// Whitespace is rejected outright, which does mean a Bedrock player is missed on
// the minority of servers that set Floodgate's replace-spaces to false. That is
// the right way to be wrong here: a missed alert costs one operator one manual
// whitelist, whereas a name assembled from chat costs them trust in every alert.
func plausiblePlayerName(name string) bool {
	if name == "" || hasUnsafeChar(name) {
		return false
	}
	if r := []rune(name); len(r) > maxJavaNameLen {
		return false
	}
	// Chat is rendered as `<Speaker> message`; no username contains either
	// bracket, so their presence means the subject came from a player.
	return !strings.ContainsAny(name, "<>")
}

// denialLog is the per-server record set. It is held by the Manager rather than
// the Instance so a server that is restarted (or crashes) between the denial and
// the next poll does not lose the attempt — the operator still needs to know
// somebody knocked.
type denialLog struct {
	entries []JoinDenial
}

// record folds an attempt into the log: a repeat by the same player inside the
// coalescing window bumps the existing record, anything else appends a new one.
// Returns nothing because callers are console-line handlers with no way to
// report an error and nothing useful to do about one.
func (d *denialLog) record(name, uuid, reason string, now time.Time) {
	ms := now.UnixMilli()
	cutoff := now.Add(-denialCoalesce).UnixMilli()

	for i := range d.entries {
		e := &d.entries[i]
		if !strings.EqualFold(e.Name, name) || e.LastAt < cutoff {
			continue
		}
		e.LastAt = ms
		e.Attempts++
		e.Reason = reason
		// A later attempt may carry an id the first one lacked (a server that
		// logs `id=<null>` until the profile is resolved).
		if e.UUID == "" && uuid != "" {
			e.UUID = uuid
			e.Bedrock = isBedrockUUID(uuid)
		}
		return
	}

	d.entries = append(d.entries, JoinDenial{
		Name:     name,
		UUID:     uuid,
		Bedrock:  isBedrockUUID(uuid),
		Reason:   reason,
		FirstAt:  ms,
		LastAt:   ms,
		Attempts: 1,
	})
	if len(d.entries) > denialCap {
		d.entries = d.entries[len(d.entries)-denialCap:]
	}
}

// prune drops records that have aged out or that the operator has since acted
// on. Resolving against the live whitelist is what makes the panel's list mean
// "still waiting on you" rather than "happened at some point": whitelisting the
// player — from the alert, the players table, or the console — clears the entry
// on the next read, wherever it was done.
func (d *denialLog) prune(whitelisted func(name, uuid string) bool, now time.Time) {
	cutoff := now.Add(-denialTTL).UnixMilli()
	kept := d.entries[:0]
	for _, e := range d.entries {
		if e.LastAt < cutoff || whitelisted(e.Name, e.UUID) {
			continue
		}
		kept = append(kept, e)
	}
	d.entries = kept
}

// recordJoinDenial is the Instance-side hook: one console line said a player was
// refused by the whitelist.
func (m *Manager) recordJoinDenial(id, name, uuid, reason string) {
	m.denialMu.Lock()
	defer m.denialMu.Unlock()
	if m.denials == nil {
		m.denials = make(map[string]*denialLog)
	}
	d := m.denials[id]
	if d == nil {
		d = &denialLog{}
		m.denials[id] = d
	}
	d.record(name, uuid, reason, time.Now())
}

// JoinDenials returns the server's unresolved whitelist rejections, freshest
// last, after pruning the ones that have expired or been whitelisted.
func (m *Manager) JoinDenials(id string) []JoinDenial {
	m.denialMu.Lock()
	d := m.denials[id]
	if d == nil || len(d.entries) == 0 {
		m.denialMu.Unlock()
		return nil
	}
	m.denialMu.Unlock()

	// Read the whitelist outside the lock: it is file I/O, and the lock is also
	// taken from the console-line path, which must never wait on a disk read.
	dir, ok := m.GetDir(id)
	var wl []whitelistEntry
	if ok {
		wl = readWhitelist(dir)
	}
	whitelisted := func(name, uuid string) bool {
		for _, e := range wl {
			if strings.EqualFold(e.Name, name) || (uuid != "" && sameUUID(e.UUID, uuid)) {
				return true
			}
		}
		return false
	}

	m.denialMu.Lock()
	defer m.denialMu.Unlock()
	d = m.denials[id]
	if d == nil {
		return nil
	}
	d.prune(whitelisted, time.Now())
	if len(d.entries) == 0 {
		return nil
	}
	out := make([]JoinDenial, len(d.entries))
	copy(out, d.entries)
	return out
}

// clearJoinDenials drops a server's records, for use when the server itself goes
// away.
func (m *Manager) clearJoinDenials(id string) {
	m.denialMu.Lock()
	delete(m.denials, id)
	m.denialMu.Unlock()
}
