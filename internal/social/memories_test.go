package social

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type nopScreener struct{}

func (nopScreener) Screen(context.Context, string) (string, string) { return "", "" }

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

func seedUser(t *testing.T, pool *pgxpool.Pool, label string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `INSERT INTO users (email) VALUES ($1) RETURNING id`,
		"sc-"+label+"-"+time.Now().Format("150405.000000000")+"@example.com").Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func TestMemories_GroupingCountsAndAttachGate(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc := NewService(NewRepository(pool), nil, nopScreener{})

	host, me, other, outsider := seedUser(t, pool, "h"), seedUser(t, pool, "me"), seedUser(t, pool, "o"), seedUser(t, pool, "x")
	mkPlan := func(title string, startsAgo time.Duration) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO plans (title, host_id, starts_at, ends_at, capacity, status)
			VALUES ($1,$2, now() - $3::float8 * interval '1 second', now() - $3::float8 * interval '1 second' + interval '2 hours', 10, 'completed')
			RETURNING id`, title, host, startsAgo.Seconds()).Scan(&id); err != nil {
			t.Fatalf("seed plan: %v", err)
		}
		return id
	}
	attend := func(planID, user string) {
		if _, err := pool.Exec(ctx, `INSERT INTO bookings (plan_id, user_id, status) VALUES ($1,$2,'attended')`, planID, user); err != nil {
			t.Fatalf("seed booking: %v", err)
		}
	}
	recent := mkPlan("Recent Dinner", 48*time.Hour)
	old := mkPlan("Old Meetup", 800*24*time.Hour)
	attend(recent, me)
	attend(recent, other)
	attend(old, me)

	if _, err := svc.CreatePost(ctx, &Post{AuthorID: outsider, PlanID: recent, Body: "spam", Visibility: "public"}); err != ErrNotAttendee {
		t.Fatalf("a non-attendee can't tag a plan, got %v", err)
	}
	if _, err := svc.CreatePost(ctx, &Post{AuthorID: me, PlanID: recent, Body: "great", Visibility: "public",
		MediaURLs: []string{"https://cdn.example/1.jpg", "https://cdn.example/2.jpg"}}); err != nil {
		t.Fatalf("an attendee can: %v", err)
	}

	years, err := svc.ListMyMemories(ctx, me, 0)
	if err != nil || len(years) != 2 {
		t.Fatalf("expected two year groups, got %+v err=%v", years, err)
	}
	if years[0].Year <= years[1].Year {
		t.Fatalf("newest year first, got %d then %d", years[0].Year, years[1].Year)
	}
	m := years[0].Memories[0]
	if m.Title != "Recent Dinner" || m.PhotoCount != 2 || m.PeopleCount != 2 {
		t.Fatalf("recent memory: %+v", m)
	}
	if only, _ := svc.ListMyMemories(ctx, me, years[1].Year); len(only) != 1 || only[0].Memories[0].Title != "Old Meetup" {
		t.Fatalf("year filter: %+v", only)
	}
	if none, _ := svc.ListMyMemories(ctx, outsider, 0); len(none) != 0 {
		t.Fatalf("someone who attended nothing has no memories, got %+v", none)
	}
}
