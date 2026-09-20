// Package notifications implements PRD §13.15 Notifications — every RPC is
// fully implemented. SendNotification is called internally by cmd/worker
// event handlers (see PRD §19), not exposed to mobile clients directly.
// Delivery is real: email via Resend (pkg/email), push via Firebase Cloud
// Messaging (pkg/push) — see service.go.
package notifications

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Notification struct {
	ID       string
	UserID   string
	Channel  string
	Title    string
	Body     string
	DeepLink string
	Read     bool
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Create(ctx context.Context, n *Notification) (*Notification, error) {
	out := *n
	err := r.pool.QueryRow(ctx, `
		INSERT INTO notifications (user_id, channel, title, body, deep_link, read)
		VALUES ($1, $2::notification_channel, $3, $4, $5, false)
		RETURNING id`,
		n.UserID, n.Channel, n.Title, n.Body, n.DeepLink,
	).Scan(&out.ID)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *Repository) ListForUser(ctx context.Context, userID string, limit int) ([]*Notification, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, channel, title, body, COALESCE(deep_link,''), read
		FROM notifications WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2`,
		userID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.UserID, &n.Channel, &n.Title, &n.Body, &n.DeepLink, &n.Read); err != nil {
			return nil, err
		}
		out = append(out, &n)
	}
	return out, rows.Err()
}

func (r *Repository) GetUserEmail(ctx context.Context, userID string) (string, error) {
	var email string
	err := r.pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, userID).Scan(&email)
	return email, err
}

// ListDevicePushTokens returns every registered device's push token for a
// user — a user can have multiple devices, so push is sent best-effort to
// each (see service.go).
func (r *Repository) ListDevicePushTokens(ctx context.Context, userID string) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT push_token FROM devices WHERE user_id = $1 AND push_token IS NOT NULL`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

// GetPreferences defaults to (true, true) when the user has never called
// UpdatePreferences — matches notification_preferences' column defaults.
func (r *Repository) GetPreferences(ctx context.Context, userID string) (pushEnabled, emailEnabled bool, err error) {
	pushEnabled, emailEnabled = true, true
	row := r.pool.QueryRow(ctx,
		`SELECT push_enabled, email_enabled FROM notification_preferences WHERE user_id = $1`, userID,
	)
	scanErr := row.Scan(&pushEnabled, &emailEnabled)
	if scanErr != nil && scanErr != pgx.ErrNoRows {
		return false, false, scanErr
	}
	return pushEnabled, emailEnabled, nil
}

func (r *Repository) MarkDelivery(ctx context.Context, notificationID, status, deliveryErr string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE notifications SET delivery_status = $2::delivery_status, sent_at = now(), delivery_error = NULLIF($3, '')
		WHERE id = $1`,
		notificationID, status, deliveryErr,
	)
	return err
}

func (r *Repository) UpsertPreferences(ctx context.Context, userID string, pushEnabled, emailEnabled bool, quietStart, quietEnd string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO notification_preferences (user_id, push_enabled, email_enabled, quiet_hours_start, quiet_hours_end)
		VALUES ($1, $2, $3, NULLIF($4,'')::time, NULLIF($5,'')::time)
		ON CONFLICT (user_id) DO UPDATE SET
			push_enabled = EXCLUDED.push_enabled,
			email_enabled = EXCLUDED.email_enabled,
			quiet_hours_start = EXCLUDED.quiet_hours_start,
			quiet_hours_end = EXCLUDED.quiet_hours_end`,
		userID, pushEnabled, emailEnabled, quietStart, quietEnd,
	)
	return err
}

// Preferences returns the caller's settings; defaults (push + e-mail on, no quiet hours) when never set.
func (r *Repository) Preferences(ctx context.Context, userID string) (push, email bool, quietStart, quietEnd string, err error) {
	push, email = true, true
	scanErr := r.pool.QueryRow(ctx,
		`SELECT push_enabled, email_enabled, COALESCE(to_char(quiet_hours_start, 'HH24:MI'), ''), COALESCE(to_char(quiet_hours_end, 'HH24:MI'), '') FROM notification_preferences WHERE user_id = $1`, userID,
	).Scan(&push, &email, &quietStart, &quietEnd)
	if scanErr != nil && scanErr != pgx.ErrNoRows {
		return false, false, "", "", scanErr
	}
	return push, email, quietStart, quietEnd, nil
}
