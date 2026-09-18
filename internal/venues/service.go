package venues

import (
	"context"
	"errors"
)

var (
	ErrInvalidInput = errors.New("venues: invalid input")
	ErrForbidden    = errors.New("venues: caller does not own this venue")
)

const defaultVenuePageSize = 50

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) CreateVenue(ctx context.Context, v *Venue) (*Venue, error) {
	if v.OwnerHostID == "" || v.Name == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Create(ctx, v)
}

func (s *Service) GetVenue(ctx context.Context, id string) (*Venue, error) {
	if id == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Get(ctx, id)
}

func (s *Service) ListMyVenues(ctx context.Context, ownerHostID string) ([]*Venue, error) {
	if ownerHostID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.ListByOwner(ctx, ownerHostID, defaultVenuePageSize)
}

// UpdateVenue and GetVenueDashboard both require the caller to own the
// venue — same fetch-then-compare ownership check used throughout this
// codebase (bookings, connections, subscriptions).
func (s *Service) UpdateVenue(ctx context.Context, id, callerID, name, address string, capacity int32) (*Venue, error) {
	if id == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	v, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if v.OwnerHostID != callerID {
		return nil, ErrForbidden
	}
	return s.repo.Update(ctx, id, name, address, capacity)
}

func (s *Service) GetVenueDashboard(ctx context.Context, venueID, callerID string) (*Dashboard, error) {
	if venueID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	v, err := s.repo.Get(ctx, venueID)
	if err != nil {
		return nil, err
	}
	if v.OwnerHostID != callerID {
		return nil, ErrForbidden
	}
	return s.repo.GetDashboard(ctx, venueID)
}
