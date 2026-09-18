package bookings

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

func seedUserAndPlan(t *testing.T, pool *pgxpool.Pool, capacity int32) (userID, planID string) {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().Format("150405.000000")

	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id`,
		"booking-test-"+suffix+"@example.com",
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	if err := pool.QueryRow(ctx, `
		INSERT INTO plans (title, host_id, starts_at, ends_at, capacity, confirmed_count, currency, status)
		VALUES ('Test Plan', $1, now() + interval '1 hour', now() + interval '2 hour', $2, 0, 'INR', 'published')
		RETURNING id`,
		userID, capacity,
	).Scan(&planID); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	return userID, planID
}

func TestRepository_CreateEnforcesCapacity(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	userID, planID := seedUserAndPlan(t, pool, 1)
	repo := NewRepository(pool)

	created, err := repo.Create(ctx, &Booking{PlanID: planID, UserID: userID, Currency: "INR"})
	if err != nil {
		t.Fatalf("first booking should succeed: %v", err)
	}
	if created.Status != "confirmed" {
		t.Errorf("expected status 'confirmed', got %q", created.Status)
	}

	got, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.PlanID != planID {
		t.Errorf("expected plan_id %q, got %q", planID, got.PlanID)
	}

	_, err = repo.Create(ctx, &Booking{PlanID: planID, UserID: userID, Currency: "INR"})
	if err != ErrPlanFull {
		t.Fatalf("expected ErrPlanFull on second booking, got %v", err)
	}
}

func TestRepository_CancelDecrementsCapacityAndFreesSeat(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	userA, planID := seedUserAndPlan(t, pool, 1)
	repo := NewRepository(pool)

	booking, err := repo.Create(ctx, &Booking{PlanID: planID, UserID: userA})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	cancelled, err := repo.Cancel(ctx, booking.ID, "test cancellation")
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if cancelled.Status != "cancelled" {
		t.Errorf("expected status 'cancelled', got %q", cancelled.Status)
	}

	var confirmedCount int32
	if err := pool.QueryRow(ctx, `SELECT confirmed_count FROM plans WHERE id = $1`, planID).Scan(&confirmedCount); err != nil {
		t.Fatalf("query confirmed_count: %v", err)
	}
	if confirmedCount != 0 {
		t.Errorf("expected confirmed_count 0 after cancel, got %d", confirmedCount)
	}

	var participantStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM plan_participants WHERE booking_id = $1`, booking.ID,
	).Scan(&participantStatus); err != nil {
		t.Fatalf("query plan_participants: %v", err)
	}
	if participantStatus != "cancelled" {
		t.Errorf("expected plan_participants status 'cancelled', got %q", participantStatus)
	}

	// The freed seat should allow a different user to book.
	userB, _ := seedUserAndPlan(t, pool, 0) // capacity irrelevant, only need a second user row
	if _, err := repo.Create(ctx, &Booking{PlanID: planID, UserID: userB}); err != nil {
		t.Fatalf("booking after cancel should succeed with freed capacity: %v", err)
	}
}

func TestRepository_CreateRejectsDuplicateActiveBooking(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	userID, planID := seedUserAndPlan(t, pool, 5)
	repo := NewRepository(pool)

	if _, err := repo.Create(ctx, &Booking{PlanID: planID, UserID: userID}); err != nil {
		t.Fatalf("first booking should succeed: %v", err)
	}
	if _, err := repo.Create(ctx, &Booking{PlanID: planID, UserID: userID}); err != ErrAlreadyBooked {
		t.Fatalf("expected ErrAlreadyBooked, got %v", err)
	}
}
