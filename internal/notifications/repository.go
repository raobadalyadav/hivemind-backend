// Package notifications implements PRD §13.15 Notifications — every RPC is
// fully implemented. SendNotification is called internally by cmd/worker
// event handlers (see PRD §19), not exposed to mobile clients directly.
//
// This scaffold persists notifications to Postgres only — actual push/email
// delivery (FCM/APNs/SMTP) is a TODO(phase1+): wire a sender interface here.
package notifications

import (
	"context"

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
		VALUES ($1, $2, $3, $4, $5, false)
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
