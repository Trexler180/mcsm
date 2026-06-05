package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestHealthAndInfoShapes(t *testing.T) {
	rr := httptest.NewRecorder()
	Health(rr, httptest.NewRequest(http.MethodGet, "/agent/v1/health", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("health status=%d", rr.Code)
	}
	var health map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if health["status"] != "ok" {
		t.Fatalf("health=%v", health)
	}

	rr = httptest.NewRecorder()
	Info(rr, httptest.NewRequest(http.MethodGet, "/agent/v1/info", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("info status=%d", rr.Code)
	}
	var info map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"hostname", "os", "arch", "memory_mb", "disk_gb", "cpu_cores", "agent_uptime_sec"} {
		if _, ok := info[key]; !ok {
			t.Fatalf("info missing %q: %v", key, info)
		}
	}
}

func TestValidateServerDirectoryRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if _, err := validateServerDirectory(root, "../escape"); err == nil {
		t.Fatal("expected traversal to be rejected")
	}
	got, err := validateServerDirectory(root, "survival")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "survival")
	if got != want {
		t.Fatalf("dir=%q want=%q", got, want)
	}
}
