// Package promotions implements PRD §13.11 "promo tools" / promoted
// listings — every RPC is fully implemented. Deliberately its own
// row-is-the-order table, kept separate from internal/payments' orders so
// the booking-payment path stays untouched (see cmd/api/webhook.go, which
// dispatches here as a fallback when a webhook's order id isn't a payments
// order).
package promotions

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pgUniqueViolation = "23505"

var ErrListingNotFound = errors.New("promotions: promoted listing not found")

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}

type PromotedListing struct {
	ID               string
	PlanID           string
	HostID           string
	AmountMinor      int64
	Currency         string
	GatewayOrderID   string
	GatewayPaymentID string
	Status           string
	StartsAt         time.Time
	EndsAt           time.Time
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Create(ctx context.Context, l *PromotedListing) (*PromotedListing, error) {
	out := *l
	out.Status = "pending_payment"
	err := r.pool.QueryRow(ctx, `
		INSERT INTO promoted_listings (plan_id, host_id, amount_minor, currency, starts_at, ends_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`,
		l.PlanID, l.HostID, l.AmountMinor, l.Currency, l.StartsAt, l.EndsAt,
	).Scan(&out.ID)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *Repository) SetGatewayOrderID(ctx context.Context, id, gatewayOrderID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE promoted_listings SET gateway_order_id = $2 WHERE id = $1`, id, gatewayOrderID,
	)
	return err
}

// FindByGatewayOrderID mirrors payments.FindOrderByCashfreeOrderID — the
// webhook's order_id is our own promoted_listings.id, sent to Cashfree as
// order_id (cf_order_id is only ever stored for reference).
func (r *Repository) FindByGatewayOrderID(ctx context.Context, id string) (*PromotedListing, error) {
	var l PromotedListing
	err := r.pool.QueryRow(ctx, `
		SELECT id, plan_id, host_id, amount_minor, currency, status
		FROM promoted_listings WHERE id = $1`, id,
	).Scan(&l.ID, &l.PlanID, &l.HostID, &l.AmountMinor, &l.Currency, &l.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrListingNotFound
		}
		return nil, err
	}
	return &l, nil
}

// MarkPaid is idempotent against webhook replay via the partial unique
// index on gateway_payment_id (migration 0023) — a second call with the
// same gatewayPaymentID hits the unique violation and is treated as an
// already-applied success rather than an error, same pattern as
// payments.Repository.MarkCaptured.
func (r *Repository) MarkPaid(ctx context.Context, id, gatewayPaymentID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE promoted_listings SET status = 'paid'::promoted_listing_status, gateway_payment_id = $2
		WHERE id = $1`,
		id, gatewayPaymentID,
	)
	if err != nil && isUniqueViolation(err) {
		return nil
	}
	return err
}

func (r *Repository) ListByHost(ctx context.Context, hostID string, limit int) ([]*PromotedListing, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, plan_id, host_id, amount_minor, currency, status, starts_at, ends_at
		FROM promoted_listings WHERE host_id = $1 ORDER BY created_at DESC LIMIT $2`,
		hostID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*PromotedListing
	for rows.Next() {
		var l PromotedListing
		if err := rows.Scan(&l.ID, &l.PlanID, &l.HostID, &l.AmountMinor, &l.Currency, &l.Status, &l.StartsAt, &l.EndsAt); err != nil {
			return nil, err
		}
		out = append(out, &l)
	}
	return out, rows.Err()
}
