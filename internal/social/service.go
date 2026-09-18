package social

import (
	"context"
	"errors"
)

var ErrInvalidInput = errors.New("social: invalid input")

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) CreatePost(ctx context.Context, p *Post) (*Post, error) {
	if p.AuthorID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Create(ctx, p)
}

func (s *Service) GetPost(ctx context.Context, id string) (*Post, error) {
	if id == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Get(ctx, id)
}
