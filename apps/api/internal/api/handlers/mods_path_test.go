package handlers

import "testing"

func TestCleanRelPathRejectsTraversal(t *testing.T) {
	bad := []string{
		"",
		"..",
		"../etc/passwd",
		"../../secret",
		"mods/../../escape",
		"./../../x",
		`..\..\windows`,
	}
	for _, p := range bad {
		if got, ok := cleanRelPath(p); ok {
			t.Errorf("cleanRelPath(%q) = (%q, true), want rejected", p, got)
		}
	}
}

func TestCleanRelPathAllowsNormalPaths(t *testing.T) {
	cases := map[string]string{
		"mods/foo.jar":          "mods/foo.jar",
		"/mods/foo.jar":         "mods/foo.jar",
		"config/sub/a.toml":     "config/sub/a.toml",
		"mods/./bar.jar":        "mods/bar.jar",
		"server.properties":     "server.properties",
		"a/b/../c.txt":          "a/c.txt", // internal .. that stays within root is fine
	}
	for in, want := range cases {
		got, ok := cleanRelPath(in)
		if !ok || got != want {
			t.Errorf("cleanRelPath(%q) = (%q, %v), want (%q, true)", in, got, ok, want)
		}
	}
}
