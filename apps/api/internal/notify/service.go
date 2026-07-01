package notify

import (
	"context"
	"time"

	"github.com/mcsm/api/internal/notify/channels"
	"github.com/mcsm/api/internal/store"
)

// Service bundles the notification subsystem so main.go wires it in one call and
// hands the Engine to detection points and the whole thing to the API handler.
type Service struct {
	Engine      *Engine
	Hub         *Hub
	Dispatcher  *Dispatcher
	VAPIDPublic string

	store   *store.Store
	webhook *channels.WebhookSender
}

// NewService provisions VAPID keys (generating them on first run), builds the
// hub, dispatcher, and engine, and returns a ready Service. Run must be called
// to start the dispatcher.
func NewService(ctx context.Context, s *store.Store, subscriber string) (*Service, error) {
	keys, err := EnsureVAPIDKeys(ctx, s, subscriber)
	if err != nil {
		return nil, err
	}
	return NewServiceWithKeys(s, *keys), nil
}

// NewServiceWithKeys builds a service from an explicit keypair, skipping store
// provisioning. Used by tests and callers that manage VAPID keys out of band; an
// empty keypair simply disables Web Push while the rest of the service works.
func NewServiceWithKeys(s *store.Store, keys VAPIDKeys) *Service {
	hub := NewHub()
	webhook := channels.NewWebhookSender()
	webpush := channels.NewWebPushSender(keys.Public, keys.Private, keys.Subscriber)
	dispatcher := NewDispatcher(s, webhook, webpush)
	engine := NewEngine(s, hub, dispatcher)
	return &Service{
		Engine:      engine,
		Hub:         hub,
		Dispatcher:  dispatcher,
		VAPIDPublic: keys.Public,
		store:       s,
		webhook:     webhook,
	}
}

// Run starts the dispatcher loop; blocks until ctx is done.
func (svc *Service) Run(ctx context.Context) { svc.Dispatcher.Run(ctx) }

// TestWebhook sends a sample alert to a channel synchronously, so the UI's
// "send test" button gets an immediate success/failure. The signing secret is
// looked up from app_secrets like a real delivery.
func (svc *Service) TestWebhook(ctx context.Context, ch *store.NotificationChannel) error {
	secret, _ := svc.store.GetSecret(ctx, WebhookSecretKey(ch.ID))
	payload := channels.WebhookPayload{
		Event:     "test",
		Severity:  SeverityInfo,
		Title:     "Test notification",
		Body:      "If you can see this, your webhook is configured correctly.",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	target := channels.WebhookTarget{URL: ch.Config.URL, Format: ch.Config.Format, Secret: secret}
	return svc.webhook.Send(ctx, target, payload)
}
