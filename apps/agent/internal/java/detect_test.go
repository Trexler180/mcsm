package java

import "testing"

func TestMajorVersion(t *testing.T) {
	cases := map[string]int{
		"21.0.5":    21,
		"17.0.13":   17,
		"1.8.0_392": 8,
		"25":        25,
		"25-ea":     25,
		"24+36":     24,
		"11.0.2":    11,
		"unknown":   0,
		"":          0,
	}
	for in, want := range cases {
		if got := majorVersion(in); got != want {
			t.Errorf("majorVersion(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseVersion(t *testing.T) {
	// Quoted form (HotSpot/Temurin).
	if v := parseVersion(`openjdk version "21.0.5" 2024-10-15`); v != "21.0.5" {
		t.Errorf("quoted parse = %q, want 21.0.5", v)
	}
	// Fallback unquoted "openjdk <ver>" line.
	if v := parseVersion("openjdk 17.0.13\nOpenJDK Runtime Environment"); v != "17.0.13" {
		t.Errorf("unquoted parse = %q, want 17.0.13", v)
	}
}
