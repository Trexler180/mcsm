package process

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Enchant is one enchantment on an item ({id, level}).
type Enchant struct {
	ID    string `json:"id"`
	Level int    `json:"level"`
}

// ItemStack is one stack in an inventory/ender-chest, identified by its slot.
// Damage/CustomName/Enchantments are best-effort and only present when the .dat
// stored them (so they stay omitted for plain stacks).
type ItemStack struct {
	Slot         int       `json:"slot"`
	ID           string    `json:"id"`
	Count        int       `json:"count"`
	Damage       int       `json:"damage,omitempty"`
	CustomName   string    `json:"custom_name,omitempty"`
	Enchantments []Enchant `json:"enchantments,omitempty"`
}

// PlayerDetail is the parsed snapshot of a single player's .dat file plus
// identity (name/online), status, and lifetime stats resolved by the caller.
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

	// SnapshotAt is the .dat file mtime — the moment this data was last written
	// to disk. It powers the "stale snapshot" hint, since Minecraft only flushes
	// playerdata on autosave/logout, not in real time.
	SnapshotAt time.Time `json:"snapshot_at,omitempty"`

	Op          bool         `json:"op,omitempty"`
	Whitelisted bool         `json:"whitelisted,omitempty"`
	Banned      bool         `json:"banned,omitempty"`
	BanReason   string       `json:"ban_reason,omitempty"`
	Bedrock     bool         `json:"bedrock,omitempty"`
	Stats       *PlayerStats `json:"stats,omitempty"`
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
// (lowercase `count` + `components`, post-1.20.5) and the legacy one (byte
// `Count` + `tag`), pulling out damage, custom name, and enchantments when
// present.
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
		tag := asMap(m["tag"])              // legacy (<=1.20.4)
		comp := asMap(m["components"])      // modern (>=1.20.5)
		out = append(out, ItemStack{
			Slot:         asInt(m["Slot"]),
			ID:           id,
			Count:        count,
			Damage:       parseDamage(tag, comp),
			CustomName:   parseCustomName(tag, comp),
			Enchantments: parseEnchantments(tag, comp),
		})
	}
	return out
}

// parseDamage reads the item's accumulated damage from either schema.
func parseDamage(tag, comp map[string]any) int {
	if comp != nil {
		if d, ok := comp["minecraft:damage"]; ok {
			return asInt(d)
		}
	}
	return asInt(tag["Damage"])
}

// parseEnchantments reads enchantments (and an enchanted book's stored
// enchantments) from either schema, returning them sorted by id for a stable
// UI. Modern: components["minecraft:enchantments"].levels is an id->level map.
// Legacy: tag.Enchantments is a list of {id, lvl}.
func parseEnchantments(tag, comp map[string]any) []Enchant {
	var out []Enchant
	if comp != nil {
		for _, key := range []string{"minecraft:enchantments", "minecraft:stored_enchantments"} {
			levels := asMap(asMap(comp[key])["levels"])
			for id, lvl := range levels {
				out = append(out, Enchant{ID: id, Level: asInt(lvl)})
			}
		}
	}
	for _, key := range []string{"Enchantments", "StoredEnchantments", "ench"} {
		for _, e := range asList(tag[key]) {
			m := asMap(e)
			id := asString(m["id"])
			if id == "" {
				continue
			}
			out = append(out, Enchant{ID: id, Level: asInt(m["lvl"])})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// parseCustomName reads a player-assigned item name from either schema and
// flattens its text-component JSON down to plain text.
func parseCustomName(tag, comp map[string]any) string {
	if comp != nil {
		if n, ok := comp["minecraft:custom_name"]; ok {
			return plainText(n)
		}
	}
	if display := asMap(tag["display"]); display != nil {
		if n, ok := display["Name"]; ok {
			return plainText(n)
		}
	}
	return ""
}

// plainText flattens a Minecraft text component (which may be a bare string, a
// JSON-encoded string, or a {text,extra:[...]} tree) into readable text.
func plainText(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	s = strings.TrimSpace(s)
	if s == "" || (s[0] != '{' && s[0] != '[' && s[0] != '"') {
		return s
	}
	var parsed any
	if json.Unmarshal([]byte(s), &parsed) != nil {
		return s
	}
	var b strings.Builder
	flattenComponent(parsed, &b)
	if out := b.String(); out != "" {
		return out
	}
	return s
}

func flattenComponent(v any, b *strings.Builder) {
	switch t := v.(type) {
	case string:
		b.WriteString(t)
	case []any:
		for _, e := range t {
			flattenComponent(e, b)
		}
	case map[string]any:
		if text, ok := t["text"].(string); ok {
			b.WriteString(text)
		}
		if extra, ok := t["extra"].([]any); ok {
			for _, e := range extra {
				flattenComponent(e, b)
			}
		}
	}
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
	datPath := filepath.Join(pdDir, uuid+".dat")
	root, err := parseNBTFile(datPath)
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
	if fi, err := os.Stat(datPath); err == nil {
		d.SnapshotAt = fi.ModTime()
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
