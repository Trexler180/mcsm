package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/api/ws"
	"github.com/mcsm/api/internal/notify"
	"github.com/mcsm/api/internal/safedial"
	"github.com/mcsm/api/internal/store"
)

var errInvalidChannel = errors.New("invalid or unknown channel")

// validateWebhookURL rejects non-http(s)/hostless URLs at config time. The
// post-DNS SSRF guard in safedial still applies at delivery time, defeating DNS
// rebinding that a config-time check alone would miss.
func validateWebhookURL(raw string) error {
	if err := safedial.ValidateHTTPURL(raw); err != nil {
		return errors.New("webhook url must be a valid http(s) URL")
	}
	return nil
}

type NotificationHandlers struct {
	store   *store.Store
	service *notify.Service
}

func NewNotificationHandlers(s *store.Store, service *notify.Service) *NotificationHandlers {
	return &NotificationHandlers{store: s, service: service}
}

// ── Event catalog ────────────────────────────────────────────────

func (h *NotificationHandlers) Events(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, notify.Catalog)
}

// ── Subscriptions ────────────────────────────────────────────────

func (h *NotificationHandlers) ListSubscriptions(w http.ResponseWriter, r *http.Request) {
	subs, err := h.store.ListSubscriptions(r.Context(), currentUserID(r))
	if err != nil {
		writeServerError(w, r, "list subscriptions", err)
		return
	}
	writeJSON(w, http.StatusOK, subs)
}

