package mc

import "testing"

func TestSortGameVersionsNewestFirst(t *testing.T) {
	versions := []GameVersion{
		{Version: "1.21.11-rc3", Stable: false},
		{Version: "1.21.10", Stable: true},
		{Version: "26.1.2", Stable: true},
		{Version: "1.21.11", Stable: true},
		{Version: "1.20.6", Stable: true},
	}
	sortGameVersions(versions)

	got := make([]string, len(versions))
	for i, v := range versions {
		got[i] = v.Version
	}
	want := []string{"26.1.2", "1.21.11", "1.21.11-rc3", "1.21.10", "1.20.6"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestStableGameVersion(t *testing.T) {
	if !stableGameVersion("1.21.11") {
		t.Fatal("release should be stable")
	}
	if stableGameVersion("1.21.11-rc3") {
		t.Fatal("release candidate should not be stable")
	}
}
