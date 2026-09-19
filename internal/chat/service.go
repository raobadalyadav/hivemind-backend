package chat

import (
	"context"
	"errors"
)

var (
	ErrInvalidInput    = errors.New("chat: invalid input")
	ErrContentRejected = errors.New("chat: message violates content policy")
)

const defaultMessagePageSize = 50

// ReportSubmitter is satisfied by *moderation.Service (wired in
// cmd/api/main.go) — declared here so chat doesn't import moderation.
type ReportSubmitter interface {
	SubmitReportForSubject(ctx context.Context, reporterID, subjectType, subjectID, reason string) (caseID string, err error)
	AutoFlagForSubject(ctx context.Context, subjectType, subjectID, actorID, severity, reason string) (caseID string, err error)
}

// ContentScreener is satisfied by *moderation.Screener — duplicated locally
// per the existing no-cross-import convention. Always available (never
// nil, never errors), unlike the truly-optional EmailSender/PushSender.
type ContentScreener interface {
	Screen(ctx context.Context, body string) (severity, reason string)
}

type Service struct {
	repo       *Repository
	reporter   ReportSubmitter
	screener   ContentScreener
	icebreaker IcebreakerGenerator
}

func NewService(repo *Repository, reporter ReportSubmitter, screener ContentScreener, icebreaker IcebreakerGenerator) *Service {
	return &Service{repo: repo, reporter: reporter, screener: screener, icebreaker: icebreaker}
}

func (s *Service) GenerateIcebreaker(ctx context.Context, roomID string) (string, error) {
	if roomID == "" {
		return "", ErrInvalidInput
	}
	interests, err := s.repo.MemberInterests(ctx, roomID)
	if err != nil {
		return "", err
	}
	return s.icebreaker.Generate(ctx, interests)
}

func (s *Service) CreateRoom(ctx context.Context, planID string) (*Room, error) {
	if planID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.GetOrCreateRoomForPlan(ctx, planID)
}

// CreateAdHocRoomWithMembers satisfies the RoomCreator interface declared
// locally in internal/availability and internal/smartgroups — the one new
// room-creation path both packages share.
func (s *Service) CreateAdHocRoomWithMembers(ctx context.Context, userIDs []string) (string, error) {
	if len(userIDs) == 0 {
		return "", ErrInvalidInput
	}
	room, err := s.repo.CreateAdHocRoom(ctx)
	if err != nil {
		return "", err
	}
	for _, userID := range userIDs {
		if err := s.repo.AddMember(ctx, room.ID, userID); err != nil {
			return "", err
		}
	}
	return room.ID, nil
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

	severity, reason := s.screener.Screen(ctx, m.Body)
	if severity == "severe" {
		return nil, ErrContentRejected
	}

	sent, err := s.repo.SendMessage(ctx, m)
	if err != nil {
		return nil, err
	}
	if severity == "review" {
		// Fire-and-forget: a screening/queue failure must never block the
		// message the user already sent.
		_, _ = s.reporter.AutoFlagForSubject(ctx, "message", sent.ID, m.SenderID, severity, reason)
	}
	return sent, nil
}

func (s *Service) ListMessages(ctx context.Context, roomID, callerID string) ([]*Message, error) {
	if roomID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	isMember, err := s.repo.IsMember(ctx, roomID, callerID)
	if err != nil {
		return nil, err
	}
	if !isMember {
		return nil, ErrNotAMember
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
