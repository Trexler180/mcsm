package handlers

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/mods/curseforge"
	"github.com/mcsm/api/internal/mods/modrinth"
	"github.com/mcsm/api/internal/store"
)

func (h *ModHandlers) reconcileFromDisk(ctx context.Context, serverID string) {
	srv, err := h.store.GetServer(ctx, serverID)
	if err != nil {
		return
	}
	node, err := h.store.GetNode(ctx, srv.NodeID)
	if err != nil {
		return
	}
	c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)

	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// The agent forgets registered directories on restart; (re)register so the
	// file listing resolves against the right base path.
	if err := c.RegisterDir(cctx, serverID, srv.DirectoryPath); err != nil {
		return
	}

	mods, err := h.store.ListMods(ctx, serverID)
	if err != nil {
		return
	}

	// Phase 1: per dir, prune deletions and collect untracked jars to adopt.
	type adoptItem struct {
		dir, name, path string
		enabled         bool
	}
	var toAdopt []adoptItem

	for _, dir := range modDirsForPlatform(srv.Platform) {
		listing, err := c.ListFiles(cctx, serverID, dir)
		if err != nil {
			// Directory absent or a transient agent error: skip this dir entirely
			// rather than risk pruning rows on incomplete information.
			continue
		}

		onDisk := map[string]bool{} // lowercase jar filename -> present
		for _, e := range listing.Entries {
			if e.Type == "file" && isJarName(e.Name) {
				onDisk[strings.ToLower(e.Name)] = true
			}
		}

		tracked := map[string]*store.InstalledMod{} // lowercase filename -> row
		for _, m := range mods {
			if samePath(m.InstallPath, dir) {
				tracked[strings.ToLower(m.FileName)] = m
			}
		}

		for _, e := range listing.Entries {
			if e.Type != "file" || !isJarName(e.Name) {
				continue
			}
			if _, ok := tracked[strings.ToLower(e.Name)]; ok {
				continue
			}
			toAdopt = append(toAdopt, adoptItem{
				dir:     dir,
				name:    e.Name,
				path:    dir + "/" + e.Name,
				enabled: !strings.HasSuffix(e.Name, disabledSuffix),
			})
		}

		// Prune tracked rows whose jar no longer exists on disk.
		for lower, m := range tracked {
			if onDisk[lower] {
				continue
			}
			if _, err := h.store.DeleteMod(ctx, m.ID); err != nil {
				continue
			}
			if m.SourceID != nil {
				_ = h.store.DeleteModDependencyEdges(ctx, serverID, *m.SourceID)
			}
		}
	}

	// Existing "custom" rows we've never hashed: retro-identify them so mods
	// imported before this feature (or while Modrinth was down) get recognized.
	var toIdentify []*store.InstalledMod
	identifyPath := map[string]string{} // mod id -> on-disk path
	for _, m := range mods {
		if m.Source == "custom" && (m.SHA512 == nil || *m.SHA512 == "") {
			toIdentify = append(toIdentify, m)
			identifyPath[m.ID] = strings.TrimRight(m.InstallPath, "/") + "/" + m.FileName
		}
	}

	if len(toAdopt) == 0 && len(toIdentify) == 0 {
		return
	}

	// Phase 2: one agent round-trip to fingerprint everything that needs identifying
	// (sha512 for Modrinth, murmur2 for CurseForge).
	paths := make([]string, 0, len(toAdopt)+len(toIdentify))
	for _, a := range toAdopt {
		paths = append(paths, a.path)
	}
	for _, p := range identifyPath {
		paths = append(paths, p)
	}
	fps, err := c.HashFiles(cctx, serverID, paths)
	if err != nil {
		fps = map[string]agent.FileFingerprint{} // adopt as custom; identification retries later
	}

	// Phase 3: match against Modrinth (by sha512) first, then CurseForge (by
	// murmur2) for whatever Modrinth didn't claim. Each is one batch call; the
	// *OK flags report whether that lookup actually completed, so a provider
	// outage defers stamping rather than branding a jar permanently unrecognized.
	mr, mrOK := h.matchModrinth(cctx, fps)
	mrTitles := h.modrinthTitles(cctx, mr)

	var cfFingerprints []uint32
	for _, fp := range fps {
		if fp.SHA512 != "" {
			if _, ok := mr[fp.SHA512]; ok {
				continue // already recognized by Modrinth — don't ask CurseForge
			}
		}
		if fp.Murmur2 != 0 {
			cfFingerprints = append(cfFingerprints, fp.Murmur2)
		}
	}
	cfEnabled := h.curseforge.Enabled()
	cf, cfOK := h.matchCurseforge(cctx, cfFingerprints)
	cfNames := h.curseforgeNames(cctx, cf)

	// A definitive pass = every enabled provider answered. Only then do we stamp
	// an unrecognized jar, so we don't keep rehashing it on every load.
	lookupOK := mrOK && (!cfEnabled || cfOK)

	// Phase 4a: adopt new jars, recognized as managed mods when a provider matched.
	for _, a := range toAdopt {
		fp := fps[a.path]
		var created *store.InstalledMod
		if rec, ok := h.recognize(fp, mr, mrTitles, cf, cfNames, jarDisplayName(a.name)); ok {
			pid, vid := rec.sourceID, rec.versionID
			created, err = h.store.CreateMod(ctx, &store.InstalledMod{
				ServerID:    serverID,
				Source:      rec.source,
				SourceID:    &pid,
				VersionID:   &vid,
				Name:        rec.name,
				Version:     rec.version,
				FileName:    a.name,
				SHA512:      strPtrOrNil(fp.SHA512),
				InstallPath: a.dir,
			})
		} else {
			m := &store.InstalledMod{
				ServerID:    serverID,
				Source:      "custom",
				Name:        jarDisplayName(a.name),
				Version:     "unknown",
				FileName:    a.name,
				InstallPath: a.dir,
			}
			if fp.SHA512 != "" && lookupOK {
				sha := fp.SHA512
				m.SHA512 = &sha
			}
			created, err = h.store.CreateMod(ctx, m)
		}
		if err != nil {
			continue
		}
		// CreateMod can't set enabled (defaults to true); fix up jars that are
		// already disabled on disk (".disabled" suffix).
		if !a.enabled {
			_ = h.store.SetModEnabled(ctx, created.ID, false, a.name)
		}
	}

	// Phase 4b: retro-identify existing custom rows (only on a definitive pass).
	if lookupOK {
		for _, m := range toIdentify {
			fp := fps[identifyPath[m.ID]]
			if fp.SHA512 == "" {
				continue // couldn't hash this one; leave it for a later pass
			}
			if rec, ok := h.recognize(fp, mr, mrTitles, cf, cfNames, m.Name); ok {
				_ = h.store.RecognizeMod(ctx, m.ID, rec.source, rec.sourceID, rec.versionID, rec.name, rec.version, fp.SHA512)
			} else {
				_ = h.store.StampModHash(ctx, m.ID, fp.SHA512) // checked, no match — don't rehash
			}
		}
	}
}

