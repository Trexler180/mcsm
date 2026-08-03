package process

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ConflictSuggestion is one actionable line from a Fabric loader "potential
// solution" block, e.g. `Replace mod 'Async' (async) 0.2.2+alpha-26.1.2 ...`.
// ModID is the loader mod id (what we match jars against); ModName is the
// human-facing display name.
type ConflictSuggestion struct {
	Action       string   `json:"action"` // "replace" | "remove" | "install"
	ModID        string   `json:"mod_id"`
	ModName      string   `json:"mod_name"`
	Version      string   `json:"version,omitempty"`
	Requirements []string `json:"requirements,omitempty"`
	// RequiredBy names the installed mods that declared this dependency, taken
	// from the loader's "More details" listing. Only set for action "install",
	// where it answers the operator's first question: what needs this?
	RequiredBy []string `json:"required_by,omitempty"`
}

// ModConflict captures a startup failure parsed from the server log, so the
// panel can show suggestions and offer one-click fixes. Kind distinguishes a
// Fabric incompatible-mods block ("incompatible") from a mod that crashed the
// server on startup, e.g. a broken mixin ("crash"); both fixes are "disable the
// named mod(s)", so they share the same downstream UI and disable flow.
type ModConflict struct {
	Detected bool `json:"detected"`
	// "incompatible" | "crash" | "java_version" | "missing_dependency". The last
	// is the same loader block as "incompatible" but every suggested fix is an
	// install rather than a disable, so the panel offers to fetch the missing
	// mods instead of turning existing ones off.
	Kind        string               `json:"kind"`
	Summary     string               `json:"summary"`
	Suggestions []ConflictSuggestion `json:"suggestions"`
	// RequiredJava is the Java feature release the server needs, set only for
	// kind "java_version" so the panel can match installed runtimes / suggest an
	// install. 0 when unknown or not applicable.
	RequiredJava int      `json:"required_java,omitempty"`
	Raw          []string `json:"raw"`
	DetectedAt   int64    `json:"detected_at"`
}

const conflictRawCap = 200

var (
	// Trigger lines: either the loader's own "Incompatible mods found!" log or
	// the FormattedException message that precedes the solution block.
	conflictTriggerRe = regexp.MustCompile(`Incompatible mods found!|incompatible with the game or each other`)
	// `- Replace mod 'Async' (async) 0.2.2+alpha-26.1.2 with any version ...`
	conflictSuggestionRe = regexp.MustCompile(`(?i)-\s*(Replace|Remove|Install)\s+mod\s+'([^']+)'\s+\(([^)]+)\)(?:\s+(\S+))?`)

	// A missing dependency is the one solution the loader states by bare mod id,
	// because the mod isn't installed so it has no name or version to print:
	// Messages.properties has `resolution.solution.addMod=Install {0}, {1}.`
	// with {0}=mod id and {1}=version requirement, rendering as
	// `\t - Install fabric, any version.` That shape does not match
	// conflictSuggestionRe (no `mod '<name>' (<id>)`), which is why a server
	// missing Fabric API used to report a conflict with zero suggestions.
	installSolutionRe = regexp.MustCompile(`(?i)^-\s*Install\s+([A-Za-z0-9_.\-]+)\s*,\s*(.+?)\.?$`)

	// `resolution.depends.missing={0} {1} requires {3} of {2}, which is missing!`
	// → `- Mod 'AppleSkin' (appleskin) 2.5.1 requires any version of fabric,
	// which is missing!`. This names both the missing dependency and the mod
	// that wants it, and appears even when no solution block was printed.
	missingDepRe = regexp.MustCompile(`(?i)mod\s+'([^']+)'\s+\(([^)]+)\)\s+(\S+)\s+requires\s+(.+?)\s+of\s+([A-Za-z0-9_.\-]+),\s*which is missing!`)

	// `resolution.depends.suggestion=You need to install {3} of {2}.` — the last
	// fallback when neither of the above parsed.
	needInstallRe = regexp.MustCompile(`(?i)You need to install\s+(.+?)\s+of\s+([A-Za-z0-9_.\-]+)\.`)
)

