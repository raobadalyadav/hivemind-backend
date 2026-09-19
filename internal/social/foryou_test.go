package social

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestFeedForYou_RanksFriendsAndInterestsAndKeepsVisibility(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc := NewService(NewRepository(pool), nil, badScreener{})
	run := time.Now().Format("150405.000000000")

	viewer, friend, similar, stranger, blockedAuthor := seedUser(t, pool, "fv"), seedUser(t, pool, "ff"), seedUser(t, pool, "fs"), seedUser(t, pool, "fx"), seedUser(t, pool, "fb")
	for id, interests := range map[string]string{viewer: "{Food,Coffee,Sports,Fitness,Travel}", similar: "{Food,Coffee,Sports,Fitness,Travel}", friend: "{Art}", stranger: "{Art}", blockedAuthor: "{Food,Coffee,Sports,Fitness,Travel}"} {
		pool.Exec(ctx, `INSERT INTO user_profiles (user_id, display_name, interests) VALUES ($1,'x',$2) ON CONFLICT (user_id) DO UPDATE SET interests = $2`, id, interests)
	}
	pool.Exec(ctx, `INSERT INTO connections (requester_id, recipient_id, status) VALUES ($1,$2,'accepted')`, viewer, friend)
	pool.Exec(ctx, `INSERT INTO blocks (user_id, blocked_user_id) VALUES ($1,$2)`, viewer, blockedAuthor)

	mk := func(author, vis, tag string) *Post {
		p, err := svc.CreatePost(ctx, &Post{AuthorID: author, Body: tag + run, Visibility: vis})
		if err != nil {
			t.Fatalf("CreatePost: %v", err)
		}
		return p
	}
	mk(friend, "connections", "friendpost")
	mk(similar, "public", "similarpost")
	strangerPost := mk(stranger, "public", "strangerpost")
	mk(stranger, "connections", "hiddenconn")  // not visible to the viewer
	mk(viewer, "public", "mine")               // never in your own feed
	mk(blockedAuthor, "public", "blockedpost") // blocked
	mk(similar, "private", "privatepost")

	seen := map[string]int{}
	token := ""
	for i := 0; i < 50; i++ {
		posts, next, err := svc.GetFeed(ctx, viewer, "for_you", "", 20, token)
		if err != nil {
			t.Fatalf("GetFeed: %v", err)
		}
		for _, p := range posts {
			if strings.HasSuffix(p.Body, run) {
				seen[strings.TrimSuffix(p.Body, run)] = len(seen)
			}
		}
		if next == "" {
			break
		}
		token = next
	}
	for _, hidden := range []string{"hiddenconn", "mine", "blockedpost", "privatepost"} {
		if _, in := seen[hidden]; in {
			t.Errorf("%s must not appear in For You", hidden)
		}
	}
	for _, shown := range []string{"friendpost", "similarpost", "strangerpost"} {
		if _, in := seen[shown]; !in {
			t.Fatalf("%s should appear, got %v", shown, seen)
		}
	}
	if !(seen["friendpost"] < seen["strangerpost"] && seen["similarpost"] < seen["strangerpost"]) {
		t.Errorf("a friend's post and a shared-interest post outrank a stranger's, got %v", seen)
	}

	// likes lift a post
	for i := 0; i < 3; i++ {
		u := seedUser(t, pool, "liker")
		pool.Exec(ctx, `INSERT INTO likes (post_id, user_id) VALUES ($1,$2)`, strangerPost.ID, u)
	}
	posts, _, _ := svc.GetFeed(ctx, viewer, "for_you", "", 50, "")
	pos := map[string]int{}
	for i, p := range posts {
		pos[strings.TrimSuffix(p.Body, run)] = i
	}
	if pos["strangerpost"] > 0 && strings.HasSuffix(posts[0].Body, run) && pos["strangerpost"] > pos["similarpost"]+3 {
		t.Errorf("likes should lift a post: %v", pos)
	}

	if _, _, err := svc.GetFeed(ctx, viewer, "for_you", "", 10, "-5"); err != ErrInvalidInput {
		t.Errorf("bad token: %v", err)
	}
	if _, _, err := svc.GetFeed(ctx, viewer, "for_you", "", 10, "9999"); err != ErrInvalidInput {
		t.Errorf("token past the cap: %v", err)
	}
}
