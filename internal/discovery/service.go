package discovery

import (
	"context"
	"errors"
)

var ErrInvalidInput = errors.New("discovery: invalid input")

const defaultPageSize = 20

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetNearbyPlans(ctx context.Context, lat, lng, radiusKM float64) ([]string, error) {
	if radiusKM <= 0 {
		return nil, ErrInvalidInput
	}
	return s.repo.NearbyPlanIDs(ctx, lat, lng, radiusKM, defaultPageSize)
}
