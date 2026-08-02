package process

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// validName matches a legal Minecraft account name. Player names reach a live
// server's stdin, so anything outside this set is rejected to prevent console
// command injection (e.g. a newline in the name).
var validName = regexp.MustCompile(`^[A-Za-z0-9_]{1,16}$`)

// knownActions is the closed set of player administration actions.
var knownActions = map[string]bool{
	"op": true, "deop": true,
	"ban": true, "pardon": true, "kick": true,
	"ban_ip": true, "pardon_ip": true,
	"whitelist_add": true, "whitelist_remove": true,
}

// PlayerAction describes one player administration request. It is a struct
// rather than a parameter list because the fields are mostly action-specific:
// only ban/kick read Reason, only ban_ip/pardon_ip read IP, only op reads Level,
// and only a Bedrock whitelist change reads Gamertag.
type PlayerAction struct {
	Action string
	Name   string
	UUID   string
	Reason string

	// IP is the target address for ban_ip / pardon_ip.
	IP string

	// Level is the desired operator permission level (1–4) for op; 0 means "use
	// the default". It only applies to offline ops.json edits — a live server's
	// /op command grants the server's own default level.
	Level int

	// Bedrock routes a whitelist change through the Floodgate identity path
	// instead of the console. It comes from the panel's explicit Java/Bedrock
	// choice rather than being guessed from the name: a Floodgate prefix that is
	// empty or alphanumeric cannot be told apart from an ordinary Java name, and
	// guessing wrong would whitelist the wrong person.
	Bedrock bool

	// Gamertag is the Xbox gamertag to resolve for a Bedrock whitelist add. When
	// empty, an already-known Floodgate UUID is used instead — which is the case
	// for a player who is already in the roster.
	Gamertag string
}

// ApplyPlayerAction performs an op/whitelist/ban-style action against a player.
// When the server is running, the action is issued as a console command so the
// live server applies and persists it. When the server is stopped, the relevant
// config file (ops.json / whitelist.json / banned-players.json) is edited
// directly so the change still takes effect on next boot. kick is the one
// online-only action; Bedrock whitelist changes are the one action that ignores
// this split entirely (see applyBedrockWhitelist).
func (m *Manager) ApplyPlayerAction(id string, req PlayerAction) error {
	req.Name = strings.TrimSpace(req.Name)
	req.IP = strings.TrimSpace(req.IP)
	req.Gamertag = strings.TrimSpace(req.Gamertag)
	if !knownActions[req.Action] {
		return fmt.Errorf("unknown action %q", req.Action)
	}
	req.Reason = sanitizeReason(req.Reason)

	dir, ok := m.GetDir(id)
	if !ok {
		return fmt.Errorf("server directory not registered")
	}

	// IP bans operate on an address rather than a player, so they take a separate
	// path that validates the IP (which also reaches stdin) instead of a name.
	if req.Action == "ban_ip" || req.Action == "pardon_ip" {
		return m.applyIPAction(id, dir, req.Action, req.Name, req.IP, req.Reason)
	}

	if req.Bedrock && isWhitelistAction(req.Action) {
		return m.applyBedrockWhitelist(id, dir, req)
	}

	// validName covers Java accounts; validPlayerName also accepts a Bedrock
	// player's Floodgate-prefixed name while still rejecting anything that could
	// inject a second console command.
	if !validPlayerName(dir, req.Name) {
		return fmt.Errorf("invalid player name")
	}

	switch m.Status(id).Status {
	case StatusOnline, StatusStarting:
		cmd, err := onlineCommand(req.Action, req.Name, req.Reason)
		if err != nil {
			return err
		}
		return m.SendCommand(id, cmd)
	default:
		if err := applyOfflineAction(dir, req.Action, req.Name, req.UUID, req.Reason, req.Level); err != nil {
			return err
		}
		m.dropRosterCache(id)
		return nil
	}
}

func isWhitelistAction(action string) bool {
	return action == "whitelist_add" || action == "whitelist_remove"
}

