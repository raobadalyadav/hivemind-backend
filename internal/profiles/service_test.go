package profiles

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hivemind/backend/pkg/media/mediatest"
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

// seedUser creates a user plus the empty profile row signup would create.
func seedUser(t *testing.T, pool *pgxpool.Pool, label string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`,
		"pf-"+label+"-"+time.Now().Format("150405.000000000")+"@example.com").Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_profiles (user_id, display_name) VALUES ($1, $2)`, id, label); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	return id
}

func sp(s string) *string { return &s }

func TestUpdateProfile_PartialUpdateDoesNotClobber(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc := NewService(NewRepository(pool))
	u := seedUser(t, pool, "clobber")

	// lower-case input must be stored in catalog casing
	if _, err := svc.UpdateProfile(ctx, &Update{UserID: u, Bio: sp("hello"), Occupation: sp("Designer"),
		Interests: []string{"food", "COFFEE", "Sports", "fitness", "Travel"}, Hobbies: []string{"chess"}}); err != nil {
		t.Fatalf("first update: %v", err)
	}
	p, err := svc.UpdateProfile(ctx, &Update{UserID: u, Education: sp("BTech")}) // only education
	if err != nil {
		t.Fatalf("second update: %v", err)
	}
	if p.Bio != "hello" || p.Occupation != "Designer" || len(p.Interests) != 5 || len(p.Hobbies) != 1 {
		t.Fatalf("an update that omits fields must not clobber them: %+v", p)
	}
	if p.Education != "BTech" {
		t.Fatalf("education not set: %+v", p)
	}
	if !strings.Contains(strings.Join(p.Interests, ","), "Coffee") {
		t.Fatalf("interests should be stored in catalog casing, got %v", p.Interests)
	}
	// an explicitly empty optional string DOES clear
	if p, _ = svc.UpdateProfile(ctx, &Update{UserID: u, Bio: sp("")}); p.Bio != "" {
		t.Fatalf("explicit empty bio should clear it, got %q", p.Bio)
	}
}

func TestUpdateProfile_InterestAndFieldValidation(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc := NewService(NewRepository(pool))
	u := seedUser(t, pool, "validate")

	bad := []*Update{
		{UserID: u, Interests: []string{"Food", "Coffee"}},                                                  // < 5
		{UserID: u, Interests: []string{"Food", "Coffee", "Sports", "Fitness", "Underwater Basketweaving"}}, // not in catalog
		{UserID: u, Gender: sp("robot")},
		{UserID: u, Hobbies: []string{" "}},
		{UserID: u, Bio: sp(strings.Repeat("x", 501))},
	}
	for i, b := range bad {
		if _, err := svc.UpdateProfile(ctx, b); err != ErrInvalidInput {
			t.Errorf("case %d must be rejected, got %v", i, err)
		}
	}
	if p, err := svc.UpdateProfile(ctx, &Update{UserID: u, Gender: sp("non_binary")}); err != nil || p.Gender != "non_binary" {
		t.Fatalf("valid gender: %+v err=%v", p, err)
	}
}

func TestProfilePhotos_CapOwnershipAndReorder(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	fm := mediatest.New(pool)
	svc := NewService(NewRepository(pool)).WithMedia(fm)
	a, b := seedUser(t, pool, "photoA"), seedUser(t, pool, "photoB")

	if _, err := svc.AddPhoto(ctx, a, "https://cdn.example/x.jpg"); err != ErrInvalidInput {
		t.Fatalf("a link is not an upload id, got %v", err)
	}
	if _, err := svc.AddPhoto(ctx, a, fm.Add(b, "image").ID); err != ErrInvalidInput {
		t.Fatalf("B's upload can't become A's photo, got %v", err)
	}
	if _, err := svc.AddPhoto(ctx, a, fm.Add(a, "video").ID); err != ErrInvalidInput {
		t.Fatalf("a video can't be a profile photo, got %v", err)
	}
	var ids []string
	for i := 0; i < MaxPhotos; i++ {
		p, err := svc.AddPhoto(ctx, a, fm.Add(a, "image").ID)
		if err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
		if p.ThumbURL == "" || p.Width != 1080 {
			t.Fatalf("photo carries thumbnail and size: %+v", p)
		}
		ids = append(ids, p.ID)
	}
	if _, err := svc.AddPhoto(ctx, a, fm.Add(a, "image").ID); err != ErrTooManyPhotos {
		t.Fatalf("7th photo must be refused, got %v", err)
	}
	if err := svc.DeletePhoto(ctx, b, ids[0]); err != ErrForbidden {
		t.Fatalf("user B must not delete A's photo, got %v", err)
	}
	if ps, _ := svc.repo.ListPhotos(ctx, a); len(ps) != MaxPhotos {
		t.Fatalf("A's photo must survive B's attempt, have %d", len(ps))
	}

	rev := make([]string, len(ids))
	for i, id := range ids {
		rev[len(ids)-1-i] = id
	}
	ps, err := svc.ReorderPhotos(ctx, a, rev)
	if err != nil || ps[0].ID != ids[len(ids)-1] {
		t.Fatalf("reorder: %+v err=%v", ps, err)
	}
	if _, err := svc.ReorderPhotos(ctx, a, ids[:3]); err != ErrInvalidInput {
		t.Fatalf("a partial id set must be rejected, got %v", err)
	}
	if _, err := svc.ReorderPhotos(ctx, b, ids); err != ErrInvalidInput {
		t.Fatalf("B can't reorder A's photos, got %v", err)
	}
	if err := svc.DeletePhoto(ctx, a, ids[0]); err != nil {
		t.Fatalf("owner deletes: %v", err)
	}
}

func TestPreferences_IntentAndPersonality(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc := NewService(NewRepository(pool))
	u := seedUser(t, pool, "prefs")

	if p, _ := svc.GetPrefs(ctx, u); len(p.Intents) != 0 || p.EnergyPref != "" {
		t.Fatalf("fresh user has no answers: %+v", p)
	}
	if _, err := svc.SetIntents(ctx, u, []string{"make_friends", "bogus"}); err != ErrInvalidInput {
		t.Fatalf("unknown intent must be rejected, got %v", err)
	}
	if p, err := svc.SetIntents(ctx, u, []string{"networking", "networking", "explore_city"}); err != nil || len(p.Intents) != 2 {
		t.Fatalf("intents deduped: %+v err=%v", p, err)
	}
	if _, err := svc.SetPersonality(ctx, u, &Prefs{EnergyPref: "spicy"}); err != ErrInvalidInput {
		t.Fatalf("bad quiz answer must be rejected, got %v", err)
	}
	p, err := svc.SetPersonality(ctx, u, &Prefs{EnergyPref: "quiet", TimePref: "evening"})
	if err != nil || p.EnergyPref != "quiet" || p.GroupPref != "" || len(p.Intents) != 2 {
		t.Fatalf("personality must keep intents and leave skipped answers blank: %+v err=%v", p, err)
	}
}

func TestInterestCatalog_HasSeededNames(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	in, cat, err := NewService(NewRepository(pool)).InterestCatalog(context.Background())
	if err != nil || len(in) < 15 || len(cat) < 15 {
		t.Fatalf("catalog should include the 15 flow.md interests: %d/%d err=%v", len(in), len(cat), err)
	}
}
