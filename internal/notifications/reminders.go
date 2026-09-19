package notifications

import (
	"context"
	"fmt"
	"time"
)

// Reminder buckets (flow.md §40: 24h / 3h / 1h before). The tightest bucket
// a booking currently falls in is the only one it can claim on a given tick.
type reminder struct {
	BookingID string
	UserID    string
	PlanID    string
	Title     string
	StartsAt  time.Time
	Kind      string
}

// claimDueReminders atomically claims (booking, kind) pairs that are due and
// not yet sent — the INSERT ... ON CONFLICT DO NOTHING is the idempotency
// guard across ticks and worker replicas. A booking made after a bucket's
// threshold never gets that bucket's reminder (no "3h reminder" minutes
// after booking).
func (r *Repository) claimDueReminders(ctx context.Context, now time.Time) ([]reminder, error) {
	rows, err := r.pool.Query(ctx, `
		WITH due AS (
			SELECT b.id AS booking_id, b.user_id, p.id AS plan_id, p.title, p.starts_at, b.created_at,
				CASE WHEN p.starts_at - $1::timestamptz <= interval '1 hour' THEN '1h'
				     WHEN p.starts_at - $1::timestamptz <= interval '3 hours' THEN '3h'
				     ELSE '24h' END AS kind
			FROM bookings b JOIN plans p ON p.id = b.plan_id
			WHERE b.status = 'confirmed' AND p.status = 'published'
			  AND p.starts_at > $1::timestamptz AND p.starts_at <= $1::timestamptz + interval '24 hours'
		), eligible AS (
			SELECT * FROM due
			WHERE created_at < starts_at - CASE kind
				WHEN '1h' THEN interval '1 hour' WHEN '3h' THEN interval '3 hours' ELSE interval '24 hours' END
		), claimed AS (
			INSERT INTO booking_reminders (booking_id, kind)
			SELECT booking_id, kind FROM eligible
			ON CONFLICT DO NOTHING
			RETURNING booking_id, kind
		)
		SELECT e.booking_id::text, e.user_id::text, e.plan_id::text, e.title, e.starts_at, e.kind
		FROM claimed c JOIN eligible e ON e.booking_id = c.booking_id AND e.kind = c.kind`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []reminder
	for rows.Next() {
		var x reminder
		if err := rows.Scan(&x.BookingID, &x.UserID, &x.PlanID, &x.Title, &x.StartsAt, &x.Kind); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (r *Repository) releaseReminderClaim(ctx context.Context, bookingID, kind string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM booking_reminders WHERE booking_id = $1 AND kind = $2`, bookingID, kind)
	return err
}

// SendDueReminders sends every due reminder exactly once and returns how
// many were sent. 24h reminders are in-app; the closer 3h/1h ones are push —
// "avoid excessive push notifications" (flow.md §40). A failed send releases
// its claim so the next tick retries.
func (s *Service) SendDueReminders(ctx context.Context, now time.Time) (int, error) {
	due, err := s.repo.claimDueReminders(ctx, now)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, d := range due {
		channel := "push"
		if d.Kind == "24h" {
			channel = "in_app"
		}
		_, err := s.SendNotification(ctx, &Notification{
			UserID:   d.UserID,
			Channel:  channel,
			Title:    fmt.Sprintf("Reminder: %s", d.Title),
			Body:     fmt.Sprintf("Starts in about %s.", map[string]string{"24h": "a day", "3h": "3 hours", "1h": "an hour"}[d.Kind]),
			DeepLink: "hivemind://bookings/" + d.BookingID,
		})
		if err != nil {
			s.logger.Error("send reminder", "error", err, "booking_id", d.BookingID, "kind", d.Kind)
			if rerr := s.repo.releaseReminderClaim(ctx, d.BookingID, d.Kind); rerr != nil {
				s.logger.Error("release reminder claim", "error", rerr)
			}
			continue
		}
		sent++
	}
	return sent, nil
}