// loaderModDisplayNames gives a human title to the handful of loader mod ids
// that are near-universal dependencies, so the summary reads "Fabric API" and
// not "fabric". Anything not listed falls back to the raw id, which the panel
// replaces with the real project title once it resolves the mod.
var loaderModDisplayNames = map[string]string{
	"fabric":     "Fabric API",
	"fabric-api": "Fabric API",
}

// loaderModDisplayName returns the friendly name for a loader mod id.
func loaderModDisplayName(id string) string {
	if n, ok := loaderModDisplayNames[strings.ToLower(id)]; ok {
		return n
	}
	return id
}

// uninstallableModIDs are dependency ids that name the platform rather than a
// mod anyone can install. When the loader reports one of these as missing the
// real problem is a wrong Minecraft/loader version, so offering to download it
// would send the operator down a dead end.
var uninstallableModIDs = map[string]bool{
	"minecraft":     true,
	"java":          true,
	"fabricloader":  true,
	"fabric-loader": true,
}

// conflictDetector is a tiny line-fed state machine. Feed it every console line;
// when it has captured a full incompatibility block it returns the parsed
// ModConflict once (done=true) and ignores further input.
type conflictDetector struct {
	active    bool
	inDetails bool
	done      bool
	raw       []string
	sugg      []ConflictSuggestion
}

// feed consumes one line. It returns a non-nil conflict exactly once, when the
// block is complete (the first stack-trace frame ends it).
func (d *conflictDetector) feed(line string) *ModConflict {
	if d.done {
		return nil
	}
	trim := strings.TrimSpace(line)

	if !d.active {
		if conflictTriggerRe.MatchString(line) {
			d.active = true
			d.raw = append(d.raw, line)
		}
		return nil
	}

	// The stack trace begins right after the human-readable block — finalize.
	if strings.HasPrefix(trim, "at ") {
		d.done = true
		return d.build()
	}

	if len(d.raw) < conflictRawCap {
		d.raw = append(d.raw, line)
	}

	if strings.Contains(line, "More details:") {
		d.inDetails = true
	}

	if m := conflictSuggestionRe.FindStringSubmatch(line); m != nil {
		d.sugg = append(d.sugg, ConflictSuggestion{
			Action:  strings.ToLower(m[1]),
			ModName: m[2],
			ModID:   m[3],
			Version: m[4],
		})
		return nil
	}

	// `- Install fabric, any version.` — a missing dependency, named by bare id.
	if m := installSolutionRe.FindStringSubmatch(trim); m != nil {
		d.addInstall(m[1], m[2], "")
		return nil
	}

	// `- Mod 'X' (x) 1.0 requires any version of fabric, which is missing!`
	// Checked regardless of section: it is the only line present when the loader
	// prints the unmet-dependency listing without a solution block.
	if m := missingDepRe.FindStringSubmatch(line); m != nil {
		d.addInstall(m[5], m[4], m[1])
		return nil
	}

	// `You need to install any version of fabric.`
	if m := needInstallRe.FindStringSubmatch(line); m != nil {
		d.addInstall(m[2], m[1], "")
		return nil
	}

	// Sub-bullets under a suggestion (the "compatible with:" requirements) —
	// only while still inside the solution section, not the "More details" dump.
	if !d.inDetails && len(d.sugg) > 0 && strings.HasPrefix(trim, "-") {
		last := &d.sugg[len(d.sugg)-1]
		last.Requirements = append(last.Requirements, strings.TrimSpace(strings.TrimLeft(trim, "- ")))
	}
	return nil
}

