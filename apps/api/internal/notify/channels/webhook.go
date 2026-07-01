// Package channels holds the outbound delivery adapters (webhook, web push)
// used by the notification dispatcher. Adapters take plain inputs and have no
// dependency on the store, so they stay easy to test in isolation.
package channels

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mcsm/api/internal/safedial"
)

// WebhookTarget is one delivery destination. Secret is empty when the channel
// has no signing secret configured.
type WebhookTarget struct {
	URL    string
	Format string // 'generic' | 'discord' | 'slack'
	Secret string
}

// WebhookPayload is the JSON sent to a "generic" webhook. It mirrors the feed
// item so a receiver can route on type/severity/server.
type WebhookPayload struct {
	Event     string         `json:"event"`
	Severity  string         `json:"severity"`
	Title     string         `json:"title"`
	Body      string         `json:"body"`
	ServerID  string         `json:"server_id,omitempty"`
	NodeID    string         `json:"node_id,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	CreatedAt string         `json:"created_at"`
}

type WebhookSender struct {
	client *http.Client
}

// NewWebhookSender builds a sender whose HTTP client refuses connections to
// non-public addresses (SSRF guard), including across redirects.
func NewWebhookSender() *WebhookSender {
	return newWebhookSender(safedial.Guard{})
}

func newWebhookSender(g safedial.Guard) *WebhookSender {
	return &WebhookSender{client: safedial.Client(g, 10*time.Second)}
}

// Send delivers payload to the target, formatting the body for Discord/Slack or
// posting the raw signed JSON for a generic endpoint. A non-2xx response is an
// error so the dispatcher retries.
func (s *WebhookSender) Send(ctx context.Context, target WebhookTarget, payload WebhookPayload) error {
	if err := safedial.ValidateHTTPURL(target.URL); err != nil {
		return err
	}

	var body []byte
	contentType := "application/json"
	switch target.Format {
	case "discord":
		body, _ = json.Marshal(discordBody(payload))
	case "slack":
		body, _ = json.Marshal(slackBody(payload))
	default:
		body, _ = json.Marshal(payload)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "mcsm-notifier/1")
	// Only the generic format carries our signature; Discord/Slack reject unknown
	// custom headers from some proxies, and the signature is meaningless to them.
	if target.Format != "discord" && target.Format != "slack" && target.Secret != "" {
		req.Header.Set("X-MCSM-Signature", "sha256="+sign(target.Secret, body))
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Drain a bounded amount so the connection can be reused.
	_, _ = io.CopyN(io.Discard, resp.Body, 4096)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook responded %d", resp.StatusCode)
	}
	return nil
}

// sign returns the lowercase hex HMAC-SHA256 of body under secret, so receivers
// can verify a delivery is authentic and untampered.
func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func discordBody(p WebhookPayload) map[string]any {
	color := 0x3498db // info: blue
	switch p.Severity {
	case "warning":
		color = 0xf1c40f
	case "critical":
		color = 0xe74c3c
	}
	return map[string]any{
		"embeds": []map[string]any{{
			"title":       p.Title,
			"description": p.Body,
			"color":       color,
			"footer":      map[string]any{"text": p.Event},
			"timestamp":   p.CreatedAt,
		}},
	}
}

func slackBody(p WebhookPayload) map[string]any {
	emoji := ":information_source:"
	switch p.Severity {
	case "warning":
		emoji = ":warning:"
	case "critical":
		emoji = ":rotating_light:"
	}
	text := fmt.Sprintf("%s *%s*", emoji, p.Title)
	if p.Body != "" {
		text += "\n" + p.Body
	}
	return map[string]any{"text": text}
}
