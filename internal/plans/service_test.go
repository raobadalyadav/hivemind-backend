package plans

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeBookingCreator struct {
	lastPlanID, lastUserID, lastKey string
	returnID                        string
	returnErr                       error
}

func (f *fakeBookingCreator) CreateBookingForPlan(ctx context.Context, planID, userID, idempotencyKey string) (string, error) {
	f.lastPlanID, f.lastUserID, f.lastKey = planID, userID, idempotencyKey
	return f.returnID, f.returnErr
}

type fakeBookingCanceller struct {
	lastPlanID, lastUserID string
	returnErr              error
}

func (f *fakeBookingCanceller) CancelBookingForPlan(ctx context.Context, planID, userID string) error {
	f.lastPlanID, f.lastUserID = planID, userID
	return f.returnErr
}

func TestService_JoinPlan_DelegatesToBookingCreator(t *testing.T) {
	creator := &fakeBookingCreator{returnID: "booking-123"}
	svc := NewService(nil, creator, &fakeBookingCanceller{}, nil)

	id, err := svc.JoinPlan(context.Background(), "plan-1", "user-1", "idem-key")
	if err != nil {
		t.Fatalf("JoinPlan: %v", err)
	}
	if id != "booking-123" {
		t.Errorf("expected booking id 'booking-123', got %q", id)
	}
	if creator.lastPlanID != "plan-1" || creator.lastUserID != "user-1" || creator.lastKey != "idem-key" {
		t.Errorf("BookingCreator called with unexpected args: %+v", creator)
	}
}

func TestService_JoinPlan_PropagatesCreatorError(t *testing.T) {
	wantErr := errors.New("plan is full")
	creator := &fakeBookingCreator{returnErr: wantErr}
	svc := NewService(nil, creator, &fakeBookingCanceller{}, nil)

	_, err := svc.JoinPlan(context.Background(), "plan-1", "user-1", "")
	if err != wantErr {
		t.Errorf("expected error to propagate, got %v", err)
	}
}

func TestService_LeavePlan_DelegatesToBookingCanceller(t *testing.T) {
	canceller := &fakeBookingCanceller{}
	svc := NewService(nil, &fakeBookingCreator{}, canceller, nil)

	if err := svc.LeavePlan(context.Background(), "plan-1", "user-1"); err != nil {
		t.Fatalf("LeavePlan: %v", err)
	}
	if canceller.lastPlanID != "plan-1" || canceller.lastUserID != "user-1" {
		t.Errorf("BookingCanceller called with unexpected args: %+v", canceller)
	}
}

func TestService_JoinPlan_RejectsMissingInput(t *testing.T) {
	svc := NewService(nil, &fakeBookingCreator{}, &fakeBookingCanceller{}, nil)
	if _, err := svc.JoinPlan(context.Background(), "", "user-1", ""); err != ErrInvalidInput {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

// A plan with a missing or inverted end would make the completion/no-show
// jobs fire immediately (ends_at defaults to year 1), so it must be rejected
// before it reaches the repository.
func TestService_CreatePlan_RejectsInvalidWindow(t *testing.T) {
	svc := NewService(nil, &fakeBookingCreator{}, &fakeBookingCanceller{}, nil)
	now := time.Now()

	cases := map[string]*Plan{
		"ends before starts": {Title: "x", HostID: "h", Capacity: 5, StartsAt: now.Add(time.Hour), EndsAt: now},
		"starts long ago":    {Title: "x", HostID: "h", Capacity: 5, StartsAt: now.Add(-2 * time.Hour), EndsAt: now.Add(time.Hour)},
	}
	for name, p := range cases {
		if _, err := svc.CreatePlan(context.Background(), p, ""); err != ErrInvalidInput {
			t.Errorf("%s: expected ErrInvalidInput, got %v", name, err)
		}
	}
}
