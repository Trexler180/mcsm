package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecovererAnswersJSON500(t *testing.T) {
	h := RequestID(Recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rr.Code != http.StatusInternalServerError || !strings.Contains(rr.Body.String(), `"code":"internal"`) {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-Request-ID") == "" {
		t.Fatal("a recovered panic should still carry its request id")
	}
}
