package notify

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/mcsm/api/internal/store"
)

// dedupeWindow collapses repeats of the same alert (e.g. a crash loop, or the
// poller re-confirming an offline node) into a single notification per user.
const dedupeWindow = 5 * time.Minute

// Engine resolves an emitted Event to its recipients, persists each one's feed
// item, pushes it live, and enqueues external deliveries. It is safe to call on
// a nil receiver (a build without notifications wired) — Emit becomes a no-op.
type Engine struct {
	store      *store.Store
	hub        *Hub
	dispatcher *Dispatcher
}

func NewEngine(s *store.Store, hub *Hub, dispatcher *Dispatcher) *Engine {
	return &Engine{store: s, hub: hub, dispatcher: dispatcher}
}

// Emit processes an event asynchronously so detection-point callers (the poller
// loop, scheduler, autoupdate) never block on notification work. Errors are
// logged and swallowed, matching the best-effort style of InsertLogEvent.
func (e *Engine) Emit(evt Event) {
	if e == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := e.process(ctx, evt); err != nil {
			log.Printf("notify: emit %s: %v", evt.Type, err)
		}
	}()
}

func (e *Engine) process(ctx context.Context, evt Event) error {
	def, ok := defByType(evt.Type)
	if !ok {
		return nil // unknown event type; ignore
	}
	if evt.Severity == "" {
		evt.Severity = def.DefaultSeverity
	}
	if evt.DedupeKey == "" {
		evt.DedupeKey = defaultDedupeKey(evt)
	}

	subs, err := e.store.MatchingSubscriptions(ctx, evt.Type, evt.ServerID)
	if err != nil {
		return err
	}

	// Aggregate channel selectors per user (a user may have both an all-servers
	// rule and a server-scoped one match the same event), respecting each rule's
	// severity threshold.
	perUser := map[string]map[string]struct{}{}
	for _, sub := range subs {
		if severityRank(evt.Severity) < severityRank(sub.MinSeverity) {
			continue
		}
		set := perUser[sub.UserID]
		if set == nil {
			set = map[string]struct{}{}
			perUser[sub.UserID] = set
		}
		for _, c := range sub.Channels {
			set[c] = struct{}{}
		}
	}

	signalNeeded := false
	for userID, channelSet := range perUser {
		if len(channelSet) == 0 {
			continue
		}
		// Re-check live permission for server-scoped events so an alert never
		// outlives the user's access to the server it concerns.
		if def.Scope == ScopeServer && evt.ServerID != "" {
			ok, err := e.userCanSeeServer(ctx, userID, evt.ServerID)
			if err != nil {
				log.Printf("notify: permission check %s/%s: %v", userID, evt.ServerID, err)
				continue
			}
			if !ok {
				continue
			}
		}

		// De-duplicate within the cooldown window.
		recent, err := e.store.RecentlyNotified(ctx, userID, evt.DedupeKey, dedupeWindow)
		if err != nil {
			log.Printf("notify: dedupe check: %v", err)
		}
		if recent {
			continue
		}

		if e.deliverToUser(ctx, userID, evt, channelSet) {
			signalNeeded = true
		}
	}

	if signalNeeded && e.dispatcher != nil {
		e.dispatcher.Signal()
	}
	return nil
}

// deliverToUser persists the feed item, pushes it live, and enqueues external
// deliveries for the selected channels. Returns true if it enqueued any external
// delivery (so the caller knows to wake the dispatcher).
func (e *Engine) deliverToUser(ctx context.Context, userID string, evt Event, channelSet map[string]struct{}) bool {
	dataJSON, _ := json.Marshal(evt.Data)
	n := &store.Notification{
		UserID:    userID,
		EventType: evt.Type,
		Severity:  evt.Severity,
		Title:     evt.Title,
		Body:      evt.Body,
		Data:      dataJSON,
		DedupeKey: evt.DedupeKey,
	}
	if evt.ServerID != "" {
		n.ServerID = &evt.ServerID
	}
	if evt.NodeID != "" {
		n.NodeID = &evt.NodeID
	}
	saved, err := e.store.InsertNotification(ctx, n)
	if err != nil {
		log.Printf("notify: insert notification: %v", err)
		return false
	}

	// In-app: always push live if the user has a stream open (the feed item is
	// persisted regardless, so a disconnected user sees it on next load).
	if _, inApp := channelSet["inapp"]; inApp {
		e.hub.Push(userID, map[string]any{"type": "notification", "data": saved})
	}

	enqueued := false
	for selector := range channelSet {
		switch {
		case selector == "webpush":
			devices, err := e.store.ListWebpushDevices(ctx, userID)
			if err != nil {
				log.Printf("notify: list devices: %v", err)
				continue
			}
			for _, dev := range devices {
				if e.enqueue(ctx, saved.ID, userID, "webpush", dev.ID) {
					enqueued = true
				}
			}
		case strings.HasPrefix(selector, "webhook:"):
			channelID := strings.TrimPrefix(selector, "webhook:")
			if e.enqueue(ctx, saved.ID, userID, "webhook", channelID) {
				enqueued = true
			}
		}
	}
	return enqueued
}

func (e *Engine) enqueue(ctx context.Context, notificationID, userID, kind, targetID string) bool {
	if err := e.store.EnqueueDelivery(ctx, &store.NotificationDelivery{
		NotificationID: notificationID,
		UserID:         userID,
		TargetKind:     kind,
		TargetID:       targetID,
	}); err != nil {
		log.Printf("notify: enqueue %s delivery: %v", kind, err)
		return false
	}
	return true
}

func (e *Engine) userCanSeeServer(ctx context.Context, userID, serverID string) (bool, error) {
	user, err := e.store.GetUserByID(ctx, userID)
	if err != nil {
		return false, err
	}
	if user.Role == "admin" {
		return true, nil
	}
	return e.store.UserHasServerPermission(ctx, userID, serverID, store.ServerPermissionView)
}

// defaultDedupeKey derives a stable key from the event's type and subject so
// repeats collapse, while distinct subjects stay separate.
func defaultDedupeKey(evt Event) string {
	switch {
	case evt.ServerID != "":
		return evt.Type + ":" + evt.ServerID
	case evt.NodeID != "":
		return evt.Type + ":" + evt.NodeID
	default:
		return evt.Type
	}
}
