package availability

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

// fakeRoomCreator satisfies RoomCreator without importing internal/chat —
// it seeds a real ad-hoc chat_rooms row directly (chat_room_id is a real
// FK, so a non-UUID placeholder won't pass) since the ownership check
// under test happens before this is ever called, not the room content itself.
type fakeRoomCreator struct {
	pool  *pgxpool.Pool
	calls int
}

func (f *fakeRoomCreator) CreateAdHocRoomWithMembers(ctx context.Context, userIDs []string) (string, error) {
	f.calls++
	var roomID string
	if err := f.pool.QueryRow(ctx, `INSERT INTO chat_rooms (plan_id) VALUES (NULL) RETURNING id`).Scan(&roomID); err != nil {
		return "", err
	}
	return roomID, nil
}

func seedUser(t *testing.T, pool *pgxpool.Pool, label string) string {
	t.Helper()
	suffix := time.Now().Format("150405.000000000")
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email) VALUES ($1) RETURNING id`, "avail-"+label+"-"+suffix+"@example.com",
	).Scan(&id); err != nil {
		t.Fatalf("seed user %s: %v", label, err)
	}
	return id
}

func TestService_AcceptActivityMatch_RejectsNonOwner(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	owner := seedUser(t, pool, "owner")
	other := seedUser(t, pool, "other")
	stranger := seedUser(t, pool, "stranger")

	repo := NewRepository(pool)
	rooms := &fakeRoomCreator{pool: pool}
	svc := NewService(repo, rooms)

	now := time.Now()
	myWindow, err := repo.Create(ctx, &Window{UserID: owner, ActivityType: "badminton", StartsAt: now, EndsAt: now.Add(2 * time.Hour)})
	if err != nil {
		t.Fatalf("seed my window: %v", err)
	}
	theirWindow, err := repo.Create(ctx, &Window{UserID: other, ActivityType: "badminton", StartsAt: now, EndsAt: now.Add(2 * time.Hour)})
	if err != nil {
		t.Fatalf("seed their window: %v", err)
	}

	if _, err := svc.AcceptActivityMatch(ctx, myWindow.ID, theirWindow.ID, stranger); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden for non-owner accept, got %v", err)
	}
	if rooms.calls != 0 {
		t.Errorf("expected no room to be created when ownership check fails, got %d calls", rooms.calls)
	}

	roomID, err := svc.AcceptActivityMatch(ctx, myWindow.ID, theirWindow.ID, owner)
	if err != nil {
		t.Fatalf("owner accept should succeed: %v", err)
	}
	if roomID == "" {
		t.Error("expected a non-empty room id from RoomCreator")
	}
}

func TestService_AcceptActivityMatch_RejectsAlreadyMatchedWindow(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	a := seedUser(t, pool, "a")
	b := seedUser(t, pool, "b")
	c := seedUser(t, pool, "c")

	repo := NewRepository(pool)
	rooms := &fakeRoomCreator{pool: pool}
	svc := NewService(repo, rooms)

	now := time.Now()
	wa, _ := repo.Create(ctx, &Window{UserID: a, ActivityType: "coffee", StartsAt: now, EndsAt: now.Add(time.Hour)})
	wb, _ := repo.Create(ctx, &Window{UserID: b, ActivityType: "coffee", StartsAt: now, EndsAt: now.Add(time.Hour)})
	wc, _ := repo.Create(ctx, &Window{UserID: c, ActivityType: "coffee", StartsAt: now, EndsAt: now.Add(time.Hour)})

	if _, err := svc.AcceptActivityMatch(ctx, wa.ID, wb.ID, a); err != nil {
		t.Fatalf("first accept should succeed: %v", err)
	}

	// wa is now 'matched' — a second accept against it must be rejected.
	if _, err := svc.AcceptActivityMatch(ctx, wa.ID, wc.ID, a); err != ErrNotOpen {
		t.Fatalf("expected ErrNotOpen for an already-matched window, got %v", err)
	}
}
