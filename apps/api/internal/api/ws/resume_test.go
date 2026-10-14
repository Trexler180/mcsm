package ws

import (
	"net/http/httptest"
	"testing"
)

func TestResumeQueryPassesOnlyAValidPosition(t *testing.T) {
	for target, want := range map[string]string{
		"/console?run=ab12&since=40&ticket=secret": "?run=ab12&since=40",
		"/console?ticket=secret":                   "",
		"/console?run=AB12&since=40":               "",
		"/console?run=ab12&since=-1":               "",
		"/console?run=ab12&since=x":                "",
		"/console?run=ab12%26evil%3D1&since=4":     "",
	} {
		if got := resumeQuery(httptest.NewRequest("GET", target, nil)); got != want {
			t.Errorf("%s -> %q, want %q", target, got, want)
		}
	}
}
