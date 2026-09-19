package meet

import (
	"context"
)

// DMOpener is satisfied by *chat.Service. Opening the room is idempotent (one
// DM per pair), so a failure here never loses the match — the room is created
// the next time either person opens the chat.
type DMOpener interface {
	OpenDM(ctx context.Context, userA, userB string) (roomID string, err error)
}

type Service struct {
	repo *Repository
	dm   DMOpener
}

func NewService(repo *Repository) *Service { return &Service{repo: repo} }

// WithDM enables opening a chat on match.
func (s *Service) WithDM(d DMOpener) *Service {
	s.dm = d
	return s
}

const (
	defaultDeck = 10
	maxDeck     = 30
)

func (s *Service) GetDeck(ctx context.Context, userID string, limit, minAge, maxAge int) ([]*Card, error) {
	if userID == "" || minAge < 0 || maxAge < 0 || (maxAge != 0 && minAge > maxAge) || maxAge > 120 {
		return nil, ErrInvalidInput
	}
	if limit <= 0 {
		limit = defaultDeck
	}
	if limit > maxDeck {
		limit = maxDeck
	}
	return s.repo.Deck(ctx, userID, limit, minAge, maxAge)
}

type SwipeOutcome struct {
	Matched    bool
	RoomID     string
	TargetName string
}

func (s *Service) Swipe(ctx context.Context, userID, targetID, action string) (*SwipeOutcome, error) {
	if userID == "" || targetID == "" || userID == targetID || (action != "pass" && action != "wave" && action != "super") {
		return nil, ErrInvalidInput
	}
	r, err := s.repo.Swipe(ctx, userID, targetID, action)
	if err != nil {
		return nil, err
	}
	out := &SwipeOutcome{Matched: r.Matched, TargetName: r.TargetName}
	if r.Matched && s.dm != nil {
		if id, err := s.dm.OpenDM(ctx, userID, targetID); err == nil {
			out.RoomID = id
		}
	}
	return out, nil
}

func (s *Service) ListWaves(ctx context.Context, userID string, sent bool) ([]Wave, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Waves(ctx, userID, sent)
}

func (s *Service) BoostStatus(ctx context.Context, userID string) (*Boost, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.BoostStatus(ctx, userID)
}

func (s *Service) ActivateBoost(ctx context.Context, userID string) (*Boost, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.ActivateBoost(ctx, userID)
}
