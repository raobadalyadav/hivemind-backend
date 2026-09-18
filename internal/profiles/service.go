package profiles

import (
	"context"
	"errors"
)

var ErrInvalidInput = errors.New("profiles: invalid input")

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) CreateProfile(ctx context.Context, p *Profile) (*Profile, error) {
	if p.UserID == "" || p.DisplayName == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Create(ctx, p)
}

func (s *Service) GetProfile(ctx context.Context, userID string) (*Profile, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Get(ctx, userID)
}
