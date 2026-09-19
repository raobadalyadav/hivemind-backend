package bookings

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hivemind/backend/pkg/security"
)

func seedExtraUser(t *testing.T, pool *pgxpool.Pool, label string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email) VALUES ($1) RETURNING id`,
		"wl-"+label+"-"+time.Now().Format("150405.000000000")+"@example.com",
	).Scan(&id); err != nil {
		t.Fatalf("seed user %s: %v", label, err)
	}
	return id
}

func waitState(t *testing.T, repo *Repository, planID, userID string) *WaitlistStatus {
	t.Helper()
	st, err := repo.GetWaitlistStatus(context.Background(), planID, userID)
	if err != nil {
		t.Fatalf("GetWaitlistStatus: %v", err)
	}
	return st
}

// TestWaitlist_CancelOffersToFirstAndBlocksOthers is the release gate for
// the waitlist: capacity lock, seat holds, offer acceptance and the sweeper
// working together.
func TestWaitlist_CancelOffersToFirstAndBlocksOthers(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := NewRepository(pool)

	_, planID := seedUserAndPlan(t, pool, 1)
	a, b, c, d, e := seedExtraUser(t, pool, "a"), seedExtraUser(t, pool, "b"), seedExtraUser(t, pool, "c"), seedExtraUser(t, pool, "d"), seedExtraUser(t, pool, "e")

	// Joining the waitlist of a plan with free seats is refused.
	if _, err := repo.JoinWaitlist(ctx, planID, a); err != ErrNotFull {
		t.Fatalf("expected ErrNotFull with a free seat, got %v", err)
	}

	bookA, err := repo.Create(ctx, &Booking{PlanID: planID, UserID: a})
	if err != nil {
		t.Fatalf("A books: %v", err)
	}

	stB, err := repo.JoinWaitlist(ctx, planID, b)
	if err != nil || stB.State != WaitlistWaiting || stB.Position != 1 {
		t.Fatalf("B should be waiting at #1, got %+v err=%v", stB, err)
	}
	stC, err := repo.JoinWaitlist(ctx, planID, c)
	if err != nil || stC.Position != 2 || stC.TotalWaiting != 2 {
		t.Fatalf("C should be waiting at #2 of 2, got %+v err=%v", stC, err)
	}

	// A cancels: B (head of queue) is offered the seat, C keeps waiting.
	if _, err := repo.Cancel(ctx, bookA.ID, "test"); err != nil {
		t.Fatalf("A cancels: %v", err)
	}
	if st := waitState(t, repo, planID, b); st.State != WaitlistOffered || st.OfferExpiresAt == nil {
		t.Fatalf("B should hold an offer, got %+v", st)
	}
	if st := waitState(t, repo, planID, c); st.State != WaitlistWaiting || st.Position != 1 {
		t.Fatalf("C should now be #1 waiting, got %+v", st)
	}

	// A stranger can't take the held seat.
	if _, err := repo.Create(ctx, &Booking{PlanID: planID, UserID: d}); err != ErrPlanFull {
		t.Fatalf("D must be blocked by B's hold with ErrPlanFull, got %v", err)
	}

	// B accepts by simply booking; the entry is consumed.
	bookB, err := repo.Create(ctx, &Booking{PlanID: planID, UserID: b})
	if err != nil {
		t.Fatalf("B claims the offered seat: %v", err)
	}
	if st := waitState(t, repo, planID, b); st.State != WaitlistNone {
		t.Fatalf("B's entry should be consumed, got %+v", st)
	}

	// B leaves too: C gets the offer. E joins behind while C's hold is live.
	if _, err := repo.Cancel(ctx, bookB.ID, "test"); err != nil {
		t.Fatalf("B cancels: %v", err)
	}
	if st := waitState(t, repo, planID, c); st.State != WaitlistOffered {
		t.Fatalf("C should be offered, got %+v", st)
	}
	if st, err := repo.JoinWaitlist(ctx, planID, e); err != nil || st.State != WaitlistWaiting {
		t.Fatalf("E should queue behind C's hold, got %+v err=%v", st, err)
	}

	// C never claims: the offer expires, the sweeper passes the seat to E.
	if _, err := pool.Exec(ctx,
		`UPDATE waitlist_entries SET offer_expires_at = now() - interval '1 minute' WHERE plan_id = $1 AND user_id = $2`,
		planID, c,
	); err != nil {
		t.Fatalf("rewind offer: %v", err)
	}
	if _, err := repo.SweepWaitlist(ctx, time.Now()); err != nil {
		t.Fatalf("SweepWaitlist: %v", err)
	}
	if st := waitState(t, repo, planID, c); st.State != WaitlistNone {
		t.Fatalf("C's expired offer should be gone, got %+v", st)
	}
	if st := waitState(t, repo, planID, e); st.State != WaitlistOffered {
		t.Fatalf("E should now hold the offer, got %+v", st)
	}

	// Leaving while holding an offer is idempotent and releases the seat.
	if err := repo.LeaveWaitlist(ctx, planID, e); err != nil {
		t.Fatalf("E leaves: %v", err)
	}
	if err := repo.LeaveWaitlist(ctx, planID, e); err != nil {
		t.Fatalf("second leave must be a no-op: %v", err)
	}
	if _, err := repo.Create(ctx, &Booking{PlanID: planID, UserID: d}); err != nil {
		t.Fatalf("D can book once nobody holds the seat: %v", err)
	}
}

func TestCancelAndRebook_ReactivatesParticipantRow(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := NewRepository(pool)

	userID, planID := seedUserAndPlan(t, pool, 3)
	first, err := repo.Create(ctx, &Booking{PlanID: planID, UserID: userID})
	if err != nil {
		t.Fatalf("book: %v", err)
	}
	if _, err := repo.Cancel(ctx, first.ID, "test"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := repo.Create(ctx, &Booking{PlanID: planID, UserID: userID}); err != nil {
		t.Fatalf("rebook after cancel must not hit plan_participants UNIQUE: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM plan_participants WHERE plan_id=$1 AND user_id=$2`, planID, userID).Scan(&status); err != nil || status != "confirmed" {
		t.Fatalf("participant row should be re-confirmed, got %q err=%v", status, err)
	}
}

