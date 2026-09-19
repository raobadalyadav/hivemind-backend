package availability

import (
	"context"
	"testing"
	"time"
)

// TestRepository_FindMatches_ActivityTypeIsExactNotPattern is a regression
// test for a wildcard-injection finding: activity_type used to be compared
// with ILIKE, so a caller broadcasting activity_type="%" (or containing
// '_') would match every other open window regardless of its real
// activity, enumerating other users' availability broadly. It's now
// case-insensitive exact equality — a literal "%" must not match a
// "badminton" window.
func TestRepository_FindMatches_ActivityTypeIsExactNotPattern(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	caller := seedUser(t, pool, "caller")
	other := seedUser(t, pool, "other")

	if _, err := pool.Exec(ctx, `
		UPDATE users SET last_location = ST_SetSRID(ST_MakePoint(77.5946, 12.9716), 4326)::geography
		WHERE id IN ($1, $2)`,
		caller, other,
	); err != nil {
		t.Fatalf("seed locations: %v", err)
	}

	repo := NewRepository(pool)
	now := time.Now()

	if _, err := repo.Create(ctx, &Window{UserID: other, ActivityType: "badminton", StartsAt: now, EndsAt: now.Add(2 * time.Hour)}); err != nil {
		t.Fatalf("seed other's window: %v", err)
	}

	wildcard := &Window{ActivityType: "%", StartsAt: now, EndsAt: now.Add(2 * time.Hour)}
	matches, err := repo.FindMatches(ctx, caller, wildcard, 10)
	if err != nil {
		t.Fatalf("FindMatches with wildcard activity_type: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf(`expected activity_type="%%" to match nothing (exact comparison, not a LIKE pattern), got %d matches`, len(matches))
	}

	exact := &Window{ActivityType: "BADMINTON", StartsAt: now, EndsAt: now.Add(2 * time.Hour)}
	matches, err = repo.FindMatches(ctx, caller, exact, 50)
	if err != nil {
		t.Fatalf("FindMatches with exact activity_type: %v", err)
	}
	found := false
	for _, m := range matches {
		if m.UserID == other {
			found = true
			break
		}
	}
	// Checks other is present, not that it's the only match — this test
	// runs against the real dev DB (not a rolled-back transaction), so
	// earlier runs' own "badminton" windows at the same coordinates can
	// still be 'open' and legitimately match too.
	if !found {
		t.Errorf("expected case-insensitive exact match to find other's badminton window among %+v", matches)
	}
}
