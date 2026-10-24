package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/agent/internal/process"
)

func serverRequest(method, path, id, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", id)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rc))
}

// slowInstall stands in for a runtime download: it holds a .part file open in
// the server directory until its context is cancelled, then removes it as the
// real download does.
func slowInstall(t *testing.T) (entered <-chan struct{}) {
	t.Helper()
	ch := make(chan struct{})
	prev := ensureRuntime
	ensureRuntime = func(ctx context.Context, dir, _, _, _, _ string) error {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		part := filepath.Join(dir, "server.jar.part")
		f, err := os.Create(part)
		if err != nil {
			return err
		}
		close(ch)
		<-ctx.Done()
		f.Close()
		os.Remove(part)
		return ctx.Err()
	}
	t.Cleanup(func() { ensureRuntime = prev })
	return ch
}

type startResult struct {
	code int
	body string
}

func startInBackground(h *ServerHandlers, dir string) <-chan startResult {
	out := make(chan startResult, 1)
	go func() {
		rr := httptest.NewRecorder()
		h.Start(rr, serverRequest(http.MethodPost, "/agent/v1/servers/srv/start", "srv",
			`{"directory":"`+filepath.ToSlash(dir)+`","platform":"paper","mc_version":"1.21.1"}`))
		out <- startResult{rr.Code, rr.Body.String()}
	}()
	return out
}

func waitStart(t *testing.T, done <-chan startResult) startResult {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("the start never gave up")
		return startResult{}
	}
}

// A kill while a start is still fetching its runtime cancels the start, which
// then never launches the server.
func TestKillCancelsAStartStillInstalling(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "srv")
	mgr := process.NewManager(root)
	h := NewServerHandlers(mgr, root)
	entered := slowInstall(t)

	done := startInBackground(h, dir)
	<-entered
	if !mgr.Starting("srv") {
		t.Fatal("the start is not recorded while it installs")
	}

	rr := httptest.NewRecorder()
	h.Kill(rr, serverRequest(http.MethodPost, "/agent/v1/servers/srv/kill", "srv", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("kill: %d %s", rr.Code, rr.Body.String())
	}
	if r := waitStart(t, done); r.code != http.StatusServiceUnavailable || !strings.Contains(r.body, "start cancelled") {
		t.Fatalf("start: %d %s", r.code, r.body)
	}
	if mgr.Starting("srv") {
		t.Fatal("the cancelled start is still recorded")
	}
	if s := mgr.Status("srv").Status; s != process.StatusOffline {
		t.Fatalf("status %q, want offline", s)
	}
}

// A purge during the install waits for the download to let go of the
// directory, so the folder really is deleted.
func TestPurgeDuringInstallDeletesTheFolder(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "srv")
	mgr := process.NewManager(root)
	h := NewServerHandlers(mgr, root)
	entered := slowInstall(t)

	done := startInBackground(h, dir)
	<-entered

	rr := httptest.NewRecorder()
	h.Purge(rr, serverRequest(http.MethodDelete, "/agent/v1/servers/srv", "srv",
		`{"directory":"`+filepath.ToSlash(dir)+`","files":true}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("purge: %d %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the server folder survived the purge: %v", err)
	}
	if r := waitStart(t, done); r.code != http.StatusServiceUnavailable {
		t.Fatalf("start: %d %s", r.code, r.body)
	}
}

// Two starts must not install into the same folder at once.
func TestSecondStartWhileInstallingIsRefused(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "srv")
	mgr := process.NewManager(root)
	h := NewServerHandlers(mgr, root)
	entered := slowInstall(t)

	done := startInBackground(h, dir)
	<-entered
	rr := httptest.NewRecorder()
	h.Start(rr, serverRequest(http.MethodPost, "/agent/v1/servers/srv/start", "srv",
		`{"directory":"`+filepath.ToSlash(dir)+`","platform":"paper","mc_version":"1.21.1"}`))
	if rr.Code != http.StatusConflict {
		t.Fatalf("second start: %d %s", rr.Code, rr.Body.String())
	}

	stop := httptest.NewRecorder()
	h.Stop(stop, serverRequest(http.MethodPost, "/agent/v1/servers/srv/stop", "srv", `{}`))
	if stop.Code != http.StatusOK {
		t.Fatalf("stop during install: %d %s", stop.Code, stop.Body.String())
	}
	waitStart(t, done)
}

// Killing a server with no process is what the kill asked for.
func TestKillWithNothingRunningSucceeds(t *testing.T) {
	root := t.TempDir()
	h := NewServerHandlers(process.NewManager(root), root)
	rr := httptest.NewRecorder()
	h.Kill(rr, serverRequest(http.MethodPost, "/agent/v1/servers/srv/kill", "srv", ""))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "offline") {
		t.Fatalf("kill: %d %s", rr.Code, rr.Body.String())
	}
}
