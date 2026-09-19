package availability

import (
	"context"
	"errors"
)

var (
	ErrInvalidInput = errors.New("availability: invalid input")
	ErrForbidden    = errors.New("availability: caller does not own this window")
)

const defaultMatchLimit = 20

// RoomCreator is satisfied by *chat.Service (wired in cmd/api/main.go) —
// declared here so this package doesn't import chat concretely.
type RoomCreator interface {
	CreateAdHocRoomWithMembers(ctx context.Context, userIDs []string) (roomID string, err error)
}

type Service struct {
	repo  *Repository
	rooms RoomCreator
}

func NewService(repo *Repository, rooms RoomCreator) *Service {
	return &Service{repo: repo, rooms: rooms}
}

func (s *Service) SetAvailability(ctx context.Context, w *Window) (*Window, error) {
	if w.UserID == "" || w.ActivityType == "" || !w.EndsAt.After(w.StartsAt) {
		return nil, ErrInvalidInput
	}
	return s.repo.Create(ctx, w)
}

// CancelAvailability requires the caller to own the window — same
// fetch-then-compare ownership shape used throughout this codebase.
func (s *Service) CancelAvailability(ctx context.Context, windowID, callerID string) error {
	if windowID == "" || callerID == "" {
		return ErrInvalidInput
	}
	w, err := s.repo.Get(ctx, windowID)
	if err != nil {
		return err
	}
	if w.UserID != callerID {
		return ErrForbidden
	}
	return s.repo.Cancel(ctx, windowID)
}

func (s *Service) FindActivityMatches(ctx context.Context, windowID, callerID string) ([]*Candidate, error) {
	if windowID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	w, err := s.repo.Get(ctx, windowID)
	if err != nil {
		return nil, err
	}
	if w.UserID != callerID {
		return nil, ErrForbidden
	}
	return s.repo.FindMatches(ctx, callerID, w, defaultMatchLimit)
}

// AcceptActivityMatch is the mutual-consent commit step — mirrors
// connections.RespondConnection's shape (only the request's own party can
// act), except here the caller commits their own window against a
// candidate's, rather than responding to an inbound request.
func (s *Service) AcceptActivityMatch(ctx context.Context, myWindowID, theirWindowID, callerID string) (roomID string, err error) {
	if myWindowID == "" || theirWindowID == "" || callerID == "" || myWindowID == theirWindowID {
		return "", ErrInvalidInput
	}
	mine, err := s.repo.Get(ctx, myWindowID)
	if err != nil {
		return "", err
	}
	if mine.UserID != callerID {
		return "", ErrForbidden
	}
	theirs, err := s.repo.Get(ctx, theirWindowID)
	if err != nil {
		return "", err
	}

	roomID, err = s.rooms.CreateAdHocRoomWithMembers(ctx, []string{mine.UserID, theirs.UserID})
	if err != nil {
		return "", err
	}
	if err := s.repo.AcceptMatch(ctx, myWindowID, theirWindowID, roomID); err != nil {
		return "", err
	}
	return roomID, nil
}
