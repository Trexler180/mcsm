package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcsm/api/internal/store"
)

// Moving servers answers with where they were; sending that back undoes it.
// Deleting a folder answers with what restores it.
func TestPlacementAndFolderUndo(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	admin := f.user("admin@example.com", "admin")
	a := f.server(admin, "Alpha")
	b := f.server(admin, "Bravo")
	folder, err := f.store.CreateServerFolder(ctx, &store.ServerFolder{Name: "Survival"})
	if err != nil {
		t.Fatal(err)
	}
	h := NewFolderHandlers(f.store)

	place := func(body string) (int, []store.ServerPlacement) {
		rr := httptest.NewRecorder()
		h.PlaceServers(rr, as(admin, "admin", http.MethodPut, "/api/v1/servers/placement", strings.NewReader(body)))
		var out struct {
			Previous []store.ServerPlacement `json:"previous"`
		}
		_ = json.NewDecoder(rr.Body).Decode(&out)
		return rr.Code, out.Previous
	}
	code, previous := place(`{"servers":[{"id":"` + b.ID + `","folder_id":"` + folder.ID + `","sort_order":0},{"id":"` + a.ID + `","folder_id":"` + folder.ID + `","sort_order":1}]}`)
	if code != http.StatusOK || len(previous) != 2 {
		t.Fatalf("place: %d %v", code, previous)
	}
	list, _ := f.store.ListServers(ctx)
	if list[0].ID != b.ID || list[0].FolderID == nil || *list[0].FolderID != folder.ID {
		t.Fatalf("Bravo should now come first in the folder: %+v", list[0])
	}

	// Undo.
	undo, _ := json.Marshal(map[string]any{"servers": previous})
	if code, _ := place(string(undo)); code != http.StatusOK {
		t.Fatalf("undo: %d", code)
	}
	if got, _ := f.store.GetServer(ctx, b.ID); got.FolderID != nil {
		t.Fatal("undo did not take Bravo back out of the folder")
	}

	if code, _ := place(`{"servers":[{"id":"` + a.ID + `","folder_id":"no-such-folder","sort_order":0}]}`); code != http.StatusBadRequest {
		t.Fatalf("unknown folder: %d, want 400", code)
	}

	// Folder delete and restore.
	_, _ = place(`{"servers":[{"id":"` + a.ID + `","folder_id":"` + folder.ID + `","sort_order":0}]}`)
	rr := httptest.NewRecorder()
	h.Delete(rr, withURLParams(as(admin, "admin", http.MethodDelete, "/api/v1/server-folders/"+folder.ID, nil), "id", folder.ID))
	if rr.Code != http.StatusOK {
		t.Fatalf("delete: %d", rr.Code)
	}
	var del struct {
		Undo json.RawMessage `json:"undo"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&del)
	if got, _ := f.store.GetServer(ctx, a.ID); got.FolderID != nil {
		t.Fatal("deleting the folder left Alpha in it")
	}
	rr = httptest.NewRecorder()
	h.Restore(rr, as(admin, "admin", http.MethodPost, "/api/v1/server-folders/restore", strings.NewReader(string(del.Undo))))
	if rr.Code != http.StatusCreated {
		t.Fatalf("restore: %d %s", rr.Code, rr.Body.String())
	}
	if got, _ := f.store.GetServer(ctx, a.ID); got.FolderID == nil || *got.FolderID != folder.ID {
		t.Fatal("restoring the folder did not put Alpha back")
	}
}
