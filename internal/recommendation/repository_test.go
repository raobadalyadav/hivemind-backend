package recommendation

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

// TestRepository_UpcomingPlanIDs_TravelCityOverride verifies flow.md §54
// Travel Mode: a caller whose home city is A can preview city B's plans by
// passing travel_city_id, without any change to their own home city_id,
// and gets today's exact behavior back when the override is left empty.
func TestRepository_UpcomingPlanIDs_TravelCityOverride(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := NewRepository(pool)

	suffix := time.Now().Format("150405.000000000")
	var homeCity, travelCity, hostID, userID string

	if err := pool.QueryRow(ctx, `INSERT INTO cities (name) VALUES ($1) RETURNING id`, "Home City "+suffix).Scan(&homeCity); err != nil {
		t.Fatalf("seed home city: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO cities (name) VALUES ($1) RETURNING id`, "Travel City "+suffix).Scan(&travelCity); err != nil {
		t.Fatalf("seed travel city: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, city_id) VALUES ($1, $2) RETURNING id`, "travel-host-"+suffix+"@example.com", homeCity,
	).Scan(&hostID); err != nil {
		t.Fatalf("seed host: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, city_id) VALUES ($1, $2) RETURNING id`, "travel-user-"+suffix+"@example.com", homeCity,
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	var homePlanID, travelPlanID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO plans (title, host_id, city_id, starts_at, ends_at, capacity, confirmed_count, currency, status)
		VALUES ('Home Plan', $1, $2, now() + interval '1 day', now() + interval '1 day 2 hour', 10, 0, 'INR', 'published')
		RETURNING id`, hostID, homeCity,
	).Scan(&homePlanID); err != nil {
		t.Fatalf("seed home plan: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO plans (title, host_id, city_id, starts_at, ends_at, capacity, confirmed_count, currency, status)
		VALUES ('Travel Plan', $1, $2, now() + interval '1 day', now() + interval '1 day 2 hour', 10, 0, 'INR', 'published')
		RETURNING id`, hostID, travelCity,
	).Scan(&travelPlanID); err != nil {
		t.Fatalf("seed travel plan: %v", err)
	}

	// No override: today's exact behavior — only the home-city plan.
	ids, err := repo.UpcomingPlanIDs(ctx, userID, "", 20)
	if err != nil {
		t.Fatalf("UpcomingPlanIDs (no override): %v", err)
	}
	if !containsID(ids, homePlanID) || containsID(ids, travelPlanID) {
		t.Errorf("expected only home-city plan without an override, got %v", ids)
	}

	// travel_city_id set: previews the travel city's plans instead.
	ids, err = repo.UpcomingPlanIDs(ctx, userID, travelCity, 20)
	if err != nil {
		t.Fatalf("UpcomingPlanIDs (travel override): %v", err)
	}
	if !containsID(ids, travelPlanID) || containsID(ids, homePlanID) {
		t.Errorf("expected only travel-city plan with travel_city_id set, got %v", ids)
	}

	// The caller's own home city_id must never be mutated by a preview.
	var cityAfter string
	if err := pool.QueryRow(ctx, `SELECT city_id FROM users WHERE id = $1`, userID).Scan(&cityAfter); err != nil {
		t.Fatalf("query user city_id: %v", err)
	}
	if cityAfter != homeCity {
		t.Errorf("travel mode preview must not persist: expected city_id %q, got %q", homeCity, cityAfter)
	}
}

func containsID(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

// TestPeopleRecommendations_IntentAndPersonalityRankFirst: with equal interest
// overlap, the person who shares the caller's social intent and personality
// answers outranks the one who doesn't (flow.md §5/§6).
func TestPeopleRecommendations_IntentAndPersonalityRankFirst(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := NewRepository(pool)

	suffix := time.Now().Format("150405.000000000")
	var city string
	if err := pool.QueryRow(ctx, `INSERT INTO cities (name) VALUES ($1) RETURNING id`, "Intent City "+suffix).Scan(&city); err != nil {
		t.Fatalf("seed city: %v", err)
	}
	mk := func(label string, intents []string, energy string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO users (email, city_id) VALUES ($1,$2) RETURNING id`,
			"int-"+label+"-"+suffix+"@example.com", city).Scan(&id); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO user_profiles (user_id, display_name, interests) VALUES ($1,$2,'{Food}')`, id, label); err != nil {
			t.Fatalf("seed profile: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO user_preferences (user_id, intents, energy_pref) VALUES ($1,$2,NULLIF($3,''))`, id, intents, energy); err != nil {
			t.Fatalf("seed prefs: %v", err)
		}
		return id
	}
	caller := mk("caller", []string{"networking"}, "quiet")
	mismatch := mk("mismatch", []string{"explore_city"}, "energetic")
	match := mk("match", []string{"networking"}, "quiet")

	ids, err := repo.PeopleRecommendationUserIDs(ctx, caller, "", 10)
	if err != nil {
		t.Fatalf("PeopleRecommendationUserIDs: %v", err)
	}
	if len(ids) != 2 || ids[0] != match || ids[1] != mismatch {
		t.Fatalf("shared intent+personality should rank first: got %v (match=%s mismatch=%s)", ids, match, mismatch)
	}
}
