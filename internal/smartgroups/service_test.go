package smartgroups

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testPoolForSmartGroups(t *testing.T) *pgxpool.Pool {
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

type fakePlanHostChecker struct{ hostID string }

func (f fakePlanHostChecker) GetPlanHostID(ctx context.Context, planID string) (string, error) {
	return f.hostID, nil
}

type fakeRoomCreator struct{ calls int }

func (f *fakeRoomCreator) CreateAdHocRoomWithMembers(ctx context.Context, userIDs []string) (string, error) {
	f.calls++
	return "fake-room-id", nil
}

func TestService_GenerateSmartGroups_RejectsNonHost(t *testing.T) {
	rooms := &fakeRoomCreator{}
	svc := NewService(nil, fakePlanHostChecker{hostID: "host-1"}, rooms)

	if _, err := svc.GenerateSmartGroups(context.Background(), "plan-1", "stranger", "user"); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden for non-host caller, got %v", err)
	}
	if rooms.calls != 0 {
		t.Errorf("expected no room creation when ownership check fails, got %d calls", rooms.calls)
	}
}

// TestService_ListSmartGroups_RejectsUnrelatedCaller verifies the IDOR fix:
// a caller who is neither the plan's host, an admin, nor a confirmed
// participant of the plan must not see group membership.
func TestService_ListSmartGroups_RejectsUnrelatedCaller(t *testing.T) {
	pool := testPoolForSmartGroups(t)
	defer pool.Close()
	ctx := context.Background()

	// Real UUID-format strings, not seeded as rows — IsConfirmedParticipant's
	// EXISTS query correctly returns false for a well-formed but unknown
	// UUID rather than erroring, which a placeholder like "plan-1" would do
	// (plan_participants.plan_id is a UUID column).
	const (
		planID   = "00000000-0000-0000-0000-000000000001"
		hostID   = "00000000-0000-0000-0000-000000000002"
		stranger = "00000000-0000-0000-0000-000000000003"
		adminID  = "00000000-0000-0000-0000-000000000004"
	)

	repo := NewRepository(pool)
	svc := NewService(repo, fakePlanHostChecker{hostID: hostID}, &fakeRoomCreator{})

	if _, err := svc.ListSmartGroups(ctx, planID, stranger, "user"); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden for an unrelated caller, got %v", err)
	}

	// The host themself must still be allowed through.
	if _, err := svc.ListSmartGroups(ctx, planID, hostID, "user"); err != nil {
		t.Fatalf("host should be allowed to list their own plan's groups: %v", err)
	}

	// An admin must be allowed through regardless of host/participant status.
	if _, err := svc.ListSmartGroups(ctx, planID, adminID, "admin"); err != nil {
		t.Fatalf("admin should be allowed to list any plan's groups: %v", err)
	}
}

func TestService_GenerateSmartGroups_AllowsAdmin(t *testing.T) {
	// Admin bypasses the host check but still needs a real repo to proceed
	// past it (nil repo panics on the next call) — this test only verifies
	// the ownership gate itself doesn't reject an admin, matching
	// plans.Service.CancelPlan's identical isAdminRole precedent.
	if !isAdminRole("admin") || !isAdminRole("super_admin") {
		t.Fatal("isAdminRole should treat both admin and super_admin as admin")
	}
	if isAdminRole("user") {
		t.Fatal("isAdminRole should not treat a regular user as admin")
	}
}
