package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mcsm/api/internal/store"
)

type fakeRunner struct {
	ran  []*store.ScheduledTask
	user string
}

func (f *fakeRunner) RunNow(task *store.ScheduledTask, userID string) (*store.TaskRun, error) {
	f.ran = append(f.ran, task)
	f.user = userID
	return &store.TaskRun{ID: "run-1", TaskID: task.ID, ServerID: task.ServerID, Action: task.Action, Status: "running"}, nil
}

// Running a task by hand runs it as the person asking, so it needs what
// scheduling it would: the permission its action requires, not just tasks.
func TestRunTaskNowNeedsTheActionsPermission(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	owner := f.user("owner@example.com", "user")
	helper := f.user("helper@example.com", "user")
	srv := f.server(owner, "Survival")
	if err := f.store.SetServerPermissions(ctx, srv.ID, helper.ID, []string{"tasks"}); err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`{"command":"say hi"}`)
	task, err := f.store.CreateTask(ctx, &store.ScheduledTask{ServerID: srv.ID, Name: "Greeting", CronExpr: "0 4 * * *", Action: "command", Payload: payload, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	h := NewTaskHandlers(f.store).WithRunner(runner)
	run := func(u *store.User) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		h.RunNow(rr, withURLParams(as(u, "user", http.MethodPost, "/", nil), "id", srv.ID, "taskId", task.ID))
		return rr
	}

	// tasks alone doesn't reach the console.
	if rr := run(helper); rr.Code != http.StatusForbidden {
		t.Fatalf("helper with only tasks: status %d, want 403", rr.Code)
	}
	if len(runner.ran) != 0 {
		t.Fatal("the task ran for someone who may not run its command")
	}

	rr := run(owner)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("owner: status %d: %s", rr.Code, rr.Body.String())
	}
	if len(runner.ran) != 1 || runner.user != owner.ID {
		t.Fatalf("ran %d times as %q, want once as the owner", len(runner.ran), runner.user)
	}

	// A task of another server is not found from this one.
	other := f.server(owner, "Creative")
	rr = httptest.NewRecorder()
	h.RunNow(rr, withURLParams(as(owner, "user", http.MethodPost, "/", nil), "id", other.ID, "taskId", task.ID))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("task through the wrong server: status %d, want 404", rr.Code)
	}
}

func TestRunTaskNowWithoutARunner(t *testing.T) {
	f := newFixture(t)
	owner := f.user("owner@example.com", "user")
	srv := f.server(owner, "Survival")
	rr := httptest.NewRecorder()
	NewTaskHandlers(f.store).RunNow(rr, withURLParams(as(owner, "user", http.MethodPost, "/", nil), "id", srv.ID, "taskId", "x"))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rr.Code)
	}
}
