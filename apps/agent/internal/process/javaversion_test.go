package process

import (
	"strings"
	"testing"
)

func TestJavaVersionDetector(t *testing.T) {
	// The exact line a Fabric server emits when the bundler was compiled for a
	// newer Java than the runtime (class 69 = Java 25, class 65 = Java 21).
	line := `Caused by: java.lang.UnsupportedClassVersionError: net/minecraft/bundler/Main has been compiled by a more recent version of the Java Runtime (class file version 69.0), this version of the Java Runtime only recognizes class file versions up to 65.0`

	var d javaVersionDetector
	got := d.feed(line)
	if got == nil {
		t.Fatal("expected a conflict, got nil")
	}
	if got.Kind != "java_version" {
		t.Fatalf("kind = %q, want java_version", got.Kind)
	}
	if !strings.Contains(got.Summary, "Java 25") || !strings.Contains(got.Summary, "Java 21") {
		t.Fatalf("summary should name both versions, got: %q", got.Summary)
	}
	if got.Suggestions == nil {
		t.Fatal("suggestions must be non-nil (marshals to [] for the web)")
	}

	// Only fires once.
	if again := d.feed(line); again != nil {
		t.Fatal("detector should report at most once")
	}
}

func TestJavaVersionDetectorIgnoresUnrelated(t *testing.T) {
	var d javaVersionDetector
	for _, line := range []string{
		"[main/INFO]: Loading Minecraft 1.21 with Fabric Loader 0.16.0",
		"java.lang.RuntimeException: An exception occurred when launching the server!",
		"[main/INFO]: ModernFix reached bootstrap stage",
	} {
		if c := d.feed(line); c != nil {
			t.Fatalf("unexpected conflict for line: %q", line)
		}
	}
}

func TestClassMajorToJava(t *testing.T) {
	cases := map[int]int{52: 8, 61: 17, 65: 21, 68: 24, 69: 25, 44: 0, 0: 0}
	for major, want := range cases {
		if got := classMajorToJava(major); got != want {
			t.Errorf("classMajorToJava(%d) = %d, want %d", major, got, want)
		}
	}
}
