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
}

// ModConflict captures a startup failure parsed from the server log, so the
// panel can show suggestions and offer one-click fixes. Kind distinguishes a
// Fabric incompatible-mods block ("incompatible") from a mod that crashed the
// server on startup, e.g. a broken mixin ("crash"); both fixes are "disable the
// named mod(s)", so they share the same downstream UI and disable flow.
type ModConflict struct {
	Detected    bool                 `json:"detected"`
	Kind        string               `json:"kind"` // "incompatible" | "crash" | "java_version"
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
)

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

	// Sub-bullets under a suggestion (the "compatible with:" requirements) —
	// only while still inside the solution section, not the "More details" dump.
	if !d.inDetails && len(d.sugg) > 0 && strings.HasPrefix(trim, "-") {
		last := &d.sugg[len(d.sugg)-1]
		last.Requirements = append(last.Requirements, strings.TrimSpace(strings.TrimLeft(trim, "- ")))
	}
	return nil
}

func (d *conflictDetector) build() *ModConflict {
	summary := "Incompatible mods detected"
	if n := len(d.sugg); n > 0 {
		summary = fmt.Sprintf("%d mod conflict(s) detected", n)
	}
	return &ModConflict{
		Detected:    true,
		Kind:        "incompatible",
		Summary:     summary,
		Suggestions: d.sugg,
		Raw:         d.raw,
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
