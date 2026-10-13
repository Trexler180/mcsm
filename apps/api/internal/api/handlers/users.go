package handlers

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/notify"
	"github.com/mcsm/api/internal/store"
)

// validRoles are the global roles. "admin" administers the panel; "user" acts
// only through per-server grants. There is deliberately no third global role:
// operator and viewer are per-server permission presets (see
// GET /permissions/presets), not account types.
var validRoles = map[string]bool{"admin": true, "user": true}

// minPasswordLength is the floor for any password set through the API. Short of
// a full policy engine, a length minimum is the single most effective control
// against trivially guessable credentials on a publicly reachable panel.
const minPasswordLength = 10

// maxPasswordLength bounds what bcrypt is asked to hash. bcrypt only reads the
// first 72 bytes, so anything longer would silently be a shorter password.
const maxPasswordLength = 72

const maxDisplayNameLength = 80

// validatePassword enforces the minimum password policy, returning a
// user-facing message when the password is unacceptable.
func validatePassword(pw string) (string, bool) {
	if len(pw) < minPasswordLength {
		return fmt.Sprintf("password must be at least %d characters", minPasswordLength), false
	}
	if len(pw) > maxPasswordLength {
		return fmt.Sprintf("password must be at most %d bytes", maxPasswordLength), false
	}
	return "", true
}

