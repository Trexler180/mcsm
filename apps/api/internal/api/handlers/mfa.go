package handlers

import (
	"net/http"
	"os"
	"time"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

type MFAHandlers struct {
	store *store.Store
	// Every MFA change re-proves the password, and spends from the same guess
	// budget as login and the other step-ups.
	ipThrottle   *auth.LoginThrottle
	acctThrottle *auth.LoginThrottle
}

// NewMFAHandlers builds the MFA handlers. pw is the shared password-guess
// budget; nil gives this handler a private one.
func NewMFAHandlers(s *store.Store, pw *PasswordThrottles) *MFAHandlers {
	pw = pw.orNew()
	return &MFAHandlers{store: s, ipThrottle: pw.IP, acctThrottle: pw.Account}
}

type mfaStepUp struct {
	Password     string `json:"password"`
	Code         string `json:"code"`
	RecoveryCode string `json:"recovery_code"`
}

// mfaIssuer is the label authenticator apps show next to the account. Override
// with APP_NAME so multiple deployments are distinguishable in one app.
func mfaIssuer() string {
	if v := os.Getenv("APP_NAME"); v != "" {
		return v
	}
	return "ServerManager"
}

// Status reports whether the caller has MFA enabled.
func (h *MFAHandlers) Status(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	cfg, err := h.store.GetUserTOTP(r.Context(), uid)
	if err != nil {
		writeServerError(w, r, "mfa status", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": cfg.Enabled})
}

// Setup generates a fresh (not-yet-active) TOTP secret and returns the secret +
// provisioning URI for the user to add to their authenticator. MFA is not
// enabled until Enable confirms a code. It needs the current password: without
// it, someone holding a stolen session could enrol their own authenticator and
// lock the real owner out. An already-enabled account must Disable first.
func (h *MFAHandlers) Setup(w http.ResponseWriter, r *http.Request) {
	uid := humanCaller(w, r)
	if uid == "" {
		return
	}
	var body mfaStepUp
	_ = decode(r, &body)
	cfg, err := h.store.GetUserTOTP(r.Context(), uid)
	if err != nil {
		writeServerError(w, r, "mfa setup lookup", err)
		return
	}
	if cfg.Enabled {
		writeError(w, http.StatusConflict, "MFA is already enabled; disable it first to re-enroll")
		return
	}
	if !stepUp(w, r, h.store, h.ipThrottle, h.acctThrottle, uid, body.Password, "", "", "mfa setup") {
		return
	}
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		writeServerError(w, r, "mfa secret", err)
		return
	}
	if err := h.store.SetUserTOTPSecret(r.Context(), uid, secret); err != nil {
		writeServerError(w, r, "mfa store secret", err)
		return
	}
	user, err := h.store.GetUserByID(r.Context(), uid)
	if err != nil {
		writeServerError(w, r, "mfa user", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"secret":      secret,
		"otpauth_url": auth.TOTPProvisioningURI(secret, mfaIssuer(), user.Email),
	})
}

// Enable verifies a code against the pending secret, turns MFA on, and returns
// one-time recovery codes (shown once; only their hashes are stored). Every
// other session ends: they were signed in without the factor now required.
func (h *MFAHandlers) Enable(w http.ResponseWriter, r *http.Request) {
	uid := humanCaller(w, r)
	if uid == "" {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := decode(r, &body); err != nil || body.Code == "" {
		writeError(w, http.StatusBadRequest, "code required")
		return
	}
	cfg, err := h.store.GetUserTOTP(r.Context(), uid)
	if err != nil {
		writeServerError(w, r, "mfa enable lookup", err)
		return
	}
	if cfg.Enabled {
		writeError(w, http.StatusConflict, "MFA already enabled")
		return
	}
	if cfg.Secret == "" {
		writeError(w, http.StatusBadRequest, "run setup first")
		return
	}
	if !auth.ValidateTOTP(cfg.Secret, body.Code, time.Now()) {
		writeError(w, http.StatusBadRequest, "invalid code")
		return
	}

	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		writeServerError(w, r, "mfa recovery gen", err)
		return
	}
	if err := h.store.EnableUserTOTP(r.Context(), uid, hashes); err != nil {
		writeServerError(w, r, "mfa enable", err)
		return
	}
	_ = h.store.DeleteSessionsForUserExcept(r.Context(), uid, currentSessionID(r))
	audit(h.store, r, "", "auth.mfa_enabled", nil)
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":        true,
		"recovery_codes": codes,
	})
}

// Disable turns MFA off. It needs the current password and a current TOTP or
// recovery code, from the shared guess budget, so a hijacked session can
// neither strip the factor without possessing it nor grind through codes.
func (h *MFAHandlers) Disable(w http.ResponseWriter, r *http.Request) {
	uid := humanCaller(w, r)
	if uid == "" {
		return
	}
	var body mfaStepUp
	_ = decode(r, &body)

	cfg, err := h.store.GetUserTOTP(r.Context(), uid)
	if err != nil {
		writeServerError(w, r, "mfa disable lookup", err)
		return
	}
	if !cfg.Enabled {
		writeJSON(w, http.StatusOK, map[string]bool{"enabled": false})
		return
	}
	if body.Code == "" && body.RecoveryCode == "" {
		writeError(w, http.StatusBadRequest, "a valid authentication or recovery code is required to disable MFA")
		return
	}
	if !stepUp(w, r, h.store, h.ipThrottle, h.acctThrottle, uid, body.Password, body.Code, body.RecoveryCode, "mfa disable") {
		return
	}
	if err := h.store.DisableUserTOTP(r.Context(), uid); err != nil {
		writeServerError(w, r, "mfa disable", err)
		return
	}
	_ = h.store.DeleteSessionsForUserExcept(r.Context(), uid, currentSessionID(r))
	audit(h.store, r, "", "auth.mfa_disabled", nil)
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": false})
}

// RegenerateRecoveryCodes replaces every recovery code with a fresh set, after
// the full step-up. The old codes stop working at once.
func (h *MFAHandlers) RegenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	uid := humanCaller(w, r)
	if uid == "" {
		return
	}
	var body mfaStepUp
	_ = decode(r, &body)
	cfg, err := h.store.GetUserTOTP(r.Context(), uid)
	if err != nil {
		writeServerError(w, r, "mfa regenerate lookup", err)
		return
	}
	if !cfg.Enabled {
		writeError(w, http.StatusConflict, "MFA is not enabled")
		return
	}
	if !stepUp(w, r, h.store, h.ipThrottle, h.acctThrottle, uid, body.Password, body.Code, "", "mfa regenerate") {
		return
	}
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		writeServerError(w, r, "mfa recovery gen", err)
		return
	}
	if err := h.store.ReplaceRecoveryCodes(r.Context(), uid, hashes); err != nil {
		writeServerError(w, r, "mfa regenerate", err)
		return
	}
	audit(h.store, r, "", "auth.mfa_recovery_regenerated", nil)
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

func newRecoveryCodes() ([]string, []string, error) {
	codes, err := auth.GenerateRecoveryCodes(recoveryCodeCount)
	if err != nil {
		return nil, nil, err
	}
	hashes := make([]string, len(codes))
	for i, c := range codes {
		hashes[i] = store.HashRecoveryCode(auth.NormalizeRecoveryCode(c))
	}
	return codes, hashes, nil
}
