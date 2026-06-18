package process

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// javaVersionDetector recognizes the most common reason a server refuses to
// boot after a Minecraft or Java upgrade: the server jar was compiled for a
// newer Java than the runtime launching it. The JVM aborts very early, before
// Minecraft's file logger exists, so this only reaches stdout/stderr — exactly
// the stream we scan:
//
//	Caused by: java.lang.UnsupportedClassVersionError: net/minecraft/bundler/Main
//	has been compiled by a more recent version of the Java Runtime (class file
//	version 69.0), this version of the Java Runtime only recognizes class file
//	versions up to 65.0
//
// The class-file major version maps to a Java feature release as
// java = major - 44 (52=Java 8 ... 61=Java 17, 65=Java 21, 69=Java 25), which
// lets us name both the version the jar needs and the version that ran it. The
// fix is "use a newer Java", so this is a distinct ModConflict kind from the
// mod-disable flows.
type javaVersionDetector struct {
	done bool
}

// Matches the UnsupportedClassVersionError JVM emits, capturing the offending
// class, the class-file version it was compiled to, and the highest version the
// running runtime supports.
var unsupportedClassVersionRe = regexp.MustCompile(
	`UnsupportedClassVersionError:\s*(\S+) has been compiled by a more recent version of the Java Runtime \(class file version (\d+)\.\d+\), this version of the Java Runtime only recognizes class file versions up to (\d+)\.\d+`)

// classMajorToJava converts a class-file major version to its Java feature
// release (java = major - 44). Returns 0 for inputs below Java 1.1's 45.
func classMajorToJava(major int) int {
	if major < 45 {
		return 0
	}
	return major - 44
}

// feed consumes one console line and returns a non-nil conflict exactly once,
// when the UnsupportedClassVersionError is seen.
func (d *javaVersionDetector) feed(line string) *ModConflict {
	if d.done {
		return nil
	}
	m := unsupportedClassVersionRe.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	d.done = true

	requiredMajor, _ := strconv.Atoi(m[2])
	currentMajor, _ := strconv.Atoi(m[3])
	requiredJava := classMajorToJava(requiredMajor)
	currentJava := classMajorToJava(currentMajor)

	var summary string
	switch {
	case requiredJava > 0 && currentJava > 0:
		summary = fmt.Sprintf(
			"This server requires Java %d or newer, but it was started with Java %d. Install Java %d (or newer) and set it as this server's Java runtime, then restart.",
			requiredJava, currentJava, requiredJava)
	case requiredJava > 0:
		summary = fmt.Sprintf(
			"This server requires Java %d or newer, but it was started with an older Java. Install Java %d (or newer) and set it as this server's Java runtime, then restart.",
			requiredJava, requiredJava)
	default:
		summary = "This server was built for a newer version of Java than the one running it. Install a newer Java and set it as this server's Java runtime, then restart."
	}

	return &ModConflict{
		Detected: true,
		Kind:     "java_version",
		Summary:  summary,
		// Not a mod-disable fix; the summary states the action. Emit an empty
		// (non-nil) slice so it marshals to [] — the web maps over suggestions
		// unconditionally and would throw on a null.
		Suggestions:  []ConflictSuggestion{},
		RequiredJava: requiredJava,
		Raw:          []string{line},
		DetectedAt:  time.Now().UnixMilli(),
	}
}
