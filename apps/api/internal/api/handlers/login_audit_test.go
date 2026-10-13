package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

func attemptLogin(h *AuthHandlers, email, password string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"email": email, "password": password, "totp_code": "000000"})
	rr := httptest.NewRecorder()
	h.Login(rr, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body)))
	return rr
}

// A failed sign-in is recorded with why it failed, never with the address in
// the clear.
func TestFailedLoginIsAuditedWithoutTheEmail(t *testing.T) {
	f := newFixture(t)
	u := f.user("someone@example.com", "user")
	h := NewAuthHandlers(f.store, "secret", auth.NewTicketStore(), nil)

	if rr := attemptLogin(h, "someone@example.com", "wrong password"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rr.Code)
	}
	if rr := attemptLogin(h, "nobody@example.com", "whatever"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rr.Code)
	}

	entries, _, err := f.store.ListAuditPage(context.Background(), store.AuditQuery{ActionPrefix: "auth.login_failed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("%d failed-login rows, want 2", len(entries))
	}
	var sawBadPassword bool
	for _, e := range entries {
		if e.Detail == nil || strings.Contains(*e.Detail, "@") {
			t.Fatalf("detail exposes the address: %v", e.Detail)
		}
		if strings.Contains(*e.Detail, "bad_password") {
			sawBadPassword = true
			if e.UserID == nil || *e.UserID != u.ID {
				t.Error("a wrong password for a real account is not attributed to it")
			}
		}
	}
	if !sawBadPassword {
		t.Error("the wrong-password attempt was not recorded with its reason")
	}
}

// A database restored without its encryption key makes TOTP secrets
// unreadable. Sign-in says so plainly instead of answering a bare 500.
func TestLoginExplainsAnUnreadableSecondFactor(t *testing.T) {
	f := newFixture(t)
	u := f.user("mfa@example.com", "user")
	ctx := context.Background()
	if err := f.store.SetUserTOTPSecret(ctx, u.ID, "JBSWY3DPEHPK3PXP"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.EnableUserTOTP(ctx, u.ID, nil); err != nil {
		t.Fatal(err)
	}
	// Same database, different key: what a restore without the key file gives.
	moved := store.New(f.db).WithEncryption("a-different-key")
	h := NewAuthHandlers(moved, "secret", auth.NewTicketStore(), nil)

	rr := attemptLogin(h, "mfa@example.com", "correct horse battery")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&out)
	if out.Code != "encryption_key_mismatch" {
		t.Fatalf("code = %q", out.Code)
	}
}
