// Package subscriptions implements PRD §13.14 Subscription (Phase 2) —
// every RPC is fully implemented. Subscribe trusts the client-supplied
// store_receipt without calling Apple/Google to verify it — same non-goal
// already established for internal/payments (Razorpay is never actually
// called either; a real verifier belongs behind an interface here later).
package subscriptions

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrSubscriptionNotFound = errors.New("subscriptions: subscription not found")

type Product struct {
	ID         string
	Name       string
	PriceMinor int64
	Currency   string
	Interval   string
}

type Subscription struct {
	ID        string
	UserID    string
	ProductID string
	Status    string
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

// Subscribe inserts the subscription and grants an entitlement keyed by the
// product name, in one transaction — this is what makes GetEntitlements
// return something real instead of an empty list.
func (r *Repository) Subscribe(ctx context.Context, userID, productID, storeReceipt string) (*Subscription, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var productName string
	if err := tx.QueryRow(ctx,
		`SELECT name FROM subscription_products WHERE id = $1`, productID,
	).Scan(&productName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, err
	}

	out := &Subscription{UserID: userID, ProductID: productID, Status: "active"}
	if err := tx.QueryRow(ctx, `
		INSERT INTO subscriptions (user_id, product_id, status, store_receipt)
		VALUES ($1, $2, 'active', NULLIF($3,''))
		RETURNING id`,
		userID, productID, storeReceipt,
	).Scan(&out.ID); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO entitlements (user_id, entitlement_key) VALUES ($1, $2)
		ON CONFLICT (user_id, entitlement_key) DO NOTHING`,
		userID, productName,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Repository) Get(ctx context.Context, id string) (*Subscription, error) {
	var s Subscription
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, product_id, status FROM subscriptions WHERE id = $1`, id,
	).Scan(&s.ID, &s.UserID, &s.ProductID, &s.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, err
	}
	return &s, nil
}

func (r *Repository) Cancel(ctx context.Context, id string) (*Subscription, error) {
	var s Subscription
	err := r.pool.QueryRow(ctx, `
		UPDATE subscriptions SET status = 'cancelled' WHERE id = $1
		RETURNING id, user_id, product_id, status`,
		id,
	).Scan(&s.ID, &s.UserID, &s.ProductID, &s.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, err
	}
	return &s, nil
}

func (r *Repository) ListEntitlements(ctx context.Context, userID string) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT entitlement_key FROM entitlements WHERE user_id = $1`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}
