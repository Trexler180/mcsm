package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/mcsm/api/internal/store"
)

// The alias table exists because Fabric API's loader mod id ("fabric") is not
// its Modrinth slug ("fabric-api"). If this mapping regresses, the one-click
// fix for the single most common missing dependency silently stops working.
func TestLoaderModAliasesResolveFabricAPI(t *testing.T) {
	for _, id := range []string{"fabric", "fabric-api", "fabric-resource-loader-v0"} {
		got := loaderModAliases[id]
		if len(got) == 0 || got[0] != "fabric-api" {
			t.Errorf("alias for %q = %v, want [fabric-api]", id, got)
		}
	}
}

// lookupMissingDepProject builds its candidate list from the alias table; doing
// that with a bare append would write through into the table's backing array
// and poison later lookups. Exercise the same construction twice.
func TestAliasTableNotMutatedByCandidateBuild(t *testing.T) {
	build := func(modID string) []string {
		aliases := loaderModAliases[strings.ToLower(modID)]
		candidates := make([]string, 0, len(aliases)+1)
		candidates = append(candidates, aliases...)
		candidates = append(candidates, modID)
		return candidates
	}

	if got := build("fabric"); len(got) != 2 || got[0] != "fabric-api" || got[1] != "fabric" {
		t.Fatalf("candidates = %v, want [fabric-api fabric]", got)
	}
	build("fabric")
	if got := loaderModAliases["fabric"]; len(got) != 1 || got[0] != "fabric-api" {
		t.Errorf("alias table mutated: fabric -> %v", got)
	}
}

// "minecraft" and "java" are reported missing on a version mismatch, but they
// can't be downloaded. Resolution must reject them before any lookup so the
// panel never offers an install that cannot work.
func TestResolveMissingDepRejectsPlatformIDs(t *testing.T) {
	h := &ModHandlers{}
	srv := &store.Server{Platform: "fabric", MCVersion: "1.21.1"}

	for _, id := range []string{"minecraft", "java", "fabricloader"} {
		res := h.resolveMissingDep(context.Background(), srv, nil, id)
		if res.Found {
			t.Errorf("%q resolved as installable", id)
		}
		if res.Error == "" {
			t.Errorf("%q returned no explanation", id)
		}
		if res.ProjectID != "" {
			t.Errorf("%q returned project id %q", id, res.ProjectID)
		}
	}
}

func TestNormalizeModID(t *testing.T) {
	cases := map[string]string{
		"cloth-config":  "clothconfig",
		"Cloth Config":  "clothconfig",
		"clothconfig":   "clothconfig",
		"fabric-api":    "fabricapi",
		"Fabric API":    "fabricapi",
		"forge_config1": "forgeconfig1",
	}
	for in, want := range cases {
		if got := normalizeModID(in); got != want {
			t.Errorf("normalizeModID(%q) = %q, want %q", in, got, want)
		}
	}
}
