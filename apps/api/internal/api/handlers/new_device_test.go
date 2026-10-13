package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/notify"
)

const (
	chromeWindows   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"
	chromeWindowsV2 = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.1.2 Safari/537.36"
	firefoxLinux    = "Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0"
)

// The first sign-in is not a new device, nor is the same browser after an
// update; a different browser is, and only its owner is told.
func TestNewDeviceSignInAlertsTheOwner(t *testing.T) {
	f := newFixture(t)
	u := f.user("someone@example.com", "user")
	engine := notify.NewEngine(f.store, notify.NewHub(), nil)
	h := NewAuthHandlers(f.store, "secret", auth.NewTicketStore(), nil).WithNotifier(engine)

	signIn := func(ua string) {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"email": "someone@example.com", "password": "correct horse battery"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
		req.Header.Set("User-Agent", ua)
		rr := httptest.NewRecorder()
		h.Login(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("login: %d %s", rr.Code, rr.Body.String())
		}
	}
	alerts := func() int {
		items, err := f.store.ListNotifications(context.Background(), u.ID, false, 50)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, it := range items {
			if it.EventType == notify.EventAuthNewDevice {
				n++
			}
		}
		return n
	}

	signIn(chromeWindows)
	signIn(chromeWindowsV2)
	time.Sleep(100 * time.Millisecond)
	if n := alerts(); n != 0 {
		t.Fatalf("%d new-device alerts for a first sign-in and a browser update", n)
	}

	signIn(firefoxLinux)
	deadline := time.Now().Add(2 * time.Second)
	for alerts() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := alerts(); n != 1 {
		t.Fatalf("%d new-device alerts after signing in from Firefox on Linux, want 1", n)
	}
	if got := deviceLabel(firefoxLinux); got != "Firefox on Linux" {
		t.Fatalf("label = %q", got)
	}
}
