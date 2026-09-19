package reviews

import (
	"context"
	"errors"
)

var (
	ErrInvalidInput       = errors.New("reviews: invalid input")
	ErrForbidden          = errors.New("reviews: caller does not own this booking")
	ErrBookingNotAttended = errors.New("reviews: booking must be attended before it can be reviewed")
)

// BookingChecker is satisfied by *bookings.Service (wired in
// cmd/api/main.go) — CreateReview uses it to derive plan_id/user_id from
// the booking itself and to require 'attended' status.
type BookingChecker interface {
	GetBookingForReview(ctx context.Context, bookingID string) (planID, userID, status string, err error)
}

type Service struct {
	repo     *Repository
	bookings BookingChecker
}

func NewService(repo *Repository, bookings BookingChecker) *Service {
	return &Service{repo: repo, bookings: bookings}
}

func (s *Service) CreateReview(ctx context.Context, bookingID, callerID string, rating int32, comment string) (*Review, error) {
	if bookingID == "" || callerID == "" || rating < 1 || rating > 5 {
		return nil, ErrInvalidInput
	}
	planID, userID, bookingStatus, err := s.bookings.GetBookingForReview(ctx, bookingID)
	if err != nil {
		return nil, err
	}
	if userID != callerID {
		return nil, ErrForbidden
	}
	if bookingStatus != "attended" {
		return nil, ErrBookingNotAttended
	}
	return s.repo.Create(ctx, bookingID, planID, userID, rating, comment)
}

func (s *Service) GetPlanReviews(ctx context.Context, planID string) ([]*Review, float64, error) {
	if planID == "" {
		return nil, 0, ErrInvalidInput
	}
	return s.repo.ListForPlan(ctx, planID, 50)
}
