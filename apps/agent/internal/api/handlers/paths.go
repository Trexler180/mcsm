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

func validateServerDirectory(root, dir string) (string, error) {
	cleanRoot, err := cleanServerRoot(root)
	if err != nil {
		return "", err
	}
	if dir == "" {
		return "", fmt.Errorf("directory is required")
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
		return "", fmt.Errorf("directory must be inside %s", cleanRoot)
	}
	return abs, nil
}
