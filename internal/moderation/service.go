package moderation

import (
	"context"
	"errors"
)

var ErrInvalidInput = errors.New("moderation: invalid input")

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) SubmitReport(ctx context.Context, c *Case) (*Case, error) {
	if c.ReporterID == "" || c.SubjectID == "" || c.Reason == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Create(ctx, c)
}

func (s *Service) GetCase(ctx context.Context, id string) (*Case, error) {
	if id == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Get(ctx, id)
}
