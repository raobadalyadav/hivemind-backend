package venues

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

func seedHostUser(t *testing.T, pool *pgxpool.Pool) (userID string) {
	t.Helper()
	suffix := time.Now().Format("150405.000000000")
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email) VALUES ($1) RETURNING id`, "venues-test-"+suffix+"@example.com",
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return userID
}

func seedCity(t *testing.T, pool *pgxpool.Pool) (cityID string) {
	t.Helper()
	suffix := time.Now().Format("150405.000000000")
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO cities (name) VALUES ($1) RETURNING id`, "Venues Test City "+suffix,
	).Scan(&cityID); err != nil {
		t.Fatalf("seed city: %v", err)
	}
	return cityID
}

func TestService_UpdateVenue_RejectsNonOwner(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	owner := seedHostUser(t, pool)
	stranger := seedHostUser(t, pool)

	repo := NewRepository(pool)
	svc := NewService(repo, nil)

	venue, err := svc.CreateVenue(ctx, &Venue{OwnerHostID: owner, CityID: seedCity(t, pool), Name: "Test Venue", Capacity: 10})
	if err != nil {
		t.Fatalf("CreateVenue: %v", err)
	}

	if _, err := svc.UpdateVenue(ctx, venue.ID, stranger, "Renamed", "", 20); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden for non-owner update, got %v", err)
	}

	updated, err := svc.UpdateVenue(ctx, venue.ID, owner, "Renamed", "", 20)
	if err != nil {
		t.Fatalf("owner update should succeed: %v", err)
	}
	if updated.Name != "Renamed" || updated.Capacity != 20 {
		t.Errorf("update did not apply: got name=%q capacity=%d", updated.Name, updated.Capacity)
	}
}

func TestService_GetVenueDashboard_RejectsNonOwner(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	owner := seedHostUser(t, pool)
	stranger := seedHostUser(t, pool)

	repo := NewRepository(pool)
	svc := NewService(repo, nil)

	venue, err := svc.CreateVenue(ctx, &Venue{OwnerHostID: owner, CityID: seedCity(t, pool), Name: "Dashboard Venue", Capacity: 10})
	if err != nil {
		t.Fatalf("CreateVenue: %v", err)
	}

	if _, err := svc.GetVenueDashboard(ctx, venue.ID, stranger); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden for non-owner dashboard read, got %v", err)
	}
	if _, err := svc.GetVenueDashboard(ctx, venue.ID, owner); err != nil {
		t.Fatalf("owner dashboard read should succeed: %v", err)
	}
}

type fakeEntitlementChecker struct{ granted bool }

func (f fakeEntitlementChecker) HasEntitlement(ctx context.Context, userID, key string) (bool, error) {
	return f.granted, nil
}

// TestService_GetVenueDashboard_GatesRepeatVisitorsOnEntitlement verifies
// the venue_pro-gated CRM feature: repeat_visitors is populated only when
// the caller holds the entitlement, omitted otherwise — without the
// entitlement check ever blocking the rest of the dashboard.
func TestService_GetVenueDashboard_GatesRepeatVisitorsOnEntitlement(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	owner := seedHostUser(t, pool)
	repo := NewRepository(pool)

	venue, err := repo.Create(ctx, &Venue{OwnerHostID: owner, CityID: seedCity(t, pool), Name: "Entitlement Venue", Capacity: 10})
	if err != nil {
		t.Fatalf("Create venue: %v", err)
	}

	withoutEntitlement := NewService(repo, fakeEntitlementChecker{granted: false})
	d, err := withoutEntitlement.GetVenueDashboard(ctx, venue.ID, owner)
	if err != nil {
		t.Fatalf("GetVenueDashboard (no entitlement): %v", err)
	}
	if d.RepeatVisitors != nil {
		t.Errorf("expected no repeat_visitors without venue_pro, got %v", d.RepeatVisitors)
	}

	withEntitlement := NewService(repo, fakeEntitlementChecker{granted: true})
	if _, err := withEntitlement.GetVenueDashboard(ctx, venue.ID, owner); err != nil {
		t.Fatalf("GetVenueDashboard (with entitlement): %v", err)
	}
}
