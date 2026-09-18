package communities

import (
	"context"
	"errors"
)

var ErrInvalidInput = errors.New("communities: invalid input")

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) CreateCommunity(ctx context.Context, c *Community) (*Community, error) {
	if c.Name == "" || c.OwnerID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Create(ctx, c)
}

func (s *Service) GetCommunity(ctx context.Context, id string) (*Community, error) {
	if id == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Get(ctx, id)
}
