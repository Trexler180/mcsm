package process

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// PlayerStats is a small, UI-friendly subset of a player's lifetime statistics
// read from the world's stats/<uuid>.json file.
type PlayerStats struct {
	PlayTimeTicks int64 `json:"play_time_ticks,omitempty"`
	Deaths        int   `json:"deaths,omitempty"`
	PlayerKills   int   `json:"player_kills,omitempty"`
	MobKills      int   `json:"mob_kills,omitempty"`
	Jumps         int   `json:"jumps,omitempty"`
	WalkedCm      int64 `json:"walked_cm,omitempty"`
}

// statsDir finds the world's per-player stats directory, handling the classic
// (<world>/stats) and newer (<world>/players/stats) layouts. Returns "" when
// neither exists.
func statsDir(dir string) string {
	world := filepath.Join(dir, levelName(dir))
	for _, candidate := range []string{
		filepath.Join(world, "players", "stats"),
		filepath.Join(world, "stats"),
	} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}

// advancementsDir finds the world's per-player advancements directory, handling
// the classic (<world>/advancements) and newer (<world>/players/advancements)
// layouts. Returns "" when neither exists.
func advancementsDir(dir string) string {
	world := filepath.Join(dir, levelName(dir))
	for _, candidate := range []string{
		filepath.Join(world, "players", "advancements"),
		filepath.Join(world, "advancements"),
	} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}

// readPlayerStats parses one player's stats file. A missing or unreadable file
// yields nil (the player simply has no stats to show), never an error.
func readPlayerStats(dir, uuid string) *PlayerStats {
	sd := statsDir(dir)
	if sd == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(sd, uuid+".json"))
	if err != nil {
		return nil
	}
	var file struct {
		Stats map[string]map[string]int64 `json:"stats"`
	}
	if json.Unmarshal(data, &file) != nil {
		return nil
	}
	custom := file.Stats["minecraft:custom"]
	if custom == nil {
		return nil
	}
	playTime := custom["minecraft:play_time"]
	if playTime == 0 {
		// Pre-1.17 stored ticks under play_one_minute (a historical misnomer).
		playTime = custom["minecraft:play_one_minute"]
	}
	return &PlayerStats{
		PlayTimeTicks: playTime,
		Deaths:        int(custom["minecraft:deaths"]),
		PlayerKills:   int(custom["minecraft:player_kills"]),
		MobKills:      int(custom["minecraft:mob_kills"]),
		Jumps:         int(custom["minecraft:jump"]),
		WalkedCm:      custom["minecraft:walk_one_cm"],
	}
}
