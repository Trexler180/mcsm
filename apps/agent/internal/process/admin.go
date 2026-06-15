package process

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
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
	"whitelist_add": true, "whitelist_remove": true,
}

// ApplyPlayerAction performs an op/whitelist/ban-style action against a player.
// When the server is running, the action is issued as a console command so the
// live server applies and persists it. When the server is stopped, the relevant
// config file (ops.json / whitelist.json / banned-players.json) is edited
// directly so the change still takes effect on next boot. kick is the one
// online-only action.
func (m *Manager) ApplyPlayerAction(id, action, name, uuid, reason string) error {
	name = strings.TrimSpace(name)
	if !knownActions[action] {
		return fmt.Errorf("unknown action %q", action)
	}
	reason = sanitizeReason(reason)

	dir, ok := m.GetDir(id)
	if !ok {
		return fmt.Errorf("server directory not registered")
	}
	// validName covers Java accounts; validPlayerName also accepts a Bedrock
	// player's Floodgate-prefixed name while still rejecting anything that could
	// inject a second console command.
	if !validPlayerName(dir, name) {
		return fmt.Errorf("invalid player name")
	}

	switch m.Status(id).Status {
	case StatusOnline, StatusStarting:
		cmd, err := onlineCommand(action, name, reason)
		if err != nil {
			return err
		}
		return m.SendCommand(id, cmd)
	default:
		if err := applyOfflineAction(dir, action, name, uuid, reason); err != nil {
			return err
		}
		// Drop the cached roster so the new status is reflected immediately.
		m.rosterMu.Lock()
		delete(m.roster, id)
		m.rosterMu.Unlock()
		return nil
	}
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
func applyOfflineAction(dir, action, name, uuid, reason string) error {
	switch action {
	case "kick":
		return fmt.Errorf("server must be online to kick players")

	case "op":
		ops := readOps(dir)
		if hasName(ops, func(e opEntry) string { return e.Name }, name) {
			return nil
		}
		ops = append(ops, opEntry{UUID: ensureUUID(dir, name, uuid), Name: name, Level: 4})
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
