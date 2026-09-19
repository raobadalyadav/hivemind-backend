package reviews

import (
	"context"
	"testing"
)

// fakeBookingChecker satisfies BookingChecker without a DB — CreateReview's
// gating logic (ownership + attended status) is pure business logic, not
// worth a full integration test.
type fakeBookingChecker struct {
	planID, userID, status string
}

func (f fakeBookingChecker) GetBookingForReview(ctx context.Context, bookingID string) (string, string, string, error) {
	return f.planID, f.userID, f.status, nil
}

func TestService_CreateReview_RejectsNonOwner(t *testing.T) {
	svc := NewService(nil, fakeBookingChecker{planID: "plan-1", userID: "owner", status: "attended"})

	if _, err := svc.CreateReview(context.Background(), "booking-1", "stranger", 5, "great!"); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden for non-owner review, got %v", err)
	}
}

func TestService_CreateReview_RejectsUnattendedBooking(t *testing.T) {
	svc := NewService(nil, fakeBookingChecker{planID: "plan-1", userID: "owner", status: "confirmed"})

	if _, err := svc.CreateReview(context.Background(), "booking-1", "owner", 5, "great!"); err != ErrBookingNotAttended {
		t.Fatalf("expected ErrBookingNotAttended for a non-attended booking, got %v", err)
	}
}

func TestService_CreateReview_RejectsInvalidRating(t *testing.T) {
	svc := NewService(nil, fakeBookingChecker{planID: "plan-1", userID: "owner", status: "attended"})

	if _, err := svc.CreateReview(context.Background(), "booking-1", "owner", 6, "too high"); err != ErrInvalidInput {
		t.Fatalf("expected ErrInvalidInput for out-of-range rating, got %v", err)
	}
}
