// Package reviews implements PRD §13.11 "reviews" (host/venue dashboard
// requirement) — every RPC is fully implemented. Rating is always computed
// live via AVG(); no cached column anywhere avoids a write-side sync
// problem nothing has asked for.
package reviews

import (
	"strconv"

	"context"
	"github.com/hivemind/backend/internal/notifications"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Review struct {
	ID      string
	PlanID  string
	UserID  string
	Rating  int32
	Comment string
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Create is idempotent via ON CONFLICT (booking_id) DO NOTHING (migration
// 0023's UNIQUE constraint) — a repeated CreateReview call for the same
// booking is a no-op, not an error. Returns the row that now exists either
// way (rows.Scan on the RETURNING clause is empty on conflict, so a
// follow-up SELECT is used in that case).
func (r *Repository) Create(ctx context.Context, bookingID, planID, userID string, rating int32, comment string) (*Review, error) {
	var rv Review
	rv.PlanID, rv.UserID, rv.Rating, rv.Comment = planID, userID, rating, comment
	err := r.pool.QueryRow(ctx, `
		INSERT INTO reviews (booking_id, plan_id, user_id, rating, comment)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (booking_id) DO NOTHING
		RETURNING id`,
		bookingID, planID, userID, rating, comment,
	).Scan(&rv.ID)
	if err != nil {
		// ON CONFLICT DO NOTHING with no row inserted returns pgx.ErrNoRows
		// from the RETURNING scan — look up the existing review instead.
		return r.getByBookingID(ctx, bookingID)
	}
	var host, title string
	if r.pool.QueryRow(ctx, `SELECT host_id::text, title FROM plans WHERE id = $1`, planID).Scan(&host, &title) == nil {
		_, _ = notifications.Emit(ctx, r.pool, notifications.Event{
			UserID: host, ActorID: userID, Type: notifications.TypeReviewReceived, TargetID: planID,
			Title: "{actor} rated your plan " + strconv.Itoa(int(rating)) + "★", Body: title,
			DeepLink: "hivemind://plans/" + planID, DedupeKey: "review:" + rv.ID,
		})
	}
	return &rv, nil
}

func (r *Repository) getByBookingID(ctx context.Context, bookingID string) (*Review, error) {
	var rv Review
	err := r.pool.QueryRow(ctx,
		`SELECT id, plan_id, user_id, rating, comment FROM reviews WHERE booking_id = $1`, bookingID,
	).Scan(&rv.ID, &rv.PlanID, &rv.UserID, &rv.Rating, &rv.Comment)
	if err != nil {
		return nil, err
	}
	return &rv, nil
}

func (r *Repository) ListForPlan(ctx context.Context, planID string, limit int) ([]*Review, float64, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, plan_id, user_id, rating, comment FROM reviews WHERE plan_id = $1 ORDER BY created_at DESC LIMIT $2`,
		planID, limit,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []*Review
	for rows.Next() {
		var rv Review
		if err := rows.Scan(&rv.ID, &rv.PlanID, &rv.UserID, &rv.Rating, &rv.Comment); err != nil {
			return nil, 0, err
		}
		out = append(out, &rv)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	var avg float64
	if err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(AVG(rating),0) FROM reviews WHERE plan_id = $1`, planID,
	).Scan(&avg); err != nil {
		return nil, 0, err
	}
	return out, avg, nil
}
