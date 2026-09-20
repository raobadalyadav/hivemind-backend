package search

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSearch_WildcardsAreLiteral(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://hivemind:hivemind@localhost:5432/hivemind?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Skipf("skipping: %v", err)
	}
	defer pool.Close()
	if pool.Ping(context.Background()) != nil {
		t.Skip("postgres not reachable")
	}
	ctx := context.Background()
	sfx := time.Now().Format("150405.000000000")
	var host string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, "srch-"+sfx+"@example.com").Scan(&host); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO plans (title, host_id, starts_at, ends_at, capacity, status)
		VALUES ($1, $2, now()+interval '2 days', now()+interval '2 days 2 hours', 5, 'published')`, "Sunset 100% fun_night "+sfx, host); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(pool)
	count := func(q string) int {
		ids, err := repo.SearchPlanIDs(ctx, q, "", 50)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		return len(ids)
	}
	if count("100% fun_night "+sfx) != 1 {
		t.Fatal("a literal % and _ in the query still find the plan")
	}
	if count("%") > 3 { // only titles that really contain a percent sign
		t.Fatalf("%% must not match every plan, got %d", count("%"))
	}
	if count("_") > 5 {
		t.Fatalf("_ must not match every character, got %d", count("_"))
	}
	if count(`\`) != 0 {
		t.Fatal("a lone backslash is literal, not an escape error")
	}
}
