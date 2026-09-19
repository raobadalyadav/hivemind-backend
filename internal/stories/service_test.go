package stories

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type screener struct{}

func (screener) Screen(_ context.Context, body string) (string, string) {
	switch body {
	case "BADWORD":
		return "severe", "x"
	case "BORDERLINE":
		return "review", "x"
	}
	return "safe", "" // what the real Screener returns for clean text
}

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
		"st-"+label+"-"+time.Now().Format("150405.000000000")+"@example.com").Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	pool.Exec(ctx, `INSERT INTO user_profiles (user_id, display_name) VALUES ($1,$2)`, id, label)
	return id
}

func TestStories_AudienceAndExpiry(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	clock := time.Now()
	svc := NewService(NewRepository(pool), screener{})
	svc.now = func() time.Time { return clock }

	author, friend, member, stranger := seedUser(t, pool, "sa"), seedUser(t, pool, "sf"), seedUser(t, pool, "sm"), seedUser(t, pool, "ss")
	var comm string
	if err := pool.QueryRow(ctx, `INSERT INTO communities (name, owner_id) VALUES ($1,$2) RETURNING id`,
		"Story Club "+time.Now().Format("150405.000000000"), author).Scan(&comm); err != nil {
		t.Fatalf("seed community: %v", err)
	}
	pool.Exec(ctx, `INSERT INTO community_members (community_id, user_id) VALUES ($1,$2),($1,$3)`, comm, author, member)
	pool.Exec(ctx, `INSERT INTO connections (requester_id, recipient_id, status) VALUES ($1,$2,'accepted')`, author, friend)

	img := "https://cdn.example/s.jpg"
	for name, bad := range map[string]*Story{
		"http media":           {AuthorID: author, MediaURL: "http://x/s.jpg", Audience: "connections"},
		"unknown audience":     {AuthorID: author, MediaURL: img, Audience: "everyone"},
		"community w/o id":     {AuthorID: author, MediaURL: img, Audience: "community"},
		"id on connections":    {AuthorID: author, MediaURL: img, Audience: "connections", CommunityID: comm},
		"bad media type":       {AuthorID: author, MediaURL: img, Audience: "connections", MediaType: "gif"},
		"non-member community": {AuthorID: stranger, MediaURL: img, Audience: "community", CommunityID: comm},
		"screened caption":     {AuthorID: author, MediaURL: img, Audience: "connections", Caption: "BADWORD"},
	} {
		if _, err := svc.CreateStory(ctx, bad); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}

	conn, err := svc.CreateStory(ctx, &Story{AuthorID: author, MediaURL: img, Audience: "connections", Caption: "hi"})
	if err != nil {
		t.Fatalf("connections story: %v", err)
	}
	if got := conn.ExpiresAt.Sub(conn.CreatedAt); got != Lifetime {
		t.Fatalf("expiry is fixed at 24h server-side, got %v", got)
	}
	comStory, err := svc.CreateStory(ctx, &Story{AuthorID: author, MediaURL: img, Audience: "community", CommunityID: comm})
	if err != nil {
		t.Fatalf("community story: %v", err)
	}
	archived, _ := svc.CreateStory(ctx, &Story{AuthorID: author, MediaURL: img, Audience: "connections", KeepArchive: true})

	ids := func(viewer string) map[string]bool {
		t.Helper()
		groups, err := svc.ListStories(ctx, viewer)
		if err != nil {
			t.Fatalf("ListStories: %v", err)
		}
		out := map[string]bool{}
		for _, g := range groups {
			for _, s := range g.Stories {
				out[s.ID] = true
			}
		}
		return out
	}
	if v := ids(friend); !v[conn.ID] || v[comStory.ID] {
		t.Errorf("friend sees the connections story only: %v", v)
	}
	if v := ids(member); v[conn.ID] || !v[comStory.ID] {
		t.Errorf("community member sees the community story only: %v", v)
	}
	if v := ids(stranger); len(v) != 0 {
		t.Errorf("stranger sees nothing: %v", v)
	}
	if v := ids(author); !v[conn.ID] || !v[comStory.ID] {
		t.Errorf("author sees their own: %v", v)
	}

	pool.Exec(ctx, `INSERT INTO blocks (user_id, blocked_user_id) VALUES ($1,$2)`, friend, author)
	if v := ids(friend); len(v) != 0 {
		t.Errorf("a block hides the author's stories: %v", v)
	}

	// delete: only the owner
	if err := svc.DeleteStory(ctx, comStory.ID, member); err != ErrForbidden {
		t.Errorf("non-owner delete: %v", err)
	}
	if err := svc.DeleteStory(ctx, comStory.ID, author); err != nil {
		t.Errorf("owner delete: %v", err)
	}
	if err := svc.DeleteStory(ctx, comStory.ID, author); err != ErrNotFound {
		t.Errorf("second delete: %v", err)
	}

	// 25 hours later: expired stories vanish from every read path; the archived one survives the purge
	clock = clock.Add(25 * time.Hour)
	pool.Exec(ctx, `DELETE FROM blocks WHERE user_id = $1`, friend)
	if v := ids(friend); len(v) != 0 {
		t.Errorf("expired stories must not be listed: %v", v)
	}
	if mine, _ := svc.ListMyStories(ctx, author, false); len(mine) != 0 {
		t.Errorf("live list should be empty: %d", len(mine))
	}
	mine, _ := svc.ListMyStories(ctx, author, true)
	if len(mine) != 1 || mine[0].ID != archived.ID {
		t.Errorf("archive should hold only the keep_archive story: %+v", mine)
	}
	n, err := svc.PurgeExpired(ctx, clock)
	if err != nil || n < 1 {
		t.Errorf("purge should delete the expired non-archived story: n=%d err=%v", n, err)
	}
	var left int
	pool.QueryRow(ctx, `SELECT count(*) FROM stories WHERE author_id = $1`, author).Scan(&left)
	if left != 1 {
		t.Errorf("only the archived story should remain, have %d", left)
	}
}
