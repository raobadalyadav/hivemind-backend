package plans

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidInput = errors.New("plans: invalid input")
)

const defaultSearchLimit = 20

// BookingCreator/BookingCanceller are satisfied by *bookings.Service (wired
// in cmd/api/main.go) — declared here, not imported from internal/bookings,
// so neither package needs to import the other.
type BookingCreator interface {
	CreateBookingForPlan(ctx context.Context, planID, userID, idempotencyKey string) (bookingID string, err error)
}

type BookingCanceller interface {
	CancelBookingForPlan(ctx context.Context, planID, userID string) error
}

type Service struct {
	repo      *Repository
	creator   BookingCreator
	canceller BookingCanceller
}

func NewService(repo *Repository, creator BookingCreator, canceller BookingCanceller) *Service {
	return &Service{repo: repo, creator: creator, canceller: canceller}
}

func (s *Service) CreatePlan(ctx context.Context, p *Plan) (*Plan, error) {
	if p.Title == "" || p.HostID == "" || p.Capacity <= 0 {
		return nil, ErrInvalidInput
	}
	if p.Currency == "" {
		p.Currency = "INR"
	}
	if p.StartsAt.IsZero() {
		p.StartsAt = time.Now()
	}
	return s.repo.Create(ctx, p)
}

func (s *Service) GetPlan(ctx context.Context, id string) (*Plan, error) {
	if id == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Get(ctx, id)
}

func (s *Service) SearchPlans(ctx context.Context, f SearchFilter) ([]*Plan, error) {
	return s.repo.Search(ctx, f, defaultSearchLimit)
}

func (s *Service) JoinPlan(ctx context.Context, planID, userID, idempotencyKey string) (string, error) {
	if planID == "" || userID == "" {
		return "", ErrInvalidInput
	}
	return s.creator.CreateBookingForPlan(ctx, planID, userID, idempotencyKey)
}

func (s *Service) LeavePlan(ctx context.Context, planID, userID string) error {
	if planID == "" || userID == "" {
		return ErrInvalidInput
	}
	return s.canceller.CancelBookingForPlan(ctx, planID, userID)
}

func (s *Service) CancelPlan(ctx context.Context, planID, reason string) (*Plan, error) {
	if planID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Cancel(ctx, planID, reason)
}
