package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/agent/internal/process"
)

// An async upload answers once the archive is in and checked, then unpacks in
// the background as a job that reports its progress and result.
func TestAsyncWorldUploadUnpacksAsAJob(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "srv")
	mgr := process.NewManager(root)
	mgr.RegisterDir("srv", dir)
	h := NewWorldHandlers(mgr)

	zipPath := filepath.Join(t.TempDir(), "world.zip")
	writeBackupZip(t, zipPath, map[string]string{
		"survival/level.dat":        "level",
		"survival/region/r.0.0.mca": "region data",
		"survival/data/raids.dat":   "raids",
	})
	archive, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", "world.zip")
	_, _ = part.Write(archive)
	_ = mw.Close()

	withID := func(r *http.Request, kv ...string) *http.Request {
		rc := chi.NewRouteContext()
		for i := 0; i+1 < len(kv); i += 2 {
			rc.URLParams.Add(kv[i], kv[i+1])
		}
		return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rc))
	}
	req := httptest.NewRequest(http.MethodPost, "/agent/v1/servers/srv/worlds/upload?async=true", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	h.Upload(rr, withID(req, "id", "srv"))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var accepted struct {
		JobID string `json:"job_id"`
		Name  string `json:"name"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&accepted)
	if accepted.JobID == "" || accepted.Name != "survival" {
		t.Fatalf("accepted: %+v", accepted)
	}

	var status WorldJobStatus
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rr := httptest.NewRecorder()
		h.Job(rr, withID(httptest.NewRequest(http.MethodGet, "/", nil), "id", "srv", "jobId", accepted.JobID))
		_ = json.NewDecoder(rr.Body).Decode(&status)
		if status.Status != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status.Status != "succeeded" || status.Done != status.Total || status.Result["name"] != "survival" {
		t.Fatalf("job: %+v", status)
	}
	if _, err := os.Stat(filepath.Join(dir, "survival", "region", "r.0.0.mca")); err != nil {
		t.Fatalf("world not unpacked: %v", err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".mcsm-world-upload-*"))
	if len(leftovers) != 0 {
		t.Fatalf("staged upload left behind: %v", leftovers)
	}
}
