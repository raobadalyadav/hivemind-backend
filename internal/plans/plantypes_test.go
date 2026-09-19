package plans

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/hivemind/backend/internal/bookings"
	"github.com/hivemind/backend/pkg/idempotency"
)

func typesGuard(t *testing.T) *idempotency.Guard {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Skipf("skipping: cannot connect to redis: %v", err)
	}
	return idempotency.NewGuard(rdb)
}

func typesPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://hivemind:hivemind@localhost:5432/hivemind?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Skipf("skipping: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("skipping: %v", err)
	}
	return pool
}

func typesUser(t *testing.T, pool *pgxpool.Pool, label string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `INSERT INTO users (email) VALUES ($1) RETURNING id`,
		"pt-"+label+"-"+time.Now().Format("150405.000000000")+"@example.com").Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

type fakeEnt struct{ granted bool }

func (f fakeEnt) HasEntitlement(context.Context, string, string) (bool, error) { return f.granted, nil }

func newTypesServices(t *testing.T, pool *pgxpool.Pool, ent EntitlementLike) (*Service, *bookings.Service) {
	bk := bookings.NewService(bookings.NewRepository(pool), typesGuard(t)).WithEntitlements(ent)
	return NewService(NewRepository(pool), bk, bk, nil), bk
}

// EntitlementLike is what the test needs from a fake — same method as
// bookings.EntitlementChecker.
type EntitlementLike interface {
	HasEntitlement(ctx context.Context, userID, key string) (bool, error)
}

func TestApprovalPlan_JoinRequiresApproval(t *testing.T) {
	pool := typesPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc, bk := newTypesServices(t, pool, fakeEnt{})

	host, stranger := typesUser(t, pool, "host"), typesUser(t, pool, "stranger")
	plan, err := svc.CreatePlan(ctx, &Plan{Title: "Approval Plan", HostID: host, Capacity: 5, JoinMode: "approval",
		StartsAt: time.Now().Add(time.Hour)}, "")
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	if _, err := bk.CreateBooking(ctx, &bookings.Booking{PlanID: plan.ID, UserID: stranger}); err != bookings.ErrApprovalRequired {
		t.Fatalf("booking an approval plan without approval must fail with ErrApprovalRequired, got %v", err)
	}

	jr, err := svc.RequestToJoinPlan(ctx, plan.ID, stranger, "please?")
	if err != nil || jr.Status != "pending" {
		t.Fatalf("request: %+v err=%v", jr, err)
	}
	if again, err := svc.RequestToJoinPlan(ctx, plan.ID, stranger, "again"); err != nil || again.ID != jr.ID {
		t.Fatalf("repeat request must return the same row, got %+v err=%v", again, err)
	}
	if _, err := svc.RespondPlanJoinRequest(ctx, jr.ID, stranger, "user", true); err != ErrForbidden {
		t.Fatalf("a non-host must not approve, got %v", err)
	}
	if d, err := svc.RespondPlanJoinRequest(ctx, jr.ID, host, "user", true); err != nil || d.Status != "approved" {
		t.Fatalf("host approves: %+v err=%v", d, err)
	}
	if d, err := svc.RespondPlanJoinRequest(ctx, jr.ID, host, "user", true); err != nil || d.Status != "approved" {
		t.Fatalf("repeating the same decision is a no-op: %+v err=%v", d, err)
	}
	if _, err := svc.RespondPlanJoinRequest(ctx, jr.ID, host, "user", false); err != ErrAlreadyDecided {
		t.Fatalf("the opposite decision must be refused, got %v", err)
	}
	if _, err := bk.CreateBooking(ctx, &bookings.Booking{PlanID: plan.ID, UserID: stranger}); err != nil {
		t.Fatalf("approved user can now book: %v", err)
	}
}

func TestPrivateInviteOnlyPlan_HiddenUntilInvited(t *testing.T) {
	pool := typesPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc, bk := newTypesServices(t, pool, fakeEnt{})

	var cityID string
	if err := pool.QueryRow(ctx, `INSERT INTO cities (name) VALUES ($1) RETURNING id`, "Private Test City "+time.Now().Format("150405.000000000")).Scan(&cityID); err != nil {
		t.Fatalf("seed city: %v", err)
	}
	host, stranger := typesUser(t, pool, "phost"), typesUser(t, pool, "pstranger")

	if _, err := svc.CreatePlan(ctx, &Plan{Title: "x", HostID: host, Capacity: 2, JoinMode: "invite_only", Visibility: "public"}, ""); err != ErrInvalidInput {
		t.Fatalf("invite_only + public must be rejected, got %v", err)
	}
	plan, err := svc.CreatePlan(ctx, &Plan{Title: "Secret Plan", HostID: host, Capacity: 3, CityID: cityID,
		JoinMode: "invite_only", Visibility: "private", StartsAt: time.Now().Add(time.Hour)}, "")
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	if _, err := svc.GetPlanAsUser(ctx, plan.ID, stranger, "user"); err != ErrPlanNotFound {
		t.Fatalf("a private plan must look nonexistent to a stranger, got %v", err)
	}
	found, _ := svc.SearchPlans(ctx, SearchFilter{CityID: cityID})
	for _, p := range found {
		if p.ID == plan.ID {
			t.Fatal("a private plan must never appear in Search")
		}
	}
	if _, err := svc.GetPlanAsUser(ctx, plan.ID, host, "user"); err != nil {
		t.Fatalf("host can read their own private plan: %v", err)
	}
	if _, err := bk.CreateBooking(ctx, &bookings.Booking{PlanID: plan.ID, UserID: stranger}); err != bookings.ErrInviteOnly {
		t.Fatalf("uninvited booking must fail with ErrInviteOnly, got %v", err)
	}

	if _, err := svc.InvitePlanUsers(ctx, plan.ID, stranger, "user", []string{stranger}); err != ErrForbidden {
		t.Fatalf("a non-host must not invite, got %v", err)
	}
	if n, err := svc.InvitePlanUsers(ctx, plan.ID, host, "user", []string{stranger}); err != nil || n != 1 {
		t.Fatalf("host invites: n=%d err=%v", n, err)
	}
	if _, err := svc.GetPlanAsUser(ctx, plan.ID, stranger, "user"); err != nil {
		t.Fatalf("an invitee can see the plan: %v", err)
	}
	if _, err := bk.CreateBooking(ctx, &bookings.Booking{PlanID: plan.ID, UserID: stranger}); err != nil {
		t.Fatalf("an invitee can book: %v", err)
	}
}

