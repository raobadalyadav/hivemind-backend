package social

import (
	"context"
	"strings"
	"testing"
	"time"
)

type badScreener struct{}

func (badScreener) Screen(_ context.Context, body string) (string, string) {
	if body == "BADWORD" {
		return "severe", "test"
	}
	return "", ""
}

func TestFeedVisibility_ConnectionsAndCommunityPosts(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc := NewService(NewRepository(pool), nil, badScreener{})

	author, friend, member, stranger := seedUser(t, pool, "fa"), seedUser(t, pool, "ff"), seedUser(t, pool, "fm"), seedUser(t, pool, "fs")
	var comm string
	if err := pool.QueryRow(ctx, `INSERT INTO communities (name, owner_id) VALUES ($1, $2) RETURNING id`,
		"Feed Club "+time.Now().Format("150405.000000000"), author).Scan(&comm); err != nil {
		t.Fatalf("seed community: %v", err)
	}
	pool.Exec(ctx, `INSERT INTO community_members (community_id, user_id) VALUES ($1,$2),($1,$3)`, comm, author, member)
	pool.Exec(ctx, `INSERT INTO connections (requester_id, recipient_id, status) VALUES ($1,$2,'accepted')`, author, friend)

	run := time.Now().Format("150405.000000000")
	mk := func(vis, comm, body string) *Post {
		t.Helper()
		p, err := svc.CreatePost(ctx, &Post{AuthorID: author, Body: body + run, Visibility: vis, CommunityID: comm})
		if err != nil {
			t.Fatalf("CreatePost(%s): %v", vis, err)
		}
		return p
	}
	priv, pub := mk("private", "", "priv"), mk("public", "", "pub")
	conn, cm := mk("connections", "", "conn"), mk("community", comm, "comm")

	canSee := func(viewer string, p *Post) bool {
		_, err := svc.GetPost(ctx, p.ID, viewer)
		if err != nil && err != ErrPostNotFound {
			t.Fatalf("unexpected error: %v", err)
		}
		return err == nil
	}
	cases := []struct {
		who  string
		name string
		post *Post
		want bool
	}{
		{author, "author/private", priv, true}, {friend, "friend/private", priv, false}, {stranger, "stranger/private", priv, false},
		{stranger, "stranger/public", pub, true},
		{friend, "friend/connections", conn, true}, {stranger, "stranger/connections", conn, false}, {member, "member/connections", conn, false},
		{member, "member/community", cm, true}, {friend, "friend/community", cm, false}, {stranger, "stranger/community", cm, false},
	}
	for _, c := range cases {
		if got := canSee(c.who, c.post); got != c.want {
			t.Errorf("%s: visible=%v, want %v", c.name, got, c.want)
		}
	}

	// every action shares the rule: a stranger can't comment on / like / save / share a hidden post
	if _, err := svc.CommentOnPost(ctx, &Comment{PostID: conn.ID, AuthorID: stranger, Body: "hi"}); err != ErrPostNotFound {
		t.Errorf("comment on hidden post: %v", err)
	}
	if _, err := svc.LikePost(ctx, cm.ID, stranger); err != ErrPostNotFound {
		t.Errorf("like hidden post: %v", err)
	}
	if err := svc.SavePost(ctx, priv.ID, friend); err != ErrPostNotFound {
		t.Errorf("save hidden post: %v", err)
	}
	if _, err := svc.SharePost(ctx, priv.ID, stranger); err != ErrPostNotFound {
		t.Errorf("share hidden post: %v", err)
	}
	if link, err := svc.SharePost(ctx, pub.ID, stranger); err != nil || link != "hivemind://posts/"+pub.ID {
		t.Errorf("share public post: %q %v", link, err)
	}

	// posting rules
	if _, err := svc.CreatePost(ctx, &Post{AuthorID: stranger, Body: "x", Visibility: "community", CommunityID: comm}); err != ErrNotMember {
		t.Errorf("non-member can't post into a community: %v", err)
	}
	if _, err := svc.CreatePost(ctx, &Post{AuthorID: author, Body: "x", Visibility: "community"}); err != ErrInvalidInput {
		t.Errorf("community visibility needs community_id: %v", err)
	}
	if _, err := svc.CreatePost(ctx, &Post{AuthorID: author, Body: "x", Visibility: "public", CommunityID: comm}); err != ErrInvalidInput {
		t.Errorf("community_id needs community visibility: %v", err)
	}
	if _, err := svc.CreatePost(ctx, &Post{AuthorID: author, Body: "x", Visibility: "friends-of-friends"}); err != ErrInvalidInput {
		t.Errorf("unknown visibility must be rejected, not treated as public: %v", err)
	}
	if _, err := svc.CreatePost(ctx, &Post{AuthorID: author, Body: "x", Visibility: "public", Media: []Media{{URL: "http://x/v.mp4", Type: "video"}}}); err != ErrInvalidInput {
		t.Errorf("non-https media: %v", err)
	}
	if v, err := svc.CreatePost(ctx, &Post{AuthorID: author, Body: "clip", Visibility: "public", Media: []Media{{URL: "https://cdn.example/v.mp4", Type: "video"}}}); err != nil || len(v.Media) != 1 {
		t.Errorf("video post: %+v %v", v, err)
	}
	if _, err := svc.CommentOnPost(ctx, &Comment{PostID: pub.ID, AuthorID: stranger, Body: "BADWORD"}); err != ErrContentRejected {
		t.Errorf("comments are screened now: %v", err)
	}

	// feeds
	titles := func(viewer, scope, community string) map[string]bool {
		t.Helper()
		out := map[string]bool{}
		token := ""
		for i := 0; i < 5000; i++ {
			posts, next, err := svc.GetFeed(ctx, viewer, scope, community, 50, token)
			if err != nil {
				t.Fatalf("GetFeed(%s,%s): %v", viewer[:4], scope, err)
			}
			for _, p := range posts {
				if strings.HasSuffix(p.Body, run) { // ignore other runs' leftovers
					out[strings.TrimSuffix(p.Body, run)] = true
				}
			}
			if next == "" {
				return out
			}
			token = next
		}
		t.Fatal("feed paging did not terminate")
		return nil
	}
	if f := titles(stranger, "global", ""); !f["pub"] || f["priv"] || f["conn"] || f["comm"] {
		t.Errorf("stranger's global feed: %v", f)
	}
	if f := titles(friend, "global", ""); !f["pub"] || !f["conn"] || f["priv"] || f["comm"] {
		t.Errorf("friend's global feed: %v", f)
	}
	if f := titles(friend, "connections", ""); !f["conn"] || !f["pub"] || f["comm"] || f["priv"] {
		t.Errorf("friend's connections feed: %v", f)
	}
	if f := titles(member, "community", comm); !f["comm"] || f["pub"] {
		t.Errorf("member's community feed: %v", f)
	}
	if _, _, err := svc.GetFeed(ctx, stranger, "community", comm, 10, ""); err != ErrPostNotFound {
		t.Errorf("non-member can't read a community feed: %v", err)
	}
	if _, _, err := svc.GetFeed(ctx, member, "global", "", 10, "garbage!!"); err != ErrInvalidInput {
		t.Errorf("bad page token: %v", err)
	}

	// blocks hide everything, both directions
	pool.Exec(ctx, `INSERT INTO blocks (user_id, blocked_user_id) VALUES ($1,$2)`, stranger, author)
	if canSee(stranger, pub) {
		t.Error("a blocked author's public post must be hidden from the blocker")
	}
	if f := titles(stranger, "global", ""); f["pub"] {
		t.Error("and absent from their feed")
	}

	// save / unsave / list, and counters
	if err := svc.SavePost(ctx, pub.ID, friend); err != nil {
		t.Fatalf("save: %v", err)
	}
	svc.SavePost(ctx, pub.ID, friend) // idempotent
	saved, _ := svc.ListSavedPosts(ctx, friend)
	if len(saved) != 1 || !saved[0].SavedByMe {
		t.Fatalf("saved list: %+v", saved)
	}
	svc.LikePost(ctx, pub.ID, friend)
	svc.CommentOnPost(ctx, &Comment{PostID: pub.ID, AuthorID: friend, Body: "nice"})
	got, _ := svc.GetPost(ctx, pub.ID, friend)
	if got.LikeCount != 1 || got.CommentCount != 1 || !got.LikedByMe {
		t.Fatalf("counters: %+v", got)
	}
	svc.UnsavePost(ctx, pub.ID, friend)
	if saved, _ = svc.ListSavedPosts(ctx, friend); len(saved) != 0 {
		t.Fatalf("unsave failed: %+v", saved)
	}
}
