package handlers

import (
	"context"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/autoupdate"
	"github.com/mcsm/api/internal/mc"
	"github.com/mcsm/api/internal/mods/curseforge"
	"github.com/mcsm/api/internal/mods/hangar"
	"github.com/mcsm/api/internal/mods/modrinth"
	"github.com/mcsm/api/internal/mods/spigotmc"
	"github.com/mcsm/api/internal/notify"
	"github.com/mcsm/api/internal/store"
)

type ModHandlers struct {
	store      *store.Store
	modrinth   *modrinth.Client
	curseforge *curseforge.Client
	hangar     *hangar.Client
	spigotmc   *spigotmc.Client
	mc         *mc.Client
	updater    *autoupdate.Engine
	notifier   *notify.Engine
}

func NewModHandlers(s *store.Store, updater *autoupdate.Engine, notifier *notify.Engine) *ModHandlers {
	return &ModHandlers{
		store:    s,
		modrinth: modrinth.New(),
		// Resolve the CurseForge key lazily from the encrypted secret store,
		// falling back to the legacy env var, so a key pasted in Settings →
		// Integrations takes effect without a restart.
		curseforge: curseforge.New(func() string {
			if v, _ := s.GetSecret(context.Background(), "curseforge_api_key"); v != "" {
				return v
			}
			return os.Getenv("CURSEFORGE_API_KEY")
		}),
		hangar:   hangar.New(),
		spigotmc: spigotmc.New(),
		mc:       mc.New(),
		updater:  updater,
		notifier: notifier,
	}
}

// sourceClient is the slice of the per-source clients the generic handlers
// dispatch over; every source normalizes into the modrinth wire shapes.
type sourceClient interface {
	Search(ctx context.Context, p modrinth.SearchParams) (*modrinth.SearchResult, error)
	GetProject(ctx context.Context, projectID string) (*modrinth.Project, error)
	GetVersions(ctx context.Context, projectID, loader, mcVersion string) ([]modrinth.Version, error)
}

func (h *ModHandlers) sourceFor(source string) sourceClient {
	switch source {
	case "curseforge":
		return h.curseforge
	case "hangar":
		return h.hangar
	case "spigotmc":
		return h.spigotmc
	default:
		return h.modrinth
	}
}

// versionFor fetches one version; it exists because Modrinth version ids are
// globally unique while the other sources scope them under a project.
func (h *ModHandlers) versionFor(ctx context.Context, source, projectID, versionID string) (*modrinth.Version, error) {
	switch source {
	case "curseforge":
		return h.curseforge.GetVersion(ctx, projectID, versionID)
	case "hangar":
		return h.hangar.GetVersion(ctx, projectID, versionID)
	case "spigotmc":
		return h.spigotmc.GetVersion(ctx, projectID, versionID)
	default:
		return h.modrinth.GetVersion(ctx, versionID)
	}
}

func (h *ModHandlers) List(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	// Reconcile the panel's view with what's actually on disk before listing, so
	// jars added or removed outside the panel (manual copy, SFTP, another tool)
	// are reflected. Best-effort: a missing dir or unreachable agent just leaves
	// the DB rows as-is.
	h.reconcileFromDisk(r.Context(), id)
	mods, err := h.store.ListMods(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if mods == nil {
		mods = []*store.InstalledMod{}
	}
	if err := h.annotateDependencies(r.Context(), id, mods); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, mods)
}

// annotateDependencies fills RequiredBy/Orphaned on each mod from the reverse
// dependency graph. A mod is orphaned when it was auto-installed as a dependency
// but no currently-installed mod still requires it.
func (h *ModHandlers) annotateDependencies(ctx context.Context, serverID string, mods []*store.InstalledMod) error {
	edges, err := h.store.ListModDependencies(ctx, serverID)
	if err != nil {
		return err
	}

	// project id -> display name, for resolving dependent names. Only count
	// dependents that are actually still installed.
	nameByPID := map[string]string{}
	for _, m := range mods {
		if m.SourceID != nil {
			nameByPID[*m.SourceID] = m.Name
		}
	}
	// dependency project id -> set of dependent project ids that still exist.
	dependents := map[string]map[string]bool{}
	for _, e := range edges {
		if _, ok := nameByPID[e.DependentProjectID]; !ok {
			continue // dependent no longer installed
		}
		if dependents[e.DependencyProjectID] == nil {
			dependents[e.DependencyProjectID] = map[string]bool{}
		}
		dependents[e.DependencyProjectID][e.DependentProjectID] = true
	}

	for _, m := range mods {
		m.RequiredBy = []string{}
		if m.SourceID == nil {
			continue
		}
		for pid := range dependents[*m.SourceID] {
			m.RequiredBy = append(m.RequiredBy, nameByPID[pid])
		}
		m.Orphaned = m.InstalledAsDep && len(m.RequiredBy) == 0
	}
	return nil
}

// reconcileFromDisk syncs installed_mods with the jar files actually present in
// the server's mod/plugin directories on the agent, so the Mods tab stays
// truthful regardless of how files got there:
//   - jars the panel doesn't track yet are adopted;
//   - tracked rows whose jar was deleted outside the panel are pruned;
//   - adopted jars and not-yet-identified "custom" jars are matched by sha512
//     against Modrinth's file index, so an imported jar that is an exact Modrinth
//     upload becomes a managed "modrinth" mod (version data + update checks)
//     rather than an opaque custom entry — the same trick the Modrinth app uses.
//
// It is best-effort and side-effect-only: any failure (agent down, directory
// missing, Modrinth unreachable) is swallowed, leaving rows as-is so the next
// load retries. Hashing is bounded to once per jar — a stored sha512 means
// "already looked up", so steady-state loads do no hashing or upstream calls.
