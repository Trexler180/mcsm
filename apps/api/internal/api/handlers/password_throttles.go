package handlers

import (
	"net/http"

	"github.com/mcsm/api/internal/auth"
)

// PasswordThrottles is the single failed-attempt budget shared by every
// endpoint that checks a password: login, and the step-ups that re-prove it on
// an already-authenticated session (access-key creation and rotation, MCP
// action approval, relaxing the approval policy).
//
// Each of those endpoints used to own its throttles, which gave an attacker a
// separate guessing budget per endpoint for the same password. Sharing them
// means a failure anywhere counts everywhere.
type PasswordThrottles struct {
	// IP is the aggressive profile, keyed by source address.
	IP *auth.LoginThrottle
	// Account is the lenient profile. Login keys it by the submitted email
	// (the account may not exist, and the response must not say); step-ups key
	// it by the signed-in user's id.
	Account *auth.LoginThrottle
}

func NewPasswordThrottles() *PasswordThrottles {
	return &PasswordThrottles{IP: auth.NewLoginThrottle(), Account: auth.NewAccountThrottle()}
}

// orNew lets a constructor accept nil, for tests that exercise one handler in
// isolation.
func (p *PasswordThrottles) orNew() *PasswordThrottles {
	if p == nil {
		return NewPasswordThrottles()
	}
	return p
}

// passwordIPKey is the per-address key every password check spends from.
func passwordIPKey(r *http.Request) string { return "ip:" + clientIP(r) }

// stepUpAccountKey is the per-account key for step-ups on a signed-in session.
func stepUpAccountKey(userID string) string { return "user:" + userID }
