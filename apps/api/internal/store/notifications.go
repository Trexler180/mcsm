package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ── Models ───────────────────────────────────────────────────────

// NotificationSubscription is one user rule: deliver <EventType> [on ServerID]
// at or above MinSeverity through Channels. ServerID nil means every server the
// user can access. Channels holds selectors: "inapp", "webpush",
// "webhook:<channel_id>".
type NotificationSubscription struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	EventType   string    `json:"event_type"`
	ServerID    *string   `json:"server_id"`
	MinSeverity string    `json:"min_severity"`
	Channels    []string  `json:"channels"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// NotificationChannelConfig is the per-channel JSON config. The HMAC secret is
// never stored here — it lives in app_secrets and is exposed only as SecretSet.
type NotificationChannelConfig struct {
	URL       string `json:"url"`
	Format    string `json:"format"` // 'generic' | 'discord' | 'slack'
	SecretSet bool   `json:"secret_set"`
}

type NotificationChannel struct {
	ID        string                    `json:"id"`
	UserID    string                    `json:"user_id"`
	Kind      string                    `json:"kind"`
	Label     string                    `json:"label"`
	Config    NotificationChannelConfig `json:"config"`
	Enabled   bool                      `json:"enabled"`
	CreatedAt time.Time                 `json:"created_at"`
	UpdatedAt time.Time                 `json:"updated_at"`
}

type WebpushDevice struct {
	ID            string     `json:"id"`
	UserID        string     `json:"user_id"`
	Endpoint      string     `json:"endpoint"`
	P256dh        string     `json:"p256dh"`
	Auth          string     `json:"auth"`
	UserAgent     string     `json:"user_agent"`
	CreatedAt     time.Time  `json:"created_at"`
	LastSuccessAt *time.Time `json:"last_success_at"`
	Failures      int        `json:"failures"`
}

type Notification struct {
	ID        string          `json:"id"`
	UserID    string          `json:"user_id"`
	EventType string          `json:"event_type"`
	Severity  string          `json:"severity"`
	ServerID  *string         `json:"server_id"`
	NodeID    *string         `json:"node_id"`
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	Data      json.RawMessage `json:"data"`
	DedupeKey string          `json:"dedupe_key"`
	CreatedAt time.Time       `json:"created_at"`
	ReadAt    *time.Time      `json:"read_at"`
}

type NotificationDelivery struct {
	ID             string    `json:"id"`
	NotificationID string    `json:"notification_id"`
	UserID         string    `json:"user_id"`
	TargetKind     string    `json:"target_kind"`
	TargetID       string    `json:"target_id"`
	Status         string    `json:"status"`
	Attempts       int       `json:"attempts"`
	NextAttemptAt  time.Time `json:"next_attempt_at"`
	LastError      string    `json:"last_error"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ── Subscriptions ────────────────────────────────────────────────

func (s *Store) ListSubscriptions(ctx context.Context, userID string) ([]*NotificationSubscription, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, event_type, server_id, min_severity, channels, enabled, created_at, updated_at
		   FROM notification_subscriptions WHERE user_id = ? ORDER BY event_type, server_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSubscriptions(rows)
}

// MatchingSubscriptions returns enabled subscriptions for an event of eventType
// whose scope covers serverID. A nil/empty serverID (node- or global-scoped
// events) matches only all-server (server_id IS NULL) rules. Severity filtering
// and the per-server permission re-check are done by the caller (the engine).
func (s *Store) MatchingSubscriptions(ctx context.Context, eventType, serverID string) ([]*NotificationSubscription, error) {
	q := `SELECT id, user_id, event_type, server_id, min_severity, channels, enabled, created_at, updated_at
	        FROM notification_subscriptions
	       WHERE enabled = 1 AND event_type = ? AND (server_id IS NULL`
	args := []any{eventType}
	if serverID != "" {
		q += ` OR server_id = ?`
		args = append(args, serverID)
	}
	q += `)`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSubscriptions(rows)
}

