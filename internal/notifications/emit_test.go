package notifications

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func emitUser(t *testing.T, pool *pgxpool.Pool, label string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `INSERT INTO users (email) VALUES ($1) RETURNING id`,
		"em-"+label+"-"+time.Now().Format("150405.000000000")+"@example.com").Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func countOf(t *testing.T, pool *pgxpool.Pool, user, typ string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM notifications WHERE user_id=$1 AND type=$2`, user, typ).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEmit_RulesAndDedupe(t *testing.T) {
	pool := reminderTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	a, b, c := emitUser(t, pool, "a"), emitUser(t, pool, "b"), emitUser(t, pool, "c")
	ev := func(to, from string) Event {
		return Event{UserID: to, ActorID: from, Type: TypePostLike, Title: "{actor} liked your post", DedupeKey: "k:" + to + ":" + from}
	}
	emit := func(e Event) bool {
		t.Helper()
		ok, err := Emit(ctx, pool, e)
		if err != nil {
			t.Fatalf("emit: %v", err)
		}
		return ok
	}

	if emit(ev(a, a)) {
		t.Fatal("never notify yourself")
	}
	if !emit(ev(a, b)) {
		t.Fatal("first like should notify")
	}
	if emit(ev(a, b)) {
		t.Fatal("a repeat while unread collapses into the first")
	}
	// once read, the same event can notify again
	if _, err := NewRepository(pool).MarkRead(ctx, a, nil, true); err != nil {
		t.Fatal(err)
	}
	if !emit(ev(a, b)) {
		t.Fatal("after reading, a new occurrence notifies again")
	}

	// a block in either direction silences it
	pool.Exec(ctx, `INSERT INTO blocks (user_id, blocked_user_id) VALUES ($1,$2)`, c, a)
	if emit(ev(a, c)) {
		t.Fatal("blocked actor must not notify")
	}

	// muted category
	svc := NewService(NewRepository(pool), nil, nil, nil)
	if err := svc.SetMuted(ctx, a, []string{"social"}); err != nil {
		t.Fatal(err)
	}
	if emit(Event{UserID: a, ActorID: b, Type: TypePostLike, Title: "x", DedupeKey: "muted"}) {
		t.Fatal("muted category must not notify")
	}
	if !emit(Event{UserID: a, ActorID: b, Type: TypeProfileView, Title: "{actor} viewed your profile", DedupeKey: "pv"}) {
		t.Fatal("other categories still notify")
	}
	if err := svc.SetMuted(ctx, a, []string{"nonsense"}); err != ErrInvalidInput {
		t.Fatalf("unknown category is rejected, got %v", err)
	}

	// suspended recipients get nothing
	pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, b)
	if emit(ev(b, a)) {
		t.Fatal("inactive recipient must not be notified")
	}
}

func TestNotifications_PagingUnreadCountAndActorName(t *testing.T) {
	pool := reminderTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	to, from := emitUser(t, pool, "to"), emitUser(t, pool, "from")
	pool.Exec(ctx, `INSERT INTO user_profiles (user_id, display_name) VALUES ($1, 'Priya Sharma')`, from)
	for i := 0; i < defaultPageSize+3; i++ {
		if _, err := Emit(ctx, pool, Event{UserID: to, ActorID: from, Type: TypeStoryView, Title: "{actor} viewed your story", DedupeKey: fmt.Sprintf("s%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewService(NewRepository(pool), nil, nil, nil)
	first, next, err := svc.ListNotifications(ctx, to, "")
	if err != nil || len(first) != defaultPageSize || next == "" {
		t.Fatalf("first page full with a cursor: len=%d next=%q err=%v", len(first), next, err)
	}
	if first[0].Title != "Priya Sharma viewed your story" || first[0].ActorName != "Priya Sharma" || first[0].Type != TypeStoryView {
		t.Fatalf("actor name is joined and substituted: %+v", first[0])
	}
	rest, next2, err := svc.ListNotifications(ctx, to, next)
	if err != nil || len(rest) != 3 || next2 != "" {
		t.Fatalf("second page is the remaining 3: len=%d next=%q err=%v", len(rest), next2, err)
	}
	if n, _ := svc.UnreadCount(ctx, to); n != defaultPageSize+3 {
		t.Fatalf("unread = %d", n)
	}
	left, err := svc.MarkRead(ctx, to, []string{first[0].ID}, false)
	if err != nil || left != defaultPageSize+2 {
		t.Fatalf("marking one read leaves the rest: %d %v", left, err)
	}
	// someone else's id is a no-op
	other := emitUser(t, pool, "other")
	if left, _ := svc.MarkRead(ctx, other, []string{first[1].ID}, false); left != 0 {
		t.Fatal("must not touch another user's notifications")
	}
	if n, _ := svc.UnreadCount(ctx, to); n != defaultPageSize+2 {
		t.Fatalf("other user's mark-read leaked: %d", n)
	}
	if left, _ := svc.MarkRead(ctx, to, nil, true); left != 0 {
		t.Fatalf("mark all read: %d", left)
	}
}
