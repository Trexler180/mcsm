package process

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// The standard server config files. ops.json/whitelist.json/banned-players.json
// all hold a JSON array of objects keyed by uuid + name; ops carry a permission
// level and bans carry a reason/expiry.

type opEntry struct {
	UUID                string `json:"uuid"`
	Name                string `json:"name"`
	Level               int    `json:"level"`
	BypassesPlayerLimit bool   `json:"bypassesPlayerLimit"`
}

type whitelistEntry struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

type bannedEntry struct {
	UUID    string `json:"uuid"`
	Name    string `json:"name"`
	Created string `json:"created,omitempty"`
	Source  string `json:"source,omitempty"`
	Expires string `json:"expires,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// bannedIPEntry mirrors a banned-ips.json record. Unlike a player ban it is
// keyed by IP address and carries no UUID/name, so it lives outside the player
// roster.
type bannedIPEntry struct {
	IP      string `json:"ip"`
	Created string `json:"created,omitempty"`
	Source  string `json:"source,omitempty"`
	Expires string `json:"expires,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// readJSONList parses a JSON array file into a slice. A missing or corrupt file
// yields nil (never an error) so callers can treat "no file" as "empty" — the
// same tolerance OfflinePlayers/usercache already rely on.
func readJSONList[T any](path string) []T {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []T
	if json.Unmarshal(data, &out) != nil {
		return nil
	}
	return out
}

func readOps(dir string) []opEntry { return readJSONList[opEntry](filepath.Join(dir, "ops.json")) }
func readWhitelist(dir string) []whitelistEntry {
	return readJSONList[whitelistEntry](filepath.Join(dir, "whitelist.json"))
}
func readBannedPlayers(dir string) []bannedEntry {
	return readJSONList[bannedEntry](filepath.Join(dir, "banned-players.json"))
}
func readBannedIPs(dir string) []bannedIPEntry {
	return readJSONList[bannedIPEntry](filepath.Join(dir, "banned-ips.json"))
}

// onlineMode reports the server's online-mode setting (default true when the
// property or file is absent, matching vanilla). Drives offline UUID handling.
func onlineMode(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "server.properties"))
	if err != nil {
		return true
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "online-mode="); ok {
			return strings.TrimSpace(v) != "false"
		}
	}
	return true
}

// playerState is the op/whitelist/ban status read from a server's config files,
// indexed by lowercased player name (the key the roster merges on).
type playerState struct {
	ops        map[string]opEntry
	whitelist  map[string]struct{}
	banned     map[string]bannedEntry
	uuidByName map[string]string
}

func (s *playerState) indexIdentity(name, uuid string) {
	if name == "" {
		return
	}
	if uuid != "" {
		if _, ok := s.uuidByName[strings.ToLower(name)]; !ok {
			s.uuidByName[strings.ToLower(name)] = uuid
		}
	}
}

// names returns every distinct player name mentioned across the state files.
// Used to surface players who hold a status (banned/op/whitelisted) but have no
// playerdata .dat of their own.
func (s *playerState) names() map[string]string {
	out := map[string]string{}
	add := func(name string) {
		if name != "" {
			out[strings.ToLower(name)] = name
		}
	}
	for _, e := range s.ops {
		add(e.Name)
	}
	for _, e := range s.banned {
		add(e.Name)
	}
	// whitelist is a name-keyed set; recover the original casing from uuidByName
	// is not possible, so fall back to the lowercased key.
	for k := range s.whitelist {
		if _, ok := out[k]; !ok {
			out[k] = k
		}
	}
	return out
}

// stamp annotates a Player with its op/whitelist/ban status and backfills its
// UUID from the state files when the roster didn't already supply one.
func (s *playerState) stamp(p *Player) {
	key := strings.ToLower(p.Name)
	if op, ok := s.ops[key]; ok {
		p.Op = true
		p.OpLevel = op.Level
	}
	if _, ok := s.whitelist[key]; ok {
		p.Whitelisted = true
	}
	if b, ok := s.banned[key]; ok {
		p.Banned = true
		p.BanReason = b.Reason
	}
	if p.UUID == "" {
		if u := s.uuidByName[key]; u != "" {
			p.UUID = u
		}
	}
}

// readServerState loads ops/whitelist/banned-players into a name-indexed view.
func readServerState(dir string) playerState {
	st := playerState{
		ops:        map[string]opEntry{},
		whitelist:  map[string]struct{}{},
		banned:     map[string]bannedEntry{},
		uuidByName: map[string]string{},
	}
	for _, e := range readOps(dir) {
		st.ops[strings.ToLower(e.Name)] = e
		st.indexIdentity(e.Name, e.UUID)
	}
	for _, e := range readWhitelist(dir) {
		st.whitelist[strings.ToLower(e.Name)] = struct{}{}
		st.indexIdentity(e.Name, e.UUID)
	}
	for _, e := range readBannedPlayers(dir) {
		st.banned[strings.ToLower(e.Name)] = e
		st.indexIdentity(e.Name, e.UUID)
	}
	return st
}