// UpsertSubscription inserts or replaces the user's rule for (event_type, scope).
// The two partial unique indexes (scoped vs all-servers) require branching the
// conflict target on whether server_id is set.
func (s *Store) UpsertSubscription(ctx context.Context, sub *NotificationSubscription) (*NotificationSubscription, error) {
	channelsJSON, _ := json.Marshal(sub.Channels)
	if sub.MinSeverity == "" {
		sub.MinSeverity = "info"
	}
	if sub.ID == "" {
		sub.ID = uuid.NewString()
	}
	conflict := `ON CONFLICT(user_id, event_type, server_id) WHERE server_id IS NOT NULL`
	if sub.ServerID == nil {
		conflict = `ON CONFLICT(user_id, event_type) WHERE server_id IS NULL`
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notification_subscriptions
		  (id, user_id, event_type, server_id, min_severity, channels, enabled, updated_at)
		VALUES (?,?,?,?,?,?,?,CURRENT_TIMESTAMP)
		`+conflict+` DO UPDATE SET
		  min_severity = excluded.min_severity,
		  channels     = excluded.channels,
		  enabled      = excluded.enabled,
		  updated_at   = CURRENT_TIMESTAMP`,
		sub.ID, sub.UserID, sub.EventType, sub.ServerID, sub.MinSeverity, string(channelsJSON), boolToInt(sub.Enabled))
	if err != nil {
		return nil, err
	}
	return s.getSubscription(ctx, sub.UserID, sub.EventType, sub.ServerID)
}

func (s *Store) getSubscription(ctx context.Context, userID, eventType string, serverID *string) (*NotificationSubscription, error) {
	q := `SELECT id, user_id, event_type, server_id, min_severity, channels, enabled, created_at, updated_at
	        FROM notification_subscriptions WHERE user_id = ? AND event_type = ? AND `
	args := []any{userID, eventType}
	if serverID == nil {
		q += `server_id IS NULL`
	} else {
		q += `server_id = ?`
		args = append(args, *serverID)
	}
	row := s.db.QueryRowContext(ctx, q, args...)
	return scanSubscriptionRow(row)
}

func (s *Store) DeleteSubscription(ctx context.Context, userID, id string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM notification_subscriptions WHERE id = ? AND user_id = ?`, id, userID)
	return err
}

func scanSubscriptions(rows *sql.Rows) ([]*NotificationSubscription, error) {
	out := []*NotificationSubscription{}
	for rows.Next() {
		sub, err := scanSubscriptionRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

func scanSubscriptionRow(row interface{ Scan(...any) error }) (*NotificationSubscription, error) {
	var sub NotificationSubscription
	var channelsJSON string
	var enabled int
	if err := row.Scan(&sub.ID, &sub.UserID, &sub.EventType, &sub.ServerID,
		&sub.MinSeverity, &channelsJSON, &enabled, &sub.CreatedAt, &sub.UpdatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(channelsJSON), &sub.Channels)
	if sub.Channels == nil {
		sub.Channels = []string{}
	}
	sub.Enabled = enabled != 0
	return &sub, nil
}

// ── Channels ─────────────────────────────────────────────────────

func (s *Store) ListChannels(ctx context.Context, userID string) ([]*NotificationChannel, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, kind, label, config, enabled, created_at, updated_at
		   FROM notification_channels WHERE user_id = ? ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*NotificationChannel{}
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetChannel(ctx context.Context, id string) (*NotificationChannel, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, kind, label, config, enabled, created_at, updated_at
		   FROM notification_channels WHERE id = ?`, id)
	c, err := scanChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

func (s *Store) CreateChannel(ctx context.Context, c *NotificationChannel) (*NotificationChannel, error) {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.Kind == "" {
		c.Kind = "webhook"
	}
	cfgJSON, _ := json.Marshal(c.Config)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO notification_channels (id, user_id, kind, label, config, enabled)
		 VALUES (?,?,?,?,?,?)`,
		c.ID, c.UserID, c.Kind, c.Label, string(cfgJSON), boolToInt(c.Enabled))
	if err != nil {
		return nil, err
	}
	return s.GetChannel(ctx, c.ID)
}

func (s *Store) UpdateChannel(ctx context.Context, c *NotificationChannel) error {
	cfgJSON, _ := json.Marshal(c.Config)
	_, err := s.db.ExecContext(ctx,
		`UPDATE notification_channels SET label = ?, config = ?, enabled = ?, updated_at = CURRENT_TIMESTAMP
		   WHERE id = ? AND user_id = ?`,
		c.Label, string(cfgJSON), boolToInt(c.Enabled), c.ID, c.UserID)
	return err
}

func (s *Store) DeleteChannel(ctx context.Context, userID, id string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM notification_channels WHERE id = ? AND user_id = ?`, id, userID)
	return err
}

func scanChannel(row interface{ Scan(...any) error }) (*NotificationChannel, error) {
	var c NotificationChannel
	var cfgJSON string
	var enabled int
	if err := row.Scan(&c.ID, &c.UserID, &c.Kind, &c.Label, &cfgJSON, &enabled, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(cfgJSON), &c.Config)
	c.Enabled = enabled != 0
	return &c, nil
}

// ── Web Push devices ─────────────────────────────────────────────

func (s *Store) ListWebpushDevices(ctx context.Context, userID string) ([]*WebpushDevice, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, endpoint, p256dh, auth, user_agent, created_at, last_success_at, failures
		   FROM webpush_devices WHERE user_id = ? ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*WebpushDevice{}
	for rows.Next() {
		d, err := scanWebpushDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetWebpushDevice(ctx context.Context, id string) (*WebpushDevice, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, endpoint, p256dh, auth, user_agent, created_at, last_success_at, failures
		   FROM webpush_devices WHERE id = ?`, id)
	d, err := scanWebpushDevice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return d, err
}

// UpsertWebpushDevice registers (or re-binds) a device by its unique endpoint,
// so re-subscribing the same browser refreshes its keys/owner rather than
// duplicating. Resets the failure counter on (re)registration.
func (s *Store) UpsertWebpushDevice(ctx context.Context, d *WebpushDevice) (*WebpushDevice, error) {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO webpush_devices (id, user_id, endpoint, p256dh, auth, user_agent)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(endpoint) DO UPDATE SET
		  user_id    = excluded.user_id,
		  p256dh     = excluded.p256dh,
		  auth       = excluded.auth,
		  user_agent = excluded.user_agent,
		  failures   = 0`,
		d.ID, d.UserID, d.Endpoint, d.P256dh, d.Auth, d.UserAgent)
	if err != nil {
		return nil, err
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, endpoint, p256dh, auth, user_agent, created_at, last_success_at, failures
		   FROM webpush_devices WHERE endpoint = ?`, d.Endpoint)
	return scanWebpushDevice(row)
}

