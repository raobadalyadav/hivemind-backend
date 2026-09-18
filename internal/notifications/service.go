package notifications

import (
	"context"
	"errors"
)

var ErrInvalidInput = errors.New("notifications: invalid input")

const defaultPageSize = 20

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) SendNotification(ctx context.Context, n *Notification) (*Notification, error) {
	if n.UserID == "" || n.Title == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Create(ctx, n)
}

func (s *Service) ListNotifications(ctx context.Context, userID string) ([]*Notification, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.ListForUser(ctx, userID, defaultPageSize)
}
