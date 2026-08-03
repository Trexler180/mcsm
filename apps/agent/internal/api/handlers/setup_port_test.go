package handlers

import (
	"net/http/httptest"
	"testing"
)

func TestQueryPort(t *testing.T) {
	// Absent reads as 0 — "the caller has nothing to say about the port" — so an
	// older panel's setup call leaves server.properties alone instead of having a
	// default guessed over it.
	if port, err := queryPort(httptest.NewRequest("POST", "/setup?dir=x", nil)); err != nil || port != 0 {
		t.Errorf("missing port = (%d, %v), want (0, nil)", port, err)
	}
	if port, err := queryPort(httptest.NewRequest("POST", "/setup?dir=x&port=25570", nil)); err != nil || port != 25570 {
		t.Errorf("port = (%d, %v), want (25570, nil)", port, err)
	}

	for _, raw := range []string{"0", "-1", "70000", "abc", "25570x"} {
		if _, err := queryPort(httptest.NewRequest("POST", "/setup?port="+raw, nil)); err == nil {
			t.Errorf("port=%q should be rejected", raw)
		}
	}
}
