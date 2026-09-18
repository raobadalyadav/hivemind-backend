package payments

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

func TestRepository_MarkCaptured(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := NewRepository(pool)

	suffix := time.Now().Format("150405.000000000")
	var userID, planID, bookingID, orderID string

	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ($1) RETURNING id`, "payments-test-"+suffix+"@example.com",
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO plans (title, host_id, starts_at, ends_at, capacity, confirmed_count, currency, status)
		VALUES ('Payments Test Plan', $1, now() + interval '1 hour', now() + interval '2 hour', 5, 0, 'INR', 'published')
		RETURNING id`, userID,
	).Scan(&planID); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO bookings (plan_id, user_id, status, price_minor, currency) VALUES ($1, $2, 'confirmed', 50000, 'INR') RETURNING id`,
		planID, userID,
	).Scan(&bookingID); err != nil {
		t.Fatalf("seed booking: %v", err)
	}

	order, err := repo.CreateOrder(ctx, &Order{BookingID: bookingID, AmountMinor: 50000, Currency: "INR"})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	orderID = order.ID

	payment, err := repo.MarkCaptured(ctx, orderID, "cf_payment_test_"+suffix, 50000, "INR")
	if err != nil {
		t.Fatalf("MarkCaptured: %v", err)
	}
	if payment.Status != "captured" {
		t.Errorf("expected status 'captured', got %q", payment.Status)
	}
	if payment.OrderID != orderID {
		t.Errorf("expected order_id %q, got %q", orderID, payment.OrderID)
	}

	var orderStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM orders WHERE id = $1`, orderID).Scan(&orderStatus); err != nil {
		t.Fatalf("query order status: %v", err)
	}
	if orderStatus != "paid" {
		t.Errorf("expected order status 'paid', got %q", orderStatus)
	}

	var reconCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM reconciliation_entries WHERE reference_id = $1 AND entry_type = 'payment'`, payment.ID,
	).Scan(&reconCount); err != nil {
		t.Fatalf("query reconciliation_entries: %v", err)
	}
	if reconCount != 1 {
		t.Errorf("expected 1 reconciliation_entries row, got %d", reconCount)
	}

	// FindCapturedPaymentForBooking should now find it.
	found, err := repo.FindCapturedPaymentForBooking(ctx, bookingID)
	if err != nil {
		t.Fatalf("FindCapturedPaymentForBooking: %v", err)
	}
	if found.ID != payment.ID {
		t.Errorf("expected found payment id %q, got %q", payment.ID, found.ID)
	}
}
