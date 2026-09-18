package chat

import (
	"context"
	"errors"
)

var (
	ErrInvalidInput = errors.New("chat: invalid input")
)

const defaultMessagePageSize = 50

// ReportSubmitter is satisfied by *moderation.Service (wired in
// cmd/api/main.go) — declared here so chat doesn't import moderation.
type ReportSubmitter interface {
	SubmitReportForSubject(ctx context.Context, reporterID, subjectType, subjectID, reason string) (caseID string, err error)
}

type Service struct {
	repo     *Repository
	reporter ReportSubmitter
}

func NewService(repo *Repository, reporter ReportSubmitter) *Service {
	return &Service{repo: repo, reporter: reporter}
}

func (s *Service) CreateRoom(ctx context.Context, planID string) (*Room, error) {
	if planID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.GetOrCreateRoomForPlan(ctx, planID)
}

// EnsureMembership is called by cmd/worker's BOOKING_CONFIRMED handler —
// idempotent room lookup/create plus member add, so retried or duplicate
// events are harmless. This is what makes PRD §31's "confirmed participants
// receive the room" actually true.
func (s *Service) EnsureMembership(ctx context.Context, planID, userID string) error {
	if planID == "" || userID == "" {
		return ErrInvalidInput
	}
	room, err := s.repo.GetOrCreateRoomForPlan(ctx, planID)
	if err != nil {
		return err
	}
	return s.repo.AddMember(ctx, room.ID, userID)
}

// SendMessage rejects senders who aren't a member of the room — the PRD §31
// "unauthorized users cannot read/send" guarantee, closing the TODO the
// scaffold left here. Membership rows are populated by cmd/worker on
// BOOKING_CONFIRMED, not by this method.
func (s *Service) SendMessage(ctx context.Context, m *Message) (*Message, error) {
	if m.RoomID == "" || m.SenderID == "" || m.Body == "" {
		return nil, ErrInvalidInput
	}
	isMember, err := s.repo.IsMember(ctx, m.RoomID, m.SenderID)
	if err != nil {
		return nil, err
	}
	if !isMember {
		return nil, ErrNotAMember
	}
	return s.repo.SendMessage(ctx, m)
}

func (s *Service) ListMessages(ctx context.Context, roomID string) ([]*Message, error) {
	if roomID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.ListMessages(ctx, roomID, defaultMessagePageSize)
}

func (s *Service) ReportMessage(ctx context.Context, messageID, reporterID, reason string) (string, error) {
	if messageID == "" || reporterID == "" || reason == "" {
		return "", ErrInvalidInput
	}
	exists, err := s.repo.MessageExists(ctx, messageID)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", ErrMessageNotFound
	}
	return s.reporter.SubmitReportForSubject(ctx, reporterID, "message", messageID, reason)
}
