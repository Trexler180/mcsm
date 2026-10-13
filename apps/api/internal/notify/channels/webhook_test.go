package channels

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mcsm/api/internal/safedial"
)

func samplePayload() WebhookPayload {
	return WebhookPayload{
		Event:    "server.crash",
		Severity: "critical",
		Title:    "survival crashed",
		Body:     "boom",
		ServerID: "srv1",
	}
}

func TestWebhookGenericSignsBody(t *testing.T) {
	var gotSig string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-MCSM-Signature")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	s := newWebhookSender(safedial.Guard{AllowLoopback: true})
	target := WebhookTarget{URL: srv.URL, Format: "generic", Secret: "topsecret"}
	if err := s.Send(context.Background(), target, samplePayload()); err != nil {
		t.Fatalf("send: %v", err)
	}

	want := "sha256=" + sign("topsecret", gotBody)
	if gotSig != want {
		t.Fatalf("signature mismatch: got %q want %q", gotSig, want)
	}
	// Body must be the raw generic payload JSON.
	var decoded WebhookPayload
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("body not generic JSON: %v", err)
	}
	if decoded.Event != "server.crash" {
		t.Fatalf("unexpected payload: %+v", decoded)
	}
}

func TestWebhookDiscordFormatNoSignature(t *testing.T) {
	var gotSig string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-MCSM-Signature")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.WriteHeader(204)
	}))
	defer srv.Close()

	s := newWebhookSender(safedial.Guard{AllowLoopback: true})
	target := WebhookTarget{URL: srv.URL, Format: "discord", Secret: "ignored"}
	if err := s.Send(context.Background(), target, samplePayload()); err != nil {
		t.Fatalf("send: %v", err)
	}
	if gotSig != "" {
		t.Fatalf("discord deliveries must not carry our signature header, got %q", gotSig)
	}
	if _, ok := body["embeds"]; !ok {
		t.Fatalf("discord body missing embeds: %v", body)
	}
}

func TestWebhookRejectsNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	s := newWebhookSender(safedial.Guard{AllowLoopback: true})
	if err := s.Send(context.Background(), WebhookTarget{URL: srv.URL, Format: "generic"}, samplePayload()); err == nil {
		t.Fatal("expected error on 500 response")
	}
}

func TestWebhookSSRFBlocksLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	// Production sender (no loopback allowance) must refuse the private target.
	s := NewWebhookSender()
	if err := s.Send(context.Background(), WebhookTarget{URL: srv.URL, Format: "generic"}, samplePayload()); err == nil {
		t.Fatal("expected SSRF guard to block loopback webhook")
	}
}

// The timestamped signature covers "<unix>.<body>", so a receiver can reject a
// captured delivery replayed later.
func TestWebhookTimestampedSignature(t *testing.T) {
	var gotTS, gotSig string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTS = r.Header.Get("X-MCSM-Timestamp")
		gotSig = r.Header.Get("X-MCSM-Signature-256")
		gotBody, _ = io.ReadAll(r.Body)
	}))
	defer srv.Close()

	s := newWebhookSender(safedial.Guard{AllowLoopback: true})
	s.now = func() time.Time { return time.Unix(1700000000, 0) }
	err := s.Send(context.Background(), WebhookTarget{URL: srv.URL, Format: "generic", Secret: "k"}, WebhookPayload{Event: "e"})
	if err != nil {
		t.Fatal(err)
	}
	if gotTS != "1700000000" {
		t.Fatalf("timestamp = %q", gotTS)
	}
	want := "t=1700000000,v1=" + sign("k", append([]byte("1700000000."), gotBody...))
	if gotSig != want {
		t.Fatalf("signature = %q, want %q", gotSig, want)
	}
}
