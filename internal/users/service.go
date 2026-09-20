package users

import (
	"context"
	"errors"
	"time"
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

// MinAgeYears is the youngest a person may be to use HiveMind.
const MinAgeYears = 18

// ErrUnderage: the date of birth makes the person younger than MinAgeYears.
var ErrUnderage = errors.New("users: you must be 18 or older to use HiveMind")

// adult reports whether someone born on dob is at least MinAgeYears old on now (calendar-exact).
func adult(dob, now time.Time) bool {
	return !dob.AddDate(MinAgeYears, 0, 0).After(now)
}

// UpdateUser changes the city and/or sets the birthday (once). Empty fields are left alone.
func (s *Service) UpdateUser(ctx context.Context, id, cityID, dateOfBirth string) (*User, error) {
	if id == "" {
		return nil, ErrInvalidInput
	}
	if dateOfBirth != "" {
		dob, err := time.Parse("2006-01-02", dateOfBirth)
		now := time.Now().UTC()
		if err != nil || dob.After(now) || dob.Before(now.AddDate(-120, 0, 0)) {
			return nil, ErrInvalidInput
		}
		if !adult(dob, now) {
			return nil, ErrUnderage
		}
		if err := s.repo.SetBirthday(ctx, id, dob); err != nil {
			return nil, err
		}
	}
	if cityID == "" && dateOfBirth != "" {
		return s.repo.Get(ctx, id)
	}
	return s.repo.UpdateCity(ctx, id, cityID)
}

func (s *Service) DeleteAccount(ctx context.Context, id string) error {
	if id == "" {
		return ErrInvalidInput
	}
	return s.repo.EraseAccount(ctx, id)
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
