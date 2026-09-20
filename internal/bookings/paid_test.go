package bookings

import (
	"context"
	"testing"
	"time"
)

// paidPlan makes a published plan that costs ₹500 and starts in startsIn.
func paidPlan(t *testing.T, r *Repository, capacity int32, startsIn time.Duration) (host, planID string) {
	t.Helper()
	host, planID = seedUserAndPlan(t, r.pool, capacity)
	if _, err := r.pool.Exec(context.Background(), `UPDATE plans SET price_minor = 50000, currency = 'INR', starts_at = $2::timestamptz, ends_at = $2::timestamptz + interval '2 hours' WHERE id = $1`,
		planID, time.Now().Add(startsIn)); err != nil {
		t.Fatal(err)
	}
	return host, planID
}

func seatCount(t *testing.T, r *Repository, planID string) int {
	t.Helper()
	var n int
	r.pool.QueryRow(context.Background(), `SELECT confirmed_count FROM plans WHERE id = $1`, planID).Scan(&n)
	return n
}

func TestPaidBooking_HoldsTheSeatUntilPaid(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := NewRepository(pool)
	_, planID := paidPlan(t, repo, 1, 72*time.Hour)
	a, b := seedExtraUser(t, pool, "pa"), seedExtraUser(t, pool, "pb")

	first, err := repo.Create(ctx, &Booking{PlanID: planID, UserID: a})
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "payment_pending" || first.PriceMinor != 50000 || first.HoldExpiresAt == nil {
		t.Fatalf("a paid seat starts as payment_pending with a hold: %+v", first)
	}
	if seatCount(t, repo, planID) != 0 {
		t.Fatal("no confirmed seat is taken before the payment")
	}
	// retrying resumes the same booking rather than stacking holds
	again, err := repo.Create(ctx, &Booking{PlanID: planID, UserID: a})
	if err != nil || again.ID != first.ID {
		t.Fatalf("retry resumes the same hold: %+v %v", again, err)
	}
	// the last seat is held for the person who is paying
	if _, err := repo.Create(ctx, &Booking{PlanID: planID, UserID: b}); err != ErrPlanFull {
		t.Fatalf("the held seat can't be booked by someone else: %v", err)
	}

	svc := &Service{repo: repo}
	if err := svc.ConfirmPaidBooking(ctx, first.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if err := svc.ConfirmPaidBooking(ctx, first.ID); err != nil {
		t.Fatalf("confirming twice is harmless: %v", err)
	}
	if seatCount(t, repo, planID) != 1 {
		t.Fatalf("exactly one seat once paid, got %d", seatCount(t, repo, planID))
	}
	got, _ := repo.Get(ctx, first.ID)
	if got.Status != "confirmed" {
		t.Fatalf("status after payment: %s", got.Status)
	}
	var participants, events int
	pool.QueryRow(ctx, `SELECT count(*) FROM plan_participants WHERE booking_id=$1 AND status='confirmed'`, first.ID).Scan(&participants)
	pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE event_type='BOOKING_CONFIRMED' AND subject_id=$1`, first.ID).Scan(&events)
	if participants != 1 || events != 1 {
		t.Fatalf("participant + one BOOKING_CONFIRMED event: %d/%d", participants, events)
	}
}

func TestPaidBooking_ExpiredHoldFreesTheSeatAndLatePaymentLosesIfTaken(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := NewRepository(pool)
	svc := &Service{repo: repo}
	_, planID := paidPlan(t, repo, 1, 72*time.Hour)
	a, b := seedExtraUser(t, pool, "ea"), seedExtraUser(t, pool, "eb")

	slow, _ := repo.Create(ctx, &Booking{PlanID: planID, UserID: a})
	pool.Exec(ctx, `UPDATE bookings SET expires_at = now() - interval '1 minute' WHERE id = $1`, slow.ID)
	if n, err := svc.ExpirePendingBookings(ctx, time.Now()); err != nil || n < 1 {
		t.Fatalf("expiry job releases the hold: %d %v", n, err)
	}
	// the seat is free again: someone else pays and gets it
	other, err := repo.Create(ctx, &Booking{PlanID: planID, UserID: b})
	if err != nil {
		t.Fatalf("seat is free after expiry: %v", err)
	}
	if err := svc.ConfirmPaidBooking(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	// the slow payer's money arrives anyway: they lose the seat and the caller must refund
	if err := svc.ConfirmPaidBooking(ctx, slow.ID); err != ErrPlanFull {
		t.Fatalf("a late payment for a lost seat is ErrPlanFull, got %v", err)
	}
	if seatCount(t, repo, planID) != 1 {
		t.Fatal("capacity is never exceeded")
	}

	// …but if nobody took the seat, a late payment still confirms
	_, plan2 := paidPlan(t, repo, 2, 72*time.Hour)
	late, _ := repo.Create(ctx, &Booking{PlanID: plan2, UserID: a})
	pool.Exec(ctx, `UPDATE bookings SET expires_at = now() - interval '1 minute' WHERE id = $1`, late.ID)
	svc.ExpirePendingBookings(ctx, time.Now())
	if err := svc.ConfirmPaidBooking(ctx, late.ID); err != nil {
		t.Fatalf("late payment with a free seat confirms: %v", err)
	}
}

func TestPaidBooking_CancelRulesAndRefundPolicy(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := NewRepository(pool)
	svc := &Service{repo: repo}
	u := seedExtraUser(t, pool, "cu")

	// a pending booking is just released: no seat, no refund event
	_, p1 := paidPlan(t, repo, 3, 72*time.Hour)
	pending, _ := repo.Create(ctx, &Booking{PlanID: p1, UserID: u})
	if _, err := repo.Cancel(ctx, pending.ID, "changed mind", false); err != nil {
		t.Fatal(err)
	}
	var events int
	pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE event_type='BOOKING_CANCELLED' AND subject_id=$1`, pending.ID).Scan(&events)
	if events != 0 || seatCount(t, repo, p1) != 0 {
		t.Fatalf("cancelling an unpaid hold changes nothing else: events=%d", events)
	}

	percent := func(startsIn time.Duration, byHost bool) int {
		_, p := paidPlan(t, repo, 3, startsIn)
		b, _ := repo.Create(ctx, &Booking{PlanID: p, UserID: u})
		if err := svc.ConfirmPaidBooking(ctx, b.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Cancel(ctx, b.ID, "x", byHost); err != nil {
			t.Fatal(err)
		}
		var n int
		pool.QueryRow(ctx, `SELECT (convert_from(payload,'UTF8')::jsonb->>'refund_percent')::int FROM outbox_events WHERE event_type='BOOKING_CANCELLED' AND subject_id=$1`, b.ID).Scan(&n)
		return n
	}
	if got := percent(72*time.Hour, false); got != 100 {
		t.Fatalf("cancelling 3 days ahead refunds in full, got %d", got)
	}
	if got := percent(6*time.Hour, false); got != 0 {
		t.Fatalf("cancelling 6 hours ahead refunds nothing, got %d", got)
	}
	if got := percent(6*time.Hour, true); got != 100 {
		t.Fatalf("a host cancellation always refunds in full, got %d", got)
	}
	if RefundPercent(time.Now().Add(24*time.Hour+time.Minute), time.Now(), false) != 100 || RefundPercent(time.Now().Add(23*time.Hour), time.Now(), false) != 0 {
		t.Fatal("the 24-hour boundary")
	}
}

func TestQuote_ShowsTheCancellationPolicyOnlyForPaidPlans(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := NewRepository(pool)
	svc := &Service{repo: repo}
	u := seedExtraUser(t, pool, "qu")
	_, paid := paidPlan(t, repo, 3, 72*time.Hour)
	q, err := svc.QuoteBooking(ctx, paid, u)
	if err != nil || q.RefundFullUntil == nil || q.RefundFullHours != 24 || q.TotalMinor != 50000+2500 {
		t.Fatalf("paid quote: %+v %v", q, err)
	}
	_, free := seedUserAndPlan(t, pool, 3)
	q, _ = svc.QuoteBooking(ctx, free, u)
	if q.RefundFullUntil != nil {
		t.Fatalf("a free plan has no refund policy: %+v", q)
	}
	owner, total, _, status, err := svc.GetBookingCharge(ctx, mustBook(t, repo, paid, u))
	if err != nil || owner != u || total != 52500 || status != "payment_pending" {
		t.Fatalf("charge is computed server-side: %s %d %s %v", owner, total, status, err)
	}
}

func mustBook(t *testing.T, r *Repository, planID, userID string) string {
	t.Helper()
	b, err := r.Create(context.Background(), &Booking{PlanID: planID, UserID: userID})
	if err != nil {
		t.Fatal(err)
	}
	return b.ID
}
