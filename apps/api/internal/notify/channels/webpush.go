package channels

import (
	"context"
	"errors"
	"io"
	"net/http"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// ErrSubscriptionGone signals that the push service permanently rejected the
// subscription (HTTP 404/410). The dispatcher prunes the device on this error
// instead of retrying.
var ErrSubscriptionGone = errors.New("push subscription gone")

// WebPushTarget is a registered browser push subscription.
type WebPushTarget struct {
	Endpoint string
	P256dh   string
	Auth     string
}

// WebPushSender encrypts and sends payloads using VAPID. The keypair is
// provisioned once and stored encrypted at rest; subscriber is a mailto: or
// origin string identifying this server to push services.
type WebPushSender struct {
	publicKey  string
	privateKey string
	subscriber string
}

func NewWebPushSender(publicKey, privateKey, subscriber string) *WebPushSender {
	return &WebPushSender{publicKey: publicKey, privateKey: privateKey, subscriber: subscriber}
}

// Send delivers an already-encoded JSON payload to one device. The webpush
// library performs RFC 8291 payload encryption and VAPID authentication.
func (s *WebPushSender) Send(ctx context.Context, target WebPushTarget, payload []byte) error {
	sub := &webpush.Subscription{
		Endpoint: target.Endpoint,
		Keys:     webpush.Keys{P256dh: target.P256dh, Auth: target.Auth},
	}
	resp, err := webpush.SendNotificationWithContext(ctx, payload, sub, &webpush.Options{
		Subscriber:      s.subscriber,
		VAPIDPublicKey:  s.publicKey,
		VAPIDPrivateKey: s.privateKey,
		TTL:             60,
		Urgency:         webpush.UrgencyNormal,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.CopyN(io.Discard, resp.Body, 4096)
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return ErrSubscriptionGone
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("push service responded " + resp.Status)
	}
	return nil
}
