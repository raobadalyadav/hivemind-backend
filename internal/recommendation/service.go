package recommendation

import (
	"context"
	"errors"
)

var (
	ErrInvalidInput = errors.New("recommendation: invalid input")
	ErrForbidden    = errors.New("recommendation: only attendees of a plan can see its matches")
)

const defaultPageSize = 20

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetRecommendedPlans(ctx context.Context, userID, travelCityID string) ([]string, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.UpcomingPlanIDs(ctx, userID, travelCityID, defaultPageSize)
}

const defaultMatchLimit = 10

func (s *Service) GetSmartMatch(ctx context.Context, callerID, callerRole, planID string) ([]string, error) {
	if callerID == "" || planID == "" {
		return nil, ErrInvalidInput
	}
	if callerRole != "admin" && callerRole != "super_admin" {
		ok, err := s.repo.IsParticipantOrHost(ctx, planID, callerID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrForbidden
		}
	}
	return s.repo.SmartMatchUserIDs(ctx, callerID, planID, defaultMatchLimit)
}

func (s *Service) GetPeopleRecommendations(ctx context.Context, callerID, travelCityID string) ([]string, error) {
	if callerID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.PeopleRecommendationUserIDs(ctx, callerID, travelCityID, defaultPageSize)
}
