package social

import (
	"context"
	"testing"
	"time"
)

func notifCount(t *testing.T, ctx context.Context, svc *Service, user, typ string) int {
	t.Helper()
	var n int
	if err := svc.repo.pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id=$1 AND type=$2`, user, typ).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestLikeAndComment_NotifyTheAuthor(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc := NewService(NewRepository(pool), nil, badScreener{})
	author, fan, blocked := seedUser(t, pool, "na"), seedUser(t, pool, "nf"), seedUser(t, pool, "nb")
	pool.Exec(ctx, `INSERT INTO user_profiles (user_id, display_name) VALUES ($1,'Riya')`, fan)
	post, err := svc.CreatePost(ctx, &Post{AuthorID: author, Body: "hello world " + time.Now().Format("150405.000000000"), Visibility: "public"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.LikePost(ctx, post.ID, author); err != nil {
		t.Fatal(err)
	}
	if n := notifCount(t, ctx, svc, author, "post_like"); n != 0 {
		t.Fatalf("liking your own post must not notify you, got %d", n)
	}
	for i := 0; i < 3; i++ { // a repeated like is one like, one notification
		if _, err := svc.LikePost(ctx, post.ID, fan); err != nil {
			t.Fatal(err)
		}
	}
	if n := notifCount(t, ctx, svc, author, "post_like"); n != 1 {
		t.Fatalf("one like → one notification, got %d", n)
	}
	var title, link string
	pool.QueryRow(ctx, `SELECT title, deep_link FROM notifications WHERE user_id=$1 AND type='post_like'`, author).Scan(&title, &link)
	if title != "Riya liked your post" || link != "hivemind://posts/"+post.ID {
		t.Fatalf("title/link: %q %q", title, link)
	}

	if _, err := svc.CommentOnPost(ctx, &Comment{PostID: post.ID, AuthorID: fan, Body: "great post!"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CommentOnPost(ctx, &Comment{PostID: post.ID, AuthorID: fan, Body: "and another"}); err != nil {
		t.Fatal(err)
	}
	if n := notifCount(t, ctx, svc, author, "post_comment"); n != 2 {
		t.Fatalf("each comment notifies, got %d", n)
	}

	pool.Exec(ctx, `INSERT INTO blocks (user_id, blocked_user_id) VALUES ($1,$2)`, author, blocked)
	if _, err := svc.LikePost(ctx, post.ID, blocked); err == nil {
		t.Fatal("a blocked user can't even see the post")
	}
	if n := notifCount(t, ctx, svc, author, "post_like"); n != 1 {
		t.Fatalf("blocked like produced a notification: %d", n)
	}
}

func TestListPosts_ProfileVisibilityAndPaging(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc := NewService(NewRepository(pool), nil, badScreener{})
	author, friend, stranger, blocked := seedUser(t, pool, "pa"), seedUser(t, pool, "pf"), seedUser(t, pool, "ps"), seedUser(t, pool, "pb")
	pool.Exec(ctx, `INSERT INTO connections (requester_id, recipient_id, status) VALUES ($1,$2,'accepted')`, author, friend)
	pool.Exec(ctx, `INSERT INTO blocks (user_id, blocked_user_id) VALUES ($1,$2)`, author, blocked)
	run := time.Now().Format("150405.000000000")
	for i, vis := range []string{"public", "public", "connections", "private", "public"} {
		if _, err := svc.CreatePost(ctx, &Post{AuthorID: author, Body: vis + run + string(rune('a'+i)), Visibility: vis}); err != nil {
			t.Fatal(err)
		}
	}
	count := func(viewer string) int {
		var all []*Post
		token := ""
		for {
			page, next, err := svc.ListPosts(ctx, author, viewer, 2, token)
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, page...)
			if next == "" {
				break
			}
			token = next
		}
		seen := map[string]bool{}
		for _, p := range all {
			if seen[p.ID] {
				t.Fatalf("paging repeated a post")
			}
			seen[p.ID] = true
		}
		return len(all)
	}
	if n := count(author); n != 5 {
		t.Fatalf("own profile shows everything incl. private: %d", n)
	}
	if n := count(friend); n != 4 {
		t.Fatalf("a connection sees public + connections: %d", n)
	}
	if n := count(stranger); n != 3 {
		t.Fatalf("a stranger sees only public: %d", n)
	}
	if n := count(blocked); n != 0 {
		t.Fatalf("a blocked viewer sees nothing: %d", n)
	}
	if n, _ := svc.repo.CountVisibleByAuthor(ctx, author, stranger); n != 3 {
		t.Fatalf("the posts count matches what can be opened: %d", n)
	}
}
