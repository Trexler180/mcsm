// Package helpermod resolves which build of the helper mod a server should run.
//
// The mod is developed and released in its own repository, independently of the
// manager. This package is how the manager finds those releases: it fetches a
// published index, picks the build matching a server's Minecraft version,
// downloads and verifies it, and caches it on disk.
//
// The agent still embeds one build (see internal/helperjar), which serves as the
// floor: an air-gapped host, or one that cannot reach the index, keeps working
// with whatever shipped in the binary.
package helpermod

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// SchemaVersion is the index format this agent understands. An index declaring
// a newer schema is ignored rather than guessed at — the embedded fallback is a
// better outcome than misreading a format we do not know.
const SchemaVersion = 1

// Index is the document published by the mod repository.
type Index struct {
	SchemaVersion int     `json:"schema_version"`
	Builds        []Build `json:"builds"`
}

// Build describes one published jar.
type Build struct {
	// ModVersion is the mod's own version, e.g. "1.1.0".
	ModVersion string `json:"mod_version"`
	// Loader is the mod loader this jar targets, e.g. "fabric".
	Loader string `json:"loader"`
	// MinecraftVersions lists the exact game versions this jar supports.
	//
	// An explicit list rather than a range expression: version ranges need a
	// parser, and a parser is a place for a subtle bug that ends with a mod
	// installed on a server it breaks. The build that produced the jar knows
	// exactly what it was compiled and tested against, so it states it.
	MinecraftVersions []string `json:"minecraft_versions"`
	// ProtocolVersion is the link protocol this build speaks.
	//
	// This is what makes independent release cadences safe. Embedding the jar in
	// the agent made skew impossible; publishing separately brings it back, so
	// each build declares its protocol and the agent installs only what it can
	// actually talk to.
	ProtocolVersion int `json:"protocol_version"`
	// URL is an https location for the jar.
	URL string `json:"url"`
	// SHA256 is the lowercase hex digest of the jar. Downloads that do not match
	// are discarded.
	SHA256 string `json:"sha256"`
	// Size in bytes, used to reject implausible downloads early.
	Size int64 `json:"size"`
}

// ParseIndex decodes and validates an index document.
func ParseIndex(data []byte) (*Index, error) {
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("helpermod: parse index: %w", err)
	}
	if idx.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("helpermod: unsupported index schema %d (this agent understands %d)",
			idx.SchemaVersion, SchemaVersion)
	}
	return &idx, nil
}

// ErrNoBuild means the index has nothing usable for a server.
var ErrNoBuild = errors.New("helpermod: no compatible build")

// Select returns the best build for a Minecraft version.
//
// maxProtocol is the newest link protocol this agent speaks; builds requiring
// anything newer are skipped. Where several builds qualify, the one declaring
// the highest protocol wins, since that is the most capable build this agent can
// still understand.
func (idx *Index) Select(loader, mcVersion string, maxProtocol int) (*Build, error) {
	if idx == nil {
		return nil, ErrNoBuild
	}

	mcVersion = strings.TrimSpace(mcVersion)
	if mcVersion == "" {
		// An unrecorded version cannot be matched, and guessing costs a server
		// that will not start.
		return nil, ErrNoBuild
	}

	var best *Build
	for i := range idx.Builds {
		b := &idx.Builds[i]
		if !strings.EqualFold(b.Loader, loader) {
			continue
		}
		if b.ProtocolVersion > maxProtocol || b.ProtocolVersion < 1 {
			continue
		}
		if !b.supports(mcVersion) {
			continue
		}
		if !b.valid() {
			continue
		}
		if best == nil || b.ProtocolVersion > best.ProtocolVersion {
			best = b
		}
	}

	if best == nil {
		return nil, ErrNoBuild
	}
	return best, nil
}

func (b *Build) supports(mcVersion string) bool {
	for _, v := range b.MinecraftVersions {
		if strings.EqualFold(strings.TrimSpace(v), mcVersion) {
			return true
		}
	}
	return false
}

// valid rejects entries that could not be acted on safely.
func (b *Build) valid() bool {
	if !strings.HasPrefix(strings.ToLower(b.URL), "https://") {
		// Refuse plaintext downloads outright. The digest would catch tampering,
		// but there is no reason to fetch a credential-free artifact over a
		// channel anyone can rewrite.
		return false
	}
	if len(b.SHA256) != 64 {
		return false
	}
	if b.Size <= 0 || b.Size > maxJarBytes {
		return false
	}
	return true
}

// maxJarBytes bounds what the agent will download. The helper is tens of
// kilobytes; anything near this ceiling means the index is wrong or hostile.
const maxJarBytes = 16 << 20
