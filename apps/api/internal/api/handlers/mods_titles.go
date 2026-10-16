package handlers

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/mcsm/api/internal/store"
)

// A mod's name is its project's title: it reads well and doesn't change when
// the mod updates. Rows written before that took the version's name instead
// ("1.4.40" from Hangar, "Sodium 0.6.0" from Modrinth); rescans repair them.

// titleLookupTimeout bounds the title lookup an install or update makes. On
// timeout the version's name stands in until the next rescan.
const titleLookupTimeout = 5 * time.Second

// maxTitleLookupsPerPass caps lookups one rescan makes against sources that
// have no batch call; the rest wait for the next pass.
const maxTitleLookupsPerPass = 20

// projectTitle is the project's title, or fallback when it can't be had.
func (h *ModHandlers) projectTitle(ctx context.Context, source, projectID, fallback string) string {
	ctx, cancel := context.WithTimeout(ctx, titleLookupTimeout)
	defer cancel()
	if p, err := h.sourceFor(source).GetProject(ctx, projectID); err == nil && p != nil {
		if t := strings.TrimSpace(p.Title); t != "" {
			return t
		}
	}
	return fallback
}

// titleLookups is how resolveTitles reaches the mod sites.
type titleLookups struct {
	// batch resolves many Modrinth projects at once: project id -> title.
	batch func(ctx context.Context, ids []string) (map[string]string, error)
	// one resolves a single project on another source.
	one func(ctx context.Context, source, id string) (string, error)
	// enabled reports whether a source can be asked at all.
	enabled func(source string) bool
}

// resolveTitles looks up the titles of the mods not yet checked and returns
// what it learned: renames (mod id -> title, only where the name differs) and
// the ids it got an answer for, which needn't be asked about again.
func resolveTitles(ctx context.Context, mods []*store.InstalledMod, checked func(id string) bool, look titleLookups) (renames map[string]string, answered []string) {
	renames = map[string]string{}
	settle := func(m *store.InstalledMod, title string) {
		answered = append(answered, m.ID)
		if t := strings.TrimSpace(title); t != "" && t != m.Name {
			renames[m.ID] = t
		}
	}

	var modrinthMods []*store.InstalledMod
	var others []*store.InstalledMod
	for _, m := range mods {
		if m.SourceID == nil || *m.SourceID == "" || checked(m.ID) || !look.enabled(m.Source) {
			continue
		}
		switch m.Source {
		case "modrinth":
			modrinthMods = append(modrinthMods, m)
		case "curseforge", "hangar", "spigotmc":
			others = append(others, m)
		}
	}

	if len(modrinthMods) > 0 {
		ids := make([]string, 0, len(modrinthMods))
		seen := map[string]bool{}
		for _, m := range modrinthMods {
			if !seen[*m.SourceID] {
				seen[*m.SourceID] = true
				ids = append(ids, *m.SourceID)
			}
		}
		if titles, err := look.batch(ctx, ids); err == nil {
			for _, m := range modrinthMods {
				// A project the batch didn't return (deleted upstream) counts as
				// answered: asking again won't change that.
				settle(m, titles[*m.SourceID])
			}
		}
	}

	for i, m := range others {
		if i >= maxTitleLookupsPerPass || ctx.Err() != nil {
			break
		}
		if title, err := look.one(ctx, m.Source, *m.SourceID); err == nil {
			settle(m, title)
		}
	}
	return renames, answered
}

// modTitles remembers which mods have had their title checked since the API
// started, so the background rescan asks each mod site about a mod once.
type modTitles struct {
	checked sync.Map // mod id -> struct{}
}

// refreshTitles renames mods whose stored name isn't their project's title.
func (h *ModHandlers) refreshTitles(ctx context.Context, mods []*store.InstalledMod) {
	renames, answered := resolveTitles(ctx, mods,
		func(id string) bool { _, ok := h.titles.checked.Load(id); return ok },
		titleLookups{
			batch: func(ctx context.Context, ids []string) (map[string]string, error) {
				projects, err := h.modrinth.GetProjects(ctx, ids)
				if err != nil {
					return nil, err
				}
				out := make(map[string]string, len(projects))
				for _, p := range projects {
					out[p.ID] = p.Title
				}
				return out, nil
			},
			one: func(ctx context.Context, source, id string) (string, error) {
				ctx, cancel := context.WithTimeout(ctx, titleLookupTimeout)
				defer cancel()
				p, err := h.sourceFor(source).GetProject(ctx, id)
				if err != nil || p == nil {
					return "", err
				}
				return p.Title, nil
			},
			enabled: func(source string) bool { return source != "curseforge" || h.curseforge.Enabled() },
		})
	for _, id := range answered {
		if title, rename := renames[id]; rename {
			if err := h.store.SetModName(ctx, id, title); err != nil {
				continue // try again next pass
			}
		}
		h.titles.checked.Store(id, struct{}{})
	}
}
