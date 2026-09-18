package bookings

import (
	"context"
	"errors"

	"github.com/hivemind/backend/pkg/idempotency"
)

var ErrInvalidInput = errors.New("bookings: invalid input")

type Service struct {
	repo  *Repository
	guard *idempotency.Guard
}

func NewService(repo *Repository, guard *idempotency.Guard) *Service {
	return &Service{repo: repo, guard: guard}
}

func (s *Service) CreateBooking(ctx context.Context, b *Booking) (*Booking, error) {
	if b.PlanID == "" || b.UserID == "" {
		return nil, ErrInvalidInput
	}
	if err := s.guard.Reserve(ctx, b.IdempotencyKey); err != nil {
		return nil, err
	}
	if b.Currency == "" {
		b.Currency = "INR"
	}
	return s.repo.Create(ctx, b)
}

func (s *Service) GetBooking(ctx context.Context, id string) (*Booking, error) {
	if id == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Get(ctx, id)
}