func seedEndedPlan(t *testing.T, pool *pgxpool.Pool, hostID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO plans (title, host_id, starts_at, ends_at, capacity, confirmed_count, currency, status)
		VALUES ('Ended Plan', $1, now() - interval '7 hours', now() - interval '5 hours', 10, 0, 'INR', 'published')
		RETURNING id`, hostID,
	).Scan(&id); err != nil {
		t.Fatalf("seed ended plan: %v", err)
	}
	return id
}

func seedConfirmedBooking(t *testing.T, pool *pgxpool.Pool, planID, userID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO bookings (plan_id, user_id, status, price_minor, currency) VALUES ($1,$2,'confirmed',0,'INR') RETURNING id`,
		planID, userID,
	).Scan(&id); err != nil {
		t.Fatalf("seed booking: %v", err)
	}
	return id
}

func bookingStatus(t *testing.T, pool *pgxpool.Pool, id string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), `SELECT status::text FROM bookings WHERE id=$1`, id).Scan(&s); err != nil {
		t.Fatalf("booking status: %v", err)
	}
	return s
}

func TestMarkNoShows_OnlyWhenCheckinUsed(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := NewRepository(pool)
	host := seedExtraUser(t, pool, "host")

	// Plan 1: host scanned one attendee → the unscanned one is a no-show.
	plan1 := seedEndedPlan(t, pool, host)
	scanned := seedConfirmedBooking(t, pool, plan1, seedExtraUser(t, pool, "scanned"))
	absent := seedConfirmedBooking(t, pool, plan1, seedExtraUser(t, pool, "absent"))
	if _, err := repo.CheckIn(ctx, scanned, host); err != nil {
		t.Fatalf("check in: %v", err)
	}

	// Plan 2: host never used check-in → nobody may be marked.
	plan2 := seedEndedPlan(t, pool, host)
	unknown := seedConfirmedBooking(t, pool, plan2, seedExtraUser(t, pool, "unknown"))

	if _, err := repo.MarkNoShows(ctx, time.Now()); err != nil {
		t.Fatalf("MarkNoShows: %v", err)
	}
	if got := bookingStatus(t, pool, absent); got != "no_show" {
		t.Errorf("unscanned attendee on a scanned plan should be no_show, got %q", got)
	}
	if got := bookingStatus(t, pool, scanned); got != "attended" {
		t.Errorf("scanned attendee must stay attended, got %q", got)
	}
	if got := bookingStatus(t, pool, unknown); got != "confirmed" {
		t.Errorf("plan without any check-in must not mark no-shows, got %q", got)
	}

	// Idempotent: a second run changes nothing for these rows.
	if _, err := repo.MarkNoShows(ctx, time.Now()); err != nil {
		t.Fatalf("second MarkNoShows: %v", err)
	}
	if got := bookingStatus(t, pool, absent); got != "no_show" {
		t.Errorf("second run must keep no_show, got %q", got)
	}
}