func (s *Store) DeleteWebpushDevice(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM webpush_devices WHERE id = ?`, id)
	return err
}

// DeleteWebpushDeviceForUser removes a device the caller owns, by endpoint.
func (s *Store) DeleteWebpushDeviceForUser(ctx context.Context, userID, endpoint string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM webpush_devices WHERE user_id = ? AND endpoint = ?`, userID, endpoint)
	return err
}

func (s *Store) MarkWebpushSuccess(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE webpush_devices SET last_success_at = CURRENT_TIMESTAMP, failures = 0 WHERE id = ?`, id)
	return err
}

func (s *Store) MarkWebpushFailure(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE webpush_devices SET failures = failures + 1 WHERE id = ?`, id)
	return err
}

func scanWebpushDevice(row interface{ Scan(...any) error }) (*WebpushDevice, error) {
	var d WebpushDevice
	if err := row.Scan(&d.ID, &d.UserID, &d.Endpoint, &d.P256dh, &d.Auth,
		&d.UserAgent, &d.CreatedAt, &d.LastSuccessAt, &d.Failures); err != nil {
		return nil, err
	}
	return &d, nil
}

// ── Notifications (feed) ─────────────────────────────────────────

func (s *Store) InsertNotification(ctx context.Context, n *Notification) (*Notification, error) {
	if n.ID == "" {
		n.ID = uuid.NewString()
	}
	if len(n.Data) == 0 {
		n.Data = json.RawMessage(`{}`)
	}
	if n.Severity == "" {
		n.Severity = "info"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO notifications (id, user_id, event_type, severity, server_id, node_id, title, body, data, dedupe_key)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		n.ID, n.UserID, n.EventType, n.Severity, n.ServerID, n.NodeID, n.Title, n.Body, string(n.Data), n.DedupeKey)
	if err != nil {
		return nil, err
	}
	return s.GetNotification(ctx, n.ID)
}

