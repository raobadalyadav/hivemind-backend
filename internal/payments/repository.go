// Package payments implements PRD §13.13 Payments — every RPC is fully
// implemented against the ledger tables. Real Razorpay calls belong behind a
// gateway.Client interface — not implemented here, per the plan's non-goal
// of no real gateway integration; CreateOrder writes the row a real gateway
// webhook would later mark captured, but nothing here calls out to Razorpay.
package payments

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrPaymentNotFound      = errors.New("payments: payment not found")
	ErrPaymentNotRefundable = errors.New("payments: payment is not in a refundable state")
)

type Order struct {
	ID             string
	BookingID      string
	AmountMinor    int64
	Currency       string
	GatewayOrderID string
	Status         string
}

type Payment struct {
	ID          string
	OrderID     string
	AmountMinor int64
	Currency    string
	Status      string
}

type Refund struct {
	ID          string
	PaymentID   string
	AmountMinor int64
	Status      string
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) CreateOrder(ctx context.Context, o *Order) (*Order, error) {
	out := *o
	out.Status = "created"
	err := r.pool.QueryRow(ctx, `
		INSERT INTO orders (booking_id, amount_minor, currency, status)
		VALUES ($1, $2, $3, 'created')
		RETURNING id`,
		o.BookingID, o.AmountMinor, o.Currency,
	).Scan(&out.ID)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *Repository) GetPayment(ctx context.Context, id string) (*Payment, error) {
	var p Payment
	err := r.pool.QueryRow(ctx,
		`SELECT id, order_id, amount_minor, currency, status FROM payments WHERE id = $1`, id,
	).Scan(&p.ID, &p.OrderID, &p.AmountMinor, &p.Currency, &p.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPaymentNotFound
		}
		return nil, err
	}
	return &p, nil
}

// FindCapturedPaymentForBooking is used by cmd/worker's BOOKING_CANCELLED
// handler to decide whether there's anything to refund.
func (r *Repository) FindCapturedPaymentForBooking(ctx context.Context, bookingID string) (*Payment, error) {
	var p Payment
	err := r.pool.QueryRow(ctx, `
		SELECT p.id, p.order_id, p.amount_minor, p.currency, p.status
		FROM payments p JOIN orders o ON o.id = p.order_id
		WHERE o.booking_id = $1 AND p.status = 'captured'
		ORDER BY p.created_at DESC LIMIT 1`,
		bookingID,
	).Scan(&p.ID, &p.OrderID, &p.AmountMinor, &p.Currency, &p.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPaymentNotFound
		}
		return nil, err
	}
	return &p, nil
}

// CreateRefund inserts the refund and an append-only reconciliation_entries
// row (PRD §29) in one transaction — RefundPayment never touches
// payments.status (a captured payment stays captured; the refund is a
// separate ledger object, matching real gateway semantics).
func (r *Repository) CreateRefund(ctx context.Context, paymentID string, amountMinor int64, reason string) (*Refund, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var status string
	if err := tx.QueryRow(ctx,
		`SELECT status FROM payments WHERE id = $1 FOR UPDATE`, paymentID,
	).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPaymentNotFound
		}
		return nil, err
	}
	if status != "captured" {
		return nil, ErrPaymentNotRefundable
	}

	var refund Refund
	refund.PaymentID = paymentID
	refund.AmountMinor = amountMinor
	refund.Status = "processed"
	if err := tx.QueryRow(ctx, `
		INSERT INTO refunds (payment_id, amount_minor, reason, status)
		VALUES ($1, $2, $3, 'processed')
		RETURNING id`,
		paymentID, amountMinor, reason,
	).Scan(&refund.ID); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO reconciliation_entries (entry_type, reference_id, amount_minor) VALUES ('refund', $1, $2)`,
		refund.ID, amountMinor,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &refund, nil
}
