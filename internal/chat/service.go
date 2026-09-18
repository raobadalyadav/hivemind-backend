package chat

import (
	"context"
	"errors"
)

var ErrInvalidInput = errors.New("chat: invalid input")

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) CreateRoom(ctx context.Context, planID string) (*Room, error) {
	if planID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.CreateRoom(ctx, planID)
}

// SendMessage does not yet check chat_members / block-list before writing —
// TODO(phase1): reject sends from non-confirmed-participants and blocked
// users, per PRD §31 "unauthorized users cannot read/send".
func (s *Service) SendMessage(ctx context.Context, m *Message) (*Message, error) {
	if m.RoomID == "" || m.SenderID == "" || m.Body == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.SendMessage(ctx, m)
}
