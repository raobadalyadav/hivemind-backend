package connections

import (
	"context"
	"errors"
	"github.com/hivemind/backend/internal/notifications"

	"github.com/hivemind/backend/pkg/idempotency"
)

var (
	ErrInvalidInput = errors.New("connections: invalid input")
	ErrForbidden    = errors.New("connections: only the recipient can respond to this request")
)

const defaultPageSize = 20

type Service struct {
	repo  *Repository
	guard *idempotency.Guard
	rooms RoomCreator
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

func (s *Service) ListConnections(ctx context.Context, userID, status, direction string) ([]*Connection, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.ListForUser(ctx, userID, status, direction, defaultPageSize)
}

// RespondConnection requires the caller to be the request's recipient —
// the requester can't accept their own request, and an unrelated user
// can't respond to someone else's.
func (s *Service) RespondConnection(ctx context.Context, connectionID, callerID string, accept bool) (*Connection, error) {
	if connectionID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	c, err := s.repo.Get(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	if c.RecipientID != callerID {
		return nil, ErrForbidden
	}
	out, err := s.repo.Respond(ctx, connectionID, accept)
	if err == nil && accept && out.Status == "accepted" && c.Status == "pending" { // first acceptance only
		_, _ = notifications.Emit(ctx, s.repo.pool, notifications.Event{
			UserID: out.RequesterID, ActorID: callerID, Type: notifications.TypeConnectionAccepted, TargetID: out.ID,
			Title: "{actor} accepted your connection request", DeepLink: "hivemind://people/" + callerID,
			DedupeKey: "connacc:" + out.ID,
		})
	}
	return out, err
}

// GetStatus: the caller's relationship with one person.
func (s *Service) GetStatus(ctx context.Context, me, other string) (state, id string, err error) {
	if me == "" || other == "" {
		return "", "", ErrInvalidInput
	}
	return s.repo.Status(ctx, me, other)
}
