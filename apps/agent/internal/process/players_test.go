package process

import (
	"reflect"
	"testing"
	"time"
)

func TestParsePlayerListLine(t *testing.T) {
	cases := []struct {
		name string
		line string
		want []string
		ok   bool
	}{
		{
			name: "vanilla names",
			line: "[12:00:00] [Server thread/INFO]: There are 2 of a max of 20 players online: Alice, Bob",
			want: []string{"Alice", "Bob"},
			ok:   true,
		},
		{
			name: "empty roster",
			line: "[12:00:00] [Server thread/INFO]: There are 0 of a max of 20 players online:",
			want: []string{},
			ok:   true,
		},
		{
			name: "slash count",
			line: "[12:00:00] [Server thread/INFO]: There are 1/20 players online: Steve",
			want: []string{"Steve"},
			ok:   true,
		},
		{
			name: "chat message ignored",
			line: "[12:00:00] [Server thread/INFO]: <Alice> There are 2 of a max of 20 players online: Alice, Bob",
			ok:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parsePlayerListLine(tc.line)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestReplacePlayersUsesListAsAuthoritativeRoster(t *testing.T) {
	inst := newInstance("server", StartConfig{})
	joined := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	refreshed := joined.Add(10 * time.Minute)

	inst.players["Alice"] = joined
	inst.players["Gone"] = joined

	inst.replacePlayers([]string{"Alice", "Bob"}, refreshed)

	got := map[string]Player{}
	for _, p := range inst.Players() {
		got[p.Name] = p
	}

	if _, ok := got["Gone"]; ok {
		t.Fatal("expected player omitted from /list to be removed")
	}
	if got["Alice"].JoinedAt != joined {
		t.Fatalf("Alice joined_at = %v, want %v", got["Alice"].JoinedAt, joined)
	}
	if got["Bob"].JoinedAt != refreshed {
		t.Fatalf("Bob joined_at = %v, want %v", got["Bob"].JoinedAt, refreshed)
	}
	if !got["Alice"].Online || !got["Bob"].Online {
		t.Fatal("expected refreshed players to be online")
	}
}
