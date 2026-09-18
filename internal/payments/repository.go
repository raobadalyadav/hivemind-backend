// Package payments implements PRD §13.13 Payments. CreateOrder is the fully
// working vertical slice (writes the orders row that a real gateway webhook
// would later mark captured); GetPayment/RefundPayment are typed stubs.
// Real Razorpay calls belong behind a gateway.Client interface — not
// implemented here, per the plan's non-goal of no real gateway integration.
package payments

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Order struct {
	ID             string
	BookingID      string
	AmountMinor    int64
	Currency       string
	GatewayOrderID string
	Status         string
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
