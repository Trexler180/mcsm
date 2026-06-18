package java

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type Installation struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	// Major is the Java feature release (8, 17, 21, 25…), parsed from Version, or
	// 0 if it couldn't be determined. It's what callers compare against a jar's
	// required version.
	Major int `json:"major"`
}

var versionRe = regexp.MustCompile(`version "([^"]+)"`)

// Detect probes well-known install locations (per-OS fixed paths and globs) plus
// whatever "java" resolves to on PATH, returning one entry per distinct runtime.
func Detect() []Installation {
	candidates := commonPaths()
	for _, pattern := range commonGlobs() {
		if matches, err := filepath.Glob(pattern); err == nil {
			candidates = append(candidates, matches...)
		}
	}
	candidates = append(candidates, "java")

	seen := make(map[string]bool)
	var result []Installation

	for _, path := range candidates {
		resolved, err := exec.LookPath(path)
		if err != nil {
			continue
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true

		out, err := exec.Command(resolved, "-version").CombinedOutput()
		if err != nil {
			continue
		}

		version := parseVersion(string(out))
		result = append(result, Installation{
			Path:    resolved,
			Version: version,
			Major:   majorVersion(version),
		})
	}
	return result
}

func parseVersion(output string) string {
	m := versionRe.FindStringSubmatch(output)
	if len(m) >= 2 {
		return m[1]
	}
	// Try openjdk style: "openjdk 21.0.5 ..."
	for _, line := range strings.Split(output, "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 2 && (parts[0] == "openjdk" || parts[0] == "java") {
			return parts[1]
		}
	}
	return "unknown"
}

// majorVersion turns a Java version string into its feature release: "21.0.5" →
// 21, "17" → 17, "1.8.0_392" → 8 (the legacy 1.x scheme), "25-ea" → 25. Returns
// 0 when it can't be parsed.
func majorVersion(v string) int {
	v = strings.TrimSpace(v)
	if v == "" || v == "unknown" {
		return 0
	}
	fields := strings.FieldsFunc(v, func(r rune) bool {
		return r == '.' || r == '_' || r == '-' || r == '+'
	})
	if len(fields) == 0 {
		return 0
	}
	first, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0
	}
	// Legacy "1.8" style: the feature version is the second component.
	if first == 1 && len(fields) >= 2 {
		if second, err := strconv.Atoi(fields[1]); err == nil {
			return second
		}
	}
	return first
}
