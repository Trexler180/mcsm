package mcpserver

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// ── Output discipline ────────────────────────────────────────────
//
// Two independent jobs, applied by shared helpers so no tool can forget one:
//
//  1. Redaction. Server logs routinely contain credentials — a plugin printing
//     its API key on startup, a stack trace carrying a JDBC URL, an operator
//     pasting a token into chat. None of that should leave the process into a
//     model's context and, from there, into a transcript.
//
//  2. Bounding. A hostile or merely broken mod can write megabytes of log. If
//     tool output were unbounded, one such server could flood the model's
//     context and crowd out everything else — including the instructions that
//     tell it not to trust this text.

const (
	// maxEvidenceChars bounds a single untrusted string. Long enough for a real
	// exception line, short enough that fifty of them are still a readable
	// answer rather than a wall.
	maxEvidenceChars = 400

	// maxLogEvents / maxAuditEntries / maxMetricPoints bound how many items a
	// single call can return, whatever the caller asks for.
	maxLogEvents    = 40
	maxAuditEntries = 25
	maxMetricPoints = 200

	// maxDiagnosticErrors is the slice of recent errors folded into a
	// diagnostics response, which is a summary rather than a log reader.
	maxDiagnosticErrors = 10

	// maxNameChars bounds operator-authored display text (server names, mod
	// names) that is trusted more than log output but still not trusted enough
	// to be unbounded.
	maxNameChars = 120
)

// secretPatterns match text that should never reach a model. They are
// deliberately broad: a false positive costs a redacted word in a log line,
// while a false negative puts a live credential into a transcript.
var secretPatterns = []*regexp.Regexp{
	// This panel's own credential families, including the ones this very
	// feature issues.
	regexp.MustCompile(`mcsm_(?:pat|mcpc|mcpa|mcpr|mcpk)_[A-Za-z0-9_\-]+`),
	// JWTs (three base64url segments).
	regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{6,}\.[A-Za-z0-9_\-]{6,}\.[A-Za-z0-9_\-]+`),
	// key = value / key: value, for anything that names itself a secret.
	regexp.MustCompile(`(?i)\b(pass(?:word|wd)?|secret|token|api[_\-]?key|access[_\-]?key|private[_\-]?key|authorization|bearer)\b\s*[:=]\s*\S+`),
	// Credentials embedded in a URL (postgres://user:pw@host, https://u:p@h).
	regexp.MustCompile(`\b[a-zA-Z][a-zA-Z0-9+.\-]*://[^\s/:@]+:[^\s/@]+@`),
	// Common third-party token shapes that show up in plugin logs.
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9\-]{10,}`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{20,}`),
}

const redactedMarker = "[redacted]"

// ansiSequences matches a terminal escape sequence whole: OSC, the ESC-form
// CSI, and the single-character C1 CSI at U+009B.
//
// Removing only the introducer is not enough. Its parameters are printable, so
// "\x1b[31m" would leave "[31m" sitting in the text — noise in the model's
// context and, worse, a way to keep a secret's keyword split so none of the
// patterns above ever see it.
//
// U+009B is written as a rune escape rather than a raw 0x9b byte, because a
// lone 0x9b is not valid UTF-8 and regexp refuses to compile a pattern
// containing one. Its branch also insists on at least one parameter byte: a
// bare CSI in front of ordinary prose would otherwise swallow the letter after
// it, and stripInvisible removes the character itself either way.
var ansiSequences = regexp.MustCompile(
	"\x1b\\][^\a\x1b]*(?:\a|\x1b\\\\)?" + // OSC ... BEL / ST
		"|\x1b\\[?[0-9;:?]*[ -/]*[@-~]" + // ESC [ ... final
		"|\u009b[0-9;:?]+[ -/]*[@-~]", // C1 CSI ... final
)

// redact removes secret-shaped substrings.
func redact(s string) string {
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, redactedMarker)
	}
	return s
}

// clean normalizes a string that is about to become untrusted evidence:
// stripped of anything invisible, redacted, and truncated to a fixed budget.
//
// The order is the load-bearing part. Stripping runs *first* because a control
// or zero-width character wedged into the middle of a secret hides it from
// every pattern above — and removing that character afterwards would hand the
// caller back a reassembled credential, which is worse than never having
// looked. Truncation runs last so a secret cannot survive by sitting past the
// cut.
func clean(s string, max int) string {
	s = stripInvisible(s)
	s = redact(s)
	return truncate(s, max)
}

// CleanUntrusted is clean at the standard evidence budget, for callers outside
// this package.
//
// It is exported because the dashboard's approval screens render the same
// model-authored text this facade does. A string that is redacted on one path
// and raw on the other is a boundary with a hole in it, and the hole is on
// whichever path someone forgets.
func CleanUntrusted(s string) string { return clean(s, maxEvidenceChars) }

// stripInvisible removes every character that renders as nothing, or as
// something other than itself, in whatever displays this text.
//
// ASCII control characters are the obvious half: carriage returns, escape
// sequences, and NULs are how a hostile writer draws a fake prompt, a fake tool
// result, or a fake system message. The rest matters just as much here and is
// far easier to miss. A language model reads Unicode tag characters, bidi
// overrides, and zero-width spaces perfectly, while an operator reading the
// same log line sees nothing at all — so each of them carries an instruction
// that cannot be reviewed. This facade hands the model text written by mods,
// plugins, and players, which is exactly the population that would use one.
func stripInvisible(s string) string {
	s = ansiSequences.ReplaceAllString(s, "")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t' || r == '\u2028' || r == '\u2029':
			// Line structure collapses to a plain space rather than being kept:
			// a preserved break is a way to forge a new line in a transcript.
			b.WriteRune(' ')
		case r < 0x20 || r == 0x7f:
			// C0 controls and DEL.
		case r >= 0x80 && r <= 0x9f:
			// C1 controls. U+009B is a bare CSI some terminals still act on.
		case r == 0x00ad:
			// Soft hyphen: invisible, and splits a word for the patterns above.
		case r >= 0x200b && r <= 0x200f:
			// Zero-width space/joiners and the LRM/RLM marks.
		case r >= 0x202a && r <= 0x202e:
			// Bidi embedding and override — U+202E reverses what a human reads.
		case r >= 0x2060 && r <= 0x2064:
			// Word joiner and the invisible math operators.
		case r >= 0x2066 && r <= 0x2069:
			// Bidi isolates.
		case r == 0xfeff:
			// Zero-width no-break space / BOM.
		case r >= 0xfff9 && r <= 0xfffb:
			// Interlinear annotation, which hides text inside other text.
		case r >= 0xe0000 && r <= 0xe007f:
			// Unicode tag characters: a complete invisible ASCII alphabet, and
			// the carrier of choice for injection a human cannot see.
		case r == utf8.RuneError:
			// Malformed input decodes to this; it is not text worth passing on.
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// truncate cuts to a rune budget and says so, so a model never silently
// believes it saw a whole message.
func truncate(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "… [truncated]"
}

// cleanName bounds operator-authored display text. It is redacted too: a server
// or folder named after a connection string is unlikely but free to defend
// against.
func cleanName(s string) string { return clean(s, maxNameChars) }

// bound clamps a caller-supplied count into [1, max], with fallback when the
// caller omitted it. Tools never trust a requested size.
func bound(requested, fallback, max int) int {
	if requested <= 0 {
		requested = fallback
	}
	if requested > max {
		return max
	}
	return requested
}
