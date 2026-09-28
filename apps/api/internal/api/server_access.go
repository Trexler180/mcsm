package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

// serverGate is one route's access requirement. Leaf gates need the exact
// permission (or its parent group); group gates are satisfied by the group or
// any leaf beneath it, which is what read/list routes want.
//
// The same struct answers the question twice per machine request — once against
// the key's scopes, once against the owner's live permissions — so the two can
// never drift into different semantics.
type serverGate struct {
	permission store.ServerPermission
	group      bool
}

// scopeSatisfied evaluates a key's own scopes. Keys can never hold `admin`
// (the store refuses to store it), so HasServerPermission's admin shortcut is
// unreachable here and no key-side bypass exists.
func (g serverGate) scopeSatisfied(scopes []string) bool {
	if g.group {
		return store.HasServerGroupAccess(scopes, g.permission)
	}
	return store.HasServerPermission(scopes, g.permission)
}

func (g serverGate) userSatisfied(r *http.Request, s *store.Store, userID, serverID string) (bool, error) {
	if g.group {
		return s.UserHasServerGroupAccess(r.Context(), userID, serverID, g.permission)
	}
	return s.UserHasServerPermission(r.Context(), userID, serverID, g.permission)
}

func requireServerAccess(s *store.Store) func(http.Handler) http.Handler {
	return requireServerPermission(s, store.ServerPermissionView)
}

// requireServerPermission gates a route on a specific permission (group or
// leaf), satisfied by the leaf, its parent group, or admin.
func requireServerPermission(s *store.Store, permission store.ServerPermission) func(http.Handler) http.Handler {
	return serverAccessGate(s, serverGate{permission: permission})
}

// requireServerGroupAccess gates a read/list route on any access within a
// group — holding the group or any of its leaves is enough.
func requireServerGroupAccess(s *store.Store, group store.ServerPermission) func(http.Handler) http.Handler {
	return serverAccessGate(s, serverGate{permission: group, group: true})
}

// serverAccessGate centralizes the claims/admin-bypass/server-id boilerplate so
// each permission middleware only supplies its requirement. The global admin
// role is read fresh from the DB so a demotion takes effect immediately.
//
// For a machine principal the decision is an intersection, evaluated in this
// order:
//
//	allowed server ∩ key scope ∩ (owner admin OR owner's live permission)
//
// The key's own terms come first and have no bypass. That ordering is the whole
// point: an admin-owned key that lists one server and one scope can still only
// do that one thing, and a later demotion or membership change still lands on
// the next request through the unchanged user term.
func serverAccessGate(s *store.Store, gate serverGate) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := auth.ClaimsFrom(r.Context())
			if claims == nil {
				writeJSONError(w, http.StatusUnauthorized, "unauthorized")
				return
			}

			serverID := chi.URLParam(r, "id")

			// Key bounds are checked before anything about the user, including
			// the admin bypass below.
			if machine := auth.MachineFrom(r.Context()); machine != nil {
				if serverID == "" {
					writeJSONError(w, http.StatusBadRequest, "server id required")
					return
				}
				if !machine.AllowsServer(serverID) || !gate.scopeSatisfied(machine.Scopes) {
					// Same 403 for "not in your allowlist" and "not in your
					// scopes" — a key learns nothing about servers it can't use.
					writeJSONError(w, http.StatusForbidden, "forbidden")
					return
				}
			}

			user, err := s.GetUserByID(r.Context(), claims.UserID)
			if err != nil {
				writeJSONError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			if user.Role == "admin" {
				next.ServeHTTP(w, r)
				return
			}

			if serverID == "" {
				writeJSONError(w, http.StatusBadRequest, "server id required")
				return
			}

			ok, err := gate.userSatisfied(r, s, claims.UserID, serverID)
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "authorization check failed")
				return
			}
			if !ok {
				writeJSONError(w, http.StatusForbidden, "forbidden")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requireAdmin gates a route on the global admin role, read fresh from the DB on
// every request. Unlike auth.AdminOnly (which trusts the role baked into the
// 15-minute access token), this makes a demotion or deletion take effect
// immediately, closing the window where a just-revoked admin keeps admin access.
//
// A machine principal never satisfies it, whoever owns the key — and that means
// either family, an access key or an MCP grant. This is what keeps server
// creation, import, clone, deletion, and membership administration out of reach
// of automation.
func requireAdmin(s *store.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if auth.IsMachine(r.Context()) {
				writeJSONError(w, http.StatusForbidden, "forbidden")
				return
			}
			claims := auth.ClaimsFrom(r.Context())
			if claims == nil {
				writeJSONError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			user, err := s.GetUserByID(r.Context(), claims.UserID)
			if err != nil || user.Role != "admin" {
				writeJSONError(w, http.StatusForbidden, "forbidden")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// machineRoutePrefix is the only route family an access key may reach. Anything
// else — authentication, tickets, sessions, users, nodes, integration secrets,
// notifications, the global audit log, and key management itself — is closed to
// machine principals, so a stolen key cannot administer the panel or mint a
// second credential.
const machineRoutePrefix = "/api/v1/servers"

// machineBoundary rejects a machine principal outside the server API family. It
// runs immediately after authentication, ahead of every route, so a new route
// added anywhere else is closed to keys by default rather than by remembering.
func machineBoundary(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.MachineFrom(r.Context()) != nil && !isServerRoute(r.URL.Path) {
			writeJSONError(w, http.StatusForbidden, "access keys may only be used on server routes")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isServerRoute(path string) bool {
	// Exact "/api/v1/servers" (the list) or anything beneath it. The trailing
	// separator matters: "/api/v1/servers-secret" must not match.
	return path == machineRoutePrefix || strings.HasPrefix(path, machineRoutePrefix+"/")
}

// requireHuman rejects machine principals outright — access keys and MCP grants
// alike. The route boundary already keeps keys away from these paths; this is
// the second, local statement of the same rule for the credential-lifecycle and
// delegation routes, where being wrong would let a credential mint or approve
// its own successor.
func requireHuman(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.IsMachine(r.Context()) {
			writeJSONError(w, http.StatusForbidden, "this route requires an interactive sign-in")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + msg + `"}`))
}
