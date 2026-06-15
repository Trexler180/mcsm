package process

import (
	"fmt"
	"regexp"
	"time"
)

// mixinCrashDetector recognizes the other common way a modded server dies on
// startup: a mod throws during the loader's pre-launch / entrypoint phase —
// most often a broken Mixin (missing or invalid mixin class) — and Fabric
// aborts with "A mod crashed on startup!".
//
// The tricky part is attribution. Fabric reports the crash as "provided by
// '<mod>'" naming whichever entrypoint owner happened to trigger the lazy class
// transform (frequently an unrelated optimization mod like c2me), NOT the buggy
// mod. The reliable culprit is named by Mixin itself:
//
//	[ERROR]: Mixin prepare for mod pingviewer failed preparing <class> in <cfg>: ...
//	... Mixin [<cfg>:<class> from mod pingviewer] ... FAILED during PREPARE
//
// So we attribute from those mixin lines and deliberately ignore "provided by".
// The fix is identical to an incompatible-mods conflict — disable the named
// mod — so we emit the shared ModConflict and reuse the same UI + disable flow.
type mixinCrashDetector struct {
	armed  bool
	done   bool
	recent []string // rolling window kept until a crash, for raw-log context
	raw    []string
	pre    []string // culprit mod ids seen before the crash headline
	post   []string // culprit mod ids seen after it (cause-chain fallback)
}

const (
	crashRawCap = 200
	// The originating mixin error precedes "A mod crashed on startup!" by its
	// own stack trace, so keep enough recent lines to fold it into the raw log.
	crashRecentCap = 60
)

var (
	crashHeadlineRe = regexp.MustCompile(`A mod crashed on startup!`)
	// "Mixin prepare for mod pingviewer failed" / "Mixin apply for mod ... failed"
	mixinForModRe = regexp.MustCompile(`Mixin (?:prepare|apply) for mod (\S+) failed`)
	// "...ExampleMixin from mod pingviewer]" inside a MixinApplyError bracket.
	mixinFromModRe = regexp.MustCompile(`from mod (\S+)\]`)
)

// mixinModID returns the culprit mod id named on a mixin failure line, or "".
func mixinModID(line string) string {
	if m := mixinForModRe.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	if m := mixinFromModRe.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}

// feed consumes one console line. It returns a non-nil conflict exactly once,
// when a startup crash has been seen AND a culprit mod attributed.
func (d *mixinCrashDetector) feed(line string) *ModConflict {
	if d.done {
		return nil
	}

	id := mixinModID(line)

	if !d.armed {
		// Mixin failures log during class transform, which happens before the
		// loader prints the headline — so the culprit is usually already known
		// by the time we arm. Keep a rolling window so the raw log can show it.
		d.recent = append(d.recent, line)
		if len(d.recent) > crashRecentCap {
			d.recent = d.recent[len(d.recent)-crashRecentCap:]
		}
		if id != "" {
			d.pre = appendUniqueStr(d.pre, id)
		}
		if crashHeadlineRe.MatchString(line) {
			d.armed = true
			d.raw = d.recent
			d.recent = nil
			if len(d.pre) > 0 {
				d.done = true
				return d.build(d.pre)
			}
		}
		return nil
	}

	// Armed but the crash wasn't attributed yet (mixin named the mod only in the
	// cause chain that follows the headline) — scan on for a "from mod X]" line.
	if len(d.raw) < crashRawCap {
		d.raw = append(d.raw, line)
	}
	if id != "" {
		d.post = appendUniqueStr(d.post, id)
		d.done = true
		return d.build(d.post)
	}
	return nil
}

func (d *mixinCrashDetector) build(ids []string) *ModConflict {
	sugg := make([]ConflictSuggestion, 0, len(ids))
	for _, id := range ids {
		sugg = append(sugg, ConflictSuggestion{Action: "remove", ModID: id, ModName: id})
	}

	summary := "A mod crashed the server on startup"
	switch len(ids) {
	case 1:
		summary = fmt.Sprintf("Mod '%s' crashed the server on startup", ids[0])
	default:
		if len(ids) > 1 {
			summary = fmt.Sprintf("%d mods crashed the server on startup", len(ids))
		}
	}

	return &ModConflict{
		Detected:    true,
		Kind:        "crash",
		Summary:     summary,
		Suggestions: sugg,
		Raw:         d.raw,
		DetectedAt:  time.Now().UnixMilli(),
	}
}

func appendUniqueStr(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}
