package venues

import (
	"context"
	"testing"
	"time"
)

// TestRepository_RepeatVisitors seeds one user with two attended bookings
// at the venue's plans and another with only one, and verifies only the
// genuine repeat visitor is returned.
func TestRepository_RepeatVisitors(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := NewRepository(pool)

	owner := seedHostUser(t, pool)
	cityID := seedCity(t, pool)
	venue, err := repo.Create(ctx, &Venue{OwnerHostID: owner, CityID: cityID, Name: "Repeat Visitor Venue", Capacity: 50})
	if err != nil {
		t.Fatalf("Create venue: %v", err)
	}

	repeatUser := seedHostUser(t, pool)
	onceUser := seedHostUser(t, pool)

	seedAttendedBooking := func(userID string) {
		suffix := time.Now().Format("150405.000000000")
		var planID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO plans (title, host_id, venue_id, starts_at, ends_at, capacity, confirmed_count, currency, status)
			VALUES ('Repeat Visitor Plan', $1, $2, now() - interval '1 day', now() - interval '1 day' + interval '2 hour', 50, 0, 'INR', 'published')
			RETURNING id`, owner, venue.ID,
		).Scan(&planID); err != nil {
			t.Fatalf("seed plan: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO bookings (plan_id, user_id, status, price_minor, currency, idempotency_key) VALUES ($1, $2, 'attended', 0, 'INR', $3)`,
			planID, userID, "repeat-visitor-"+suffix+userID,
		); err != nil {
			t.Fatalf("seed booking: %v", err)
		}
	}

	seedAttendedBooking(repeatUser)
	seedAttendedBooking(repeatUser)
	seedAttendedBooking(onceUser)

	visitors, err := repo.RepeatVisitors(ctx, venue.ID, 10)
	if err != nil {
		t.Fatalf("RepeatVisitors: %v", err)
	}
	if len(visitors) != 1 {
		t.Fatalf("expected exactly 1 repeat visitor, got %d: %+v", len(visitors), visitors)
	}
	if visitors[0].UserID != repeatUser || visitors[0].VisitCount != 2 {
		t.Errorf("expected repeatUser with 2 visits, got %+v", visitors[0])
	}
}
