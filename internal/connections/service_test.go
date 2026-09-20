package connections

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/hivemind/backend/pkg/idempotency"
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

func seedUser(t *testing.T, pool *pgxpool.Pool, label string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`,
		"cn-"+label+"-"+time.Now().Format("150405.000000000")+"@example.com").Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_profiles (user_id, display_name) VALUES ($1,$2)`, id, label); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	return id
}

func seedPlan(t *testing.T, pool *pgxpool.Pool, host string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO plans (title, host_id, starts_at, ends_at, capacity, status)
		VALUES ('t', $1, now() - interval '5 hours', now() - interval '3 hours', 10, 'completed') RETURNING id`, host).Scan(&id); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	return id
}

func attend(t *testing.T, pool *pgxpool.Pool, planID, userID string) {
	t.Helper()
	ctx := context.Background()
	var bid string
	if err := pool.QueryRow(ctx, `INSERT INTO bookings (plan_id, user_id, status) VALUES ($1,$2,'attended') RETURNING id`, planID, userID).Scan(&bid); err != nil {
		t.Fatalf("seed booking: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO plan_participants (plan_id, user_id, booking_id) VALUES ($1,$2,$3)`, planID, userID, bid); err != nil {
		t.Fatalf("seed participant: %v", err)
	}
}

func TestRequestConnection_Hardened(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc := NewService(NewRepository(pool))
	a, b, c := seedUser(t, pool, "a"), seedUser(t, pool, "b"), seedUser(t, pool, "c")

	first, err := svc.RequestConnection(ctx, &Connection{RequesterID: a, RecipientID: b})
	if err != nil || first.Status != "pending" {
		t.Fatalf("first request: %+v err=%v", first, err)
	}
	again, err := svc.RequestConnection(ctx, &Connection{RequesterID: a, RecipientID: b})
	if err != nil || again.ID != first.ID {
		t.Fatalf("a repeated request must return the same row, got %+v err=%v", again, err)
	}
	// B requesting A back = mutual intent → accepted, still one row
	rev, err := svc.RequestConnection(ctx, &Connection{RequesterID: b, RecipientID: a})
	if err != nil || rev.ID != first.ID || rev.Status != "accepted" {
		t.Fatalf("a reverse request must accept the existing one, got %+v err=%v", rev, err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM connections WHERE requester_id IN ($1,$2) AND recipient_id IN ($1,$2)`, a, b).Scan(&n)
	if n != 1 {
		t.Fatalf("exactly one connection row per pair, got %d", n)
	}

	// decided requests can't be flipped
	req, _ := svc.RequestConnection(ctx, &Connection{RequesterID: a, RecipientID: c})
	if _, err := svc.RespondConnection(ctx, req.ID, c, true); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if _, err := svc.RespondConnection(ctx, req.ID, c, false); err != ErrAlreadyDecided {
		t.Fatalf("accepted → rejected must be refused, got %v", err)
	}
	if r, err := svc.RespondConnection(ctx, req.ID, c, true); err != nil || r.Status != "accepted" {
		t.Fatalf("repeating the same decision is fine: %+v err=%v", r, err)
	}

	// blocks look like a missing user
	d := seedUser(t, pool, "d")
	pool.Exec(ctx, `INSERT INTO blocks (user_id, blocked_user_id) VALUES ($1,$2)`, d, a)
	if _, err := svc.RequestConnection(ctx, &Connection{RequesterID: a, RecipientID: d}); err != ErrUserNotFound {
		t.Fatalf("connecting to someone who blocked you must fail as not found, got %v", err)
	}
}

func TestRequestConnection_OriginPlanMustBeShared(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc := NewService(NewRepository(pool))
	host, a, b, outsider := seedUser(t, pool, "oh"), seedUser(t, pool, "oa"), seedUser(t, pool, "ob"), seedUser(t, pool, "oo")
	plan := seedPlan(t, pool, host)
	attend(t, pool, plan, a)
	attend(t, pool, plan, b)

	if _, err := svc.RequestConnection(ctx, &Connection{RequesterID: a, RecipientID: b, OriginPlanID: plan}); err != nil {
		t.Fatalf("co-attendees can cite the plan: %v", err)
	}
	if _, err := svc.RequestConnection(ctx, &Connection{RequesterID: outsider, RecipientID: b, OriginPlanID: plan}); err != ErrBadOriginPlan {
		t.Fatalf("a non-attendee can't claim the plan as origin, got %v", err)
	}
}

type fakeRooms struct{ got []string }

func (f *fakeRooms) CreateAdHocRoomWithMembers(_ context.Context, ids []string) (string, error) {
	f.got = ids
	return "00000000-0000-0000-0000-000000000001", nil
}

func TestMeetAgainGroup_ConsentAndIdempotency(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("skipping: redis: %v", err)
	}
	rooms := &fakeRooms{}
	svc := NewService(NewRepository(pool)).WithMeetAgain(idempotency.NewGuard(rdb), rooms)

	host := seedUser(t, pool, "mh")
	me, friend, stranger, absent := seedUser(t, pool, "me"), seedUser(t, pool, "friend"), seedUser(t, pool, "stranger"), seedUser(t, pool, "absent")
	plan := seedPlan(t, pool, host)
	attend(t, pool, plan, me)
	attend(t, pool, plan, friend)
	attend(t, pool, plan, stranger)
	pool.Exec(ctx, `INSERT INTO connections (requester_id, recipient_id, status) VALUES ($1,$2,'accepted')`, me, friend)
	pool.Exec(ctx, `INSERT INTO connections (requester_id, recipient_id, status) VALUES ($1,$2,'accepted')`, me, absent)

	if _, err := svc.ListPeopleMet(ctx, plan, absent); err != ErrNotAttendee {
		t.Fatalf("a non-attendee can't list people met, got %v", err)
	}
	people, err := svc.ListPeopleMet(ctx, plan, me)
	if err != nil || len(people) != 2 {
		t.Fatalf("me met two people: %+v err=%v", people, err)
	}
	for _, p := range people {
		if (p.UserID == friend && p.State != "connected") || (p.UserID == stranger && p.State != "none") {
			t.Fatalf("wrong connection state: %+v", p)
		}
	}

	if _, err := svc.CreateMeetAgainGroup(ctx, plan, me, []string{stranger}, ""); err != ErrInviteeRules {
		t.Fatalf("an unconnected attendee must be refused, got %v", err)
	}
	if _, err := svc.CreateMeetAgainGroup(ctx, plan, me, []string{absent}, ""); err != ErrInviteeRules {
		t.Fatalf("a connection who didn't attend must be refused, got %v", err)
	}
	if _, err := svc.CreateMeetAgainGroup(ctx, plan, absent, []string{friend}, ""); err != ErrNotAttendee {
		t.Fatalf("a non-attendee can't start a group, got %v", err)
	}
	key := "ma-" + time.Now().Format("150405.000000000")
	room, err := svc.CreateMeetAgainGroup(ctx, plan, me, []string{friend}, key)
	if err != nil || room == "" || len(rooms.got) != 2 {
		t.Fatalf("valid group: room=%q members=%v err=%v", room, rooms.got, err)
	}
	if _, err := svc.CreateMeetAgainGroup(ctx, plan, me, []string{friend}, key); err != idempotency.ErrDuplicateRequest {
		t.Fatalf("a replay must be a duplicate, got %v", err)
	}
}

func TestListConnections_StatusAndDirectionFiltersInSQL(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	me, a, b, c := seedUser(t, pool, "fme"), seedUser(t, pool, "fa"), seedUser(t, pool, "fb"), seedUser(t, pool, "fc")
	add := func(req, rec, st string) {
		if _, err := pool.Exec(ctx, `INSERT INTO connections (requester_id, recipient_id, status) VALUES ($1,$2,$3::connection_status)`, req, rec, st); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	add(a, me, "pending")  // incoming request
	add(me, b, "pending")  // my outgoing request
	add(c, me, "accepted") // a friend
	repo := NewRepository(pool)
	kinds := func(status, dir string) map[string]bool {
		list, err := repo.ListForUser(ctx, me, status, dir, 50)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		m := map[string]bool{}
		for _, x := range list {
			m[x.RequesterID+">"+x.RecipientID] = true
		}
		return m
	}
	if got := kinds("pending", "incoming"); len(got) != 1 || !got[a+">"+me] {
		t.Errorf("incoming pending = only a's request: %v", got)
	}
	if got := kinds("pending", "outgoing"); len(got) != 1 || !got[me+">"+b] {
		t.Errorf("outgoing pending: %v", got)
	}
	if got := kinds("accepted", ""); len(got) != 1 || !got[c+">"+me] {
		t.Errorf("accepted either way: %v", got)
	}
	if got := kinds("", ""); len(got) != 3 {
		t.Errorf("no filter = all three: %v", got)
	}
}
