package recommendation

import (
	"context"
	"errors"
)

var ErrInvalidInput = errors.New("recommendation: invalid input")

const defaultPageSize = 20

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetRecommendedPlans(ctx context.Context, userID string) ([]string, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.UpcomingPlanIDs(ctx, userID, defaultPageSize)
}
