package bookings

import (
	"context"
	"testing"

	"github.com/hivemind/backend/pkg/idempotency"
	"github.com/redis/go-redis/v9"
)

func testGuard(t *testing.T) *idempotency.Guard {
	t.Helper()
	addr := "localhost:6379"
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Skipf("skipping: cannot connect to redis: %v", err)
	}
	return idempotency.NewGuard(rdb)
}

func TestService_CancelBookingAsUser_RejectsNonOwner(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	owner, planID := seedUserAndPlan(t, pool, 5)
	stranger, _ := seedUserAndPlan(t, pool, 0)

	repo := NewRepository(pool)
	svc := NewService(repo, testGuard(t))

	booking, err := repo.Create(ctx, &Booking{PlanID: planID, UserID: owner})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// A stranger (not the booking owner, not the plan host, not an admin)
	// must not be able to cancel someone else's booking.
	_, err = svc.CancelBookingAsUser(ctx, booking.ID, stranger, "user", "not mine")
	if err != ErrForbidden {
		t.Fatalf("expected ErrForbidden for non-owner cancel, got %v", err)
	}

	// The actual owner can cancel their own booking.
	cancelled, err := svc.CancelBookingAsUser(ctx, booking.ID, owner, "user", "changed my mind")
	if err != nil {
		t.Fatalf("owner cancel should succeed: %v", err)
	}
	if cancelled.Status != "cancelled" {
		t.Errorf("expected status 'cancelled', got %q", cancelled.Status)
	}
}

func TestService_CancelBookingAsUser_AllowsAdmin(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	owner, planID := seedUserAndPlan(t, pool, 5)
	admin, _ := seedUserAndPlan(t, pool, 0)

	repo := NewRepository(pool)
	svc := NewService(repo, testGuard(t))

	booking, err := repo.Create(ctx, &Booking{PlanID: planID, UserID: owner})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.CancelBookingAsUser(ctx, booking.ID, admin, "admin", "policy violation"); err != nil {
		t.Fatalf("admin cancel should succeed: %v", err)
	}
}

func TestService_GetBookingAsUser_RejectsNonOwner(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	owner, planID := seedUserAndPlan(t, pool, 5)
	stranger, _ := seedUserAndPlan(t, pool, 0)

	repo := NewRepository(pool)
	svc := NewService(repo, testGuard(t))

	booking, err := repo.Create(ctx, &Booking{PlanID: planID, UserID: owner})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.GetBookingAsUser(ctx, booking.ID, stranger, "user"); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden for non-owner read, got %v", err)
	}
	if _, err := svc.GetBookingAsUser(ctx, booking.ID, owner, "user"); err != nil {
		t.Fatalf("owner read should succeed: %v", err)
	}
}
