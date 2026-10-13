package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

// sessionToken signs in userID the way the login handler does: a real session
// row and an access token bound to it. The router checks the session on every
// request, so a session-less token would be refused.
func sessionToken(t testing.TB, s *store.Store, secret, userID, email, role string) (string, error) {
	t.Helper()
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	sid, err := s.CreateRefreshToken(context.Background(), userID, "test-"+hex.EncodeToString(b), "", "", time.Now().Add(time.Hour))
	if err != nil {
		return "", err
	}
	return auth.IssueSessionToken(secret, userID, email, role, sid)
}
