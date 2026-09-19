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

func (s *Service) GetHomeFeed(ctx context.Context, userID, section, travelCityID string) ([]string, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.HomeFeedPlanIDs(ctx, userID, section, travelCityID, defaultPageSize)
}

func (s *Service) DetectCity(ctx context.Context, lat, lng float64) (id, name string, err error) {
	return s.repo.DetectCity(ctx, lat, lng)
}
