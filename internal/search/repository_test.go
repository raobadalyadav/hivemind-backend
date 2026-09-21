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
	// only plans that really contain the character may match it (the count is taken from the data itself, so the
	// test doesn't depend on how many rows earlier runs left behind)
	literal := func(ch string) int {
		var n int
		pool.QueryRow(ctx, `SELECT count(*) FROM plans_discoverable WHERE status = 'published' AND (position($1 in title) > 0 OR position($1 in description) > 0)`, ch).Scan(&n)
		return n
	}
	for _, ch := range []string{"%", "_"} {
		if got, want := count(ch), literal(ch); got != min(want, 50) {
			t.Fatalf("%q must match only plans containing it: got %d want %d", ch, got, min(want, 50))
		}
	}
	if count(`\`) != 0 {
		t.Fatal("a lone backslash is literal, not an escape error")
	}
}
