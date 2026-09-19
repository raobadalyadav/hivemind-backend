package bookings

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/hivemind/backend/pkg/eventbus"
)

// noShowGrace is how long after a plan ends an unscanned booking waits
// before it can be marked no-show.
const noShowGrace = 2 * time.Hour

type planCompletedPayload struct {
	PlanID string `json:"plan_id"`
}

// CompleteEndedPlans moves published plans past their end to 'completed'
// and emits PLAN_COMPLETED (its first producer). Plans with no valid end
// (ends_at <= starts_at) are left alone.
func (r *Repository) CompleteEndedPlans(ctx context.Context, now time.Time) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		UPDATE plans SET status = 'completed'::plan_status, updated_at = now()
		WHERE status = 'published' AND ends_at > starts_at AND ends_at < $1::timestamptz
		RETURNING id::text`, now)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, id := range ids {
		payload, _ := json.Marshal(planCompletedPayload{PlanID: id})
		if err := eventbus.Enqueue(ctx, tx, "PLAN_COMPLETED", id, payload); err != nil {
			return 0, err
		}
	}
	return len(ids), tx.Commit(ctx)
}

type noShowPayload struct {
	BookingID string `json:"booking_id"`
	PlanID    string `json:"plan_id"`
	UserID    string `json:"user_id"`
}

// MarkNoShows marks confirmed bookings as no_show once the plan ended more
// than noShowGrace ago — but ONLY for plans where the host actually used
// check-in (≥1 checkins row). If the host never scanned anyone, absence of
// a scan proves nothing, and every attendee would be falsely penalised.
func (r *Repository) MarkNoShows(ctx context.Context, now time.Time) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		UPDATE bookings b SET status = 'no_show'::booking_status, updated_at = now()
		FROM plans p
		WHERE b.plan_id = p.id AND b.status = 'confirmed'
		  AND p.ends_at > p.starts_at
		  AND p.ends_at + $2::interval < $1::timestamptz
		  AND EXISTS (SELECT 1 FROM checkins c JOIN bookings b2 ON b2.id = c.booking_id WHERE b2.plan_id = p.id)
		RETURNING b.id::text, b.plan_id::text, b.user_id::text`,
		now, strconv.Itoa(int(noShowGrace.Seconds()))+" seconds",
	)
	if err != nil {
		return 0, err
	}
	var marked []noShowPayload
	for rows.Next() {
		var p noShowPayload
		if err := rows.Scan(&p.BookingID, &p.PlanID, &p.UserID); err != nil {
			rows.Close()
			return 0, err
		}
		marked = append(marked, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, p := range marked {
		payload, _ := json.Marshal(p)
		if err := eventbus.Enqueue(ctx, tx, "BOOKING_NO_SHOW", p.BookingID, payload); err != nil {
			return 0, err
		}
	}
	return len(marked), tx.Commit(ctx)
}
