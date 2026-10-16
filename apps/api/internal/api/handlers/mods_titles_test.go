package handlers

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/mcsm/api/internal/store"
)

func titledMod(id, source, project, name string) *store.InstalledMod {
	m := &store.InstalledMod{ID: id, Source: source, Name: name}
	if project != "" {
		m.SourceID = &project
	}
	return m
}

func TestResolveTitlesRenamesVersionNamedMods(t *testing.T) {
	mods := []*store.InstalledMod{
		titledMod("a", "hangar", "Chunky", "1.4.40"),
		titledMod("b", "modrinth", "AANobbMI", "Sodium 0.6.0"),
		titledMod("c", "modrinth", "P7dR8mSH", "Fabric API"),
		titledMod("d", "custom", "", "MyPlugin"),
	}
	batched := 0
	renames, answered := resolveTitles(context.Background(), mods, func(string) bool { return false }, titleLookups{
		batch: func(_ context.Context, ids []string) (map[string]string, error) {
			batched++
			if len(ids) != 2 {
				t.Fatalf("batch ids = %v, want both Modrinth projects", ids)
			}
			return map[string]string{"AANobbMI": "Sodium", "P7dR8mSH": "Fabric API"}, nil
		},
		one: func(_ context.Context, source, id string) (string, error) {
			if source != "hangar" || id != "Chunky" {
				t.Fatalf("unexpected lookup %s/%s", source, id)
			}
			return "Chunky", nil
		},
		enabled: func(string) bool { return true },
	})
	if batched != 1 {
		t.Fatalf("Modrinth asked %d times, want one batch", batched)
	}
	want := map[string]string{"a": "Chunky", "b": "Sodium"}
	if fmt.Sprint(renames) != fmt.Sprint(want) {
		t.Fatalf("renames = %v, want %v", renames, want)
	}
	if len(answered) != 3 {
		t.Fatalf("answered = %v, want the three tracked mods", answered)
	}
}

func TestResolveTitlesSkipsCheckedAndRetriesFailures(t *testing.T) {
	mods := []*store.InstalledMod{
		titledMod("a", "hangar", "Chunky", "1.4.40"),
		titledMod("b", "spigotmc", "1234", "2.0"),
		titledMod("c", "curseforge", "5678", "jei-1.21.jar"),
	}
	renames, answered := resolveTitles(context.Background(), mods, func(id string) bool { return id == "a" }, titleLookups{
		batch: func(context.Context, []string) (map[string]string, error) {
			t.Fatal("no Modrinth mods")
			return nil, nil
		},
		one: func(_ context.Context, source, _ string) (string, error) {
			if source == "hangar" {
				t.Fatal("asked about a mod already checked")
			}
			return "", errors.New("site down")
		},
		enabled: func(source string) bool { return source != "curseforge" },
	})
	if len(renames) != 0 || len(answered) != 0 {
		t.Fatalf("renames = %v, answered = %v; a failed lookup must be retried later", renames, answered)
	}
}

func TestResolveTitlesCapsLookupsPerPass(t *testing.T) {
	var mods []*store.InstalledMod
	for i := 0; i < maxTitleLookupsPerPass+5; i++ {
		mods = append(mods, titledMod(fmt.Sprint(i), "hangar", fmt.Sprint("p", i), "1.0"))
	}
	asked := 0
	_, answered := resolveTitles(context.Background(), mods, func(string) bool { return false }, titleLookups{
		one:     func(context.Context, string, string) (string, error) { asked++; return "Title", nil },
		enabled: func(string) bool { return true },
	})
	if asked != maxTitleLookupsPerPass || len(answered) != maxTitleLookupsPerPass {
		t.Fatalf("asked %d, answered %d; want %d", asked, len(answered), maxTitleLookupsPerPass)
	}
}
