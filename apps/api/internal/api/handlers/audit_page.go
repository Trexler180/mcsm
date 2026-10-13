package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

// auditPageEntry is one row of the audit log page. actor_email is resolved for
// admins, and for anyone's own rows; other people stay ids.
type auditPageEntry struct {
	*store.AuditEntry
	ActorEmail string `json:"actor_email,omitempty"`
}

type auditPage struct {
	Entries []auditPageEntry `json:"entries"`
	// NextCursor fetches the next (older) page as ?before=<cursor>; absent on
	// the last page.
	NextCursor int64 `json:"next_cursor,omitempty"`
}

// Page lists the audit log newest first, with filters and a cursor:
//
//	?server_id=&user_id=&action=<prefix>&since=<RFC 3339>&until=<RFC 3339>
//	&before=<cursor>&limit=<1-500>
//
// Admins see everything. Everyone else sees rows about servers they can view
// plus their own panel-wide actions; a filter only narrows that.
func (h *AuditHandlers) Page(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := auth.ClaimsFrom(ctx)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	isAdmin, err := callerIsAdmin(r, h.store)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	qv := r.URL.Query()
	q := store.AuditQuery{
		ServerID:     qv.Get("server_id"),
		UserID:       qv.Get("user_id"),
		ActionPrefix: strings.TrimSpace(qv.Get("action")),
	}
	if v := qv.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			writeFieldError(w, "limit", "limit must be between 1 and 500")
			return
		}
		q.Limit = n
	}
	if v := qv.Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			writeFieldError(w, "before", "before must be a cursor from a previous page")
			return
		}
		q.Before = n
	}
	for _, f := range []struct {
		name string
		dst  *time.Time
	}{{"since", &q.Since}, {"until", &q.Until}} {
		if v := qv.Get(f.name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				writeFieldError(w, f.name, f.name+" must be an RFC 3339 time")
				return
			}
			*f.dst = t
		}
	}

	if !isAdmin {
		servers, err := h.store.ListServersForUser(ctx, claims.UserID)
		if err != nil {
			writeServerError(w, r, "audit page: servers", err)
			return
		}
		ids := make([]string, 0, len(servers))
		for _, s := range servers {
			ids = append(ids, s.ID)
		}
		q.Scope = &store.AuditScope{ServerIDs: ids, UserID: claims.UserID}
	}

	entries, next, err := h.store.ListAuditPage(ctx, q)
	if err != nil {
		writeServerError(w, r, "audit page", err)
		return
	}

	emails := map[string]string{}
	if isAdmin {
		if users, err := h.store.ListUsers(ctx); err == nil {
			for _, u := range users {
				emails[u.ID] = u.Email
			}
		}
	} else if me, err := h.store.GetUserByID(ctx, claims.UserID); err == nil {
		emails[me.ID] = me.Email
	}
	out := auditPage{Entries: make([]auditPageEntry, 0, len(entries)), NextCursor: next}
	for _, e := range entries {
		pe := auditPageEntry{AuditEntry: e}
		if e.UserID != nil {
			pe.ActorEmail = emails[*e.UserID]
		}
		out.Entries = append(out.Entries, pe)
	}
	writeJSON(w, http.StatusOK, out)
}
