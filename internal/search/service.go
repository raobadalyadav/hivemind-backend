package search

import (
	"context"
	"errors"
)

var ErrInvalidInput = errors.New("search: invalid input")

const defaultPageSize = 20

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) SearchText(ctx context.Context, query, cityID string) ([]string, error) {
	if query == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.SearchPlanIDs(ctx, query, cityID, defaultPageSize)
}
