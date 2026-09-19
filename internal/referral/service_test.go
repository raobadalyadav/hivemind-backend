package referral

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
		t.Skipf("skipping: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("skipping: %v", err)
	}
	return pool
}

func seedUser(t *testing.T, pool *pgxpool.Pool, label string, age time.Duration) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email, created_at) VALUES ($1, now() - $2::float8 * interval '1 second') RETURNING id`,
		"rf-"+label+"-"+time.Now().Format("150405.000000000")+"@example.com", age.Seconds()).Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func balance(t *testing.T, pool *pgxpool.Pool, user string) int64 {
	t.Helper()
	var n int64
	pool.QueryRow(context.Background(), `SELECT COALESCE(sum(amount_minor),0) FROM credit_ledger WHERE user_id = $1`, user).Scan(&n)
	return n
}

func TestReferral_RewardsBothOnceAndBlocksAbuse(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc := NewService(NewRepository(pool))

	referrer, friend, old, another := seedUser(t, pool, "ref", time.Hour), seedUser(t, pool, "new", time.Hour),
		seedUser(t, pool, "old", 30*24*time.Hour), seedUser(t, pool, "new2", time.Hour)

	code, err := svc.GetMyCode(ctx, referrer)
	if err != nil || len(code) != 8 {
		t.Fatalf("code: %q err=%v", code, err)
	}
	if again, _ := svc.GetMyCode(ctx, referrer); again != code {
		t.Fatalf("the code must be stable: %q vs %q", again, code)
	}

	if err := svc.ApplyCode(ctx, referrer, code); err != ErrSelfReferral {
		t.Fatalf("own code: %v", err)
	}
	if err := svc.ApplyCode(ctx, friend, "ZZZZZZZZ"); err != ErrCodeNotFound {
		t.Fatalf("unknown code: %v", err)
	}
	if err := svc.ApplyCode(ctx, friend, "bad"); err != ErrInvalidInput {
		t.Fatalf("malformed code: %v", err)
	}
	if err := svc.ApplyCode(ctx, old, code); err != ErrWindowClosed {
		t.Fatalf("an old account can't claim: %v", err)
	}

	if err := svc.ApplyCode(ctx, friend, "  "+lower(code)+" "); err != nil { // case/space-insensitive
		t.Fatalf("apply: %v", err)
	}
	if balance(t, pool, referrer) != RewardMinor || balance(t, pool, friend) != RewardMinor {
		t.Fatalf("both sides get %d, got referrer=%d friend=%d", RewardMinor, balance(t, pool, referrer), balance(t, pool, friend))
	}
	if err := svc.ApplyCode(ctx, friend, code); err != ErrAlreadyReferred {
		t.Fatalf("a second apply must fail: %v", err)
	}
	if balance(t, pool, referrer) != RewardMinor {
		t.Fatalf("a failed second apply must not pay again, referrer=%d", balance(t, pool, referrer))
	}
	// the other referrer's code can't be used either once the referee already used one
	code2, _ := svc.GetMyCode(ctx, another)
	if err := svc.ApplyCode(ctx, friend, code2); err != ErrAlreadyReferred {
		t.Fatalf("one referral per user: %v", err)
	}

	n, earned, _ := svc.Stats(ctx, referrer)
	if n != 1 || earned != RewardMinor {
		t.Fatalf("stats: %d/%d", n, earned)
	}
}

func TestReferral_CapStopsFarming(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc := NewService(NewRepository(pool))
	referrer := seedUser(t, pool, "capref", time.Hour)
	code, _ := svc.GetMyCode(ctx, referrer)
	for i := 0; i < Cap; i++ {
		u := seedUser(t, pool, "capnew", time.Hour)
		if err := svc.ApplyCode(ctx, u, code); err != nil {
			t.Fatalf("referral %d: %v", i, err)
		}
	}
	if err := svc.ApplyCode(ctx, seedUser(t, pool, "capover", time.Hour), code); err != ErrCapReached {
		t.Fatalf("over the cap: %v", err)
	}
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
