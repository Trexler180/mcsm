package handlers

import (
	"archive/zip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/agent/internal/process"
)

func writeBackupZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range entries {
		entry, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func restoreRequest(t *testing.T, h *BackupHandlers, serverID, backupID string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Post("/servers/{id}/backups/{backupId}/restore", h.Restore)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/servers/"+serverID+"/backups/"+backupID+"/restore", nil)
	router.ServeHTTP(rec, req)
	return rec
}

func TestRestoreRejectsUnsafeArchiveWithoutTouchingLiveServer(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "server")
	if err := os.MkdirAll(dst, 0755); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(dst, "live.txt")
	if err := os.WriteFile(live, []byte("keep me"), 0644); err != nil {
		t.Fatal(err)
	}
	writeBackupZip(t, filepath.Join(root, "mcsm-backups", "srv", "bad.zip"), map[string]string{
		"../escape.txt": "nope",
	})

	mgr := process.NewManager(root)
	mgr.RegisterDir("srv", dst)
	rec := restoreRequest(t, NewBackupHandlers(mgr, root), "srv", "bad")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got, err := os.ReadFile(live)
	if err != nil {
		t.Fatalf("live file was removed: %v", err)
	}
	if string(got) != "keep me" {
		t.Fatalf("live file changed to %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("archive escaped restore directory: %v", err)
	}
}

func TestRestoreSwapsFullyExtractedTreeIntoPlace(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "server")
	if err := os.MkdirAll(dst, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "old.txt"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	writeBackupZip(t, filepath.Join(root, "mcsm-backups", "srv", "good.zip"), map[string]string{
		"world/level.dat": "new world",
	})

	mgr := process.NewManager(root)
	mgr.RegisterDir("srv", dst)
	rec := restoreRequest(t, NewBackupHandlers(mgr, root), "srv", "good")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dst, "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("old tree was not replaced: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "world", "level.dat"))
	if err != nil || string(got) != "new world" {
		t.Fatalf("restored file = %q, %v", got, err)
	}
}

func TestBackupBlockedMsg(t *testing.T) {
	total := 240 * gib // danger floor clamps to 10 GiB
	cases := []struct {
		name     string
		free     int64
		estimate int64
		blocked  bool
	}{
		{"plenty of space", 100 * gib, 10 * gib, false},
		{"lands exactly on floor", 20 * gib, 10 * gib, false},
		{"would dip under floor", 15 * gib, 10 * gib, true},
		{"estimate exceeds free", 5 * gib, 10 * gib, true},
		{"unknown total", 0, 10 * gib, false},
	}
	for _, c := range cases {
		tot := total
		if c.name == "unknown total" {
			tot = 0
		}
		got := backupBlockedMsg(c.free, tot, c.estimate)
		if (got != "") != c.blocked {
			t.Errorf("%s: blocked=%v msg=%q", c.name, c.blocked, got)
		}
	}
}

func TestLowSpaceWarning(t *testing.T) {
	total := 240 * gib // warn floor clamps to 24 GiB
	if w := lowSpaceWarning(100*gib, total); w != "" {
		t.Errorf("expected no warning with plenty free, got %q", w)
	}
	if w := lowSpaceWarning(10*gib, total); w == "" {
		t.Error("expected a warning when free space is under the warn floor")
	}
	if w := lowSpaceWarning(10*gib, 0); w != "" {
		t.Errorf("expected no warning with unknown total, got %q", w)
	}
}

func TestFloorsClamp(t *testing.T) {
	if f := dangerFloor(10 * gib); f != 2*gib {
		t.Errorf("small disk danger floor = %d, want 2 GiB", f)
	}
	if f := dangerFloor(4096 * gib); f != 10*gib {
		t.Errorf("huge disk danger floor = %d, want 10 GiB", f)
	}
	if f := warnFloor(10 * gib); f != 5*gib {
		t.Errorf("small disk warn floor = %d, want 5 GiB", f)
	}
	if f := warnFloor(4096 * gib); f != 25*gib {
		t.Errorf("huge disk warn floor = %d, want 25 GiB", f)
	}
}

func TestEstimateBackupSize(t *testing.T) {
	src := t.TempDir()
	write := func(rel string, n int) {
		p := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, n), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("world/region.mca", 1000)
	write("server.jar", 500)
	write("logs/latest.log", 9999)      // skipped dir
	write("session.lock", 123)          // skipped ext
	write("mcsm-backups/old.zip", 8888) // skipped dir

	skipRoots := []string{
		filepath.Join(src, "mcsm-backups"),
		filepath.Join(src, "logs"),
	}
	skipExt := map[string]bool{".lock": true, ".lck": true}
	if got := estimateBackupSize(src, skipRoots, skipExt); got != 1500 {
		t.Errorf("estimate = %d, want 1500", got)
	}
}
