package files

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return base
}

// A whole file comes back when it fits, so a caller that asks for a tail of a
// short log is not handed a fragment of it.
func TestReadContentTailReturnsWholeSmallFile(t *testing.T) {
	body := "line one\nline two\nline three\n"
	base := writeTemp(t, "latest.log", body)

	got, err := ReadContentTail(base, "/latest.log", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("got %q, want the whole file", got)
	}
}

// The point of the parameter: a file far larger than the ceiling is readable,
// and only its end comes back.
func TestReadContentTailBoundsALargeFile(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20000; i++ {
		b.WriteString("a line of perfectly ordinary minecraft log output\n")
	}
	b.WriteString("THE LAST LINE\n")
	base := writeTemp(t, "latest.log", b.String())

	got, err := ReadContentTail(base, "/latest.log", 4096)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > 4096 {
		t.Fatalf("tail was %d bytes, want at most 4096", len(got))
	}
	if !strings.Contains(string(got), "THE LAST LINE") {
		t.Error("the tail does not include the end of the file")
	}
}

// A cut that lands mid-line drops the partial line rather than returning a
// fragment that reads like a truncated timestamp.
func TestReadContentTailStartsAtALineBoundary(t *testing.T) {
	body := "aaaaaaaaaaaaaaaaaaaaaaaaa\nbbbbbbbbbbbbbbbbbbbbbbbbb\nccccccccccccccccccccccccc\n"
	base := writeTemp(t, "latest.log", body)

	// 40 bytes lands inside the second line.
	got, err := ReadContentTail(base, "/latest.log", 40)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimRight(string(got), "\n"), "\n") {
		if line != "" && len(line) != 25 {
			t.Fatalf("a partial line survived: %q", line)
		}
	}
}

// Zero means "no bound", which is what a caller written before this parameter
// existed sends — the compatibility case that keeps an older panel working.
func TestReadContentTailZeroReadsEverything(t *testing.T) {
	body := strings.Repeat("x", 5000)
	base := writeTemp(t, "latest.log", body)

	got, err := ReadContentTail(base, "/latest.log", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(body) {
		t.Fatalf("got %d bytes, want the whole %d", len(got), len(body))
	}
}

// The tail read is anchored to the server root like every other file operation.
func TestReadContentTailCannotEscapeTheRoot(t *testing.T) {
	base := writeTemp(t, "latest.log", "safe\n")
	for _, path := range []string{"../../../etc/passwd", "/../secret", `..\..\secret`} {
		if _, err := ReadContentTail(base, path, 1024); err == nil {
			t.Errorf("path %q escaped the server root", path)
		}
	}
}
