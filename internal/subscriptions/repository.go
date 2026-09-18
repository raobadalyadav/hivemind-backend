// Package subscriptions implements PRD §13.14 Subscription (Phase 2).
// GetCatalog is the fully working vertical slice; Subscribe/
// CancelSubscription/GetEntitlements are typed stubs — real store-receipt
// verification (Apple/Google) belongs behind a separate verifier interface.
package subscriptions

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Product struct {
	ID         string
	Name       string
	PriceMinor int64
	Currency   string
	Interval   string
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) ListProducts(ctx context.Context) ([]*Product, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, name, price_minor, currency, interval FROM subscription_products`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var products []*Product
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.Name, &p.PriceMinor, &p.Currency, &p.Interval); err != nil {
			return nil, err
		}
		products = append(products, &p)
	}
	return products, rows.Err()
}
