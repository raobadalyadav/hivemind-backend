package admin

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

func TestRepository_CreateCity_And_UpdateCityStatus(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := NewRepository(pool)

	suffix := time.Now().Format("150405.000000000")
	city, err := repo.CreateCity(ctx, "Test City "+suffix, "TS", "IN", "")
	if err != nil {
		t.Fatalf("CreateCity: %v", err)
	}
	if city.Status != "pre_launch" {
		t.Errorf("expected new city to default to 'pre_launch', got %q", city.Status)
	}

	var active bool
	if err := pool.QueryRow(ctx, `SELECT active FROM cities WHERE id = $1`, city.ID).Scan(&active); err != nil {
		t.Fatalf("query active: %v", err)
	}
	if active {
		t.Error("expected derived active=false while status='pre_launch'")
	}

	if err := repo.UpdateCityStatus(ctx, city.ID, "active", ""); err != nil {
		t.Fatalf("UpdateCityStatus: %v", err)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status::text, active FROM cities WHERE id = $1`, city.ID).Scan(&status, &active); err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "active" {
		t.Errorf("expected status 'active', got %q", status)
	}
	if !active {
		t.Error("expected derived active=true once status='active'")
	}

	var auditCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_logs WHERE subject_type = 'city' AND subject_id = $1`, city.ID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("query audit_logs: %v", err)
	}
	if auditCount != 2 { // create_city + update_city_status
		t.Errorf("expected 2 audit_logs rows (create + update), got %d", auditCount)
	}
}
