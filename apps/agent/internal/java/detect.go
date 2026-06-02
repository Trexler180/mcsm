package java

import (
	"os/exec"
	"regexp"
	"strings"
)

type Installation struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}

var versionRe = regexp.MustCompile(`version "([^"]+)"`)

func Detect() []Installation {
	candidates := commonPaths()
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
		result = append(result, Installation{Path: resolved, Version: version})
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
