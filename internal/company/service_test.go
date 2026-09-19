package company

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

func testGuard(t *testing.T) *idempotency.Guard {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Skipf("skipping: cannot connect to redis: %v", err)
	}
	return idempotency.NewGuard(rdb)
}

func seedUser(t *testing.T, pool *pgxpool.Pool, label string) string {
	t.Helper()
	suffix := time.Now().Format("150405.000000000")
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email) VALUES ($1) RETURNING id`, "company-"+label+"-"+suffix+"@example.com",
	).Scan(&id); err != nil {
		t.Fatalf("seed user %s: %v", label, err)
	}
	return id
}

func seedPlanWithCapacity(t *testing.T, pool *pgxpool.Pool, hostID string, capacity int32) string {
	t.Helper()
	var planID string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO plans (title, host_id, starts_at, ends_at, capacity, confirmed_count, currency, status)
		VALUES ('Team Booking Test Plan', $1, now() + interval '1 hour', now() + interval '2 hour', $2, 0, 'INR', 'published')
		RETURNING id`, hostID, capacity,
	).Scan(&planID); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	return planID
}

func TestService_AddCompanyMember_RejectsNonOwner(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	owner := seedUser(t, pool, "owner")
	stranger := seedUser(t, pool, "stranger")
	employee := seedUser(t, pool, "employee")

	repo := NewRepository(pool)
	bookingsSvc := bookings.NewService(bookings.NewRepository(pool), testGuard(t))
	svc := NewService(repo, bookingsSvc)

	c, err := svc.CreateCompany(ctx, "Test Co", "billing@example.com", owner)
	if err != nil {
		t.Fatalf("CreateCompany: %v", err)
	}

	if _, err := svc.AddCompanyMember(ctx, c.ID, stranger, employee); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden for non-owner AddCompanyMember, got %v", err)
	}

	if _, err := svc.AddCompanyMember(ctx, c.ID, owner, employee); err != nil {
		t.Fatalf("owner AddCompanyMember should succeed: %v", err)
	}
}

// TestService_CreateTeamBooking_RejectsNonMemberEmployee is a regression
// test for a HIGH-severity IDOR finding: an owner used to be able to book
// any arbitrary user_id as an "employee" — including a stranger who never
// joined the company — since only company ownership was checked, not
// whether the target user was actually a member. No booking should be
// created for any employee in the request once one of them isn't a member.
func TestService_CreateTeamBooking_RejectsNonMemberEmployee(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	owner := seedUser(t, pool, "owner4")
	host := seedUser(t, pool, "host4")
	member := seedUser(t, pool, "member4")
	stranger := seedUser(t, pool, "stranger4") // never added to the company

	repo := NewRepository(pool)
	bookingsSvc := bookings.NewService(bookings.NewRepository(pool), testGuard(t))
	svc := NewService(repo, bookingsSvc)

	c, err := svc.CreateCompany(ctx, "IDOR Test Co", "", owner)
	if err != nil {
		t.Fatalf("CreateCompany: %v", err)
	}
	if _, err := svc.AddCompanyMember(ctx, c.ID, owner, member); err != nil {
		t.Fatalf("AddCompanyMember: %v", err)
	}
	planID := seedPlanWithCapacity(t, pool, host, 10)

	if _, err := svc.CreateTeamBooking(ctx, c.ID, owner, planID, []string{member, stranger}); err != ErrNotAllMembers {
		t.Fatalf("expected ErrNotAllMembers when one employee_user_id isn't a company member, got %v", err)
	}

	var bookingCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bookings WHERE plan_id = $1`, planID).Scan(&bookingCount); err != nil {
		t.Fatalf("query bookings: %v", err)
	}
	if bookingCount != 0 {
		t.Errorf("expected no bookings created when any employee fails the membership check, got %d", bookingCount)
	}
}

func TestService_CreateTeamBooking_RejectsNonOwner(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	owner := seedUser(t, pool, "owner2")
	stranger := seedUser(t, pool, "stranger2")
	host := seedUser(t, pool, "host2")
	employee := seedUser(t, pool, "employee2")

	repo := NewRepository(pool)
	bookingsSvc := bookings.NewService(bookings.NewRepository(pool), testGuard(t))
	svc := NewService(repo, bookingsSvc)

	c, err := svc.CreateCompany(ctx, "Test Co 2", "", owner)
	if err != nil {
		t.Fatalf("CreateCompany: %v", err)
	}
	planID := seedPlanWithCapacity(t, pool, host, 10)

	if _, err := svc.CreateTeamBooking(ctx, c.ID, stranger, planID, []string{employee}); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden for non-owner CreateTeamBooking, got %v", err)
	}
}

// TestService_CreateTeamBooking_SurfacesCapacityFailure verifies team
// bookings reuse bookings.Service's real capacity enforcement — booking
// more employees than the plan's remaining capacity fails partway through,
// and the result reports which employee didn't fit and which bookings
// (before that point) are real.
func TestService_CreateTeamBooking_SurfacesCapacityFailure(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	owner := seedUser(t, pool, "owner3")
	host := seedUser(t, pool, "host3")
	e1 := seedUser(t, pool, "e1")
	e2 := seedUser(t, pool, "e2")

	repo := NewRepository(pool)
	bookingsSvc := bookings.NewService(bookings.NewRepository(pool), testGuard(t))
	svc := NewService(repo, bookingsSvc)

	c, err := svc.CreateCompany(ctx, "Capacity Test Co", "", owner)
	if err != nil {
		t.Fatalf("CreateCompany: %v", err)
	}
	if _, err := svc.AddCompanyMember(ctx, c.ID, owner, e1); err != nil {
		t.Fatalf("AddCompanyMember e1: %v", err)
	}
	if _, err := svc.AddCompanyMember(ctx, c.ID, owner, e2); err != nil {
		t.Fatalf("AddCompanyMember e2: %v", err)
	}
	planID := seedPlanWithCapacity(t, pool, host, 1) // room for exactly one

	result, err := svc.CreateTeamBooking(ctx, c.ID, owner, planID, []string{e1, e2})
	if err != nil {
		t.Fatalf("CreateTeamBooking: %v", err)
	}
	if len(result.BookingIDs) != 1 {
		t.Errorf("expected exactly 1 booking created before capacity ran out, got %d", len(result.BookingIDs))
	}
	if result.FailedEmployeeID != e2 {
		t.Errorf("expected e2 to be reported as the failed employee, got %q", result.FailedEmployeeID)
	}
	if result.Err == nil {
		t.Error("expected a non-nil Err describing the capacity failure")
	}

	var companyID *string
	if err := pool.QueryRow(ctx, `SELECT company_id::text FROM bookings WHERE id = $1`, result.BookingIDs[0]).Scan(&companyID); err != nil {
		t.Fatalf("query booking company_id: %v", err)
	}
	if companyID == nil || *companyID != c.ID {
		t.Errorf("expected the successful booking tagged with company_id %q, got %v", c.ID, companyID)
	}
}
