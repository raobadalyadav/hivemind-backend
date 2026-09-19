package externalevents

import (
	"context"
	"os"
	"sync"
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

func seedUser(t *testing.T, pool *pgxpool.Pool, label, cityID string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, city_id) VALUES ($1, NULLIF($2,'')::uuid) RETURNING id`,
		"ev-"+label+"-"+time.Now().Format("150405.000000000")+"@example.com", cityID).Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	pool.Exec(ctx, `INSERT INTO user_profiles (user_id, display_name) VALUES ($1,$2)`, id, label)
	return id
}

// rooms is a chat stand-in that records members and hands out distinct room ids.
type rooms struct {
	mu      sync.Mutex
	n       int
	members map[string]map[string]bool
	pool    *pgxpool.Pool
}

func (r *rooms) CreateAdHocRoomWithMembers(ctx context.Context, ids []string) (string, error) {
	var id string
	if err := r.pool.QueryRow(ctx, `INSERT INTO chat_rooms (plan_id) VALUES (NULL) RETURNING id`).Scan(&id); err != nil {
		return "", err
	}
	for _, u := range ids {
		if err := r.AddRoomMember(ctx, id, u); err != nil {
			return "", err
		}
	}
	return id, nil
}

func (r *rooms) AddRoomMember(_ context.Context, room, user string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.members == nil {
		r.members = map[string]map[string]bool{}
	}
	if r.members[room] == nil {
		r.members[room] = map[string]bool{}
	}
	r.members[room][user] = true
	return nil
}

func TestExternalEvents_InterestPeopleAndGroup(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	rm := &rooms{pool: pool}
	svc := NewService(NewRepository(pool), rm)

	var city, evID string
	pool.QueryRow(ctx, `INSERT INTO cities (name) VALUES ($1) RETURNING id`, "Event City "+time.Now().Format("150405.000000000")).Scan(&city)
	if err := pool.QueryRow(ctx, `INSERT INTO external_events (city_id, title, source_url, starts_at)
		VALUES ($1, 'Jazz Night', 'https://tickets.example/jazz', now() + interval '2 days') RETURNING id`, city).Scan(&evID); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	a, b, c, blocked, hidden := seedUser(t, pool, "ea", city), seedUser(t, pool, "eb", city), seedUser(t, pool, "ec", city),
		seedUser(t, pool, "eblocked", city), seedUser(t, pool, "ehidden", city)

	list, err := svc.List(ctx, a, "", "", 10)
	if err != nil || len(list) != 1 || list[0].ID != evID {
		t.Fatalf("the caller's city listing: %+v err=%v", list, err)
	}
	if other := seedUser(t, pool, "eother", ""); true {
		if l, _ := svc.List(ctx, other, "", "", 10); len(l) != 0 {
			t.Fatalf("a user with no city sees none: %+v", l)
		}
	}

	// not interested → no people list, no group
	if _, err := svc.ListInterestedPeople(ctx, a, evID); err != ErrNotInterested {
		t.Fatalf("people list needs interest: %v", err)
	}
	if _, err := svc.JoinEventGroup(ctx, a, evID); err != ErrNotInterested {
		t.Fatalf("group needs interest: %v", err)
	}

	for _, u := range []string{a, b, c, blocked, hidden} {
		if _, err := svc.SetInterest(ctx, u, evID, true); err != nil {
			t.Fatalf("interest: %v", err)
		}
	}
	svc.SetInterest(ctx, a, evID, true) // idempotent
	ev, _ := svc.Get(ctx, a, evID)
	if ev.InterestedCount != 5 || !ev.IAmInterested {
		t.Fatalf("event counters: %+v", ev)
	}
	pool.Exec(ctx, `INSERT INTO blocks (user_id, blocked_user_id) VALUES ($1,$2)`, a, blocked)
	pool.Exec(ctx, `UPDATE user_profiles SET show_in_participant_previews = false WHERE user_id = $1`, hidden)
	people, err := svc.ListInterestedPeople(ctx, a, evID)
	if err != nil || len(people) != 2 {
		t.Fatalf("expected b and c only (not me, not blocked, not opted-out): %+v err=%v", people, err)
	}

	// concurrent first joins converge on ONE shared room
	var wg sync.WaitGroup
	got := make([]string, 3)
	for i, u := range []string{a, b, c} {
		wg.Add(1)
		go func(i int, u string) {
			defer wg.Done()
			r, err := svc.JoinEventGroup(ctx, u, evID)
			if err != nil {
				t.Errorf("join: %v", err)
			}
			got[i] = r
		}(i, u)
	}
	wg.Wait()
	if got[0] == "" || got[0] != got[1] || got[1] != got[2] {
		t.Fatalf("all joiners must share one room: %v", got)
	}
	if !rm.members[got[0]][a] || !rm.members[got[0]][b] || !rm.members[got[0]][c] {
		t.Fatalf("everyone must be a member: %v", rm.members[got[0]])
	}
	if ev, _ = svc.Get(ctx, a, evID); !ev.HasGroup {
		t.Fatal("event should report has_group")
	}

	// un-interest, and inactive events disappear
	svc.SetInterest(ctx, c, evID, false)
	if _, err := svc.JoinEventGroup(ctx, c, evID); err != ErrNotInterested {
		t.Fatalf("after withdrawing interest: %v", err)
	}
	pool.Exec(ctx, `UPDATE external_events SET active = false WHERE id = $1`, evID)
	if _, err := svc.Get(ctx, a, evID); err != ErrNotFound {
		t.Fatalf("deactivated event must be NotFound: %v", err)
	}
	if _, err := svc.Get(ctx, a, "not-a-uuid"); err != ErrNotFound {
		t.Fatalf("bad id: %v", err)
	}
}
