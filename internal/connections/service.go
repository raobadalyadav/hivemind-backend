package connections

import (
	"context"
	"errors"
)

var ErrInvalidInput = errors.New("connections: invalid input")

const defaultPageSize = 20

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) RequestConnection(ctx context.Context, c *Connection) (*Connection, error) {
	if c.RequesterID == "" || c.RecipientID == "" || c.RequesterID == c.RecipientID {
		return nil, ErrInvalidInput
	}
	return s.repo.Create(ctx, c)
}

func (s *Service) ListConnections(ctx context.Context, userID string) ([]*Connection, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.ListForUser(ctx, userID, defaultPageSize)
}
