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
}

func NewMFAHandlers(s *store.Store) *MFAHandlers {
	return &MFAHandlers{store: s}
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
// enabled until Enable confirms a code. Re-running Setup before enabling rotates
// the pending secret, which is fine; an already-enabled account must Disable
// first so an attacker on a live session can't silently re-key the factor.
func (h *MFAHandlers) Setup(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	cfg, err := h.store.GetUserTOTP(r.Context(), uid)
	if err != nil {
		writeServerError(w, r, "mfa setup lookup", err)
		return
	}
	if cfg.Enabled {
		writeError(w, http.StatusConflict, "MFA is already enabled; disable it first to re-enroll")
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
		"secret":       secret,
		"otpauth_url":  auth.TOTPProvisioningURI(secret, mfaIssuer(), user.Email),
	})
}

// Enable verifies a code against the pending secret, turns MFA on, and returns
// one-time recovery codes (shown once; only their hashes are stored).
func (h *MFAHandlers) Enable(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
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

	codes, err := auth.GenerateRecoveryCodes(recoveryCodeCount)
	if err != nil {
		writeServerError(w, r, "mfa recovery gen", err)
		return
	}
	hashes := make([]string, len(codes))
	for i, c := range codes {
		hashes[i] = store.HashRecoveryCode(auth.NormalizeRecoveryCode(c))
	}
	if err := h.store.EnableUserTOTP(r.Context(), uid, hashes); err != nil {
		writeServerError(w, r, "mfa enable", err)
		return
	}
	audit(h.store, r, "", "auth.mfa_enabled", nil)
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":        true,
		"recovery_codes": codes,
	})
}

// Disable turns MFA off, requiring a current TOTP or recovery code so a hijacked
// session can't strip the second factor without possessing it.
func (h *MFAHandlers) Disable(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	var body struct {
		Code         string `json:"code"`
		RecoveryCode string `json:"recovery_code"`
	}
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

	ok := false
	switch {
	case body.Code != "":
		ok = auth.ValidateTOTP(cfg.Secret, body.Code, time.Now())
	case body.RecoveryCode != "":
		consumed, err := h.store.ConsumeRecoveryCode(r.Context(), uid, auth.NormalizeRecoveryCode(body.RecoveryCode))
		if err != nil {
			writeServerError(w, r, "mfa disable recovery", err)
			return
		}
		ok = consumed
	}
	if !ok {
		writeError(w, http.StatusBadRequest, "a valid authentication or recovery code is required to disable MFA")
		return
	}
	if err := h.store.DisableUserTOTP(r.Context(), uid); err != nil {
		writeServerError(w, r, "mfa disable", err)
		return
	}
	audit(h.store, r, "", "auth.mfa_disabled", nil)
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": false})
}
