package notify

import (
	"context"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/mcsm/api/internal/store"
)

// Secret keys under which the VAPID keypair is stored (encrypted at rest) in
// app_secrets. The public key is also handed to browsers to create push
// subscriptions; the private key never leaves the server.
const (
	secretVAPIDPublic  = "vapid_public_key"
	secretVAPIDPrivate = "vapid_private_key"
)

// VAPIDKeys is the provisioned keypair plus the subscriber identity sent to push
// services.
type VAPIDKeys struct {
	Public     string
	Private    string
	Subscriber string
}

// EnsureVAPIDKeys returns the stored VAPID keypair, generating and persisting a
// fresh one on first use so Web Push works with no manual configuration. The
// subscriber is a contact URL push services may use; we derive a stable mailto
// from the app origin when available, else a placeholder.
func EnsureVAPIDKeys(ctx context.Context, s *store.Store, subscriber string) (*VAPIDKeys, error) {
	pub, err := s.GetSecret(ctx, secretVAPIDPublic)
	if err != nil {
		return nil, err
	}
	priv, err := s.GetSecret(ctx, secretVAPIDPrivate)
	if err != nil {
		return nil, err
	}
	if pub != "" && priv != "" {
		return &VAPIDKeys{Public: pub, Private: priv, Subscriber: subscriber}, nil
	}

	newPriv, newPub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return nil, err
	}
	if err := s.SetSecret(ctx, secretVAPIDPublic, newPub, "system"); err != nil {
		return nil, err
	}
	if err := s.SetSecret(ctx, secretVAPIDPrivate, newPriv, "system"); err != nil {
		return nil, err
	}
	return &VAPIDKeys{Public: newPub, Private: newPriv, Subscriber: subscriber}, nil
}
