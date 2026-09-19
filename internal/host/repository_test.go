package host

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://hivemind:hivemind@localhost:5432/hivemind?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Skipf("skipping: cannot connect to postgres: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("skipping: postgres not reachable: %v", err)
	}
	return pool
}

// TestRepository_GetPayoutBalance_ExcludesRefunds seeds two bookings for the
// same host — one captured-and-kept, one captured-then-refunded — and
// verifies the refunded one is excluded from the payable balance (the bug
// the plan's validation pass flagged in a naive SUM(captured) query).
func TestRepository_GetPayoutBalance_ExcludesRefunds(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := NewRepository(pool)

	suffix := time.Now().Format("150405.000000000")
	var hostID, buyerID, planID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ($1) RETURNING id`, "host-test-"+suffix+"@example.com",
	).Scan(&hostID); err != nil {
		t.Fatalf("seed host: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ($1) RETURNING id`, "buyer-test-"+suffix+"@example.com",
	).Scan(&buyerID); err != nil {
		t.Fatalf("seed buyer: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO plans (title, host_id, starts_at, ends_at, capacity, confirmed_count, currency, status)
		VALUES ('Host Payout Test Plan', $1, now() + interval '1 hour', now() + interval '2 hour', 10, 0, 'INR', 'published')
		RETURNING id`, hostID,
	).Scan(&planID); err != nil {
		t.Fatalf("seed plan: %v", err)
	}

	seedCapturedPayment := func(refund bool) {
		var bookingID, orderID, paymentID string
		if err := pool.QueryRow(ctx,
			`INSERT INTO bookings (plan_id, user_id, status, price_minor, currency) VALUES ($1, $2, 'confirmed', 100000, 'INR') RETURNING id`,
			planID, buyerID,
		).Scan(&bookingID); err != nil {
			t.Fatalf("seed booking: %v", err)
		}
		if err := pool.QueryRow(ctx,
			`INSERT INTO orders (booking_id, amount_minor, currency, status) VALUES ($1, 100000, 'INR', 'paid') RETURNING id`,
			bookingID,
		).Scan(&orderID); err != nil {
			t.Fatalf("seed order: %v", err)
		}
		if err := pool.QueryRow(ctx, `
			INSERT INTO payments (order_id, amount_minor, currency, status, gateway_payment_id)
			VALUES ($1, 100000, 'INR', 'captured', $2) RETURNING id`,
			orderID, "cf_pay_"+suffix+"_"+bookingID,
		).Scan(&paymentID); err != nil {
			t.Fatalf("seed payment: %v", err)
		}
		if refund {
			if _, err := pool.Exec(ctx,
				`INSERT INTO refunds (payment_id, amount_minor, status) VALUES ($1, 100000, 'processed')`, paymentID,
			); err != nil {
				t.Fatalf("seed refund: %v", err)
			}
		}
	}

	seedCapturedPayment(false) // kept: contributes 100000
	seedCapturedPayment(true)  // refunded: must NOT contribute

	netCaptured, priorPayouts, err := repo.GetPayoutBalance(ctx, hostID)
	if err != nil {
		t.Fatalf("GetPayoutBalance: %v", err)
	}
	if netCaptured != 100000 {
		t.Errorf("expected net_captured 100000 (refunded booking excluded), got %d", netCaptured)
	}
	if priorPayouts != 0 {
		t.Errorf("expected prior_payouts 0, got %d", priorPayouts)
	}
}
