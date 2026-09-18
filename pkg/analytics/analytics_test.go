package analytics

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRecorder_Record(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://hivemind:hivemind@localhost:5432/hivemind?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Skipf("skipping: cannot connect to postgres: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("skipping: postgres not reachable: %v", err)
	}

	r := NewRecorder(pool)
	ctx := context.Background()

	if err := r.Record(ctx, "", "test_event", map[string]any{"foo": "bar", "n": 3}); err != nil {
		t.Fatalf("Record with no user_id: %v", err)
	}

	var userID string
	email := "analytics-test-" + time.Now().Format("150405.000000000") + "@example.com"
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ($1) RETURNING id`, email,
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	if err := r.Record(ctx, userID, "session_active", nil); err != nil {
		t.Fatalf("Record with user_id: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM events WHERE user_id = $1 AND event_name = 'session_active'`, userID,
	).Scan(&count); err != nil {
		t.Fatalf("verify row: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 event row, got %d", count)
	}
}
