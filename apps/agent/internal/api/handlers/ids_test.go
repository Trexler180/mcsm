package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/agent/internal/process"
)

func TestValidatePathID(t *testing.T) {
	valid := []string{
		"3f1c9a52-7b1d-4c8e-9f2a-1b2c3d4e5f6a",
		"backup-1719800000",
		"a",
		"Server_1.old",
		"0" + strings.Repeat("x", 127),
	}
	for _, s := range valid {
		if err := validatePathID(s); err != nil {
			t.Errorf("validatePathID(%q) rejected a valid id: %v", s, err)
		}
	}

	invalid := []string{
		"",
		"..",
		".",
		".hidden",
		"-dash-first",
		"a/b",
		`a\b`,
		"../../etc/passwd",
		"..\\..\\windows",
		"a b",
		"0" + strings.Repeat("x", 128), // 129 chars
	}
	for _, s := range invalid {
		if err := validatePathID(s); err == nil {
			t.Errorf("validatePathID(%q) accepted an unsafe id", s)
		}
	}
}

// TestDownloadBackupRejectsTraversal drives a real request through a chi route
// to prove URL-encoded traversal in the backupId segment is rejected before
// any filesystem access.
func TestDownloadBackupRejectsTraversal(t *testing.T) {
	h := NewBackupHandlers(process.NewManager(t.TempDir()), t.TempDir())
	router := chi.NewRouter()
	router.Get("/servers/{id}/backups/{backupId}/download", h.DownloadBackup)

	req := httptest.NewRequest(http.MethodGet,
		"/servers/srv1/backups/..%2F..%2F..%2Fsecrets/download", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("traversal backupId: got status %d, want 400", rec.Code)
	}
}