// validateEmail normalizes and checks an address.
func validateEmail(raw string) (string, error) {
	email := store.NormalizeEmail(raw)
	if email == "" {
		return "", errors.New("email is required")
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 254 {
		return "", errors.New("email is not a valid address")
	}
	return email, nil
}

type UserHandlers struct {
	store     *store.Store
	jwtSecret string
}

func NewUserHandlers(s *store.Store, jwtSecret string) *UserHandlers {
	return &UserHandlers{store: s, jwtSecret: jwtSecret}
}

func (h *UserHandlers) List(w http.ResponseWriter, r *http.Request) {
	users, err := h.store.ListUsers(r.Context())
	if err != nil {
		writeServerError(w, r, "list users", err)
		return
	}
	if users == nil {
		users = []*store.User{}
	}
	writeJSON(w, http.StatusOK, users)
}

func (h *UserHandlers) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email       string  `json:"email"`
		Password    string  `json:"password"`
		Role        string  `json:"role"`
		DisplayName *string `json:"display_name"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	email, err := validateEmail(body.Email)
	if err != nil {
		writeFieldError(w, "email", err.Error())
		return
	}
	if msg, ok := validatePassword(body.Password); !ok {
		writeFieldError(w, "password", msg)
		return
	}
	if body.Role == "" {
		body.Role = "user"
	}
	if !validRoles[body.Role] {
		writeFieldError(w, "role", "role must be admin or user")
		return
	}
	displayName, err := cleanDisplayName(body.DisplayName)
	if err != nil {
		writeFieldError(w, "display_name", err.Error())
		return
	}

	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeServerError(w, r, "create user: hash", err)
		return
	}
	user, err := h.store.CreateUser(r.Context(), email, hash, body.Role)
	if errors.Is(err, store.ErrEmailTaken) {
		writeCodedError(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	if err != nil {
		writeServerError(w, r, "create user", err)
		return
	}
	if displayName != nil {
		if _, err := h.store.UpdateUserAdmin(r.Context(), user.ID, store.UserAdminUpdate{DisplayName: displayName}); err == nil {
			user.DisplayName = body.DisplayName
		}
	}
	// Best-effort: a missing default subscription costs the user some alerts they
	// can still switch on themselves, which is not worth failing account creation
	// and leaving them with no account at all.
	if err := h.store.SeedDefaultSubscriptions(r.Context(), user.ID, notify.DefaultOnTypes()); err != nil {
		log.Printf("seed default subscriptions for %s: %v", user.ID, err)
	}
	audit(h.store, r, "", "user.create", map[string]any{"user_id": user.ID, "email": user.Email, "role": user.Role})
	writeJSON(w, http.StatusCreated, user)
}

// cleanDisplayName trims a display name; an explicit empty string clears it.
func cleanDisplayName(p *string) (*string, error) {
	if p == nil {
		return nil, nil
	}
	v := strings.TrimSpace(*p)
	if len(v) > maxDisplayNameLength {
		return nil, fmt.Errorf("display name must be at most %d characters", maxDisplayNameLength)
	}
	return &v, nil
}

func (h *UserHandlers) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := h.store.GetUserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	var body struct {
		DisplayName *string `json:"display_name"`
		Role        *string `json:"role"`
		Password    string  `json:"password"`
		// DisableMFA lets an admin clear a locked-out user's second factor (e.g.
		// lost authenticator with no recovery codes left). This route is admin-only.
		DisableMFA bool `json:"disable_mfa"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	// Validate everything before changing anything, so a request either applies
	// whole or not at all.
	upd := store.UserAdminUpdate{ResetMFA: body.DisableMFA}
	if upd.DisplayName, err = cleanDisplayName(body.DisplayName); err != nil {
		writeFieldError(w, "display_name", err.Error())
		return
	}
	if body.Role != nil && *body.Role != "" {
		if !validRoles[*body.Role] {
			writeFieldError(w, "role", "role must be admin or user")
			return
		}
		// Guard against an admin locking themselves out by self-demotion.
		if id == currentUserID(r) && *body.Role != "admin" {
			writeFieldError(w, "role", "you cannot change your own admin role")
			return
		}
		upd.Role = body.Role
	}
	if body.Password != "" {
		if msg, ok := validatePassword(body.Password); !ok {
			writeFieldError(w, "password", msg)
			return
		}
		hash, err := auth.HashPassword(body.Password)
		if err != nil {
			writeServerError(w, r, "update user: hash", err)
			return
		}
		upd.PasswordHash = &hash
	}

	ended, err := h.store.UpdateUserAdmin(r.Context(), id, upd)
	if errors.Is(err, store.ErrLastAdmin) {
		writeFieldError(w, "role", "the panel needs at least one administrator")
		return
	}
	if err != nil {
		writeServerError(w, r, "update user", err)
		return
	}

	changes := map[string]any{}
	if upd.DisplayName != nil {
		changes["display_name"] = *upd.DisplayName
	}
	if upd.Role != nil && *upd.Role != existing.Role {
		changes["role"] = map[string]string{"from": existing.Role, "to": *upd.Role}
	}
	if upd.PasswordHash != nil {
		changes["password"] = "reset"
	}
	if upd.ResetMFA {
		changes["mfa"] = "reset"
	}
	if len(changes) > 0 {
		audit(h.store, r, "", "user.update", map[string]any{
			"user_id": id, "email": existing.Email, "changes": changes, "sessions_ended": ended,
		})
	}

	updated, err := h.store.GetUserByID(r.Context(), id)
	if err != nil {
		writeServerError(w, r, "update user: reread", err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *UserHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == currentUserID(r) {
		writeError(w, http.StatusBadRequest, "you cannot delete your own account")
		return
	}
	target, err := h.store.GetUserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if target.Role == "admin" {
		if n, err := h.store.CountAdmins(r.Context()); err == nil && n <= 1 {
			writeError(w, http.StatusConflict, "the panel needs at least one administrator")
			return
		}
	}
	if err := h.store.DeleteUser(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrUserOwnsServers) {
			writeError(w, http.StatusConflict, "this user still owns one or more servers; reassign or delete those servers before removing the user")
			return
		}
		writeServerError(w, r, "delete user", err)
		return
	}
	audit(h.store, r, "", "user.delete", map[string]any{"user_id": id, "email": target.Email, "role": target.Role})
	w.WriteHeader(http.StatusNoContent)
}
