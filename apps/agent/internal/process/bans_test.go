package process

import (
	"path/filepath"
	"testing"
)

func TestValidIP(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"203.0.113.7", true},
		{"::1", true},
		{"2001:db8::1", true},
		{"", false},
		{"not-an-ip", false},
		// Anything that could break out of the ban-ip console command must fail.
		{"203.0.113.7\nstop", false},
		{"203.0.113.7 op Notch", false},
		{"999.999.999.999", false},
	}
	for _, c := range cases {
		if got := validIP(c.in); got != c.want {
			t.Errorf("validIP(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestReadBannedIPsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "banned-ips.json")

	if got := readBannedIPs(dir); got != nil {
		t.Fatalf("missing file should read as nil, got %#v", got)
	}

	entries := []bannedIPEntry{
		{IP: "203.0.113.7", Created: "2024-01-02 15:04:05 +0000", Source: "Console", Expires: "forever", Reason: "spam"},
	}
	if err := writeJSONList(path, entries); err != nil {
		t.Fatal(err)
	}

	got := readBannedIPs(dir)
	if len(got) != 1 || got[0].IP != "203.0.113.7" || got[0].Reason != "spam" {
		t.Fatalf("round-trip mismatch: %#v", got)
	}
}
