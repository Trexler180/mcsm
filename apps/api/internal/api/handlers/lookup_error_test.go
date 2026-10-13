package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A missing server is a 404; a database that fails is not, and must not be
// reported as one.
func TestLookupErrorsAreNotAllNotFound(t *testing.T) {
	f := newFixture(t)

	rr := httptest.NewRecorder()
	if _, _, ok := serverAgent(rr, httptest.NewRequest(http.MethodGet, "/", nil), f.store, "no-such-server"); ok || rr.Code != http.StatusNotFound {
		t.Fatalf("missing server: ok=%v status=%d, want 404", ok, rr.Code)
	}

	_ = f.db.Close()
	rr = httptest.NewRecorder()
	if _, _, ok := serverAgent(rr, httptest.NewRequest(http.MethodGet, "/", nil), f.store, "no-such-server"); ok || rr.Code != http.StatusInternalServerError {
		t.Fatalf("failed database: ok=%v status=%d, want 500", ok, rr.Code)
	}
}
