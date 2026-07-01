package notify

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math/rand"
	"time"

	"github.com/mcsm/api/internal/notify/channels"
	"github.com/mcsm/api/internal/store"
)

const (
	dispatchBatch    = 50
	fallbackInterval = 30 * time.Second
	maxAttempts      = 6
	backoffBase      = 30 * time.Second
	backoffCap       = time.Hour
)

// WebhookSecretKey is the app_secrets key under which a channel's HMAC signing
// secret is stored, encrypted at rest. Shared with the handler that writes it.
func WebhookSecretKey(channelID string) string { return "webhook_secret:" + channelID }

// Dispatcher drains the durable delivery outbox. It wakes on a signal from the
// engine (new work enqueued) or a slow fallback ticker (to pick up due retries),
// so it is event-driven rather than a busy poll. All state lives in SQLite, so
// pending work survives a restart.
type Dispatcher struct {
	store   *store.Store
	webhook *channels.WebhookSender
	webpush *channels.WebPushSender
	signal  chan struct{}
}

func NewDispatcher(s *store.Store, webhook *channels.WebhookSender, webpush *channels.WebPushSender) *Dispatcher {
	return &Dispatcher{
		store:   s,
		webhook: webhook,
		webpush: webpush,
		signal:  make(chan struct{}, 1),
	}
}

// Signal nudges the dispatcher to drain now. Non-blocking and coalescing: a
// pending wake is enough, extra signals are dropped.
func (d *Dispatcher) Signal() {
	select {
	case d.signal <- struct{}{}:
	default:
	}
}

// Run blocks until ctx is done. Spawn it in its own goroutine.
func (d *Dispatcher) Run(ctx context.Context) {
	t := time.NewTicker(fallbackInterval)
	defer t.Stop()
	for {
		d.drain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-d.signal:
		}
	}
}

func (d *Dispatcher) drain(ctx context.Context) {
	for {
		due, err := d.store.ClaimDueDeliveries(ctx, dispatchBatch)
		if err != nil {
			log.Printf("notify: claim deliveries: %v", err)
			return
		}
		if len(due) == 0 {
			return
		}
		for _, del := range due {
			if ctx.Err() != nil {
				return
			}
			d.deliver(ctx, del)
		}
		if len(due) < dispatchBatch {
			return
		}
	}
}

func (d *Dispatcher) deliver(ctx context.Context, del *store.NotificationDelivery) {
	n, err := d.store.GetNotification(ctx, del.NotificationID)
	if err != nil {
		d.retry(ctx, del, err)
		return
	}
	if n == nil {
		_ = d.store.MarkDeliverySkipped(ctx, del.ID, "notification gone")
		return
	}

	switch del.TargetKind {
	case "webhook":
		d.deliverWebhook(ctx, del, n)
	case "webpush":
		d.deliverWebPush(ctx, del, n)
	default:
		_ = d.store.MarkDeliverySkipped(ctx, del.ID, "unknown target kind")
	}
}

func (d *Dispatcher) deliverWebhook(ctx context.Context, del *store.NotificationDelivery, n *store.Notification) {
	ch, err := d.store.GetChannel(ctx, del.TargetID)
	if err != nil {
		d.retry(ctx, del, err)
		return
	}
	if ch == nil || !ch.Enabled {
		_ = d.store.MarkDeliverySkipped(ctx, del.ID, "channel removed or disabled")
		return
	}
	secret, _ := d.store.GetSecret(ctx, WebhookSecretKey(ch.ID))

	var data map[string]any
	_ = json.Unmarshal(n.Data, &data)
	payload := channels.WebhookPayload{
		Event:     n.EventType,
		Severity:  n.Severity,
		Title:     n.Title,
		Body:      n.Body,
		Data:      data,
		CreatedAt: n.CreatedAt.UTC().Format(time.RFC3339),
	}
	if n.ServerID != nil {
		payload.ServerID = *n.ServerID
	}
	if n.NodeID != nil {
		payload.NodeID = *n.NodeID
	}

	target := channels.WebhookTarget{URL: ch.Config.URL, Format: ch.Config.Format, Secret: secret}
	if err := d.webhook.Send(ctx, target, payload); err != nil {
		d.retry(ctx, del, err)
		return
	}
	_ = d.store.MarkDeliverySent(ctx, del.ID)
}

func (d *Dispatcher) deliverWebPush(ctx context.Context, del *store.NotificationDelivery, n *store.Notification) {
	if d.webpush == nil {
		_ = d.store.MarkDeliverySkipped(ctx, del.ID, "web push not configured")
		return
	}
	dev, err := d.store.GetWebpushDevice(ctx, del.TargetID)
	if err != nil {
		d.retry(ctx, del, err)
		return
	}
	if dev == nil {
		_ = d.store.MarkDeliverySkipped(ctx, del.ID, "device removed")
		return
	}

	payload, _ := json.Marshal(webPushPayload(n))
	target := channels.WebPushTarget{Endpoint: dev.Endpoint, P256dh: dev.P256dh, Auth: dev.Auth}
	err = d.webpush.Send(ctx, target, payload)
	if errors.Is(err, channels.ErrSubscriptionGone) {
		// The browser unsubscribed or the subscription expired: prune the device
		// so we stop trying, and retire this delivery without counting a failure.
		_ = d.store.DeleteWebpushDevice(ctx, dev.ID)
		_ = d.store.MarkDeliverySkipped(ctx, del.ID, "subscription gone")
		return
	}
	if err != nil {
		_ = d.store.MarkWebpushFailure(ctx, dev.ID)
		d.retry(ctx, del, err)
		return
	}
	_ = d.store.MarkWebpushSuccess(ctx, dev.ID)
	_ = d.store.MarkDeliverySent(ctx, del.ID)
}

// webPushPayload is the JSON the service worker receives and renders as a system
// notification. Kept small and free of anything the user shouldn't see offline.
func webPushPayload(n *store.Notification) map[string]any {
	serverID := ""
	if n.ServerID != nil {
		serverID = *n.ServerID
	}
	return map[string]any{
		"id":        n.ID,
		"title":     n.Title,
		"body":      n.Body,
		"event":     n.EventType,
		"severity":  n.Severity,
		"server_id": serverID,
	}
}

// retry advances a delivery's backoff schedule, or marks it permanently failed
// once it has exhausted maxAttempts.
func (d *Dispatcher) retry(ctx context.Context, del *store.NotificationDelivery, cause error) {
	attempts := del.Attempts + 1
	terminal := attempts >= maxAttempts
	next := time.Now().Add(backoff(attempts))
	if err := d.store.ScheduleDeliveryRetry(ctx, del.ID, next, cause.Error(), terminal); err != nil {
		log.Printf("notify: schedule retry %s: %v", del.ID, err)
	}
}

// backoff is exponential with a cap and ±20% jitter so retries from many failed
// deliveries don't thunder together.
func backoff(attempt int) time.Duration {
	d := backoffBase << (attempt - 1)
	if d > backoffCap || d <= 0 {
		d = backoffCap
	}
	jitter := time.Duration(rand.Int63n(int64(d) / 5))
	return d - (jitter / 2) + jitter
}
