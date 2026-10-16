package autoupdate

import (
	"context"

	"github.com/mcsm/api/internal/mods/hangar"
	"github.com/mcsm/api/internal/mods/modrinth"
	"github.com/mcsm/api/internal/mods/spigotmc"
)

// Updatable reports whether mods from source take part in update checks and
// update runs. CurseForge stays out: its key-less version listing is paged and
// filtered enough that "the newest compatible version" can't be trusted.
func Updatable(source string) bool {
	switch source {
	case "modrinth", "hangar", "spigotmc":
		return true
	}
	return false
}

// sites sends each lookup to the site the mod came from. Every site's
// versions arrive in Modrinth's shape.
type sites struct {
	modrinth *modrinth.Client
	hangar   *hangar.Client
	spigotmc *spigotmc.Client
}

func newSites() sites {
	return sites{modrinth: modrinth.New(), hangar: hangar.New(), spigotmc: spigotmc.New()}
}

func (s sites) GetVersions(ctx context.Context, source, projectID, loader, mcVersion string) ([]modrinth.Version, error) {
	switch source {
	case "hangar":
		return s.hangar.GetVersions(ctx, projectID, loader, mcVersion)
	case "spigotmc":
		return s.spigotmc.GetVersions(ctx, projectID, loader, mcVersion)
	default:
		return s.modrinth.GetVersions(ctx, projectID, loader, mcVersion)
	}
}

// GetVersion takes the project too: Modrinth version ids are unique on their
// own, but the other sites number versions within a project.
func (s sites) GetVersion(ctx context.Context, source, projectID, versionID string) (*modrinth.Version, error) {
	switch source {
	case "hangar":
		return s.hangar.GetVersion(ctx, projectID, versionID)
	case "spigotmc":
		return s.spigotmc.GetVersion(ctx, projectID, versionID)
	default:
		return s.modrinth.GetVersion(ctx, versionID)
	}
}

func (s sites) Download(ctx context.Context, fileURL, wantSHA string) (string, error) {
	return s.modrinth.Download(ctx, fileURL, wantSHA)
}
