package bookings

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/hivemind/backend/pkg/eventbus"
)

// waitlistOfferTTL is how long an offered seat is held for the head of the
// waitlist before it moves to the next person.
const waitlistOfferTTL = 30 * time.Minute

var ErrNotFull = errors.New("bookings: plan has free seats, book directly")

// WaitlistStatus state values.
const (
	WaitlistNone    = "none"
	WaitlistWaiting = "waiting"
	WaitlistOffered = "offered"
)

type WaitlistStatus struct {
	PlanID         string
	State          string
	Position       int32
	TotalWaiting   int32
	OfferExpiresAt *time.Time
}

type queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// activeHolds counts unexpired seat offers held for users other than
// exceptUserID. Expiry is lazy — an expired offer stops holding a seat the
// moment offer_expires_at passes, before the worker sweeper marks it.
func activeHolds(ctx context.Context, q queryer, planID, exceptUserID string) (int32, error) {
	var n int32
	err := q.QueryRow(ctx, `
		SELECT count(*) FROM waitlist_entries
		WHERE plan_id = $1 AND status = 'offered' AND offer_expires_at > now() AND user_id::text <> $2`,
		planID, exceptUserID,
	).Scan(&n)
	return n, err
}

// offerFreedSeats promotes the head of the waitlist into seat offers. The
// caller must already hold the plan row lock (Cancel's UPDATE plans, or an
// explicit SELECT ... FOR UPDATE), which serializes it against Create.
func offerFreedSeats(ctx context.Context, tx pgx.Tx, planID string, now time.Time) error {
	var capacity, confirmed int32
	var status string
	var startsAt time.Time
	if err := tx.QueryRow(ctx,
		`SELECT capacity, confirmed_count, status::text, starts_at FROM plans WHERE id = $1`, planID,
	).Scan(&capacity, &confirmed, &status, &startsAt); err != nil {
		return err
	}
	// A cancelled/completed/started plan must not create offers (the
	// PLAN_CANCELLED fan-out cancels every booking and frees every seat).
	if status != "published" || !startsAt.After(now) {
		return nil
	}
	holds, err := activeHolds(ctx, tx, planID, "")
	if err != nil {
		return err
	}
	free := capacity - confirmed - holds
	if free <= 0 {
		return nil
	}

	rows, err := tx.Query(ctx, `
		UPDATE waitlist_entries SET status = 'offered'::waitlist_status,
			offered_at = $3::timestamptz, offer_expires_at = $4::timestamptz, updated_at = $3::timestamptz
		WHERE id IN (
			SELECT id FROM waitlist_entries
			WHERE plan_id = $1 AND status = 'waiting'
			ORDER BY created_at, id LIMIT $2
			FOR UPDATE SKIP LOCKED)
		RETURNING user_id::text`,
		planID, free, now, now.Add(waitlistOfferTTL),
	)
	if err != nil {
		return err
	}
	var users []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			rows.Close()
			return err
		}
		users = append(users, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, u := range users {
		if err := eventbus.EnqueueNotifyUser(ctx, tx, eventbus.NotifyUserPayload{
			UserID:   u,
			Title:    "A seat opened up",
			Body:     "Claim your seat within 30 minutes before it goes to the next person.",
			DeepLink: "hivemind://plans/" + planID,
			Channel:  "push",
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) lockPlan(ctx context.Context, tx pgx.Tx, planID string) (capacity, confirmed int32, status string, startsAt time.Time, err error) {
	err = tx.QueryRow(ctx,
		`SELECT capacity, confirmed_count, status::text, starts_at FROM plans WHERE id = $1 FOR UPDATE`, planID,
	).Scan(&capacity, &confirmed, &status, &startsAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrPlanNotFound
	}
	return
}

// JoinWaitlist queues the caller for a full plan. The full-check runs under
// the plan row lock so a seat can't free up between the check and the insert.
func (r *Repository) JoinWaitlist(ctx context.Context, planID, userID string) (*WaitlistStatus, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	capacity, confirmed, status, startsAt, err := r.lockPlan(ctx, tx, planID)
	if err != nil {
		return nil, err
	}
	if status != "published" || !startsAt.After(time.Now()) {
		return nil, ErrPlanNotFound
	}

	var booked int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM bookings WHERE plan_id = $1 AND user_id = $2 AND status = 'confirmed'`, planID, userID,
	).Scan(&booked); err != nil {
		return nil, err
	}
	if booked > 0 {
		return nil, ErrAlreadyBooked
	}

	// A caller holding a live offer should just book — report OFFERED.
	if st, err := waitlistStatus(ctx, tx, planID, userID); err != nil {
		return nil, err
	} else if st.State == WaitlistOffered {
		return st, nil
	}

	holds, err := activeHolds(ctx, tx, planID, userID)
	if err != nil {
		return nil, err
	}
	if confirmed+holds < capacity {
		return nil, ErrNotFull
	}

	// Re-joining after left/expired/accepted goes to the back of the queue;
	// an existing waiting entry keeps its place.
	if _, err := tx.Exec(ctx, `
		INSERT INTO waitlist_entries (plan_id, user_id) VALUES ($1, $2)
		ON CONFLICT (plan_id, user_id) DO UPDATE SET
			status = 'waiting'::waitlist_status, created_at = now(),
			offered_at = NULL, offer_expires_at = NULL, updated_at = now()
		WHERE waitlist_entries.status IN ('left','expired','accepted')`,
		planID, userID,
	); err != nil {
		return nil, err
	}

	st, err := waitlistStatus(ctx, tx, planID, userID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return st, nil
}

// LeaveWaitlist is idempotent. Leaving while holding an offer releases the
// seat to the next person in the same transaction.
func (r *Repository) LeaveWaitlist(ctx context.Context, planID, userID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, _, _, _, err := r.lockPlan(ctx, tx, planID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE waitlist_entries SET status = 'left'::waitlist_status, updated_at = now()
		WHERE plan_id = $1 AND user_id = $2 AND status IN ('waiting','offered')`,
		planID, userID,
	); err != nil {
		return err
	}
	if err := offerFreedSeats(ctx, tx, planID, time.Now()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func waitlistStatus(ctx context.Context, q queryer, planID, userID string) (*WaitlistStatus, error) {
	st := &WaitlistStatus{PlanID: planID, State: WaitlistNone}

	var status string
	var expires *time.Time
	var createdAt time.Time
	var id string
	err := q.QueryRow(ctx, `
		SELECT status::text, offer_expires_at, created_at, id::text FROM waitlist_entries
		WHERE plan_id = $1 AND user_id = $2`, planID, userID,
	).Scan(&status, &expires, &createdAt, &id)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}

	switch {
	case status == "offered" && expires != nil && expires.After(time.Now()):
		st.State = WaitlistOffered
		st.OfferExpiresAt = expires
	case status == "waiting":
		st.State = WaitlistWaiting
		if err := q.QueryRow(ctx, `
			SELECT count(*) FROM waitlist_entries
			WHERE plan_id = $1 AND status = 'waiting' AND (created_at, id) <= ($2::timestamptz, $3::uuid)`,
			planID, createdAt, id,
		).Scan(&st.Position); err != nil {
			return nil, err
		}
		if err := q.QueryRow(ctx,
			`SELECT count(*) FROM waitlist_entries WHERE plan_id = $1 AND status = 'waiting'`, planID,
		).Scan(&st.TotalWaiting); err != nil {
			return nil, err
		}
	}
	return st, nil
}

func (r *Repository) GetWaitlistStatus(ctx context.Context, planID, userID string) (*WaitlistStatus, error) {
	return waitlistStatus(ctx, r.pool, planID, userID)
}

func (r *Repository) ListMyWaitlist(ctx context.Context, userID string) ([]*WaitlistStatus, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT plan_id::text FROM waitlist_entries
		WHERE user_id = $1 AND (status = 'waiting' OR (status = 'offered' AND offer_expires_at > now()))
		ORDER BY updated_at DESC LIMIT 50`, userID)
	if err != nil {
		return nil, err
	}
	var planIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		planIDs = append(planIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []*WaitlistStatus
	for _, id := range planIDs {
		st, err := waitlistStatus(ctx, r.pool, id, userID)
		if err != nil {
			return nil, err
		}
		if st.State != WaitlistNone {
			out = append(out, st)
		}
	}
	return out, nil
}

// HoldsForOthers is used by QuoteBooking so a quote never advertises a seat
// that is currently held for another user's waitlist offer.
func (r *Repository) HoldsForOthers(ctx context.Context, planID, userID string) (int32, error) {
	return activeHolds(ctx, r.pool, planID, userID)
}

// SweepWaitlist expires stale offers and re-offers the freed seats. Safe to
// run concurrently on several workers: the expiry is one UPDATE and offers
// use FOR UPDATE SKIP LOCKED under the plan row lock.
func (r *Repository) SweepWaitlist(ctx context.Context, now time.Time) (int, error) {
	rows, err := r.pool.Query(ctx, `
		UPDATE waitlist_entries SET status = 'expired'::waitlist_status, updated_at = $1::timestamptz
		WHERE status = 'offered' AND offer_expires_at <= $1::timestamptz
		RETURNING plan_id::text`, now)
	if err != nil {
		return 0, err
	}
	seen := map[string]bool{}
	var planIDs []string
	n := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		n++
		if !seen[id] {
			seen[id] = true
			planIDs = append(planIDs, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, planID := range planIDs {
		if err := r.offerForPlan(ctx, planID, now); err != nil {
			return n, err
		}
	}
	return n, nil
}

func (r *Repository) offerForPlan(ctx context.Context, planID string, now time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, _, _, _, err := r.lockPlan(ctx, tx, planID); err != nil {
		return err
	}
	if err := offerFreedSeats(ctx, tx, planID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ExpireWaitlistForPlan is called when a plan is cancelled — nobody should
// be offered a seat on a plan that no longer exists.
func (r *Repository) ExpireWaitlistForPlan(ctx context.Context, planID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE waitlist_entries SET status = 'expired'::waitlist_status, updated_at = now()
		WHERE plan_id = $1 AND status IN ('waiting','offered')`, planID)
	return err
}

type BookingSummary struct {
	Booking   Booking
	PlanTitle string
	StartsAt  time.Time
	EndsAt    time.Time
	Reviewed  bool
}

const (
	TabUpcoming  = "upcoming"
	TabPast      = "past"
	TabCancelled = "cancelled"
)

// ListMyBookings implements flow.md §17's Upcoming/Past/Cancelled tabs. A
// plan's end is GREATEST(ends_at, starts_at) so legacy rows with no
// end time behave as a point-in-time plan.
func (r *Repository) ListMyBookings(ctx context.Context, userID, tab string) ([]*BookingSummary, error) {
	var where, order string
	switch tab {
	case TabPast:
		where = `(b.status IN ('attended','no_show') OR (b.status = 'confirmed' AND GREATEST(p.ends_at, p.starts_at) < now()))`
		order = "p.starts_at DESC"
	case TabCancelled:
		where = `b.status IN ('cancelled','refunded')`
		order = "b.updated_at DESC"
	default:
		where = `(b.status = 'confirmed' AND GREATEST(p.ends_at, p.starts_at) >= now())`
		order = "p.starts_at ASC"
	}
	rows, err := r.pool.Query(ctx, `
		SELECT b.id, b.plan_id, b.user_id, b.status::text, b.price_minor, b.currency, b.created_at, b.updated_at,
			p.title, p.starts_at, p.ends_at,
			EXISTS (SELECT 1 FROM reviews rv WHERE rv.booking_id = b.id)
		FROM bookings b JOIN plans p ON p.id = b.plan_id
		WHERE b.user_id = $1 AND `+where+` ORDER BY `+order+` LIMIT 100`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*BookingSummary
	for rows.Next() {
		var s BookingSummary
		if err := rows.Scan(&s.Booking.ID, &s.Booking.PlanID, &s.Booking.UserID, &s.Booking.Status,
			&s.Booking.PriceMinor, &s.Booking.Currency, &s.Booking.CreatedAt, &s.Booking.UpdatedAt,
			&s.PlanTitle, &s.StartsAt, &s.EndsAt, &s.Reviewed); err != nil {
			return nil, err
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}
