package handlers

import (
	"fmt"
	"path/filepath"
	"strings"
)

func cleanServerRoot(root string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("server root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func resolveServerDirectory(root, requested, name string) (string, error) {
	cleanRoot, err := cleanServerRoot(root)
	if err != nil {
		return "", err
	}

	dir := requested
	if strings.TrimSpace(dir) == "" {
		dir = safePathSegment(name)
		if dir == "" {
			return "", fmt.Errorf("server directory is required")
		}
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(cleanRoot, dir)
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)

	rel, err := filepath.Rel(cleanRoot, abs)
	if err != nil {
		return "", err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("server directory must be inside %s", cleanRoot)
	}
	return abs, nil
}

func safePathSegment(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
