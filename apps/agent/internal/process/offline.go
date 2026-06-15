package process

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type usercacheEntry struct {
	Name string `json:"name"`
	UUID string `json:"uuid"`
}

// levelName reads the world directory name from server.properties, defaulting
// to "world" when the file or the level-name key is absent.
func levelName(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "server.properties"))
	if err != nil {
		return "world"
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if name, ok := strings.CutPrefix(line, "level-name="); ok {
			if name = strings.TrimSpace(name); name != "" {
				return name
			}
		}
	}
	return "world"
}

// usercache maps a lowercased player UUID to its last-known name, read from the
// server's usercache.json. Missing/corrupt file yields an empty map.
func usercache(dir string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(filepath.Join(dir, "usercache.json"))
	if err != nil {
		return out
	}
	var entries []usercacheEntry
	if json.Unmarshal(data, &entries) != nil {
		return out
	}
	for _, e := range entries {
		if e.UUID != "" && e.Name != "" {
			out[strings.ToLower(e.UUID)] = e.Name
		}
	}
	return out
}

// playerDataDir finds the world's per-player .dat directory, handling both the
// classic layout (<world>/playerdata) and the newer layout introduced around
// MC 26.x (<world>/players/data). Returns "" when neither exists.
func playerDataDir(dir string) string {
	world := filepath.Join(dir, levelName(dir))
	for _, candidate := range []string{
		filepath.Join(world, "players", "data"),
		filepath.Join(world, "playerdata"),
	} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}

// OfflinePlayers reads the per-player .dat files in the world's playerdata
// directory and returns one Player per file. The file mtime is used as the
// last-seen time and usercache.json resolves UUIDs to names; players whose name
// can't be resolved fall back to their UUID. A missing playerdata dir (server
// never generated a world yet) returns an empty roster, not an error.
func OfflinePlayers(dir string) ([]Player, error) {
	pdDir := playerDataDir(dir)
	if pdDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(pdDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	names := usercache(dir)
	out := make([]Player, 0, len(entries))
	for _, e := range entries {
		// Skip dirs and Minecraft's `.dat_old` backups; only take `.dat`.
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".dat") {
			continue
		}
		uuid := strings.TrimSuffix(e.Name(), ".dat")
		info, err := e.Info()
		if err != nil {
			continue
		}
		name := names[strings.ToLower(uuid)]
		if name == "" {
			name = uuid
		}
		out = append(out, Player{
			Name:     name,
			UUID:     uuid,
			Online:   false,
			LastSeen: info.ModTime(),
		})
	}
	return out, nil
}

// buildOfflineRoster reads the world's offline players and stamps each with op/
// whitelist/ban status. Players who hold a status but have no playerdata of
// their own (e.g. someone banned or whitelisted by name before they ever
// joined) are appended as state-only entries so they remain manageable.
func buildOfflineRoster(dir string, state playerState) []Player {
	players, _ := OfflinePlayers(dir)
	prefix, _ := bedrockPrefix(dir)

	seen := make(map[string]bool, len(players))
	for i := range players {
		seen[strings.ToLower(players[i].Name)] = true
		state.stamp(&players[i])
		stampBedrock(&players[i], prefix)
	}

	for key, name := range state.names() {
		if seen[key] {
			continue
		}
		seen[key] = true
		p := Player{Name: name, Online: false}
		state.stamp(&p)
		stampBedrock(&p, prefix)
		players = append(players, p)
	}
	return players
}

// rosterFingerprint is a cheap signature of the files AllPlayers reads. It
// changes whenever a player joins/leaves the world (playerdata dir mtime) or
// op/whitelist/ban state is edited, which is exactly when the cached roster
// must be rebuilt. Per-.dat content changes (which only move an existing
// player's last_seen) are intentionally not tracked, to keep the common online
// poll cheap; the detail view reads each .dat live anyway.
func rosterFingerprint(dir string) string {
	var b strings.Builder
	paths := []string{
		playerDataDir(dir),
		filepath.Join(dir, "usercache.json"),
		filepath.Join(dir, "ops.json"),
		filepath.Join(dir, "whitelist.json"),
		filepath.Join(dir, "banned-players.json"),
	}
	for _, p := range paths {
		if p == "" {
			b.WriteByte('-')
			continue
		}
		if fi, err := os.Stat(p); err == nil {
			fmt.Fprintf(&b, "%d:%d;", fi.ModTime().UnixNano(), fi.Size())
		} else {
			b.WriteByte('x')
		}
	}
	return b.String()
}
