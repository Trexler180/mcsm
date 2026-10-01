package handlers

import (
	"net/http"
	"time"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
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

// verifyStepUp enforces the step-up every sensitive action on a signed-in
// session requires: the current password and, when the account has MFA
// enabled, a current TOTP code. Recovery codes are not accepted here.
//
// Every failure is the same generic 401, so the response cannot tell a wrong
// password from a wrong code, and every failure — an empty password included —
// spends from the shared budget. label names the caller in server logs only.
// Returns false when it has already written a response.
func verifyStepUp(w http.ResponseWriter, r *http.Request, s *store.Store, ip, acct *auth.LoginThrottle, userID, password, totpCode, label string) bool {
	ipKey := passwordIPKey(r)
	acctKey := stepUpAccountKey(userID)
	if ok, retry := ip.Allowed(ipKey); !ok {
		tooManyRequests(w, retry)
		return false
	}
	if ok, retry := acct.Allowed(acctKey); !ok {
		tooManyRequests(w, retry)
		return false
	}
	fail := func() {
		ip.Fail(ipKey)
		acct.Fail(acctKey)
		writeError(w, http.StatusUnauthorized, "reauthentication failed")
	}

	if password == "" {
		// Still a failed attempt: an empty password must not be a free probe.
		fail()
		return false
	}
	hash, err := s.GetUserPasswordHash(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "reauthentication failed")
		return false
	}
	if !auth.CheckPassword(hash, password) {
		fail()
		return false
	}
	cfg, err := s.GetUserTOTP(r.Context(), userID)
	if err != nil {
		writeServerError(w, r, label+": totp lookup", err)
		return false
	}
	if cfg.Enabled && !auth.ValidateTOTP(cfg.Secret, totpCode, time.Now()) {
		fail()
		return false
	}

	ip.Reset(ipKey)
	acct.Reset(acctKey)
	return true
}