func (h *NotificationHandlers) UpsertSubscription(w http.ResponseWriter, r *http.Request) {
	var body struct {
		EventType   string   `json:"event_type"`
		ServerID    *string  `json:"server_id"`
		MinSeverity string   `json:"min_severity"`
		Channels    []string `json:"channels"`
		Enabled     bool     `json:"enabled"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if _, ok := notify.DefByType(body.EventType); !ok {
		writeError(w, http.StatusBadRequest, "unknown event type")
		return
	}
	if !validSeverity(body.MinSeverity) {
		writeError(w, http.StatusBadRequest, "invalid severity")
		return
	}
	userID := currentUserID(r)

	// A server-scoped subscription is only allowed for a server the caller can
	// see, and only for server-scoped events.
	if body.ServerID != nil && *body.ServerID != "" {
		def, _ := notify.DefByType(body.EventType)
		if def.Scope != notify.ScopeServer {
			writeError(w, http.StatusBadRequest, "event type is not server-scoped")
			return
		}
		ok, err := h.canSeeServer(r, userID, *body.ServerID)
		if err != nil {
			writeServerError(w, r, "permission check", err)
			return
		}
		if !ok {
			writeError(w, http.StatusForbidden, "no access to that server")
			return
		}
	} else {
		body.ServerID = nil
	}

	if err := validateChannelSelectors(r, h.store, userID, body.Channels); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	sub, err := h.store.UpsertSubscription(r.Context(), &store.NotificationSubscription{
		UserID:      userID,
		EventType:   body.EventType,
		ServerID:    body.ServerID,
		MinSeverity: body.MinSeverity,
		Channels:    body.Channels,
		Enabled:     body.Enabled,
	})
	if err != nil {
		writeServerError(w, r, "upsert subscription", err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

func (h *NotificationHandlers) DeleteSubscription(w http.ResponseWriter, r *http.Request) {
	if err := h.store.DeleteSubscription(r.Context(), currentUserID(r), chi.URLParam(r, "id")); err != nil {
		writeServerError(w, r, "delete subscription", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Channels (webhooks) ──────────────────────────────────────────

func (h *NotificationHandlers) ListChannels(w http.ResponseWriter, r *http.Request) {
	chans, err := h.store.ListChannels(r.Context(), currentUserID(r))
	if err != nil {
		writeServerError(w, r, "list channels", err)
		return
	}
	writeJSON(w, http.StatusOK, chans)
}

func (h *NotificationHandlers) CreateChannel(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeChannelBody(w, r)
	if !ok {
		return
	}
	userID := currentUserID(r)
	ch, err := h.store.CreateChannel(r.Context(), &store.NotificationChannel{
		UserID: userID,
		Kind:   "webhook",
		Label:  body.Label,
		Config: store.NotificationChannelConfig{URL: body.URL, Format: body.Format, SecretSet: body.Secret != ""},
		Enabled: true,
	})
	if err != nil {
		writeServerError(w, r, "create channel", err)
		return
	}
	if body.Secret != "" {
		if err := h.store.SetSecret(r.Context(), notify.WebhookSecretKey(ch.ID), body.Secret, userID); err != nil {
			writeServerError(w, r, "store channel secret", err)
			return
		}
	}
	audit(h.store, r, "", "notification.channel.create", map[string]string{"id": ch.ID})
	writeJSON(w, http.StatusOK, ch)
}

func (h *NotificationHandlers) UpdateChannel(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeChannelBody(w, r)
	if !ok {
		return
	}
	userID := currentUserID(r)
	id := chi.URLParam(r, "id")
	existing, err := h.ownedChannel(r, userID, id)
	if err != nil || existing == nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	secretSet := existing.Config.SecretSet
	if body.Secret != "" {
		if err := h.store.SetSecret(r.Context(), notify.WebhookSecretKey(id), body.Secret, userID); err != nil {
			writeServerError(w, r, "store channel secret", err)
			return
		}
		secretSet = true
	}
	existing.Label = body.Label
	existing.Config = store.NotificationChannelConfig{URL: body.URL, Format: body.Format, SecretSet: secretSet}
	existing.Enabled = body.Enabled
	if err := h.store.UpdateChannel(r.Context(), existing); err != nil {
		writeServerError(w, r, "update channel", err)
		return
	}
	writeJSON(w, http.StatusOK, existing)
}

func (h *NotificationHandlers) DeleteChannel(w http.ResponseWriter, r *http.Request) {
	userID := currentUserID(r)
	id := chi.URLParam(r, "id")
	if err := h.store.DeleteChannel(r.Context(), userID, id); err != nil {
		writeServerError(w, r, "delete channel", err)
		return
	}
	_ = h.store.DeleteSecret(r.Context(), notify.WebhookSecretKey(id))
	audit(h.store, r, "", "notification.channel.delete", map[string]string{"id": id})
	w.WriteHeader(http.StatusNoContent)
}

func (h *NotificationHandlers) TestChannel(w http.ResponseWriter, r *http.Request) {
	ch, err := h.ownedChannel(r, currentUserID(r), chi.URLParam(r, "id"))
	if err != nil || ch == nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if err := h.service.TestWebhook(r.Context(), ch); err != nil {
		writeError(w, http.StatusBadGateway, "delivery failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ── Web Push ─────────────────────────────────────────────────────

func (h *NotificationHandlers) VAPID(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"public_key": h.service.VAPIDPublic})
}

func (h *NotificationHandlers) RegisterPush(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Endpoint string `json:"endpoint"`
		P256dh   string `json:"p256dh"`
		Auth     string `json:"auth"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.Endpoint == "" || body.P256dh == "" || body.Auth == "" {
		writeError(w, http.StatusBadRequest, "endpoint, p256dh and auth are required")
		return
	}
	dev, err := h.store.UpsertWebpushDevice(r.Context(), &store.WebpushDevice{
		UserID:    currentUserID(r),
		Endpoint:  body.Endpoint,
		P256dh:    body.P256dh,
		Auth:      body.Auth,
		UserAgent: userAgent(r),
	})
	if err != nil {
		writeServerError(w, r, "register push device", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": dev.ID})
}

func (h *NotificationHandlers) UnregisterPush(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := decode(r, &body); err != nil || body.Endpoint == "" {
		writeError(w, http.StatusBadRequest, "endpoint is required")
		return
	}
	if err := h.store.DeleteWebpushDeviceForUser(r.Context(), currentUserID(r), body.Endpoint); err != nil {
		writeServerError(w, r, "unregister push device", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Feed ─────────────────────────────────────────────────────────

func (h *NotificationHandlers) Feed(w http.ResponseWriter, r *http.Request) {
	unread := r.URL.Query().Get("unread") == "1"
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.store.ListNotifications(r.Context(), currentUserID(r), unread, limit)
	if err != nil {
		writeServerError(w, r, "list notifications", err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *NotificationHandlers) UnreadCount(w http.ResponseWriter, r *http.Request) {
	n, err := h.store.UnreadCount(r.Context(), currentUserID(r))
	if err != nil {
		writeServerError(w, r, "unread count", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": n})
}

func (h *NotificationHandlers) MarkRead(w http.ResponseWriter, r *http.Request) {
	if err := h.store.MarkNotificationRead(r.Context(), currentUserID(r), chi.URLParam(r, "id")); err != nil {
		writeServerError(w, r, "mark read", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *NotificationHandlers) MarkAllRead(w http.ResponseWriter, r *http.Request) {
	if err := h.store.MarkAllNotificationsRead(r.Context(), currentUserID(r)); err != nil {
		writeServerError(w, r, "mark all read", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Live stream (WebSocket) ──────────────────────────────────────

func (h *NotificationHandlers) Stream(w http.ResponseWriter, r *http.Request) {
	userID := currentUserID(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ch, unsub := h.service.Hub.Subscribe(userID)
	ws.ServeStream(w, r, ch, unsub)
}

// ── helpers ──────────────────────────────────────────────────────

type channelBody struct {
	Label   string `json:"label"`
	URL     string `json:"url"`
	Format  string `json:"format"`
	Secret  string `json:"secret"`
	Enabled bool   `json:"enabled"`
}

func decodeChannelBody(w http.ResponseWriter, r *http.Request) (channelBody, bool) {
	var body channelBody
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return body, false
	}
	body.URL = strings.TrimSpace(body.URL)
	if body.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return body, false
	}
	if err := validateWebhookURL(body.URL); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return body, false
	}
	switch body.Format {
	case "", "generic":
		body.Format = "generic"
	case "discord", "slack":
	default:
		writeError(w, http.StatusBadRequest, "invalid format")
		return body, false
	}
	return body, true
}

func (h *NotificationHandlers) ownedChannel(r *http.Request, userID, id string) (*store.NotificationChannel, error) {
	ch, err := h.store.GetChannel(r.Context(), id)
	if err != nil || ch == nil || ch.UserID != userID {
		return nil, err
	}
	return ch, nil
}

func (h *NotificationHandlers) canSeeServer(r *http.Request, userID, serverID string) (bool, error) {
	user, err := h.store.GetUserByID(r.Context(), userID)
	if err != nil {
		return false, err
	}
	if user.Role == "admin" {
		return true, nil
	}
	return h.store.UserHasServerPermission(r.Context(), userID, serverID, store.ServerPermissionView)
}

func validSeverity(s string) bool {
	switch s {
	case notify.SeverityInfo, notify.SeverityWarning, notify.SeverityCritical:
		return true
	}
	return false
}

// validateChannelSelectors ensures every selector is "inapp", "webpush", or a
// "webhook:<id>" the caller owns.
func validateChannelSelectors(r *http.Request, s *store.Store, userID string, selectors []string) error {
	for _, sel := range selectors {
		switch {
		case sel == "inapp" || sel == "webpush":
		case strings.HasPrefix(sel, "webhook:"):
			id := strings.TrimPrefix(sel, "webhook:")
			ch, err := s.GetChannel(r.Context(), id)
			if err != nil {
				return err
			}
			if ch == nil || ch.UserID != userID {
				return errInvalidChannel
			}
		default:
			return errInvalidChannel
		}
	}
	return nil
}