// applyBedrockWhitelist edits whitelist.json directly and then has a running
// server re-read it, taking the same path whether the server is up or down.
//
// This deliberately breaks the online/offline split every other action follows.
// The console route cannot work here: `whitelist add <name>` makes the server
// resolve the name against Mojang, which has no record of a Floodgate name, so
// it fails for exactly the players this feature exists to admit. `whitelist
// reload` takes no arguments, so it is safe to send for a player the server
// could never have looked up itself.
func (m *Manager) applyBedrockWhitelist(id, dir string, req PlayerAction) error {
	wl := readWhitelist(dir)

	if req.Action == "whitelist_remove" {
		// Match on UUID first. A player who changes their gamertag keeps their
		// XUID, so the stored name can be stale while the UUID stays correct —
		// matching by name alone would leave the entry unremovable from the UI.
		switch {
		case isBedrockUUID(req.UUID):
			wl = without(wl, func(e whitelistEntry) bool { return !sameUUID(e.UUID, req.UUID) })
		case req.Name != "":
			wl = without(wl, func(e whitelistEntry) bool { return !strings.EqualFold(e.Name, req.Name) })
		default:
			return fmt.Errorf("a uuid or name is required to remove a whitelist entry")
		}
		return m.commitWhitelist(id, dir, wl)
	}

	uuid, name := req.UUID, req.Name
	if req.Gamertag != "" {
		// Resolved here rather than trusting a UUID the panel looked up moments
		// ago: the entry written is then always what the resolver actually
		// returns, so a stale preview cannot put the wrong person on the
		// whitelist. The lookup is memoised, so this is normally free.
		ident, err := m.ResolveBedrock(id, req.Gamertag)
		if err != nil {
			return err
		}
		uuid, name = ident.UUID, ident.Name
	}

	// No usable identity means no entry. Writing a name-only record here would
	// reproduce the defect this path exists to fix: vanilla keys the whitelist on
	// the profile UUID, so such an entry looks right in the UI and admits nobody.
	if !isBedrockUUID(uuid) {
		return fmt.Errorf("could not determine this player's Floodgate identity")
	}
	// hasControlChar rather than hasUnsafeChar: this name only ever reaches a
	// JSON file, and a server with replace-spaces disabled has spaces in it.
	if name == "" || hasControlChar(name) {
		return fmt.Errorf("invalid player name")
	}

	// Replace any existing entry for this UUID so a gamertag change refreshes the
	// stored name instead of leaving a duplicate behind.
	wl = without(wl, func(e whitelistEntry) bool { return !sameUUID(e.UUID, uuid) })
	wl = append(wl, whitelistEntry{UUID: uuid, Name: name})
	return m.commitWhitelist(id, dir, wl)
}

// commitWhitelist persists whitelist.json and asks a running server to re-read
// it, so the change takes effect immediately rather than at next boot.
func (m *Manager) commitWhitelist(id, dir string, wl []whitelistEntry) error {
	if err := writeJSONList(filepath.Join(dir, "whitelist.json"), wl); err != nil {
		return err
	}
	m.dropRosterCache(id)

	switch m.Status(id).Status {
	case StatusOnline, StatusStarting:
		return m.SendCommand(id, "whitelist reload")
	}
	return nil
}

// sameUUID compares two UUIDs ignoring case and dashes, since a minted UUID and
// one read back out of a config file are not always written the same way.
func sameUUID(a, b string) bool {
	norm := func(s string) string { return strings.ReplaceAll(strings.ToLower(s), "-", "") }
	return a != "" && norm(a) == norm(b)
}

