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

// SubmitReportForSubject satisfies chat.ReportSubmitter — the adapter
// ReportMessage calls into, so chat doesn't need to import moderation.
func (s *Service) SubmitReportForSubject(ctx context.Context, reporterID, subjectType, subjectID, reason string) (string, error) {
	c, err := s.SubmitReport(ctx, &Case{ReporterID: reporterID, SubjectType: subjectType, SubjectID: subjectID, Reason: reason})
	if err != nil {
		return "", err
	}
	return c.ID, nil
}

func (s *Service) GetCase(ctx context.Context, id string) (*Case, error) {
	if id == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Get(ctx, id)
}

func (s *Service) ResolveCase(ctx context.Context, caseID, resolution string) (*Case, error) {
	if caseID == "" || resolution == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Resolve(ctx, caseID, resolution)
}

func (s *Service) BlockUser(ctx context.Context, userID, blockedUserID string) error {
	if userID == "" || blockedUserID == "" || userID == blockedUserID {
		return ErrInvalidInput
	}
	return s.repo.BlockUser(ctx, userID, blockedUserID)
}
