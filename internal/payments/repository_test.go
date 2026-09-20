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

// TestRepository_ReserveCoupon_Exhaustion seeds a coupon with max_uses=1 and
// verifies a second reservation is rejected — the row-locked
// UPDATE...WHERE uses_count < max_uses in ReserveCoupon is what enforces
// this atomically.
func TestRepository_ReserveCoupon_Exhaustion(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := NewRepository(pool)

	suffix := time.Now().Format("150405.000000000")
	code := "TESTCOUPON" + suffix
	if _, err := pool.Exec(ctx,
		`INSERT INTO promo_codes (code, discount_type, discount_value, max_uses) VALUES ($1, 'percent', 10, 1)`,
		code,
	); err != nil {
		t.Fatalf("seed coupon: %v", err)
	}

	discount, err := repo.ReserveCoupon(ctx, code, 100000)
	if err != nil {
		t.Fatalf("first ReserveCoupon should succeed: %v", err)
	}
	if discount != 10000 {
		t.Errorf("expected 10%% discount of 100000 = 10000, got %d", discount)
	}

	if _, err := repo.ReserveCoupon(ctx, code, 100000); err != ErrCouponInvalid {
		t.Fatalf("expected ErrCouponInvalid once max_uses is exhausted, got %v", err)
	}
}

// TestRepository_CreateOrderWithCredits_CapsAtBalance verifies a credit
// spend never exceeds the user's actual balance, even when the order
// amount requested is larger.
func TestRepository_CreateOrderWithCredits_CapsAtBalance(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := NewRepository(pool)

	suffix := time.Now().Format("150405.000000000")
	var userID, planID, bookingID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ($1) RETURNING id`, "credits-test-"+suffix+"@example.com",
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO plans (title, host_id, starts_at, ends_at, capacity, confirmed_count, currency, status)
		VALUES ('Credits Test Plan', $1, now() + interval '1 hour', now() + interval '2 hour', 5, 0, 'INR', 'published')
		RETURNING id`, userID,
	).Scan(&planID); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO bookings (plan_id, user_id, status, price_minor, currency) VALUES ($1, $2, 'confirmed', 100000, 'INR') RETURNING id`,
		planID, userID,
	).Scan(&bookingID); err != nil {
		t.Fatalf("seed booking: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO credit_ledger (user_id, amount_minor, reason) VALUES ($1, 3000, 'test_grant')`, userID,
	); err != nil {
		t.Fatalf("seed credit grant: %v", err)
	}

	order, spent, err := repo.CreateOrderWithCredits(ctx, &Order{BookingID: bookingID, AmountMinor: 100000, Currency: "INR"}, userID, true)
	if err != nil {
		t.Fatalf("CreateOrderWithCredits: %v", err)
	}
	if spent != 3000 {
		t.Errorf("expected creditsSpentMinor capped at balance 3000, got %d", spent)
	}
	if order.AmountMinor != 97000 {
		t.Errorf("expected order amount 100000-3000=97000, got %d", order.AmountMinor)
	}

	balance, err := repo.GetCreditBalance(ctx, userID)
	if err != nil {
		t.Fatalf("GetCreditBalance: %v", err)
	}
	if balance != 0 {
		t.Errorf("expected balance 0 after spending it all, got %d", balance)
	}
}

func TestGetPayment_OnlyTheOwnerOrStaff(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := NewRepository(pool)
	svc := &Service{repo: repo}
	suffix := time.Now().Format("150405.000000000")
	var owner, other, plan, booking string
	pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, "pay-own-"+suffix+"@example.com").Scan(&owner)
	pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, "pay-oth-"+suffix+"@example.com").Scan(&other)
	pool.QueryRow(ctx, `INSERT INTO plans (title, host_id, starts_at, ends_at, capacity, status) VALUES ('P', $1, now()+interval '1 day', now()+interval '2 days', 5, 'published') RETURNING id`, other).Scan(&plan)
	pool.QueryRow(ctx, `INSERT INTO bookings (plan_id, user_id, status, price_minor, currency) VALUES ($1,$2,'confirmed',50000,'INR') RETURNING id`, plan, owner).Scan(&booking)
	order, err := repo.CreateOrder(ctx, &Order{BookingID: booking, AmountMinor: 50000, Currency: "INR"})
	if err != nil {
		t.Fatal(err)
	}
	pay, err := repo.MarkCaptured(ctx, order.ID, "cf_own_"+suffix, 50000, "INR")
	if err != nil {
		t.Fatal(err)
	}

	if p, err := svc.GetPayment(ctx, pay.ID, owner, false); err != nil || p.ID != pay.ID {
		t.Fatalf("the payer sees their payment: %v", err)
	}
	if _, err := svc.GetPayment(ctx, pay.ID, other, false); err != ErrPaymentNotFound {
		t.Fatalf("someone else's payment must look like it doesn't exist, got %v", err)
	}
	if _, err := svc.GetPayment(ctx, pay.ID, other, true); err != nil {
		t.Fatalf("staff can read any payment: %v", err)
	}
	if _, err := svc.GetPayment(ctx, "not-a-uuid", owner, false); err != ErrPaymentNotFound {
		t.Fatalf("a malformed id is simply not found, got %v", err)
	}
}
