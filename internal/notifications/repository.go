// Package notifications implements PRD §13.15 Notifications — every RPC is
// fully implemented. SendNotification is called internally by cmd/worker
// event handlers (see PRD §19), not exposed to mobile clients directly.
// Delivery is real: email via Resend (pkg/email), push via Firebase Cloud
// Messaging (pkg/push) — see service.go.
package notifications

import (
	"context"
	"time"

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

	// Social events (see emit.go); empty on older rows.
	Type          string
	ActorID       string
	ActorName     string
	ActorPhotoURL string
	TargetID      string
	CreatedAt     time.Time
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

// ListForUser returns the newest notifications first. before (zero = newest)
// is the created_at of the last row of the previous page.
func (r *Repository) ListForUser(ctx context.Context, userID string, limit int, before time.Time) ([]*Notification, error) {
	var cursor *time.Time
	if !before.IsZero() {
		cursor = &before
	}
	rows, err := r.pool.Query(ctx, `
		SELECT n.id, n.user_id, n.channel, n.title, n.body, COALESCE(n.deep_link,''), n.read,
		       COALESCE(n.type,''), COALESCE(n.actor_id::text,''), COALESCE(up.display_name,''),
		       COALESCE((SELECT COALESCE(NULLIF(pp.thumb_url,''), pp.url) FROM profile_photos pp
		                 WHERE pp.user_id = n.actor_id ORDER BY pp.position, pp.created_at LIMIT 1),''),
		       COALESCE(n.target_id,''), n.created_at
		FROM notifications n LEFT JOIN user_profiles up ON up.user_id = n.actor_id
		WHERE n.user_id = $1 AND ($3::timestamptz IS NULL OR n.created_at < $3)
		ORDER BY n.created_at DESC LIMIT $2`,
		userID, limit, cursor,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.UserID, &n.Channel, &n.Title, &n.Body, &n.DeepLink, &n.Read,
			&n.Type, &n.ActorID, &n.ActorName, &n.ActorPhotoURL, &n.TargetID, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &n)
	}
	return out, rows.Err()
}

// MarkRead marks the caller's own notifications read (ids, or every unread one
// when all) and returns the unread count afterwards.
func (r *Repository) MarkRead(ctx context.Context, userID string, ids []string, all bool) (int, error) {
	if all {
		if _, err := r.pool.Exec(ctx, `UPDATE notifications SET read = true WHERE user_id = $1 AND NOT read`, userID); err != nil {
			return 0, err
		}
	} else if len(ids) > 0 {
		if _, err := r.pool.Exec(ctx, `UPDATE notifications SET read = true WHERE user_id = $1 AND id = ANY($2::uuid[]) AND NOT read`, userID, ids); err != nil {
			return 0, err
		}
	}
	return r.UnreadCount(ctx, userID)
}

func (r *Repository) UnreadCount(ctx context.Context, userID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id = $1 AND NOT read`, userID).Scan(&n)
	return n, err
}

// MutedCategories are the categories the user turned off.
func (r *Repository) MutedCategories(ctx context.Context, userID string) ([]string, error) {
	var muted []string
	err := r.pool.QueryRow(ctx, `SELECT muted_categories FROM notification_preferences WHERE user_id = $1`, userID).Scan(&muted)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return muted, err
}

func (r *Repository) SetMutedCategories(ctx context.Context, userID string, muted []string) error {
	if muted == nil {
		muted = []string{}
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO notification_preferences (user_id, muted_categories) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET muted_categories = EXCLUDED.muted_categories`, userID, muted)
	return err
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
