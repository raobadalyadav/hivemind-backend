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
	ErrCouponInvalid        = errors.New("payments: coupon code is invalid, expired, or exhausted")
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
// UnsettledOrderIDs: orders opened in the last 6 hours (older than 1 minute, so the app's own verify goes
// first) that never turned paid — including ones whose hold already ran out, since a late payment must be refunded.
func (r *Repository) UnsettledOrderIDs(ctx context.Context, limit int) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id FROM orders
		WHERE status IN ('created', 'cancelled') AND amount_minor > 0
		  AND created_at < now() - interval '1 minute' AND created_at > now() - interval '6 hours'
		ORDER BY created_at LIMIT $1`, limit)
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

// PaymentOwner returns the user a payment belongs to (payment → order → booking).
func (r *Repository) PaymentOwner(ctx context.Context, paymentID string) (string, error) {
	var owner string
	err := r.pool.QueryRow(ctx, `
		SELECT b.user_id::text FROM payments p
		JOIN orders o ON o.id = p.order_id
		JOIN bookings b ON b.id = o.booking_id
		WHERE p.id = $1`, paymentID).Scan(&owner)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isBadUUID(err) {
			return "", ErrPaymentNotFound
		}
		return "", err
	}
	return owner, nil
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

// ReserveCoupon atomically validates and reserves one use of a coupon
// before CreateOrder runs — a single row-locked UPDATE...WHERE, so a lost
// race under concurrency rejects the request instead of creating an order
// whose discount was never actually reserved (0 rows updated = exhausted).
func (r *Repository) ReserveCoupon(ctx context.Context, code string, amountMinor int64) (discountMinor int64, err error) {
	var id, discountType string
	var discountValue int64
	var maxUses int32
	if err := r.pool.QueryRow(ctx, `
		SELECT id, discount_type, discount_value, max_uses FROM promo_codes
		WHERE code = $1 AND active AND (expires_at IS NULL OR expires_at > now())`,
		code,
	).Scan(&id, &discountType, &discountValue, &maxUses); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrCouponInvalid
		}
		return 0, err
	}

	if discountType == "fixed" {
		discountMinor = discountValue
	} else {
		discountMinor = amountMinor * discountValue / 100
	}
	if discountMinor > amountMinor {
		discountMinor = amountMinor
	}

	tag, err := r.pool.Exec(ctx, `
		UPDATE promo_codes SET uses_count = uses_count + 1
		WHERE id = $1 AND (max_uses = 0 OR uses_count < max_uses)`,
		id,
	)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		return 0, ErrCouponInvalid
	}
	return discountMinor, nil
}

// CreateOrderWithCredits is CreateOrder plus an optional credit spend, in
// one transaction: the order row must exist before credit_ledger.order_id
// can reference it, so both writes commit or roll back together — a failed
// credit debit must not leave a charged order with no matching ledger
// entry. SELECT...FOR UPDATE on the caller's ledger rows is a
// serialization barrier so two concurrent spends by the same user can't
// both pass the balance check.
// ponytail: balance is recomputed by summing the ledger every call —
// correct for a single user's realistic concurrency, not built to survive a
// deliberate concurrent double-spend attack. Upgrade to a maintained
// balance column with a CHECK >= 0 if that ever needs to be load-bearing.
func (r *Repository) CreateOrderWithCredits(ctx context.Context, o *Order, userID string, useCredits bool) (order *Order, creditsSpentMinor int64, err error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(ctx)

	if useCredits {
		var balance int64
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(SUM(amount_minor),0) FROM (
				SELECT amount_minor FROM credit_ledger WHERE user_id = $1 FOR UPDATE
			) locked`, userID,
		).Scan(&balance); err != nil {
			return nil, 0, err
		}
		if balance > 0 {
			creditsSpentMinor = balance
			if creditsSpentMinor > o.AmountMinor {
				creditsSpentMinor = o.AmountMinor
			}
		}
	}

	out := *o
	out.AmountMinor = o.AmountMinor - creditsSpentMinor
	out.Status = "created"
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (booking_id, amount_minor, currency, status)
		VALUES ($1, $2, $3, 'created')
		RETURNING id`,
		o.BookingID, out.AmountMinor, o.Currency,
	).Scan(&out.ID); err != nil {
		return nil, 0, err
	}

	if creditsSpentMinor > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO credit_ledger (user_id, amount_minor, reason, order_id) VALUES ($1, $2, 'booking_spend', $3)`,
			userID, -creditsSpentMinor, out.ID,
		); err != nil {
			return nil, 0, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, 0, err
	}
	return &out, creditsSpentMinor, nil
}

