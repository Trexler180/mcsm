package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mcsm/api/internal/auth"
)

// codeAt is what an authenticator shows for secret at t (RFC 6238, SHA-1, six
// digits, 30 seconds).
func codeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(at.Unix()/30))
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	o := sum[len(sum)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[o:])&0x7fffffff)%1_000_000)
}

// An authenticator code signs in once: the same code a second time, while it
// is still current, is refused (RFC 6238 §5.2).
func TestATOTPCodeIsGoodOnce(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	u := f.user("mfa@example.com", "user")
	const secret = "JBSWY3DPEHPK3PXP"
	if err := f.store.SetUserTOTPSecret(ctx, u.ID, secret); err != nil {
		t.Fatal(err)
	}
	if err := f.store.EnableUserTOTP(ctx, u.ID, nil); err != nil {
		t.Fatal(err)
	}
	h := NewAuthHandlers(f.store, "secret", auth.NewTicketStore(), nil)
	login := func(code string) int {
		body, _ := json.Marshal(map[string]string{"email": "mfa@example.com", "password": "correct horse battery", "totp_code": code})
		rr := httptest.NewRecorder()
		h.Login(rr, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body)))
		return rr.Code
	}

	now := time.Now()
	code := codeAt(t, secret, now)
	if got := login(code); got != http.StatusOK {
		t.Fatalf("first use: %d, want 200", got)
	}
	if got := login(code); got != http.StatusUnauthorized {
		t.Fatalf("the same code again: %d, want 401", got)
	}
	// An earlier code that is still inside the drift window is spent too.
	if got := login(codeAt(t, secret, now.Add(-30*time.Second))); got != http.StatusUnauthorized {
		t.Fatalf("an older code after a newer one: %d, want 401", got)
	}
	// The next one works.
	if got := login(codeAt(t, secret, now.Add(30*time.Second))); got != http.StatusOK {
		t.Fatalf("the next code: %d, want 200", got)
	}
}
