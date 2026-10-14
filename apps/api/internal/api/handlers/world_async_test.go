package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/events"
	"github.com/mcsm/api/internal/operations"
	"github.com/mcsm/api/internal/store"
)

func TestAsyncWorldUploadFollowsTheUnpack(t *testing.T) {
	prev := worldJobPoll
	worldJobPoll = 10 * time.Millisecond
	defer func() { worldJobPoll = prev }()

	var polls atomic.Int32
	agentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/register"):
		case strings.HasSuffix(r.URL.Path, "/worlds/upload"):
			if r.URL.Query().Get("async") != "true" {
				t.Error("the API did not ask for an async unpack")
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"job_id": "j1", "name": "survival", "files": 3, "bytes": 100})
		case strings.HasSuffix(r.URL.Path, "/worlds/jobs/j1"):
			status := "running"
			if polls.Add(1) >= 3 {
				status = "succeeded"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "j1", "status": status, "done_bytes": 50, "total_bytes": 100,
				"result": map[string]any{"name": "survival"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer agentSrv.Close()

	f := newFixture(t)
	ctx := context.Background()
	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(agentSrv.URL, "http://"))
	port, _ := strconv.Atoi(portStr)
	node, _ := f.store.CreateNode(ctx, &store.Node{Name: "fake", FQDN: host, Port: port, Scheme: "http"}, "tok")
	owner := f.user("owner@example.com", "user")
	srv, err := f.store.CreateServer(ctx, &store.Server{NodeID: node.ID, OwnerID: owner.ID, Name: "SMP", Platform: "paper",
		MCVersion: "1.21.4", DirectoryPath: "servers/smp", JavaBinary: "java", Port: 25565, RAMMbMin: 512, RAMMbMax: 1024})
	if err != nil {
		t.Fatal(err)
	}
	ops := operations.New(f.store, events.New())
	h := NewFileHandlers(f.store).WithOperations(ops)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", "world.zip")
	_, _ = part.Write([]byte("zip bytes"))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/"+srv.ID+"/worlds/upload?async=true", &body)
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{UserID: owner.ID, Role: "user"}))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	h.UploadWorld(rr, withURLParams(req, "id", srv.ID))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var accepted struct {
		Operation store.Operation `json:"operation"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&accepted)
	if accepted.Operation.Kind != operations.KindWorldLoad {
		t.Fatalf("operation: %+v", accepted.Operation)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		op, err := f.store.GetOperation(ctx, accepted.Operation.ID)
		if err == nil && op.Status != store.OpRunning {
			if op.Status != store.OpSucceeded {
				t.Fatalf("operation ended %s: %s", op.Status, op.Error)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the operation never finished")
}
