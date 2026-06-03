package process

import (
	"fmt"
	"path/filepath"
)

// ItemStack is one stack in an inventory/ender-chest, identified by its slot.
type ItemStack struct {
	Slot  int    `json:"slot"`
	ID    string `json:"id"`
	Count int    `json:"count"`
}

// PlayerDetail is the parsed snapshot of a single player's .dat file plus
// identity (name/online) resolved by the caller.
type PlayerDetail struct {
	Name         string      `json:"name"`
	UUID         string      `json:"uuid"`
	Online       bool        `json:"online"`
	Health       float64     `json:"health"`
	MaxHealth    float64     `json:"max_health"`
	Food         int         `json:"food"`
	XpLevel      int         `json:"xp_level"`
	XpTotal      int         `json:"xp_total"`
	GameMode     int         `json:"game_mode"`
	Dimension    string      `json:"dimension"`
	Pos          []float64   `json:"pos"`
	Score        int         `json:"score"`
	SelectedSlot int         `json:"selected_slot"`
	Inventory    []ItemStack `json:"inventory"`
	EnderChest   []ItemStack `json:"ender_chest"`
}

// Loose accessors over the generic NBT tree. Everything numeric is stored as
// int64/float64 by the reader, so these coerce between them tolerantly.

func asInt(v any) int {
	switch t := v.(type) {
	case int64:
		return int(t)
	case float64:
		return int(t)
	}
	return 0
}

func asFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int64:
		return float64(t)
	}
	return 0
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// parseItems reads an inventory-style list. Handles both the modern item format
// (lowercase `count`, post-1.20.5) and the legacy one (byte `Count`).
func parseItems(v any) []ItemStack {
	list := asList(v)
	out := make([]ItemStack, 0, len(list))
	for _, e := range list {
		m := asMap(e)
		id := asString(m["id"])
		if id == "" {
			continue
		}
		count := asInt(m["count"])
		if count == 0 {
			count = asInt(m["Count"])
		}
		out = append(out, ItemStack{Slot: asInt(m["Slot"]), ID: id, Count: count})
	}
	return out
}

// maxHealth pulls the max-health attribute base, falling back to 20. Handles
// both the modern (`id`/`base`) and legacy (`Name`/`Base`) attribute schemas.
func maxHealth(root map[string]any) float64 {
	for _, a := range asList(root["attributes"]) {
		m := asMap(a)
		name := asString(m["id"])
		if name == "" {
			name = asString(m["Name"])
		}
		if name == "minecraft:max_health" || name == "minecraft:generic.max_health" {
			if b, ok := m["base"]; ok {
				return asFloat(b)
			}
			if b, ok := m["Base"]; ok {
				return asFloat(b)
			}
		}
	}
	return 20
}

// ReadPlayerDetail parses one player's .dat file from the world's playerdata
// directory into a PlayerDetail (without name/online, which the caller fills).
func ReadPlayerDetail(dir, uuid string) (*PlayerDetail, error) {
	pdDir := playerDataDir(dir)
	if pdDir == "" {
		return nil, fmt.Errorf("no playerdata directory")
	}
	root, err := parseNBTFile(filepath.Join(pdDir, uuid+".dat"))
	if err != nil {
		return nil, err
	}

	d := &PlayerDetail{
		UUID:         uuid,
		Health:       asFloat(root["Health"]),
		MaxHealth:    maxHealth(root),
		Food:         asInt(root["foodLevel"]),
		XpLevel:      asInt(root["XpLevel"]),
		XpTotal:      asInt(root["XpTotal"]),
		GameMode:     asInt(root["playerGameType"]),
		Dimension:    asString(root["Dimension"]),
		Score:        asInt(root["Score"]),
		SelectedSlot: asInt(root["SelectedItemSlot"]),
		Inventory:    parseItems(root["Inventory"]),
		EnderChest:   parseItems(root["EnderItems"]),
	}
	for _, p := range asList(root["Pos"]) {
		d.Pos = append(d.Pos, asFloat(p))
	}
	return d, nil
}

// ValidUUID guards the path component: only hex digits and dashes, so a UUID
// can't escape the playerdata directory.
func ValidUUID(s string) bool {
	if len(s) < 32 || len(s) > 36 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') || c == '-') {
			return false
		}
	}
	return true
}