func (r *Repository) GetCreditBalance(ctx context.Context, userID string) (int64, error) {
	var balance int64
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount_minor),0) FROM credit_ledger WHERE user_id = $1`, userID,
	).Scan(&balance)
	return balance, err
}

// BeginRefund records a pending refund attempt after checking the payment is captured and the refunds
// (attempts that didn't fail) don't add up to more than was paid. It returns the attempt and the order the payment belongs to.
func (r *Repository) BeginRefund(ctx context.Context, paymentID string, amountMinor int64, reason string) (*Refund, string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback(ctx)

	var status, orderID string
	var paid int64
	if err := tx.QueryRow(ctx, `SELECT status, order_id::text, amount_minor FROM payments WHERE id = $1 FOR UPDATE`, paymentID).Scan(&status, &orderID, &paid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isBadUUID(err) {
			return nil, "", ErrPaymentNotFound
		}
		return nil, "", err
	}
	if status != "captured" {
		return nil, "", ErrPaymentNotRefundable
	}
	var refunded int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(amount_minor),0) FROM refunds WHERE payment_id = $1 AND status <> 'failed'`, paymentID).Scan(&refunded); err != nil {
		return nil, "", err
	}
	if amountMinor <= 0 || refunded+amountMinor > paid {
		return nil, "", ErrPaymentNotRefundable
	}
	refund := Refund{PaymentID: paymentID, AmountMinor: amountMinor, Status: "pending"}
	if err := tx.QueryRow(ctx, `
		INSERT INTO refunds (payment_id, amount_minor, reason, status) VALUES ($1, $2, $3, 'pending') RETURNING id`,
		paymentID, amountMinor, reason).Scan(&refund.ID); err != nil {
		return nil, "", err
	}
	return &refund, orderID, tx.Commit(ctx)
}

// FailRefund marks an attempt the gateway rejected (it stops counting toward the refunded total).
func (r *Repository) FailRefund(ctx context.Context, refundID string) error {
	_, err := r.pool.Exec(ctx, `UPDATE refunds SET status = 'failed', updated_at = now() WHERE id = $1`, refundID)
	return err
}

// CompleteRefund settles an attempt the gateway accepted and writes the append-only reconciliation entry (PRD §29).
func (r *Repository) CompleteRefund(ctx context.Context, refundID, gatewayRefundID string, amountMinor int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE refunds SET status = 'processed', gateway_refund_id = NULLIF($2,''), updated_at = now() WHERE id = $1`, refundID, gatewayRefundID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO reconciliation_entries (entry_type, reference_id, amount_minor) VALUES ('refund', $1, $2)`, refundID, amountMinor); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetRefundStatus records the gateway's final result for a refund attempt (ignores ids that aren't ours).
func (r *Repository) SetRefundStatus(ctx context.Context, refundID, status string) error {
	_, err := r.pool.Exec(ctx, `UPDATE refunds SET status = $2::refund_status, updated_at = now() WHERE id::text = $1`, refundID, status)
	return err
}

// RefundedTotal is what has been (or is being) returned for a payment.
func (r *Repository) RefundedTotal(ctx context.Context, paymentID string) (int64, error) {
	var n int64
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(sum(amount_minor),0) FROM refunds WHERE payment_id = $1 AND status <> 'failed'`, paymentID).Scan(&n)
	return n, err
}

// MarkOrderPaid settles an order that had nothing left to charge (covered by credits / a coupon).
func (r *Repository) MarkOrderPaid(ctx context.Context, orderID string) error {
	_, err := r.pool.Exec(ctx, `UPDATE orders SET status = 'paid' WHERE id = $1`, orderID)
	return err
}

// CancelUnpaidOrdersForBooking closes every still-unpaid order of a booking and reverses the credits each took.
func (r *Repository) CancelUnpaidOrdersForBooking(ctx context.Context, bookingID string) error {
	return r.cancelUnpaid(ctx, `o.booking_id = $1`, bookingID)
}

// ReleaseAbandonedOrders does the same for bookings that are no longer payment_pending (expired or cancelled).
func (r *Repository) ReleaseAbandonedOrders(ctx context.Context) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM orders o JOIN bookings b ON b.id = o.booking_id
		WHERE o.status = 'created' AND b.status <> 'payment_pending'`).Scan(&n)
	if err != nil || n == 0 {
		return 0, err
	}
	return n, r.cancelUnpaid(ctx, `o.booking_id IN (SELECT id FROM bookings WHERE status <> 'payment_pending') AND $1 = $1`, "")
}

func (r *Repository) cancelUnpaid(ctx context.Context, where, arg string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `UPDATE orders o SET status = 'cancelled' WHERE o.status = 'created' AND `+where+` RETURNING o.id`, arg)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		// credit_ledger is append-only: give the credits back with a new positive entry.
		if _, err := tx.Exec(ctx, `
			INSERT INTO credit_ledger (user_id, amount_minor, reason, order_id)
			SELECT user_id, -sum(amount_minor), 'booking_spend_reversed', order_id FROM credit_ledger
			WHERE order_id = $1 AND reason = 'booking_spend'
			GROUP BY user_id, order_id HAVING sum(amount_minor) < 0`, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// isBadUUID: a malformed id is "not found", not an internal error.

// isBadUUID: a malformed id is "not found", not an internal error.
func isBadUUID(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "22P02"
}