func TestCompleteEndedPlans(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := NewRepository(pool)

	host := seedExtraUser(t, pool, "chost")
	ended := seedEndedPlan(t, pool, host)
	_, upcoming := seedUserAndPlan(t, pool, 2)

	if _, err := repo.CompleteEndedPlans(ctx, time.Now()); err != nil {
		t.Fatalf("CompleteEndedPlans: %v", err)
	}
	var s1, s2 string
	pool.QueryRow(ctx, `SELECT status::text FROM plans WHERE id=$1`, ended).Scan(&s1)
	pool.QueryRow(ctx, `SELECT status::text FROM plans WHERE id=$1`, upcoming).Scan(&s2)
	if s1 != "completed" || s2 != "published" {
		t.Errorf("ended plan should be completed and upcoming published, got %q / %q", s1, s2)
	}
}

func TestScanPass_RejectsNonHostAndOutsideWindow(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := NewRepository(pool)
	svc := NewService(repo, nil).WithPassSecret([]byte("test-pass-secret"))

	host, planID := seedUserAndPlan(t, pool, 5) // starts in 1h → inside the window
	attendee := seedExtraUser(t, pool, "att")
	stranger := seedExtraUser(t, pool, "str")
	booking, err := repo.Create(ctx, &Booking{PlanID: planID, UserID: attendee})
	if err != nil {
		t.Fatalf("book: %v", err)
	}

	if _, _, _, err := svc.GetPass(ctx, booking.ID, stranger); err != ErrForbidden {
		t.Fatalf("only the booking owner may fetch the pass, got %v", err)
	}
	if _, _, _, err := svc.GetPass(ctx, booking.ID, host); err != ErrForbidden {
		t.Fatalf("even the host must not mint an attendee's pass, got %v", err)
	}
	_, payload, _, err := svc.GetPass(ctx, booking.ID, attendee)
	if err != nil {
		t.Fatalf("owner GetPass: %v", err)
	}

	if _, _, err := svc.ScanPass(ctx, payload, stranger, "user"); err != ErrForbidden {
		t.Fatalf("stranger must not scan, got %v", err)
	}
	if _, _, err := svc.ScanPass(ctx, payload+"x", host, "user"); err != ErrInvalidPass {
		t.Fatalf("tampered pass must be rejected, got %v", err)
	}
	if _, _, err := svc.ScanPass(ctx, security.SignPass([]byte("wrong"), booking.ID, time.Now().Add(time.Hour)), host, "user"); err != ErrInvalidPass {
		t.Fatalf("pass signed with another secret must be rejected, got %v", err)
	}

	b, name, err := svc.ScanPass(ctx, payload, host, "user")
	if err != nil || b.Status != "attended" {
		t.Fatalf("host scan should check in, got %+v err=%v", b, err)
	}
	_ = name
	if _, _, err := svc.ScanPass(ctx, payload, host, "user"); err != ErrBookingNotFound {
		t.Fatalf("replayed scan must not check in twice, got %v", err)
	}

	// Outside the window: move the plan 10h out and scan a fresh booking.
	other := seedExtraUser(t, pool, "att2")
	b2, err := repo.Create(ctx, &Booking{PlanID: planID, UserID: other})
	if err != nil {
		t.Fatalf("book 2: %v", err)
	}
	_, p2, _, err := svc.GetPass(ctx, b2.ID, other)
	if err != nil {
		t.Fatalf("GetPass 2: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE plans SET starts_at = now() + interval '10 hours', ends_at = now() + interval '11 hours' WHERE id = $1`, planID); err != nil {
		t.Fatalf("move plan: %v", err)
	}
	if _, _, err := svc.ScanPass(ctx, p2, host, "user"); err != ErrOutsideWindow {
		t.Fatalf("scan far before the plan must be ErrOutsideWindow, got %v", err)
	}
}
