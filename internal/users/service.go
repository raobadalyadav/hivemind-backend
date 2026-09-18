package users

import (
	"context"
	"errors"
)

var ErrInvalidInput = errors.New("users: invalid input")

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetUser(ctx context.Context, id string) (*User, error) {
	if id == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Get(ctx, id)
}

func (s *Service) UpdateUser(ctx context.Context, id, cityID string) (*User, error) {
	if id == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.UpdateCity(ctx, id, cityID)
}

func (s *Service) DeleteAccount(ctx context.Context, id string) error {
	if id == "" {
		return ErrInvalidInput
	}
	return s.repo.SoftDelete(ctx, id)
}

func (s *Service) RegisterDevice(ctx context.Context, userID, deviceID, pushToken, platform string) error {
	if userID == "" || deviceID == "" {
		return ErrInvalidInput
	}
	return s.repo.UpsertDevice(ctx, userID, deviceID, pushToken, platform)
}

func (s *Service) UpdateLocation(ctx context.Context, userID string, lat, lng float64) error {
	if userID == "" {
		return ErrInvalidInput
	}
	return s.repo.UpdateLocation(ctx, userID, lat, lng)
}