// recognition is a resolved upstream identity for a jar.
type recognition struct {
	source, sourceID, versionID, name, version string
}

// recognize resolves a jar's fingerprint to an upstream identity, preferring a
// Modrinth sha512 match and falling back to a CurseForge murmur2 match. fallback
// is the display name to use when neither source supplies a good title.
func (h *ModHandlers) recognize(fp agent.FileFingerprint, mr map[string]modrinth.Version, mrTitles map[string]string, cf map[uint32]curseforge.FingerprintMatch, cfNames map[int]string, fallback string) (recognition, bool) {
	if fp.SHA512 != "" {
		if v, ok := mr[fp.SHA512]; ok {
			return recognition{
				source:    "modrinth",
				sourceID:  v.ProjectID,
				versionID: v.ID,
				name:      recognizedName(mrTitles[v.ProjectID], v.Name, fallback),
				version:   v.VersionNumber,
			}, true
		}
	}
	if fp.Murmur2 != 0 {
		if m, ok := cf[fp.Murmur2]; ok {
			return recognition{
				source:    "curseforge",
				sourceID:  fmt.Sprint(m.ModID),
				versionID: fmt.Sprint(m.FileID),
				name:      recognizedName(cfNames[m.ModID], m.DisplayName, fallback),
				version:   m.FileName,
			}, true
		}
	}
	return recognition{}, false
}

