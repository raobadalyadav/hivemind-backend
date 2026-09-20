package users

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
	if err != nil || pool.Ping(context.Background()) != nil {
		t.Skip("postgres not reachable")
	}
	return pool
}

func TestDeleteAccount_ErasesPersonalDataButKeepsMoneyRecords(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc := NewService(NewRepository(pool))
	sfx := time.Now().Format("150405.000000000")
	email := "erase-" + sfx + "@example.com"
	var me, host, plan, booking string
	pool.QueryRow(ctx, `INSERT INTO users (email, date_of_birth) VALUES ($1, '1995-01-01') RETURNING id`, email).Scan(&me)
	pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, "erase-host-"+sfx+"@example.com").Scan(&host)
	must := func(q string, a ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, a...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	must(`INSERT INTO user_profiles (user_id, display_name, bio, interests) VALUES ($1,'Priya','my private bio','{coffee}')`, me)
	must(`INSERT INTO oauth_identities (user_id, provider, provider_user_id) VALUES ($1,'google',$2)`, me, "g-"+sfx)
	must(`INSERT INTO refresh_tokens (user_id, token_hash, expires_at) VALUES ($1,$2, now()+interval '30 days')`, me, "h-"+sfx)
	must(`INSERT INTO posts (author_id, body, visibility) VALUES ($1,'a post','public')`, me)
	must(`INSERT INTO notifications (user_id, channel, title, body) VALUES ($1,'in_app','t','b')`, me)
	must(`INSERT INTO plans (title, host_id, starts_at, ends_at, capacity, status) VALUES ('Past', $1, now()-interval '3 days', now()-interval '2 days', 5, 'published')`, host)
	pool.QueryRow(ctx, `SELECT id FROM plans WHERE host_id=$1`, host).Scan(&plan)
	pool.QueryRow(ctx, `INSERT INTO bookings (plan_id, user_id, status, price_minor, currency) VALUES ($1,$2,'attended',50000,'INR') RETURNING id`, plan, me).Scan(&booking)

	if err := svc.DeleteAccount(ctx, me); err != nil {
		t.Fatalf("delete: %v", err)
	}

	count := func(q string, a ...any) int {
		var n int
		pool.QueryRow(ctx, q, a...).Scan(&n)
		return n
	}
	for name, q := range map[string]string{
		"oauth identity": `SELECT count(*) FROM oauth_identities WHERE user_id=$1`,
		"refresh tokens": `SELECT count(*) FROM refresh_tokens WHERE user_id=$1`,
		"posts":          `SELECT count(*) FROM posts WHERE author_id=$1`,
		"notifications":  `SELECT count(*) FROM notifications WHERE user_id=$1`,
	} {
		if n := count(q, me); n != 0 {
			t.Errorf("%s should be erased, %d left", name, n)
		}
	}
	var gotEmail, name, bio, status string
	var dob *time.Time
	pool.QueryRow(ctx, `SELECT email, status, date_of_birth FROM users WHERE id=$1`, me).Scan(&gotEmail, &status, &dob)
	pool.QueryRow(ctx, `SELECT display_name, bio FROM user_profiles WHERE user_id=$1`, me).Scan(&name, &bio)
	if status != "deleted" || gotEmail == email || dob != nil {
		t.Errorf("identity not cleared: %q %q %v", gotEmail, status, dob)
	}
	if name != "Deleted user" || bio != "" {
		t.Errorf("profile not anonymised: %q %q", name, bio)
	}
	if count(`SELECT count(*) FROM bookings WHERE id=$1`, booking) != 1 {
		t.Error("booking history must survive (money records)")
	}
	// the e-mail is free again
	if _, err := pool.Exec(ctx, `INSERT INTO users (email) VALUES ($1)`, email); err != nil {
		t.Errorf("the address can sign up again: %v", err)
	}
}

func TestDeleteAccount_RefusedWhileCommitmentsExist(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc := NewService(NewRepository(pool))
	sfx := time.Now().Format("150405.000000000")
	var host, guest string
	pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, "busy-host-"+sfx+"@example.com").Scan(&host)
	pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, "busy-guest-"+sfx+"@example.com").Scan(&guest)
	var plan string
	pool.QueryRow(ctx, `INSERT INTO plans (title, host_id, starts_at, ends_at, capacity, status) VALUES ('Soon', $1, now()+interval '2 days', now()+interval '2 days 2 hours', 5, 'published') RETURNING id`, host).Scan(&plan)
	pool.Exec(ctx, `INSERT INTO bookings (plan_id, user_id, status, price_minor, currency) VALUES ($1,$2,'confirmed',0,'INR')`, plan, guest)

	if err := svc.DeleteAccount(ctx, host); err != ErrActiveCommitments {
		t.Fatalf("a host with a live plan can't delete yet: %v", err)
	}
	if err := svc.DeleteAccount(ctx, guest); err != ErrActiveCommitments {
		t.Fatalf("a guest with an upcoming booking can't delete yet: %v", err)
	}
	var status string
	pool.QueryRow(ctx, `SELECT status FROM users WHERE id=$1`, host).Scan(&status)
	if status != "active" {
		t.Fatalf("a refused deletion must change nothing, status=%s", status)
	}
}
