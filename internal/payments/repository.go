// Package payments implements PRD §13.13 Payments against the ledger
// tables, with real Cashfree calls behind the GatewayClient interface (see
// service.go) — CreateOrder actually calls Cashfree's Orders API, and
// MarkCaptured is what cmd/api's webhook HTTP handler calls once Cashfree
// confirms a payment, closing the loop this scaffold previously only wrote
// half of (a payments row was never created anywhere before this).
package payments

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pgUniqueViolation = "23505"

var (
	ErrPaymentNotFound      = errors.New("payments: payment not found")
	ErrPaymentNotRefundable = errors.New("payments: payment is not in a refundable state")
)

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}

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

// SetGatewayOrderID is called after CreateOrder's initial insert, once
// Cashfree has returned its cf_order_id — the order must exist first since
// its own UUID is what's sent to Cashfree as order_id.
func (r *Repository) SetGatewayOrderID(ctx context.Context, orderID, gatewayOrderID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE orders SET gateway_order_id = $2 WHERE id = $1`, orderID, gatewayOrderID,
	)
	return err
}

// FindOrderByCashfreeOrderID is used by the Cashfree webhook handler. The
// webhook's data.order.order_id is the value CreateOrder sent Cashfree as
// order_id — which is our own orders.id, not Cashfree's separately assigned
// cf_order_id (that one is only stored as gateway_order_id for reference).
// So this looks up by orders.id.
func (r *Repository) FindOrderByCashfreeOrderID(ctx context.Context, cashfreeOrderID string) (*Order, error) {
	var o Order
	err := r.pool.QueryRow(ctx,
		`SELECT id, booking_id, amount_minor, currency, COALESCE(gateway_order_id,''), status FROM orders WHERE id = $1`,
		cashfreeOrderID,
	).Scan(&o.ID, &o.BookingID, &o.AmountMinor, &o.Currency, &o.GatewayOrderID, &o.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPaymentNotFound
		}
		return nil, err
	}
	return &o, nil
}

// MarkCaptured is what cmd/api's Cashfree webhook handler calls once a
// PAYMENT_SUCCESS_WEBHOOK is verified — inserts the payments row (never
// created anywhere else — CreateOrder only ever wrote the orders row a
// webhook would confirm) and an append-only reconciliation_entries row
// (PRD §29), transactionally.
// MarkCaptured is idempotent against webhook replay/retry: a second call
// with the same gatewayPaymentID hits the UNIQUE constraint on
// payments.gateway_payment_id (migration 0022) and returns the
// already-recorded payment instead of erroring or double-crediting the
// ledger — Cashfree (and an attacker replaying a captured signed payload)
// can both safely retry this.
func (r *Repository) MarkCaptured(ctx context.Context, orderID, gatewayPaymentID string, amountMinor int64, currency string) (*Payment, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var p Payment
	p.OrderID = orderID
	p.AmountMinor = amountMinor
	p.Currency = currency
	p.Status = "captured"
	if err := tx.QueryRow(ctx, `
		INSERT INTO payments (order_id, amount_minor, currency, status, gateway_payment_id)
		VALUES ($1, $2, $3, 'captured', $4)
		RETURNING id`,
		orderID, amountMinor, currency, gatewayPaymentID,
	).Scan(&p.ID); err != nil {
		if isUniqueViolation(err) {
			return r.GetPaymentByGatewayID(ctx, gatewayPaymentID)
		}
		return nil, err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE orders SET status = 'paid' WHERE id = $1`, orderID,
	); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO reconciliation_entries (entry_type, reference_id, amount_minor) VALUES ('payment', $1, $2)`,
		p.ID, amountMinor,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &p, nil
}

// GetBookingUser resolves the (userID, email) that placed a booking — what
// CreateOrder needs to pass Cashfree customer_id/customer_email.
func (r *Repository) GetBookingUser(ctx context.Context, bookingID string) (userID, email string, err error) {
	err = r.pool.QueryRow(ctx, `
		SELECT u.id, u.email FROM bookings b JOIN users u ON u.id = b.user_id WHERE b.id = $1`,
		bookingID,
	).Scan(&userID, &email)
	return userID, email, err
}

// GetPaymentByGatewayID backs MarkCaptured's idempotent-replay path. Reads
// via r.pool, not the aborted transaction that hit the unique violation —
// Postgres refuses further statements on a tx after an error until rollback.
func (r *Repository) GetPaymentByGatewayID(ctx context.Context, gatewayPaymentID string) (*Payment, error) {
	var p Payment
	err := r.pool.QueryRow(ctx,
		`SELECT id, order_id, amount_minor, currency, status FROM payments WHERE gateway_payment_id = $1`,
		gatewayPaymentID,
	).Scan(&p.ID, &p.OrderID, &p.AmountMinor, &p.Currency, &p.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPaymentNotFound
		}
		return nil, err
	}
	return &p, nil
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