// matchModrinth looks the distinct, non-empty sha512s up against Modrinth's file
// index. The bool is whether the lookup completed (false on a network/API error)
// so callers can defer stamping/retrying when Modrinth was simply unreachable.
func (h *ModHandlers) matchModrinth(ctx context.Context, fps map[string]agent.FileFingerprint) (map[string]modrinth.Version, bool) {
	seen := map[string]bool{}
	var list []string
	for _, fp := range fps {
		if fp.SHA512 != "" && !seen[fp.SHA512] {
			seen[fp.SHA512] = true
			list = append(list, fp.SHA512)
		}
	}
	if len(list) == 0 {
		return map[string]modrinth.Version{}, true
	}
	res, err := h.modrinth.GetVersionsByHashes(ctx, list, "sha512")
	if err != nil {
		return map[string]modrinth.Version{}, false
	}
	return res, true
}

// matchCurseforge looks the distinct, non-zero murmur2 fingerprints up against
// CurseForge. When CurseForge isn't enabled (no key/proxy) it reports success
// with no matches, so recognition simply skips it without blocking stamping.
func (h *ModHandlers) matchCurseforge(ctx context.Context, fingerprints []uint32) (map[uint32]curseforge.FingerprintMatch, bool) {
	if !h.curseforge.Enabled() {
		return map[uint32]curseforge.FingerprintMatch{}, true
	}
	seen := map[uint32]bool{}
	var list []uint32
	for _, fp := range fingerprints {
		if fp != 0 && !seen[fp] {
			seen[fp] = true
			list = append(list, fp)
		}
	}
	if len(list) == 0 {
		return map[uint32]curseforge.FingerprintMatch{}, true
	}
	res, err := h.curseforge.MatchFingerprints(ctx, list)
	if err != nil {
		return map[uint32]curseforge.FingerprintMatch{}, false
	}
	return res, true
}

// curseforgeNames resolves the matched CurseForge mod ids to display titles in
// one batch. Best-effort: an error yields no names and callers fall back.
func (h *ModHandlers) curseforgeNames(ctx context.Context, cf map[uint32]curseforge.FingerprintMatch) map[int]string {
	seen := map[int]bool{}
	var ids []int
	for _, m := range cf {
		if m.ModID != 0 && !seen[m.ModID] {
			seen[m.ModID] = true
			ids = append(ids, m.ModID)
		}
	}
	if len(ids) == 0 {
		return map[int]string{}
	}
	names, err := h.curseforge.GetModNames(ctx, ids)
	if err != nil {
		return map[int]string{}
	}
	return names
}

// strPtrOrNil returns nil for an empty string, else a pointer to it.
func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// modrinthTitles resolves project id -> display title for the matched versions in
// one batch call. Best-effort: an error or missing id just yields no title, and
// the caller falls back to the version name.
func (h *ModHandlers) modrinthTitles(ctx context.Context, matched map[string]modrinth.Version) map[string]string {
	seen := map[string]bool{}
	var ids []string
	for _, v := range matched {
		if v.ProjectID != "" && !seen[v.ProjectID] {
			seen[v.ProjectID] = true
			ids = append(ids, v.ProjectID)
		}
	}
	out := map[string]string{}
	if len(ids) == 0 {
		return out
	}
	projects, err := h.modrinth.GetProjects(ctx, ids)
	if err != nil {
		return out
	}
	for _, p := range projects {
		out[p.ID] = p.Title
	}
	return out
}

// recognizedName picks the best available display name for a recognized mod,
// preferring the project title, then the version name, then a filename-derived
// fallback.
func recognizedName(title, versionName, fallback string) string {
	if title != "" {
		return title
	}
	if versionName != "" {
		return versionName
	}
	return fallback
}

// modDirsForPlatform lists the server-relative directories that hold loadable
// jars. Both are scanned regardless of platform — a directory that doesn't exist
// simply fails to list and is skipped — so a server with both stays in sync.
func modDirsForPlatform(platform string) []string {
	return []string{"/mods", "/plugins"}
}

// isJarName reports whether a filename is a (possibly disabled) jar.
func isJarName(name string) bool {
	l := strings.ToLower(name)
	return strings.HasSuffix(l, ".jar") || strings.HasSuffix(l, ".jar"+disabledSuffix)
}

// jarDisplayName derives a human label from a jar filename by stripping the
// ".disabled" marker and the ".jar" extension.
func jarDisplayName(name string) string {
	name = strings.TrimSuffix(name, disabledSuffix)
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// samePath compares two server-relative dir paths, ignoring a trailing slash and
// case (Windows agents list case-insensitive paths).
func samePath(a, b string) bool {
	norm := func(p string) string {
		p = strings.TrimSuffix(filepath.ToSlash(p), "/")
		if p == "" {
			p = "/"
		}
		return strings.ToLower(p)
	}
	return norm(a) == norm(b)
}
