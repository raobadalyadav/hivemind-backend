package plans

import (
	"context"
	"errors"
	"time"
)

var ErrInvalidInput = errors.New("plans: invalid input")

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
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