// DeletePlayerData permanently removes a player's saved data from disk: their
// playerdata .dat (and Minecraft's .dat_old backup), their stats file, and their
// advancements file. It is offline-only — a running server keeps the player in
// memory and would rewrite the .dat on the next save, so deleting underneath it
// is racy and ineffective. Each removal is idempotent: a missing file is not an
// error, so a partially-present player still cleans up.
func (m *Manager) DeletePlayerData(id, uuid string) error {
	if !ValidUUID(uuid) {
		return fmt.Errorf("invalid player uuid")
	}
	switch m.Status(id).Status {
	case StatusOnline, StatusStarting:
		return fmt.Errorf("server must be offline to delete player data")
	}

	dir, ok := m.GetDir(id)
	if !ok {
		return fmt.Errorf("server directory not registered")
	}

	var targets []string
	if pd := playerDataDir(dir); pd != "" {
		targets = append(targets, filepath.Join(pd, uuid+".dat"), filepath.Join(pd, uuid+".dat_old"))
	}
	if sd := statsDir(dir); sd != "" {
		targets = append(targets, filepath.Join(sd, uuid+".json"))
	}
	if ad := advancementsDir(dir); ad != "" {
		targets = append(targets, filepath.Join(ad, uuid+".json"))
	}

	for _, path := range targets {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	m.dropRosterCache(id)
	return nil
}

// applyIPAction adds or removes a banned-ips.json entry. Online it runs the
// server's ban-ip/pardon-ip console commands; offline it edits the file. ban-ip
// may target a raw address or an online player's name (the server resolves the
// name to its current IP); pardon-ip and every offline edit require an explicit,
// validated IP since no live session exists to resolve a name against.
func (m *Manager) applyIPAction(id, dir, action, name, ip, reason string) error {
	online := false
	switch m.Status(id).Status {
	case StatusOnline, StatusStarting:
		online = true
	}

	if action == "pardon_ip" {
		if !validIP(ip) {
			return fmt.Errorf("invalid ip address")
		}
		if online {
			return m.SendCommand(id, "pardon-ip "+ip)
		}
		ips := without(readBannedIPs(dir), func(e bannedIPEntry) bool { return !strings.EqualFold(e.IP, ip) })
		if err := writeJSONList(filepath.Join(dir, "banned-ips.json"), ips); err != nil {
			return err
		}
		m.dropRosterCache(id)
		return nil
	}

	// ban_ip
	if online {
		target := ip
		if target == "" {
			// No address given: ban the named online player's current IP.
			if !validPlayerName(dir, name) {
				return fmt.Errorf("invalid player name")
			}
			target = name
		} else if !validIP(target) {
			return fmt.Errorf("invalid ip address")
		}
		cmd := "ban-ip " + target
		if reason != "" {
			cmd += " " + reason
		}
		return m.SendCommand(id, cmd)
	}

	// Offline we can only write a concrete address — there's no session to map a
	// name to a last-known IP.
	if !validIP(ip) {
		return fmt.Errorf("an ip address is required to ban while the server is offline")
	}
	if reason == "" {
		reason = "Banned by an operator."
	}
	// Replace any existing entry so a re-ban can update the reason.
	ips := without(readBannedIPs(dir), func(e bannedIPEntry) bool { return !strings.EqualFold(e.IP, ip) })
	ips = append(ips, bannedIPEntry{
		IP:      ip,
		Created: time.Now().Format("2006-01-02 15:04:05 -0700"),
		Source:  "Console",
		Expires: "forever",
		Reason:  reason,
	})
	if err := writeJSONList(filepath.Join(dir, "banned-ips.json"), ips); err != nil {
		return err
	}
	m.dropRosterCache(id)
	return nil
}

// validIP reports whether s is a parseable IPv4/IPv6 literal. Rejecting anything
// else keeps a crafted "address" from injecting a second console command.
func validIP(s string) bool {
	return s != "" && net.ParseIP(s) != nil
}

// dropRosterCache evicts the memoised offline roster for a server so the next
// read reflects a config change applied while it was stopped.
func (m *Manager) dropRosterCache(id string) {
	m.rosterMu.Lock()
	delete(m.roster, id)
	m.rosterMu.Unlock()
}

// sanitizeReason strips newlines (which would break out of a console command)
// and trims the reason to a sane length.
func sanitizeReason(reason string) string {
	reason = strings.ReplaceAll(reason, "\r", " ")
	reason = strings.ReplaceAll(reason, "\n", " ")
	reason = strings.TrimSpace(reason)
	if len(reason) > 200 {
		reason = reason[:200]
	}
	return reason
}

// onlineCommand maps an action to the console command the running server runs.
func onlineCommand(action, name, reason string) (string, error) {
	switch action {
	case "op":
		return "op " + name, nil
	case "deop":
		return "deop " + name, nil
	case "pardon":
		return "pardon " + name, nil
	case "whitelist_add":
		return "whitelist add " + name, nil
	case "whitelist_remove":
		return "whitelist remove " + name, nil
	case "ban":
		if reason != "" {
			return "ban " + name + " " + reason, nil
		}
		return "ban " + name, nil
	case "kick":
		if reason != "" {
			return "kick " + name + " " + reason, nil
		}
		return "kick " + name, nil
	}
	return "", fmt.Errorf("unknown action %q", action)
}

// applyOfflineAction edits the relevant config file directly. Each edit is
// idempotent (adding an existing entry is a no-op; removing a missing one
// succeeds), matching how the running server treats these commands.
func applyOfflineAction(dir, action, name, uuid, reason string, level int) error {
	switch action {
	case "kick":
		return fmt.Errorf("server must be online to kick players")

	case "op":
		ops := readOps(dir)
		if hasName(ops, func(e opEntry) string { return e.Name }, name) {
			return nil
		}
		ops = append(ops, opEntry{UUID: ensureUUID(dir, name, uuid), Name: name, Level: normalizeOpLevel(level)})
		return writeJSONList(filepath.Join(dir, "ops.json"), ops)

	case "deop":
		ops := without(readOps(dir), func(e opEntry) bool { return !strings.EqualFold(e.Name, name) })
		return writeJSONList(filepath.Join(dir, "ops.json"), ops)

	case "whitelist_add":
		wl := readWhitelist(dir)
		if hasName(wl, func(e whitelistEntry) string { return e.Name }, name) {
			return nil
		}
		wl = append(wl, whitelistEntry{UUID: ensureUUID(dir, name, uuid), Name: name})
		return writeJSONList(filepath.Join(dir, "whitelist.json"), wl)

	case "whitelist_remove":
		wl := without(readWhitelist(dir), func(e whitelistEntry) bool { return !strings.EqualFold(e.Name, name) })
		return writeJSONList(filepath.Join(dir, "whitelist.json"), wl)

	case "ban":
		// Replace any existing entry so a re-ban can update the reason.
		bans := without(readBannedPlayers(dir), func(e bannedEntry) bool { return !strings.EqualFold(e.Name, name) })
		if reason == "" {
			reason = "Banned by an operator."
		}
		bans = append(bans, bannedEntry{
			UUID:    ensureUUID(dir, name, uuid),
			Name:    name,
			Created: time.Now().Format("2006-01-02 15:04:05 -0700"),
			Source:  "Console",
			Expires: "forever",
			Reason:  reason,
		})
		return writeJSONList(filepath.Join(dir, "banned-players.json"), bans)

	case "pardon":
		bans := without(readBannedPlayers(dir), func(e bannedEntry) bool { return !strings.EqualFold(e.Name, name) })
		return writeJSONList(filepath.Join(dir, "banned-players.json"), bans)
	}
	return fmt.Errorf("unknown action %q", action)
}

// normalizeOpLevel clamps a requested operator permission level to Minecraft's
// 1–4 range, defaulting to 4 (full operator) when unset or out of range.
func normalizeOpLevel(level int) int {
	if level < 1 || level > 4 {
		return 4
	}
	return level
}

// without returns the items for which keep returns true (a filtered copy).
func without[T any](items []T, keep func(T) bool) []T {
	out := make([]T, 0, len(items))
	for _, it := range items {
		if keep(it) {
			out = append(out, it)
		}
	}
	return out
}

// hasName reports whether any item's name (via getName) matches, ignoring case.
func hasName[T any](items []T, getName func(T) string, name string) bool {
	for _, it := range items {
		if strings.EqualFold(getName(it), name) {
			return true
		}
	}
	return false
}

// writeJSONList atomically writes a pretty-printed JSON array (temp file +
// rename) so a crash mid-write can't corrupt the server's config file.
func writeJSONList(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename has consumed it

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// ensureUUID prefers a caller-supplied UUID, otherwise resolves one.
func ensureUUID(dir, name, uuid string) string {
	if uuid != "" {
		return uuid
	}
	return resolveUUID(dir, name)
}

// resolveUUID finds a player's UUID for a config file entry: the server's own
// usercache first, then (offline-mode) a deterministic offline UUID, otherwise
// Mojang. Returns "" if every source fails — the entry is still written, it just
// won't match by UUID until the player next connects and the server fixes it.
func resolveUUID(dir, name string) string {
	for u, n := range usercache(dir) {
		if strings.EqualFold(n, name) {
			return u
		}
	}
	if !onlineMode(dir) {
		return offlineUUID(name)
	}
	return mojangUUID(name)
}

// offlineUUID reproduces Java's UUID.nameUUIDFromBytes("OfflinePlayer:<name>"),
// the deterministic UUID an offline-mode server assigns a player.
func offlineUUID(name string) string {
	h := md5.Sum([]byte("OfflinePlayer:" + name))
	h[6] = (h[6] & 0x0f) | 0x30 // version 3
	h[8] = (h[8] & 0x3f) | 0x80 // IETF variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

// mojangUUID looks up an online-mode account's UUID. Best-effort: any failure
// (offline host, unknown name, timeout) yields "".
func mojangUUID(name string) string {
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get("https://api.mojang.com/users/profiles/minecraft/" + url.PathEscape(name))
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var body struct {
		ID string `json:"id"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil || len(body.ID) != 32 {
		return ""
	}
	id := strings.ToLower(body.ID)
	return id[0:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:32]
}
