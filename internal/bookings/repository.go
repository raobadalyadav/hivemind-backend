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
	"github.com/hivemind/backend/internal/notifications"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	// HoldExpiresAt is set while the booking is payment_pending: pay before it or the seat is released.
	HoldExpiresAt *time.Time
}

// PaymentHold is how long a seat stays reserved for someone who has started paying.
const PaymentHold = 15 * time.Minute

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
	// Seats offered to other waitlisted users are held for them until the
	// offer expires (expiry is lazy: only unexpired offers count).
	holds, err := activeHolds(ctx, tx, b.PlanID, b.UserID)
	if err != nil {
		return nil, err
	}
	if confirmedCount+holds >= capacity {
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
	out.PriceMinor = priceMinor
	out.Currency = currency

	if priceMinor > 0 {
		// A paid seat is only held until the payment lands. Someone who comes back to a hold they already
		// started gets that same booking (so retrying "Reserve" resumes the payment instead of stacking holds).
		var resumed Booking
		err := tx.QueryRow(ctx, `
			SELECT id, status::text, price_minor, currency, created_at, updated_at, expires_at FROM bookings
			WHERE plan_id = $1 AND user_id = $2 AND status = 'payment_pending' AND expires_at > now()`,
			b.PlanID, b.UserID,
		).Scan(&resumed.ID, &resumed.Status, &resumed.PriceMinor, &resumed.Currency, &resumed.CreatedAt, &resumed.UpdatedAt, &resumed.HoldExpiresAt)
		if err == nil {
			resumed.PlanID, resumed.UserID = b.PlanID, b.UserID
			return &resumed, tx.Commit(ctx)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		// Older, expired holds of theirs are finished for good.
		if _, err := tx.Exec(ctx, `UPDATE bookings SET status = 'cancelled', updated_at = now()
			WHERE plan_id = $1 AND user_id = $2 AND status = 'payment_pending'`, b.PlanID, b.UserID); err != nil {
			return nil, err
		}
		out.Status = "payment_pending"
		expires := time.Now().Add(PaymentHold)
		out.HoldExpiresAt = &expires
		if err := tx.QueryRow(ctx, `
			INSERT INTO bookings (plan_id, user_id, status, price_minor, currency, idempotency_key, expires_at)
			VALUES ($1, $2, 'payment_pending', $3, $4, NULLIF($5, ''), $6)
			RETURNING id, created_at, updated_at`,
			b.PlanID, b.UserID, priceMinor, currency, b.IdempotencyKey, expires,
		).Scan(&out.ID, &out.CreatedAt, &out.UpdatedAt); err != nil {
			return nil, err
		}
		return &out, tx.Commit(ctx)
	}

	out.Status = "confirmed"
	err = tx.QueryRow(ctx, `
		INSERT INTO bookings (plan_id, user_id, status, price_minor, currency, idempotency_key)
		VALUES ($1, $2, 'confirmed', $3, $4, NULLIF($5, ''))
		RETURNING id, created_at, updated_at`,
		b.PlanID, b.UserID, priceMinor, currency, b.IdempotencyKey,
	).Scan(&out.ID, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if err := seatConfirmedBooking(ctx, tx, &out); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	r.notifyHost(ctx, out.PlanID, out.UserID)
	return &out, nil
}

// seatConfirmedBooking does everything a confirmed booking implies, inside the caller's transaction (which
// must hold the plan row lock): the participant row, the seat count, the consumed waitlist entry and the
// BOOKING_CONFIRMED event. Free bookings run it at creation; paid ones when the payment is confirmed.
func seatConfirmedBooking(ctx context.Context, tx pgx.Tx, out *Booking) error {
	// UNIQUE(plan_id,user_id) + a surviving 'cancelled' row would make a
	// cancel-then-rebook (e.g. accepting a waitlist offer) fail with 23505,
	// so a returning user re-activates their row instead.
	if _, err := tx.Exec(ctx, `
		INSERT INTO plan_participants (plan_id, user_id, booking_id) VALUES ($1, $2, $3)
		ON CONFLICT (plan_id, user_id) DO UPDATE SET
			status = 'confirmed', booking_id = EXCLUDED.booking_id,
			joined_at = now(), checked_in_at = NULL`,
		out.PlanID, out.UserID, out.ID,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE plans SET confirmed_count = confirmed_count + 1 WHERE id = $1`, out.PlanID,
	); err != nil {
		return err
	}
	// Booking a seat consumes the caller's waitlist entry (offered or waiting).
	if _, err := tx.Exec(ctx, `
		UPDATE waitlist_entries SET status = 'accepted'::waitlist_status, updated_at = now()
		WHERE plan_id = $1 AND user_id = $2 AND status IN ('waiting','offered')`,
		out.PlanID, out.UserID,
	); err != nil {
		return err
	}
	payload, err := json.Marshal(outboxPayload{BookingID: out.ID, PlanID: out.PlanID, UserID: out.UserID})
	if err != nil {
		return err
	}
	return eventbus.Enqueue(ctx, tx, "BOOKING_CONFIRMED", out.ID, payload)
}

// ConfirmPaid turns a payment_pending booking into a confirmed one once its payment is captured. It is
// idempotent (already confirmed → nil). If the hold expired and someone else took the last seat,
// it returns ErrPlanFull and the caller refunds the payment.
func (r *Repository) ConfirmPaid(ctx context.Context, bookingID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var b Booking
	var status string
	if err := tx.QueryRow(ctx, `SELECT plan_id::text, user_id::text, status::text, price_minor, currency FROM bookings WHERE id = $1`, bookingID).
		Scan(&b.PlanID, &b.UserID, &status, &b.PriceMinor, &b.Currency); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isBadUUID(err) {
			return ErrBookingNotFound
		}
		return err
	}
	b.ID = bookingID
	if status == "confirmed" || status == "attended" {
		return nil
	}
	if status != "payment_pending" && status != "cancelled" { // cancelled = the hold expired before the payment arrived
		return ErrBookingNotFound
	}
	var capacity, confirmed int32
	var planStatus string
	if err := tx.QueryRow(ctx, `SELECT capacity, confirmed_count, status::text FROM plans WHERE id = $1 FOR UPDATE`, b.PlanID).Scan(&capacity, &confirmed, &planStatus); err != nil {
		return err
	}
	holds, err := activeHolds(ctx, tx, b.PlanID, b.UserID)
	if err != nil {
		return err
	}
	if planStatus != "published" || confirmed+holds >= capacity {
		return ErrPlanFull
	}
	if _, err := tx.Exec(ctx, `UPDATE bookings SET status = 'confirmed', expires_at = NULL, updated_at = now() WHERE id = $1`, bookingID); err != nil {
		return err
	}
	if err := seatConfirmedBooking(ctx, tx, &b); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	r.notifyHost(ctx, b.PlanID, b.UserID)
	return nil
}

// ExpirePending releases seats whose payment never came. Safe to run on several workers at once.
func (r *Repository) ExpirePending(ctx context.Context, now time.Time) (int, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE bookings SET status = 'cancelled', updated_at = now()
		WHERE status = 'payment_pending' AND expires_at <= $1`, now)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// notifyHost tells a plan's host that someone booked a seat (best-effort).
func (r *Repository) notifyHost(ctx context.Context, planID, attendeeID string) {
	var host, title string
	if err := r.pool.QueryRow(ctx, `SELECT host_id::text, title FROM plans WHERE id = $1`, planID).Scan(&host, &title); err != nil {
		return
	}
	_, _ = notifications.Emit(ctx, r.pool, notifications.Event{
		UserID: host, ActorID: attendeeID, Type: notifications.TypePlanAttendee, TargetID: planID,
		Title: "{actor} joined your plan", Body: title, DeepLink: "hivemind://plans/" + planID,
		DedupeKey: "attendee:" + planID + ":" + attendeeID,
	})
}

func isBadUUID(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "22P02"
}

func (r *Repository) Get(ctx context.Context, id string) (*Booking, error) {
	var b Booking
	err := r.pool.QueryRow(ctx, `
		SELECT id, plan_id, user_id, status, price_minor, currency, created_at, updated_at, expires_at
		FROM bookings WHERE id = $1`, id,
	).Scan(&b.ID, &b.PlanID, &b.UserID, &b.Status, &b.PriceMinor, &b.Currency, &b.CreatedAt, &b.UpdatedAt, &b.HoldExpiresAt)
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
	// RefundPercent of what was paid goes back (0–100), decided at cancellation time by RefundPercent.
	RefundPercent int `json:"refund_percent"`
}

// RefundFullHours: cancel at least this long before the start for a full refund.
const RefundFullHours = 24

// RefundPercent is the platform cancellation policy: everything back when the host cancels the plan (byHost)
// or the guest cancels at least RefundFullHours before it starts; nothing once it's closer than that.
func RefundPercent(startsAt, now time.Time, byHost bool) int {
	if byHost || startsAt.Sub(now) >= RefundFullHours*time.Hour {
		return 100
	}
	return 0
}

// Cancel transitions the booking to 'cancelled', decrements the plan's
// confirmed_count, marks the plan_participants row cancelled, and writes a
// BOOKING_CANCELLED outbox event — all transactionally, mirroring Create's
// shape. The worker consumes that event to evaluate a refund (see
// internal/payments) when a captured payment exists. A booking still waiting for its payment is simply
// released (no seat was taken, nothing was charged). byHost marks a host/plan cancellation (full refund).
func (r *Repository) Cancel(ctx context.Context, bookingID, reason string, byHost bool) (*Booking, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var b Booking
	var prev string
	if err := tx.QueryRow(ctx, `SELECT status::text FROM bookings WHERE id = $1 FOR UPDATE`, bookingID).Scan(&prev); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isBadUUID(err) {
			return nil, ErrBookingNotFound
		}
		return nil, err
	}
	err = tx.QueryRow(ctx, `
		UPDATE bookings SET status = 'cancelled', updated_at = now()
		WHERE id = $1 AND status IN ('confirmed','payment_pending')
		RETURNING id, plan_id, user_id, status, price_minor, currency, created_at, updated_at`,
		bookingID,
	).Scan(&b.ID, &b.PlanID, &b.UserID, &b.Status, &b.PriceMinor, &b.Currency, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBookingNotFound
		}
		return nil, err
	}
	if prev == "payment_pending" {
		return &b, tx.Commit(ctx) // nothing was charged and no seat was taken
	}

	var startsAt time.Time
	if err := tx.QueryRow(ctx, `SELECT starts_at FROM plans WHERE id = $1`, b.PlanID).Scan(&startsAt); err != nil {
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

	// The UPDATE plans above holds the plan row lock for this tx, so the
	// freed seat can be offered to the head of the waitlist race-free.
	if err := offerFreedSeats(ctx, tx, b.PlanID, time.Now()); err != nil {
		return nil, err
	}

	payload, err := json.Marshal(cancelOutboxPayload{BookingID: b.ID, PlanID: b.PlanID, UserID: b.UserID, Reason: reason, RefundPercent: RefundPercent(startsAt, time.Now(), byHost)})
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

	if _, err := tx.Exec(ctx,
		`UPDATE plan_participants SET checked_in_at = now() WHERE booking_id = $1`, bookingID,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &b, nil
}
