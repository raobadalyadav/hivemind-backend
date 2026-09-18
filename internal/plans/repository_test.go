package plans

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

func TestRepository_CreateAndGet(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	var hostID string
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ($1) RETURNING id`,
		"repo-test-"+time.Now().Format("150405.000000")+"@example.com",
	).Scan(&hostID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	repo := NewRepository(pool)
	lat, lng := 28.6, 77.2
	created, err := repo.Create(ctx, &Plan{
		Title:     "Test Plan",
		HostID:    hostID,
		Capacity:  5,
		StartsAt:  time.Now().Add(time.Hour),
		EndsAt:    time.Now().Add(2 * time.Hour),
		Currency:  "INR",
		Latitude:  &lat,
		Longitude: &lng,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if created.Status != "published" {
		t.Errorf("expected status 'published', got %q", created.Status)
	}

	got, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "Test Plan" {
		t.Errorf("expected title 'Test Plan', got %q", got.Title)
	}
	if got.Latitude == nil || *got.Latitude != lat {
		t.Errorf("expected latitude %v, got %v", lat, got.Latitude)
	}
}
