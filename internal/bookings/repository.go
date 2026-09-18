// Package bookings implements PRD §13.6 Booking — every RPC is fully
// implemented. CreateBooking enforces capacity transactionally (PRD §8
// "Capacity must be enforced transactionally in PostgreSQL; no overbooking
// under concurrent requests") and writes a BOOKING_CONFIRMED outbox event
// (PRD §19) in the same transaction.
package bookings

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hivemind/backend/pkg/eventbus"
)

var (
	ErrPlanFull        = errors.New("bookings: plan is at capacity")
	ErrAlreadyBooked   = errors.New("bookings: user already has an active booking for this plan")
	ErrPlanNotFound    = errors.New("bookings: plan not found")
	ErrBookingNotFound = errors.New("bookings: booking not found")
)

type Booking struct {
	ID             string
	PlanID         string
	UserID         string
	Status         string
	PriceMinor     int64
	Currency       string
	IdempotencyKey string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type outboxPayload struct {
	BookingID string `json:"booking_id"`
	PlanID    string `json:"plan_id"`
	UserID    string `json:"user_id"`
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Create locks the plan row, checks capacity, inserts the booking, inserts
// the plan_participants row, increments confirmed_count, and enqueues a
// BOOKING_CONFIRMED outbox event — all in one transaction so a concurrent
// request can never both pass the capacity check.
func (r *Repository) Create(ctx context.Context, b *Booking) (*Booking, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Price is always sourced from the locked plan row, never trusted from
	// the caller — CreateBookingRequest doesn't even carry a price field.
	var capacity, confirmedCount int32
	var priceMinor int64
	var currency, planStatus string
	err = tx.QueryRow(ctx,
		`SELECT capacity, confirmed_count, price_minor, currency, status FROM plans WHERE id = $1 FOR UPDATE`,
		b.PlanID,
	).Scan(&capacity, &confirmedCount, &priceMinor, &currency, &planStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlanNotFound
		}
		return nil, err
	}
	if planStatus != "published" {
		return nil, ErrPlanNotFound
	}
	if confirmedCount >= capacity {
		return nil, ErrPlanFull
	}

	var existing int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM bookings WHERE plan_id = $1 AND user_id = $2 AND status = 'confirmed'`,
		b.PlanID, b.UserID,
	).Scan(&existing); err != nil {
		return nil, err
	}
	if existing > 0 {
		return nil, ErrAlreadyBooked
	}

	out := *b
	out.Status = "confirmed"
	out.PriceMinor = priceMinor
	out.Currency = currency
	err = tx.QueryRow(ctx, `
		INSERT INTO bookings (plan_id, user_id, status, price_minor, currency, idempotency_key)
		VALUES ($1, $2, 'confirmed', $3, $4, NULLIF($5, ''))
		RETURNING id, created_at, updated_at`,
		b.PlanID, b.UserID, priceMinor, currency, b.IdempotencyKey,
	).Scan(&out.ID, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO plan_participants (plan_id, user_id, booking_id) VALUES ($1, $2, $3)`,
		b.PlanID, b.UserID, out.ID,
	); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE plans SET confirmed_count = confirmed_count + 1 WHERE id = $1`, b.PlanID,
	); err != nil {
		return nil, err
	}

	payload, err := json.Marshal(outboxPayload{BookingID: out.ID, PlanID: out.PlanID, UserID: out.UserID})
	if err != nil {
		return nil, err
	}
	if err := eventbus.Enqueue(ctx, tx, "BOOKING_CONFIRMED", out.ID, payload); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *Repository) Get(ctx context.Context, id string) (*Booking, error) {
	var b Booking
	err := r.pool.QueryRow(ctx, `
		SELECT id, plan_id, user_id, status, price_minor, currency, created_at, updated_at
		FROM bookings WHERE id = $1`, id,
	).Scan(&b.ID, &b.PlanID, &b.UserID, &b.Status, &b.PriceMinor, &b.Currency, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBookingNotFound
		}
		return nil, err
	}
	return &b, nil
}

type PlanPricing struct {
	PriceMinor     int64
	Currency       string
	Capacity       int32
	ConfirmedCount int32
	Status         string
	StartsAt       time.Time
}

func (r *Repository) GetPlanPricing(ctx context.Context, planID string) (*PlanPricing, error) {
	var p PlanPricing
	err := r.pool.QueryRow(ctx,
		`SELECT price_minor, currency, capacity, confirmed_count, status, starts_at FROM plans WHERE id = $1`,
		planID,
	).Scan(&p.PriceMinor, &p.Currency, &p.Capacity, &p.ConfirmedCount, &p.Status, &p.StartsAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlanNotFound
		}
		return nil, err
	}
	return &p, nil
}

// GetPlanHostID is used by the service layer to authorize host-only actions
// (CancelBooking, CheckIn) without pulling in internal/plans as a dependency.
func (r *Repository) GetPlanHostID(ctx context.Context, planID string) (string, error) {
	var hostID string
	err := r.pool.QueryRow(ctx, `SELECT host_id::text FROM plans WHERE id = $1`, planID).Scan(&hostID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrPlanNotFound
		}
		return "", err
	}
	return hostID, nil
}

// FindActiveBookingID looks up a user's confirmed booking for a plan — used
// by LeavePlan, which only carries (plan_id, user_id), not a booking id.
func (r *Repository) FindActiveBookingID(ctx context.Context, planID, userID string) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx,
		`SELECT id FROM bookings WHERE plan_id = $1 AND user_id = $2 AND status = 'confirmed'`,
		planID, userID,
	).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrBookingNotFound
		}
		return "", err
	}
	return id, nil
}

// ListConfirmedBookingIDsForPlan is used by cmd/worker's PLAN_CANCELLED
// handler to fan out per-booking cancellation.
func (r *Repository) ListConfirmedBookingIDsForPlan(ctx context.Context, planID string) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id FROM bookings WHERE plan_id = $1 AND status = 'confirmed'`, planID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type cancelOutboxPayload struct {
	BookingID string `json:"booking_id"`
	PlanID    string `json:"plan_id"`
	UserID    string `json:"user_id"`
	Reason    string `json:"reason"`
}

// Cancel transitions the booking to 'cancelled', decrements the plan's
// confirmed_count, marks the plan_participants row cancelled, and writes a
// BOOKING_CANCELLED outbox event — all transactionally, mirroring Create's
// shape. The worker consumes that event to evaluate a refund (see
// internal/payments) when a captured payment exists.
func (r *Repository) Cancel(ctx context.Context, bookingID, reason string) (*Booking, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var b Booking
	err = tx.QueryRow(ctx, `
		UPDATE bookings SET status = 'cancelled', updated_at = now()
		WHERE id = $1 AND status = 'confirmed'
		RETURNING id, plan_id, user_id, status, price_minor, currency, created_at, updated_at`,
		bookingID,
	).Scan(&b.ID, &b.PlanID, &b.UserID, &b.Status, &b.PriceMinor, &b.Currency, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBookingNotFound
		}
		return nil, err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE plans SET confirmed_count = confirmed_count - 1 WHERE id = $1`, b.PlanID,
	); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE plan_participants SET status = 'cancelled' WHERE booking_id = $1`, b.ID,
	); err != nil {
		return nil, err
	}

	payload, err := json.Marshal(cancelOutboxPayload{BookingID: b.ID, PlanID: b.PlanID, UserID: b.UserID, Reason: reason})
	if err != nil {
		return nil, err
	}
	if err := eventbus.Enqueue(ctx, tx, "BOOKING_CANCELLED", b.ID, payload); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &b, nil
}

// CheckIn marks attendance: booking status → 'attended', checked_in_at set,
// and a checkins row inserted for the audit trail (PRD §31 "Host/admin can
// validate attendance and it is reflected in booking history").
func (r *Repository) CheckIn(ctx context.Context, bookingID, checkedInBy string) (*Booking, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var b Booking
	err = tx.QueryRow(ctx, `
		UPDATE bookings SET status = 'attended', checked_in_at = now(), updated_at = now()
		WHERE id = $1 AND status = 'confirmed'
		RETURNING id, plan_id, user_id, status, price_minor, currency, created_at, updated_at`,
		bookingID,
	).Scan(&b.ID, &b.PlanID, &b.UserID, &b.Status, &b.PriceMinor, &b.Currency, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBookingNotFound
		}
		return nil, err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO checkins (booking_id, checked_in_by) VALUES ($1, NULLIF($2,'')::uuid)`,
		bookingID, checkedInBy,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &b, nil
}
