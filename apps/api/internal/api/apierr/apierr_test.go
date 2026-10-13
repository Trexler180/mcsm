package apierr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnvelopeDefaults(t *testing.T) {
	for _, tc := range []struct {
		status    int
		code      string
		retryable bool
	}{
		{http.StatusBadRequest, CodeInvalidRequest, false},
		{http.StatusNotFound, CodeNotFound, false},
		{http.StatusConflict, CodeConflict, false},
		{http.StatusTooManyRequests, CodeRateLimited, true},
		{http.StatusBadGateway, CodeBadGateway, true},
		{http.StatusServiceUnavailable, CodeUnavailable, true},
	} {
		rr := httptest.NewRecorder()
		Write(rr, tc.status, "", "msg")
		var b Body
		if err := json.NewDecoder(rr.Body).Decode(&b); err != nil {
			t.Fatal(err)
		}
		if rr.Code != tc.status || b.Error != "msg" || b.Code != tc.code || b.Retryable != tc.retryable {
			t.Errorf("%d: got %d %+v", tc.status, rr.Code, b)
		}
		if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%d: content type %q", tc.status, ct)
		}
	}
	// A specific code wins over the default.
	rr := httptest.NewRecorder()
	WriteBody(rr, http.StatusBadRequest, Body{Error: "bad port", Code: CodeValidation, Field: "port"})
	var b Body
	_ = json.NewDecoder(rr.Body).Decode(&b)
	if b.Code != CodeValidation || b.Field != "port" {
		t.Fatalf("got %+v", b)
	}
}
