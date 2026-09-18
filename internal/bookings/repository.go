// Package bookings implements PRD §13.6 Booking. CreateBooking/GetBooking are
// the fully working vertical slice: CreateBooking enforces capacity
// transactionally (PRD §8 "Capacity must be enforced transactionally in
// PostgreSQL; no overbooking under concurrent requests") and writes a
// BOOKING_CONFIRMED outbox event (PRD §19) in the same transaction.
package bookings

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hivemind/backend/pkg/eventbus"
)

var ErrPlanFull = errors.New("bookings: plan is at capacity")

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

// Create locks the plan row, checks capacity, inserts the booking, increments
// confirmed_count, and enqueues a BOOKING_CONFIRMED outbox event — all in one
// transaction so a concurrent request can never both pass the capacity check.
//
// TODO(phase1): also insert a plan_participants row (PRD §18's representative
// schema for participant visibility/check-in) — this scaffold only tracks
// the booking + confirmed_count for now.
func (r *Repository) Create(ctx context.Context, b *Booking) (*Booking, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var capacity, confirmedCount int32
	err = tx.QueryRow(ctx,
		`SELECT capacity, confirmed_count FROM plans WHERE id = $1 FOR UPDATE`,
		b.PlanID,
	).Scan(&capacity, &confirmedCount)
	if err != nil {
		return nil, err
	}
	if confirmedCount >= capacity {
		return nil, ErrPlanFull
	}

	out := *b
	out.Status = "confirmed"
	err = tx.QueryRow(ctx, `
		INSERT INTO bookings (plan_id, user_id, status, price_minor, currency, idempotency_key)
		VALUES ($1, $2, 'confirmed', $3, $4, NULLIF($5, ''))
		RETURNING id, created_at, updated_at`,
		b.PlanID, b.UserID, b.PriceMinor, b.Currency, b.IdempotencyKey,
	).Scan(&out.ID, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
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
		return nil, err
	}
	return &b, nil
}