func TestPremiumPlan_RequiresEntitlement(t *testing.T) {
	pool := typesPool(t)
	defer pool.Close()
	ctx := context.Background()

	product := "test_premium_" + time.Now().Format("150405.000000000")
	if _, err := pool.Exec(ctx, `INSERT INTO subscription_products (name, price_minor) VALUES ($1, 100)`, product); err != nil {
		t.Fatalf("seed product: %v", err)
	}
	host, user := typesUser(t, pool, "prhost"), typesUser(t, pool, "pruser")

	svcNo, bkNo := newTypesServices(t, pool, fakeEnt{granted: false})
	if _, err := svcNo.CreatePlan(ctx, &Plan{Title: "typo", HostID: host, Capacity: 2, RequiresEntitlement: "no_such_product"}, ""); err != ErrInvalidInput {
		t.Fatalf("an unknown entitlement product must be rejected, got %v", err)
	}
	plan, err := svcNo.CreatePlan(ctx, &Plan{Title: "Premium", HostID: host, Capacity: 2, RequiresEntitlement: product, StartsAt: time.Now().Add(time.Hour)}, "")
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if _, err := bkNo.CreateBooking(ctx, &bookings.Booking{PlanID: plan.ID, UserID: user}); err != bookings.ErrEntitlementRequired {
		t.Fatalf("no entitlement → ErrEntitlementRequired, got %v", err)
	}
	_, bkYes := newTypesServices(t, pool, fakeEnt{granted: true})
	if _, err := bkYes.CreateBooking(ctx, &bookings.Booking{PlanID: plan.ID, UserID: user}); err != nil {
		t.Fatalf("with the entitlement booking succeeds: %v", err)
	}
}

func TestRecurringPlan_ExtendIsIdempotentAndCancelFutureStopsSeries(t *testing.T) {
	pool := typesPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc, _ := newTypesServices(t, pool, fakeEnt{})
	host := typesUser(t, pool, "rechost")

	plan, err := svc.CreatePlan(ctx, &Plan{Title: "Weekly Football", HostID: host, Capacity: 10,
		StartsAt: time.Now().Add(2 * time.Hour)}, "FREQ=WEEKLY;COUNT=4")
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if plan.SeriesID != plan.ID {
		t.Fatalf("the template is its own series id, got series=%q id=%q", plan.SeriesID, plan.ID)
	}
	count := func() int {
		var n int
		pool.QueryRow(ctx, `SELECT count(*) FROM plans WHERE series_id = $1`, plan.ID).Scan(&n)
		return n
	}
	if n := count(); n != 4 {
		t.Fatalf("COUNT=4 should materialise 4 plans, got %d", n)
	}
	if _, err := svc.repo.ExtendSeries(ctx, plan.ID, time.Now()); err != nil {
		t.Fatalf("second ExtendSeries: %v", err)
	}
	if n := count(); n != 4 {
		t.Fatalf("ExtendSeries must be idempotent, got %d rows", n)
	}

	// Discovery shows only the next occurrence of the series.
	var visible int
	pool.QueryRow(ctx, `SELECT count(*) FROM plans_discoverable WHERE series_id = $1`, plan.ID).Scan(&visible)
	if visible != 1 {
		t.Fatalf("plans_discoverable must collapse a series to one row, got %d", visible)
	}

	// Cancel from the 2nd occurrence onward: 1st stays published.
	var secondID string
	pool.QueryRow(ctx, `SELECT id::text FROM plans WHERE series_id = $1 ORDER BY starts_at OFFSET 1 LIMIT 1`, plan.ID).Scan(&secondID)
	if _, err := svc.CancelPlan(ctx, secondID, host, "user", "rain", true); err != nil {
		t.Fatalf("CancelPlan cancel_future: %v", err)
	}
	var published, cancelled int
	pool.QueryRow(ctx, `SELECT count(*) FROM plans WHERE series_id = $1 AND status = 'published'`, plan.ID).Scan(&published)
	pool.QueryRow(ctx, `SELECT count(*) FROM plans WHERE series_id = $1 AND status = 'cancelled'`, plan.ID).Scan(&cancelled)
	if published != 1 || cancelled != 3 {
		t.Fatalf("expected 1 published / 3 cancelled, got %d / %d", published, cancelled)
	}
	if _, err := svc.repo.ExtendSeries(ctx, plan.ID, time.Now()); err != nil {
		t.Fatalf("ExtendSeries after cancel: %v", err)
	}
	if n := count(); n != 4 {
		t.Fatalf("an ended series must not regenerate occurrences, got %d rows", n)
	}
}
