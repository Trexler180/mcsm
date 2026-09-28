package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
	"github.com/mcsm/api/migrations"
)

const keyTestSecret = "key-test-secret"
const keyTestPassword = "correct horse battery"

type keyHandlerEnv struct {
	store   *store.Store
	db      *sql.DB
	router  http.Handler
	ownerID string
	otherID string
	adminID string
	serverA string
	serverB string
}

// keyHandlerRouter mounts the lifecycle routes under the same auth middleware
// the real router uses, without the machine-boundary middleware — so these
// tests prove the handler's own guards, not just the route boundary in front
// of them.
func newKeyHandlerEnv(t *testing.T) *keyHandlerEnv {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(db, "."); err != nil {
		t.Fatal(err)
	}

	s := store.New(db)
	ctx := context.Background()
	node, err := s.CreateNode(ctx, &store.Node{Name: "local", FQDN: "localhost", Port: 8090, Scheme: "http"}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword(keyTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	mkUser := func(email, role string) string {
		u, err := s.CreateUser(ctx, email, hash, role)
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	ownerID := mkUser("owner@example.com", "user")
	otherID := mkUser("other@example.com", "user")
	adminID := mkUser("admin@example.com", "admin")

	mkServer := func(name string) string {
		srv, err := s.CreateServer(ctx, &store.Server{
			NodeID: node.ID, OwnerID: ownerID, Name: name, Platform: "paper",
			MCVersion: "1.21.4", DirectoryPath: "servers/" + name, JavaBinary: "java",
			Port: 25565, RAMMbMin: 512, RAMMbMax: 2048,
		})
		if err != nil {
			t.Fatal(err)
		}
		return srv.ID
	}

	h := NewAPIKeyHandlers(s, nil)
	r := chi.NewRouter()
	r.Use(auth.Middleware(keyTestSecret, auth.NewTicketStore(), func(ctx context.Context, presented, ip string) (*auth.MachineIdentity, error) {
		res, err := s.AuthenticateAccessKey(ctx, presented)
		if err != nil {
			return nil, err
		}
		return &auth.MachineIdentity{
			KeyID: res.Key.ID, UserID: res.User.ID, Email: res.User.Email, Role: res.User.Role,
			Scopes: res.Key.Scopes, ServerIDs: res.Key.ServerIDs,
		}, nil
	}))
	r.Route("/api/v1/auth/api-keys", func(r chi.Router) {
		r.Get("/", h.List)
		r.Post("/", h.Create)
		r.Post("/{id}/rotate", h.Rotate)
		r.Delete("/{id}", h.Revoke)
	})

	return &keyHandlerEnv{
		store: s, db: db, router: r,
		ownerID: ownerID, otherID: otherID, adminID: adminID,
		serverA: mkServer("alpha"), serverB: mkServer("beta"),
	}
}

// expire moves a key's expiry into the past, standing in for the clock running
// past a lifetime the store refuses to set directly.
func (e *keyHandlerEnv) expire(t *testing.T, keyID string) {
	t.Helper()
	if _, err := e.db.Exec(`UPDATE api_keys SET expires_at = ? WHERE id = ?`,
		time.Now().Add(-time.Hour).UTC(), keyID); err != nil {
		t.Fatal(err)
	}
}

func (e *keyHandlerEnv) jwt(t *testing.T, userID, role string) string {
	t.Helper()
	tok, err := auth.IssueAccessToken(keyTestSecret, userID, "u@example.com", role)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (e *keyHandlerEnv) call(t *testing.T, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	e.router.ServeHTTP(rr, req)
	return rr
}

func keyBody(serverIDs, scopes []string, extra map[string]any) map[string]any {
	body := map[string]any{
		"name":       "diagnosis agent",
		"server_ids": serverIDs,
		"scopes":     scopes,
		"expires_at": time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339),
		"password":   keyTestPassword,
	}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

// The happy path: the raw token appears exactly once, in the create response,
// and never again in any listing.
func TestCreateAccessKeyReturnsTheSecretOnce(t *testing.T) {
	e := newKeyHandlerEnv(t)
	token := e.jwt(t, e.ownerID, "user")

	rr := e.call(t, http.MethodPost, "/api/v1/auth/api-keys", token,
		keyBody([]string{e.serverA}, []string{"view", "files.read"}, nil))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var created struct {
		Key   store.APIKey `json:"key"`
		Token string       `json:"token"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Token, auth.AccessKeyPrefix) {
		t.Fatalf("token=%q lacks the reserved prefix", created.Token)
	}
	if strings.Join(created.Key.Scopes, ",") != "files.read,view" {
		t.Fatalf("scopes=%v want normalized", created.Key.Scopes)
	}
	if created.Key.ExpiresAt == nil {
		t.Fatal("a key must carry an expiry")
	}

	list := e.call(t, http.MethodGet, "/api/v1/auth/api-keys", token, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d", list.Code)
	}
	body := list.Body.String()
	if strings.Contains(body, created.Token) {
		t.Fatal("the raw token is re-exposed by the listing")
	}
	if strings.Contains(body, "token_hash") || strings.Contains(body, store.HashAccessKey(created.Token)) {
		t.Fatalf("the listing exposes the stored hash: %s", body)
	}
	if !strings.Contains(body, created.Key.TokenPrefix) {
		t.Fatal("the listing should show the non-secret display prefix")
	}
}

// Creating and rotating are step-up operations: password always, TOTP when the
// account has it. Every failure is the same generic 401.
func TestAccessKeyLifecycleRequiresReauthentication(t *testing.T) {
	e := newKeyHandlerEnv(t)
	token := e.jwt(t, e.ownerID, "user")
	create := func(extra map[string]any) *httptest.ResponseRecorder {
		return e.call(t, http.MethodPost, "/api/v1/auth/api-keys", token,
			keyBody([]string{e.serverA}, []string{"view"}, extra))
	}

	for _, tc := range []struct {
		name  string
		extra map[string]any
	}{
		{"no password", map[string]any{"password": ""}},
		{"wrong password", map[string]any{"password": "not the password"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := create(tc.extra)
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d want 401", rr.Code)
			}
			if !strings.Contains(rr.Body.String(), "reauthentication failed") {
				t.Fatalf("body=%s want a generic reauthentication failure", rr.Body.String())
			}
		})
	}

	// Turn MFA on for this account.
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := e.store.SetUserTOTPSecret(ctx, e.ownerID, secret); err != nil {
		t.Fatal(err)
	}
	if err := e.store.EnableUserTOTP(ctx, e.ownerID, []string{store.HashRecoveryCode("recovery-code")}); err != nil {
		t.Fatal(err)
	}

	if rr := create(nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("missing TOTP status=%d want 401", rr.Code)
	}
	if rr := create(map[string]any{"totp_code": "000000"}); rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong TOTP status=%d want 401", rr.Code)
	}
	// A recovery code is not a substitute for routine key issuance.
	if rr := create(map[string]any{"recovery_code": "recovery-code"}); rr.Code != http.StatusUnauthorized {
		t.Fatalf("recovery code status=%d want 401 — recovery codes must not issue keys", rr.Code)
	}

	rr := create(map[string]any{"totp_code": totpCodeFor(t, secret, time.Now())})
	if rr.Code != http.StatusCreated {
		t.Fatalf("valid TOTP status=%d body=%s", rr.Code, rr.Body.String())
	}

	// Rotation demands the same step-up.
	var created struct {
		Key store.APIKey `json:"key"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	rotatePath := "/api/v1/auth/api-keys/" + created.Key.ID + "/rotate"
	if bad := e.call(t, http.MethodPost, rotatePath, token, map[string]any{"password": "wrong"}); bad.Code != http.StatusUnauthorized {
		t.Fatalf("rotate without reauth status=%d want 401", bad.Code)
	}
}

// A key can only ever narrow its owner's authority.
func TestCreateAccessKeyCannotEscalate(t *testing.T) {
	e := newKeyHandlerEnv(t)
	ctx := context.Background()
	// The collaborator holds a narrow set on server A, and nothing on B.
	if err := e.store.SetServerPermissions(ctx, e.serverA, e.otherID, []string{"view", "power.restart"}); err != nil {
		t.Fatal(err)
	}
	token := e.jwt(t, e.otherID, "user")
	create := func(servers, scopes []string) *httptest.ResponseRecorder {
		return e.call(t, http.MethodPost, "/api/v1/auth/api-keys", token, keyBody(servers, scopes, nil))
	}

	for _, tc := range []struct {
		name    string
		servers []string
		scopes  []string
		want    int
	}{
		{"a scope they do not hold", []string{e.serverA}, []string{"console"}, http.StatusBadRequest},
		{"a wider group than they hold", []string{e.serverA}, []string{"power"}, http.StatusBadRequest},
		{"a server they cannot reach", []string{e.serverB}, []string{"view"}, http.StatusBadRequest},
		{"a server that does not exist", []string{"no-such-server"}, []string{"view"}, http.StatusBadRequest},
		{"the admin scope", []string{e.serverA}, []string{"admin"}, http.StatusBadRequest},
		{"no scopes at all", []string{e.serverA}, []string{}, http.StatusBadRequest},
		{"no servers at all", []string{}, []string{"view"}, http.StatusBadRequest},
		{"exactly what they hold", []string{e.serverA}, []string{"view", "power.restart"}, http.StatusCreated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := create(tc.servers, tc.scopes)
			if rr.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}

	// Missing and unreachable servers must be indistinguishable.
	missing := create([]string{"no-such-server"}, []string{"view"}).Body.String()
	unreachable := create([]string{e.serverB}, []string{"view"}).Body.String()
	if missing != unreachable {
		t.Fatalf("responses differ and leak server existence:\n%s\n%s", missing, unreachable)
	}
}

func TestCreateAccessKeyRejectsInvalidExpiry(t *testing.T) {
	e := newKeyHandlerEnv(t)
	token := e.jwt(t, e.ownerID, "user")

	for _, tc := range []struct {
		name   string
		expiry any
	}{
		{"missing", ""},
		{"not a timestamp", "next tuesday"},
		{"in the past", time.Now().Add(-time.Hour).Format(time.RFC3339)},
		{"beyond the 90 day cap", time.Now().Add(120 * 24 * time.Hour).Format(time.RFC3339)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := e.call(t, http.MethodPost, "/api/v1/auth/api-keys", token,
				keyBody([]string{e.serverA}, []string{"view"}, map[string]any{"expires_at": tc.expiry}))
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status=%d want 400 body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

// Keys are strictly per-owner: another user's key id is a 404, not a 403, so it
// cannot be used to probe which key ids exist.
func TestAccessKeysAreScopedToTheirOwner(t *testing.T) {
	e := newKeyHandlerEnv(t)
	ownerToken := e.jwt(t, e.ownerID, "user")

	rr := e.call(t, http.MethodPost, "/api/v1/auth/api-keys", ownerToken,
		keyBody([]string{e.serverA}, []string{"view"}, nil))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rr.Code, rr.Body.String())
	}
	var created struct {
		Key store.APIKey `json:"key"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	// A global admin is still not entitled to another user's credential.
	for _, actor := range []struct {
		name   string
		userID string
		role   string
	}{
		{"another user", e.otherID, "user"},
		{"a global admin", e.adminID, "admin"},
	} {
		t.Run(actor.name, func(t *testing.T) {
			token := e.jwt(t, actor.userID, actor.role)
			if got := e.call(t, http.MethodDelete, "/api/v1/auth/api-keys/"+created.Key.ID, token, nil).Code; got != http.StatusNotFound {
				t.Fatalf("revoke status=%d want 404", got)
			}
			rot := e.call(t, http.MethodPost, "/api/v1/auth/api-keys/"+created.Key.ID+"/rotate", token,
				map[string]any{"password": keyTestPassword})
			if rot.Code != http.StatusNotFound {
				t.Fatalf("rotate status=%d want 404", rot.Code)
			}
			listed := e.call(t, http.MethodGet, "/api/v1/auth/api-keys", token, nil)
			if strings.Contains(listed.Body.String(), created.Key.ID) {
				t.Fatalf("another user's key appeared in the listing: %s", listed.Body.String())
			}
		})
	}
}

func TestRevokeAccessKeyIsIdempotentAndAudited(t *testing.T) {
	e := newKeyHandlerEnv(t)
	token := e.jwt(t, e.ownerID, "user")

	rr := e.call(t, http.MethodPost, "/api/v1/auth/api-keys", token,
		keyBody([]string{e.serverA}, []string{"view"}, nil))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rr.Code, rr.Body.String())
	}
	var created struct {
		Key   store.APIKey `json:"key"`
		Token string       `json:"token"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	path := "/api/v1/auth/api-keys/" + created.Key.ID
	for i := 0; i < 2; i++ {
		if got := e.call(t, http.MethodDelete, path, token, nil).Code; got != http.StatusNoContent {
			t.Fatalf("revoke #%d status=%d want 204", i+1, got)
		}
	}
	if got := e.call(t, http.MethodDelete, "/api/v1/auth/api-keys/nope", token, nil).Code; got != http.StatusNotFound {
		t.Fatalf("unknown key status=%d want 404", got)
	}

	entries, err := e.store.ListAudit(context.Background(), "", 20)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		seen[entry.Action] = true
		if entry.Detail != nil && strings.Contains(*entry.Detail, created.Token) {
			t.Fatalf("audit detail leaked the raw token: %s", *entry.Detail)
		}
		if entry.Detail != nil && strings.Contains(*entry.Detail, created.Key.TokenPrefix) {
			t.Fatalf("audit detail leaked the token prefix: %s", *entry.Detail)
		}
	}
	if !seen["auth.api_key.create"] || !seen["auth.api_key.revoke"] {
		t.Fatalf("missing lifecycle audit events: %v", seen)
	}
}

// Rotation replaces the secret and keeps everything else, expiry included, so
// rotating an expired key would hand back a token that is already dead. It has
// to be refused, and the refusal has to point at the only real remedy.
func TestRotateRejectsAnExpiredKey(t *testing.T) {
	e := newKeyHandlerEnv(t)
	token := e.jwt(t, e.ownerID, "user")

	rr := e.call(t, http.MethodPost, "/api/v1/auth/api-keys", token,
		keyBody([]string{e.serverA}, []string{"view"}, nil))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rr.Code, rr.Body.String())
	}
	var created struct {
		Key store.APIKey `json:"key"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	// While it is live, rotation works — so the refusal below is about expiry
	// and not about anything else in the request.
	rotatePath := "/api/v1/auth/api-keys/" + created.Key.ID + "/rotate"
	if ok := e.call(t, http.MethodPost, rotatePath, token,
		map[string]any{"password": keyTestPassword}); ok.Code != http.StatusOK {
		t.Fatalf("live rotate status=%d body=%s", ok.Code, ok.Body.String())
	}

	e.expire(t, created.Key.ID)
	before, err := e.store.GetAccessKey(context.Background(), created.Key.ID, e.ownerID)
	if err != nil {
		t.Fatal(err)
	}

	expired := e.call(t, http.MethodPost, rotatePath, token, map[string]any{"password": keyTestPassword})
	if expired.Code != http.StatusConflict {
		t.Fatalf("expired rotate status=%d want 409 body=%s", expired.Code, expired.Body.String())
	}
	body := expired.Body.String()
	if !strings.Contains(body, "expired") || !strings.Contains(body, "create a new one") {
		t.Fatalf("body=%s want a message that says to create a new key", body)
	}
	if strings.Contains(body, auth.AccessKeyPrefix) {
		t.Fatalf("a refused rotation returned credential material: %s", body)
	}

	// The refusal changed nothing: same secret, same expiry.
	after, err := e.store.GetAccessKey(context.Background(), created.Key.ID, e.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TokenPrefix != before.TokenPrefix {
		t.Fatal("a refused rotation still replaced the secret")
	}
	if after.ExpiresAt == nil || !after.ExpiresAt.Equal(*before.ExpiresAt) {
		t.Fatalf("expiry moved from %v to %v", before.ExpiresAt, after.ExpiresAt)
	}

	// Revoking an expired key is still allowed — retiring it is the one thing
	// left to do with it.
	if got := e.call(t, http.MethodDelete, "/api/v1/auth/api-keys/"+created.Key.ID, token, nil).Code; got != http.StatusNoContent {
		t.Fatalf("revoke of an expired key status=%d want 204", got)
	}
}

// An access key must never be able to read, mint, or rotate a credential — not
// even its own. The router puts a boundary in front of these routes; the
// handler-level guard is the second, independent statement of the same rule.
func TestAccessKeysCannotUseTheLifecycleRoutes(t *testing.T) {
	e := newKeyHandlerEnv(t)
	key, token, err := e.store.CreateAccessKey(context.Background(), e.ownerID, "agent",
		[]string{"view"}, []string{e.serverA}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	// Mounted here without the machine boundary, so what refuses these calls is
	// the handler's own currentUserID/machine check plus requireHuman upstream.
	// List is the disclosure risk; create and rotate are the escalation risk.
	if got := e.call(t, http.MethodPost, "/api/v1/auth/api-keys", token,
		keyBody([]string{e.serverA}, []string{"view"}, nil)).Code; got == http.StatusCreated {
		t.Fatal("an access key minted another access key")
	}
	if got := e.call(t, http.MethodPost, "/api/v1/auth/api-keys/"+key.ID+"/rotate", token,
		map[string]any{"password": keyTestPassword}).Code; got == http.StatusOK {
		t.Fatal("an access key rotated itself")
	}
}

// totpCodeFor computes the code an authenticator app would show, so the test
// can exercise the real TOTP step-up rather than stubbing it out. RFC 6238 with
// the same 6 digits / 30s period the auth package uses.
func totpCodeFor(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		t.Fatal(err)
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(at.Unix())/30)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[off]&0x7f) << 24) | (uint32(sum[off+1]) << 16) |
		(uint32(sum[off+2]) << 8) | uint32(sum[off+3])
	return fmt.Sprintf("%06d", value%1000000)
}

// Step-up reauthentication is password guessing against an already-authenticated
// session, so repeated failures have to lock out rather than run unbounded.
func TestAccessKeyReauthenticationIsThrottled(t *testing.T) {
	e := newKeyHandlerEnv(t)
	token := e.jwt(t, e.ownerID, "user")

	var locked bool
	for i := 0; i < 12; i++ {
		rr := e.call(t, http.MethodPost, "/api/v1/auth/api-keys", token,
			keyBody([]string{e.serverA}, []string{"view"}, map[string]any{"password": "wrong"}))
		if rr.Code == http.StatusTooManyRequests {
			if rr.Header().Get("Retry-After") == "" {
				t.Fatal("a lockout should tell the caller when to retry")
			}
			locked = true
			break
		}
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status=%d want 401 or 429", i+1, rr.Code)
		}
	}
	if !locked {
		t.Fatal("unlimited password guesses were allowed against the key lifecycle")
	}

	// And the correct password does not slip through while locked out.
	rr := e.call(t, http.MethodPost, "/api/v1/auth/api-keys", token,
		keyBody([]string{e.serverA}, []string{"view"}, nil))
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d want 429 while locked out", rr.Code)
	}
}
