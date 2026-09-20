package moderation

import (
	"context"
	"errors"
	"strings"
)

var (
	ErrInvalidInput = errors.New("moderation: invalid input")
	ErrCaseNotFound = errors.New("moderation: case not found")
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// ReportableTypes is everything a person can report from the app.
var ReportableTypes = map[string]bool{"user": true, "plan": true, "message": true, "post": true, "comment": true, "story": true, "community": true}

const maxReportReason = 1000

func (s *Service) SubmitReport(ctx context.Context, c *Case) (*Case, error) {
	c.Reason = strings.TrimSpace(c.Reason)
	if c.ReporterID == "" || c.SubjectID == "" || c.Reason == "" || len(c.Reason) > maxReportReason || !ReportableTypes[c.SubjectType] {
		return nil, ErrInvalidInput
	}
	if c.SubjectType == "user" && c.SubjectID == c.ReporterID {
		return nil, ErrInvalidInput // reporting yourself is never meaningful
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

// AutoFlagForSubject satisfies chat.ReportSubmitter's extended interface
// (and social's local equivalent) — called after a ContentScreener returns
// 'review' severity. 'safe' never reaches here (callers check severity
// before calling); 'severe' rejects the write entirely and also never
// reaches here.
func (s *Service) AutoFlagForSubject(ctx context.Context, subjectType, subjectID, actorID, severity, reason string) (string, error) {
	if subjectID == "" || actorID == "" || severity == "" {
		return "", ErrInvalidInput
	}
	return s.repo.CreateAutoFlagged(ctx, subjectType, subjectID, actorID, severity, reason)
}

// GetCase returns a case to whoever filed it (or staff); for anyone else it doesn't exist.
func (s *Service) GetCase(ctx context.Context, id, callerID string, staff bool) (*Case, error) {
	if id == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	c, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !staff && c.ReporterID != callerID {
		return nil, ErrCaseNotFound
	}
	return c, nil
}

func (s *Service) ResolveCase(ctx context.Context, caseID, resolution string) (*Case, error) {
	if caseID == "" || resolution == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.Resolve(ctx, caseID, resolution)
}

func (s *Service) GetTrustBadges(ctx context.Context, userID string) ([]string, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	sig, err := s.repo.GetTrustSignals(ctx, userID)
	if err != nil {
		return nil, err
	}
	return DeriveBadges(sig), nil
}

func (s *Service) BlockUser(ctx context.Context, userID, blockedUserID string) error {
	if userID == "" || blockedUserID == "" || userID == blockedUserID {
		return ErrInvalidInput
	}
	return s.repo.BlockUser(ctx, userID, blockedUserID)
}

func (s *Service) ListBlockedUsers(ctx context.Context, userID string) ([]BlockedUser, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.ListBlocked(ctx, userID)
}

func (s *Service) UnblockUser(ctx context.Context, userID, blockedUserID string) error {
	if userID == "" || blockedUserID == "" {
		return ErrInvalidInput
	}
	return s.repo.UnblockUser(ctx, userID, blockedUserID)
}