func (s *Store) GetNotification(ctx context.Context, id string) (*Notification, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, event_type, severity, server_id, node_id, title, body, data, dedupe_key, created_at, read_at
		   FROM notifications WHERE id = ?`, id)
	n, err := scanNotification(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return n, err
}

// RecentlyNotified reports whether a notification with the same dedupe_key was
// created for the user within the cooldown window — used to collapse repeats
// such as a crash loop into a single alert.
func (s *Store) RecentlyNotified(ctx context.Context, userID, dedupeKey string, within time.Duration) (bool, error) {
	if dedupeKey == "" {
		return false, nil
	}
	cutoff := time.Now().UTC().Add(-within).Format("2006-01-02 15:04:05")
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM notifications WHERE user_id = ? AND dedupe_key = ? AND created_at >= ? LIMIT 1`,
		userID, dedupeKey, cutoff).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) ListNotifications(ctx context.Context, userID string, unreadOnly bool, limit int) ([]*Notification, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	q := `SELECT id, user_id, event_type, severity, server_id, node_id, title, body, data, dedupe_key, created_at, read_at
	        FROM notifications WHERE user_id = ?`
	if unreadOnly {
		q += ` AND read_at IS NULL`
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, q, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Notification{}
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) UnreadCount(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notifications WHERE user_id = ? AND read_at IS NULL`, userID).Scan(&n)
	return n, err
}

func (s *Store) MarkNotificationRead(ctx context.Context, userID, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE notifications SET read_at = CURRENT_TIMESTAMP WHERE id = ? AND user_id = ? AND read_at IS NULL`,
		id, userID)
	return err
}

func (s *Store) MarkAllNotificationsRead(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE notifications SET read_at = CURRENT_TIMESTAMP WHERE user_id = ? AND read_at IS NULL`, userID)
	return err
}

func scanNotification(row interface{ Scan(...any) error }) (*Notification, error) {
	var n Notification
	var data string
	if err := row.Scan(&n.ID, &n.UserID, &n.EventType, &n.Severity, &n.ServerID, &n.NodeID,
		&n.Title, &n.Body, &data, &n.DedupeKey, &n.CreatedAt, &n.ReadAt); err != nil {
		return nil, err
	}
	n.Data = json.RawMessage(data)
	return &n, nil
}

// ── Deliveries (durable outbox) ──────────────────────────────────

func (s *Store) EnqueueDelivery(ctx context.Context, d *NotificationDelivery) error {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO notification_deliveries (id, notification_id, user_id, target_kind, target_id, status)
		 VALUES (?,?,?,?,?,'pending')`,
		d.ID, d.NotificationID, d.UserID, d.TargetKind, d.TargetID)
	return err
}

// ClaimDueDeliveries returns pending deliveries whose next_attempt_at has passed.
// A single dispatcher goroutine consumes these, so no row-level locking/claim
// flag is needed.
func (s *Store) ClaimDueDeliveries(ctx context.Context, limit int) ([]*NotificationDelivery, error) {
	if limit <= 0 {
		limit = 50
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, notification_id, user_id, target_kind, target_id, status, attempts, next_attempt_at, last_error, created_at, updated_at
		   FROM notification_deliveries
		  WHERE status = 'pending' AND next_attempt_at <= ?
		  ORDER BY next_attempt_at LIMIT ?`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*NotificationDelivery{}
	for rows.Next() {
		var d NotificationDelivery
		if err := rows.Scan(&d.ID, &d.NotificationID, &d.UserID, &d.TargetKind, &d.TargetID,
			&d.Status, &d.Attempts, &d.NextAttemptAt, &d.LastError, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &d)
	}
	return out, rows.Err()
}

func (s *Store) MarkDeliverySent(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE notification_deliveries
		    SET status = 'sent', attempts = attempts + 1, last_error = '', updated_at = CURRENT_TIMESTAMP
		  WHERE id = ?`, id)
	return err
}

// ScheduleDeliveryRetry bumps the attempt count and re-arms the row for a later
// attempt. terminal=true marks it permanently failed instead.
func (s *Store) ScheduleDeliveryRetry(ctx context.Context, id string, nextAttempt time.Time, lastErr string, terminal bool) error {
	status := "pending"
	if terminal {
		status = "failed"
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE notification_deliveries
		    SET status = ?, attempts = attempts + 1, next_attempt_at = ?, last_error = ?, updated_at = CURRENT_TIMESTAMP
		  WHERE id = ?`,
		status, nextAttempt.UTC().Format("2006-01-02 15:04:05"), truncErr(lastErr), id)
	return err
}

// MarkDeliverySkipped retires a delivery whose target no longer exists (channel
// deleted, device pruned) without counting it as a failure.
func (s *Store) MarkDeliverySkipped(ctx context.Context, id, reason string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE notification_deliveries
		    SET status = 'skipped', last_error = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		truncErr(reason), id)
	return err
}

// ── helpers ──────────────────────────────────────────────────────

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func truncErr(s string) string {
	if len(s) > 500 {
		return s[:500]
	}
	return s
}
