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
		INSERT INTO plans (title, host_id, starts_at, ends_at, capacity, confirmed_count, currency)
		VALUES ('Test Plan', $1, now() + interval '1 hour', now() + interval '2 hour', $2, 0, 'INR')
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
