package process

import "fmt"

// Bans is a server's full ban state read from disk: player bans
// (banned-players.json) and IP bans (banned-ips.json), each with the metadata
// the server stores (reason, created/expires timestamps, source).
type Bans struct {
	Players []bannedEntry   `json:"players"`
	IPs     []bannedIPEntry `json:"ips"`
}

// Bans returns the server's player and IP ban lists. A missing config file
// yields an empty list (never an error), matching how the roster tolerates
// absent files — the server simply hasn't banned anyone yet.
func (m *Manager) Bans(id string) (Bans, error) {
	dir, ok := m.GetDir(id)
	if !ok {
		return Bans{}, fmt.Errorf("server directory not registered")
	}
	players := readBannedPlayers(dir)
	if players == nil {
		players = []bannedEntry{}
	}
	ips := readBannedIPs(dir)
	if ips == nil {
		ips = []bannedIPEntry{}
	}
	return Bans{Players: players, IPs: ips}, nil
}
