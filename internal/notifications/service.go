package notifications

import (
	"context"
	"errors"
	"log/slog"
)

var ErrInvalidInput = errors.New("notifications: invalid input")

const defaultPageSize = 20

// EmailSender/PushSender are satisfied by *email.Client/*push.Client (wired
// in cmd/api and cmd/worker's main.go) — declared here, not imported
// concrete types, so this package doesn't depend on pkg/email or pkg/push
// directly. Either may be nil (credentials not configured), in which case
// that channel's dispatch is skipped and logged, not treated as an error —
// same graceful-degradation pattern as the OAuth verifiers.
type EmailSender interface {
	Send(ctx context.Context, to, subject, htmlBody string) error
}

type PushSender interface {
	Send(ctx context.Context, deviceToken, title, body string, data map[string]string) error
}

type Service struct {
	repo   *Repository
	email  EmailSender
	push   PushSender
	logger *slog.Logger
}

func NewService(repo *Repository, emailSender EmailSender, pushSender PushSender, logger *slog.Logger) *Service {
	return &Service{repo: repo, email: emailSender, push: pushSender, logger: logger}
}

// SendNotification persists the notification, then dispatches it through
// the real channel (email via Resend, push via FCM) — dispatch failures
// don't fail the call, they're recorded on the row via delivery_status so a
// bad push token doesn't take down the whole request.
func (s *Service) SendNotification(ctx context.Context, n *Notification) (*Notification, error) {
	if n.UserID == "" || n.Title == "" {
		return nil, ErrInvalidInput
	}
	created, err := s.repo.Create(ctx, n)
	if err != nil {
		return nil, err
	}
	switch created.Channel {
	case "email":
		s.dispatchEmail(ctx, created)
	case "push":
		s.dispatchPush(ctx, created)
	default: // in_app: no external dispatch, client polls ListNotifications
	}
	return created, nil
}

func (s *Service) dispatchEmail(ctx context.Context, n *Notification) {
	if s.email == nil {
		s.logger.Warn("email sender not configured, skipping delivery", "notification_id", n.ID)
		return
	}
	_, emailEnabled, err := s.repo.GetPreferences(ctx, n.UserID)
	if err != nil {
		s.logger.Error("load notification preferences", "error", err)
		return
	}
	if !emailEnabled {
		return
	}

	to, err := s.repo.GetUserEmail(ctx, n.UserID)
	if err != nil || to == "" {
		s.markDelivery(ctx, n.ID, "failed", "no email address on file")
		return
	}
	if err := s.email.Send(ctx, to, n.Title, n.Body); err != nil {
		s.markDelivery(ctx, n.ID, "failed", err.Error())
		return
	}
	s.markDelivery(ctx, n.ID, "sent", "")
}

func (s *Service) dispatchPush(ctx context.Context, n *Notification) {
	if s.push == nil {
		s.logger.Warn("push sender not configured, skipping delivery", "notification_id", n.ID)
		return
	}
	pushEnabled, _, err := s.repo.GetPreferences(ctx, n.UserID)
	if err != nil {
		s.logger.Error("load notification preferences", "error", err)
		return
	}
	if !pushEnabled {
		return
	}

	tokens, err := s.repo.ListDevicePushTokens(ctx, n.UserID)
	if err != nil {
		s.markDelivery(ctx, n.ID, "failed", err.Error())
		return
	}
	if len(tokens) == 0 {
		s.markDelivery(ctx, n.ID, "failed", "no registered device")
		return
	}

	// Best-effort across every registered device — one dead token doesn't
	// block delivery to the user's other devices.
	var lastErr error
	sentAny := false
	for _, token := range tokens {
		if err := s.push.Send(ctx, token, n.Title, n.Body, map[string]string{"deep_link": n.DeepLink}); err != nil {
			lastErr = err
			continue
		}
		sentAny = true
	}
	if sentAny {
		s.markDelivery(ctx, n.ID, "sent", "")
		return
	}
	errMsg := ""
	if lastErr != nil {
		errMsg = lastErr.Error()
	}
	s.markDelivery(ctx, n.ID, "failed", errMsg)
}

func (s *Service) markDelivery(ctx context.Context, notificationID, status, errMsg string) {
	if err := s.repo.MarkDelivery(ctx, notificationID, status, errMsg); err != nil {
		s.logger.Error("mark notification delivery", "error", err)
	}
}

func (s *Service) ListNotifications(ctx context.Context, userID string) ([]*Notification, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.ListForUser(ctx, userID, defaultPageSize)
}

func (s *Service) UpdatePreferences(ctx context.Context, userID string, pushEnabled, emailEnabled bool, quietStart, quietEnd string) error {
	if userID == "" {
		return ErrInvalidInput
	}
	return s.repo.UpsertPreferences(ctx, userID, pushEnabled, emailEnabled, quietStart, quietEnd)
}