// addInstall records a missing dependency, merging repeats: the same dependency
// is usually named once in the solution block and again by every mod that wants
// it, and each mention contributes a different piece (version requirement,
// dependent name).
func (d *conflictDetector) addInstall(modID, versionReq, requiredBy string) {
	modID = strings.TrimSpace(modID)
	if modID == "" || uninstallableModIDs[strings.ToLower(modID)] {
		return
	}
	versionReq = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(versionReq), "."))

	for i := range d.sugg {
		s := &d.sugg[i]
		if s.Action != "install" || !strings.EqualFold(s.ModID, modID) {
			continue
		}
		if versionReq != "" {
			s.Requirements = appendUniqueStr(s.Requirements, versionReq)
		}
		if requiredBy != "" {
			s.RequiredBy = appendUniqueStr(s.RequiredBy, requiredBy)
		}
		return
	}

	s := ConflictSuggestion{
		Action:  "install",
		ModID:   modID,
		ModName: loaderModDisplayName(modID),
	}
	if versionReq != "" {
		s.Requirements = []string{versionReq}
	}
	if requiredBy != "" {
		s.RequiredBy = []string{requiredBy}
	}
	d.sugg = append(d.sugg, s)
}

func (d *conflictDetector) build() *ModConflict {
	// A block whose every fix is "install X" is a missing dependency, not a
	// clash between installed mods — different cause, different one-click fix.
	installs, others := 0, 0
	for _, s := range d.sugg {
		if s.Action == "install" {
			installs++
		} else {
			others++
		}
	}

	kind := "incompatible"
	summary := "Incompatible mods detected"
	switch {
	case installs > 0 && others == 0:
		kind = "missing_dependency"
		names := make([]string, 0, installs)
		for _, s := range d.sugg {
			if s.Action == "install" {
				names = append(names, s.ModName)
			}
		}
		if installs == 1 {
			summary = fmt.Sprintf("%s is required but not installed", names[0])
		} else {
			summary = fmt.Sprintf("%d required dependencies are missing: %s", installs, strings.Join(names, ", "))
		}
	case len(d.sugg) > 0:
		summary = fmt.Sprintf("%d mod conflict(s) detected", len(d.sugg))
	}
	// Zero parsed suggestions must marshal to [] not null — the web maps over
	// suggestions (same contract as the java-version detector).
	sugg := d.sugg
	if sugg == nil {
		sugg = []ConflictSuggestion{}
	}
	raw := d.raw
	if raw == nil {
		raw = []string{}
	}
	return &ModConflict{
		Detected:    true,
		Kind:        kind,
		Summary:     summary,
		Suggestions: sugg,
		Raw:         raw,
		DetectedAt:  time.Now().UnixMilli(),
	}
}

// fabricMeta is the slice of fabric.mod.json we care about for matching a jar to
// a loader mod id.
type fabricMeta struct {
	ID string `json:"id"`
}

// disableModsByID scans <dir>/mods for enabled .jar files, reads each jar's
// fabric.mod.json id, and renames any whose id is in ids to "<name>.disabled"
// (mod loaders skip that suffix). Returns the filenames that were disabled.
func disableModsByID(dir string, ids []string) ([]string, error) {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id != "" {
			want[id] = true
		}
	}
	if len(want) == 0 {
		return nil, fmt.Errorf("no mod ids supplied")
	}

	modsDir := filepath.Join(dir, "mods")
	entries, err := os.ReadDir(modsDir)
	if err != nil {
		return nil, fmt.Errorf("read mods dir: %w", err)
	}

	var disabled []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".jar") {
			continue
		}
		jarPath := filepath.Join(modsDir, e.Name())
		id, ok := jarModID(jarPath)
		if !ok || !want[id] {
			continue
		}
		if err := os.Rename(jarPath, jarPath+".disabled"); err != nil {
			return disabled, fmt.Errorf("disable %s: %w", e.Name(), err)
		}
		disabled = append(disabled, e.Name())
	}
	return disabled, nil
}

// jarModID opens a jar and returns the id from its fabric.mod.json, if present.
func jarModID(jarPath string) (string, bool) {
	zr, err := zip.OpenReader(jarPath)
	if err != nil {
		return "", false
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != "fabric.mod.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", false
		}
		var meta fabricMeta
		err = json.NewDecoder(rc).Decode(&meta)
		rc.Close()
		if err != nil || meta.ID == "" {
			return "", false
		}
		return meta.ID, true
	}
	return "", false
}
