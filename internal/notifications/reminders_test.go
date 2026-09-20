package notifications

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func reminderTestPool(t *testing.T) *pgxpool.Pool {
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

func seedReminderBooking(t *testing.T, pool *pgxpool.Pool, startsIn time.Duration, bookedAgo time.Duration) (userID, bookingID string) {
	t.Helper()
	ctx := context.Background()
	sfx := time.Now().Format("150405.000000000")
	var hostID, planID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, "rem-host-"+sfx+"@example.com").Scan(&hostID); err != nil {
		t.Fatalf("seed host: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, "rem-user-"+sfx+"@example.com").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO plans (title, host_id, starts_at, ends_at, capacity, confirmed_count, currency, status)
		VALUES ('Reminder Plan', $1, now() + $2::interval, now() + $2::interval + interval '2 hours', 10, 1, 'INR', 'published')
		RETURNING id`, hostID, secs(startsIn),
	).Scan(&planID); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO bookings (plan_id, user_id, status, price_minor, currency, created_at)
		VALUES ($1, $2, 'confirmed', 0, 'INR', now() - $3::interval) RETURNING id`,
		planID, userID, secs(bookedAgo),
	).Scan(&bookingID); err != nil {
		t.Fatalf("seed booking: %v", err)
	}
	return userID, bookingID
}

func secs(d time.Duration) string { return fmt.Sprintf("%d seconds", int64(d.Seconds())) }

func reminderKinds(t *testing.T, pool *pgxpool.Pool, bookingID string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT kind FROM booking_reminders WHERE booking_id=$1 ORDER BY kind`, bookingID)
	if err != nil {
		t.Fatalf("query reminders: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		rows.Scan(&k)
		out = append(out, k)
	}
	return out
}

func TestSendDueReminders_OneKindOnceOnly(t *testing.T) {
	pool := reminderTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc := NewService(NewRepository(pool), nil, nil, slog.Default())

	// Booked a week ago, plan starts in 2h → the 3h bucket, exactly once.
	userID, bookingID := seedReminderBooking(t, pool, 2*time.Hour, 7*24*time.Hour)
	now := time.Now()
	if _, err := svc.SendDueReminders(ctx, now); err != nil {
		t.Fatalf("SendDueReminders: %v", err)
	}
	if got := reminderKinds(t, pool, bookingID); len(got) != 1 || got[0] != "3h" {
		t.Fatalf("expected exactly one 3h reminder, got %v", got)
	}
	if _, err := svc.SendDueReminders(ctx, now); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if got := reminderKinds(t, pool, bookingID); len(got) != 1 {
		t.Fatalf("second tick must not resend, got %v", got)
	}
	var notifCount int
	pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id=$1`, userID).Scan(&notifCount)
	if notifCount != 1 {
		t.Fatalf("expected exactly one notification row, got %d", notifCount)
	}

	// 90 minutes later the tighter 1h bucket applies (plan in 30 min).
	if _, err := svc.SendDueReminders(ctx, now.Add(90*time.Minute)); err != nil {
		t.Fatalf("later tick: %v", err)
	}
	if got := reminderKinds(t, pool, bookingID); len(got) != 2 || got[0] != "1h" || got[1] != "3h" {
		t.Fatalf("expected 1h and 3h claims, got %v", got)
	}

	// Booked only 20 minutes ago for a plan in 2h → no reminder minutes after booking.
	_, fresh := seedReminderBooking(t, pool, 2*time.Hour, 20*time.Minute)
	if _, err := svc.SendDueReminders(ctx, time.Now()); err != nil {
		t.Fatalf("fresh tick: %v", err)
	}
	if got := reminderKinds(t, pool, fresh); len(got) != 0 {
		t.Fatalf("a booking made after the threshold must get no reminder, got %v", got)
	}
}

func TestPreferences_DefaultsThenSavedValues(t *testing.T) {
	pool := reminderTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	user, _ := seedReminderBooking(t, pool, time.Hour, time.Hour)
	repo := NewRepository(pool)
	push, email, qs, qe, err := repo.Preferences(ctx, user)
	if err != nil || !push || !email || qs != "" || qe != "" {
		t.Fatalf("never set = both on, no quiet hours: %v %v %q %q %v", push, email, qs, qe, err)
	}
	if err := repo.UpsertPreferences(ctx, user, false, true, "22:00", "07:30"); err != nil {
		t.Fatalf("save: %v", err)
	}
	push, email, qs, qe, err = repo.Preferences(ctx, user)
	if err != nil || push || !email || qs != "22:00" || qe != "07:30" {
		t.Errorf("saved values come back: push=%v email=%v %q-%q err=%v", push, email, qs, qe, err)
	}
}
