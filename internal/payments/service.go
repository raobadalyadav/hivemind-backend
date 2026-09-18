package payments

import (
	"context"
	"errors"
)

var ErrInvalidInput = errors.New("payments: invalid input")

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) CreateOrder(ctx context.Context, o *Order) (*Order, error) {
	if o.BookingID == "" || o.AmountMinor <= 0 {
		return nil, ErrInvalidInput
	}
	if o.Currency == "" {
		o.Currency = "INR"
	}
	return s.repo.CreateOrder(ctx, o)
}
