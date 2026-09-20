package stories

import (
	"context"
	"testing"
	"time"
)

func TestStoryView_LikeAndViewers(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc := NewService(NewRepository(pool), screener{})
	author, friend, stranger := seedUser(t, pool, "va"), seedUser(t, pool, "vf"), seedUser(t, pool, "vs")
	pool.Exec(ctx, `INSERT INTO connections (requester_id, recipient_id, status) VALUES ($1,$2,'accepted')`, author, friend)
	var story string
	if err := pool.QueryRow(ctx, `INSERT INTO stories (author_id, media_url, audience, created_at, expires_at)
		VALUES ($1,'https://x/s.jpg','connections', now(), now() + interval '24 hours') RETURNING id`, author).Scan(&story); err != nil {
		t.Fatal(err)
	}
	count := func(user, typ string) int {
		var n int
		pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id=$1 AND type=$2`, user, typ).Scan(&n)
		return n
	}

	if err := svc.MarkViewed(ctx, story, stranger); err != ErrNotFound {
		t.Fatalf("a non-connection can't view (and can't probe ids): %v", err)
	}
	if err := svc.MarkViewed(ctx, story, author); err != nil || count(author, "story_view") != 0 {
		t.Fatalf("viewing your own story is a no-op: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := svc.MarkViewed(ctx, story, friend); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(author, "story_view"); n != 1 {
		t.Fatalf("re-watching tells the author once, got %d", n)
	}

	if n, err := svc.SetLike(ctx, story, friend, true); err != nil || n != 1 {
		t.Fatalf("like: %d %v", n, err)
	}
	if n, _ := svc.SetLike(ctx, story, friend, true); n != 1 || count(author, "story_like") != 1 {
		t.Fatalf("a repeated like is idempotent and notifies once: %d/%d", n, count(author, "story_like"))
	}
	if _, err := svc.SetLike(ctx, story, stranger, true); err != ErrNotFound {
		t.Fatalf("a stranger can't like: %v", err)
	}

	if _, err := svc.ListViewers(ctx, story, friend); err != ErrForbidden {
		t.Fatalf("only the author sees viewers: %v", err)
	}
	vs, err := svc.ListViewers(ctx, story, author)
	if err != nil || len(vs) != 1 || vs[0].UserID != friend || !vs[0].Liked || vs[0].DisplayName != "vf" {
		t.Fatalf("viewers: %+v %v", vs, err)
	}
	mine, _ := svc.ListMyStories(ctx, author, false)
	if len(mine) != 1 || mine[0].ViewerCount != 1 || mine[0].LikeCount != 1 {
		t.Fatalf("counts on the author's story: %+v", mine)
	}
	seen, _ := svc.ListStories(ctx, friend)
	if len(seen) != 1 || !seen[0].Stories[0].LikedByMe {
		t.Fatalf("liked_by_me for the viewer: %+v", seen)
	}

	// once expired it can't be viewed any more
	pool.Exec(ctx, `UPDATE stories SET expires_at = now() - interval '1 second', created_at = now() - interval '25 hours' WHERE id=$1`, story)
	if err := svc.MarkViewed(ctx, story, friend); err != ErrNotFound {
		t.Fatalf("expired stories are gone: %v", err)
	}
	_ = time.Now
}
