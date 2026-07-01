package process

import (
	"os"
	"testing"

	"github.com/mcsm/agent/internal/java"
)

func TestPickFallbackJava(t *testing.T) {
	installs := []java.Installation{
		{Path: "/jvm/21/bin/java", Major: 21},
		{Path: "/jvm/25/bin/java", Major: 25},
		{Path: "/jvm/17/bin/java", Major: 17},
	}

	// Exact feature-version match is preferred over "newest".
	if got := pickFallbackJava(21, installs); got == nil || got.Major != 21 {
		t.Errorf("want major 21 match, got %+v", got)
	}
	// No match → newest installed.
	if got := pickFallbackJava(99, installs); got == nil || got.Major != 25 {
		t.Errorf("want newest (25) when no match, got %+v", got)
	}
	// Unknown desired major (0) → newest installed.
	if got := pickFallbackJava(0, installs); got == nil || got.Major != 25 {
		t.Errorf("want newest (25) for unknown want, got %+v", got)
	}
	// Nothing installed → nil.
	if got := pickFallbackJava(21, nil); got != nil {
		t.Errorf("want nil for empty installs, got %+v", got)
	}
}

func TestResolveJavaFallsBackWhenMissing(t *testing.T) {
	stale := `C:\Program Files\Eclipse Adoptium\jdk-25.0.3.9-hotspot\bin\java.exe`
	installs := []java.Installation{
		{Path: "/jvm/21/bin/java", Major: 21},
		{Path: "/jvm/25.0.2/bin/java", Major: 25},
	}
	path, note, err := resolveJava(stale, installs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The stale path asked for Java 25, so the Java 25 install must win over 21.
	if path != "/jvm/25.0.2/bin/java" {
		t.Errorf("path = %q, want the Java 25 install", path)
	}
	if note == "" {
		t.Error("expected a substitution note for the console, got empty")
	}
}

func TestResolveJavaErrorsWhenNoneInstalled(t *testing.T) {
	_, _, err := resolveJava(`C:\nope\jdk-25\bin\java.exe`, nil)
	if err == nil {
		t.Fatal("expected an error when the configured Java is missing and none is installed")
	}
}

func TestResolveJavaKeepsExistingPath(t *testing.T) {
	// The test binary itself is a path that exec.LookPath resolves, standing in
	// for a present java_binary — resolveJava must return it unchanged with no note.
	self := os.Args[0]
	path, note, err := resolveJava(self, nil)
	if err != nil {
		t.Fatalf("unexpected error for existing path: %v", err)
	}
	if path != self {
		t.Errorf("path = %q, want unchanged %q", path, self)
	}
	if note != "" {
		t.Errorf("expected no substitution note, got %q", note)
	}
}
