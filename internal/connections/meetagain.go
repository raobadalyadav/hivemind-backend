package connections

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/hivemind/backend/pkg/eventbus"
	"github.com/hivemind/backend/pkg/idempotency"
)

var (
	ErrNotAttendee  = errors.New("connections: only people who attended this plan can do that")
	ErrInviteeRules = errors.New("connections: invitees must have attended the plan and be your accepted connections")
)

const maxMeetAgainInvitees = 5

type PersonMet struct {
	UserID      string
	DisplayName string
	Occupation  string
	PhotoURL    string
	State       string // none | pending | connected
}

// RoomCreator is satisfied by *chat.Service (declared here, consumer-side,
// with primitive types only).
type RoomCreator interface {
	CreateAdHocRoomWithMembers(ctx context.Context, userIDs []string) (roomID string, err error)
}

func (r *Repository) attended(ctx context.Context, planID, userID string) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM attended_bookings WHERE plan_id = $1 AND user_id = $2)`, planID, userID).Scan(&ok)
	return ok, err
}

// PeopleMet lists the other attendees of a plan (no blocks in either
// direction) with the caller's connection state to each.
func (r *Repository) PeopleMet(ctx context.Context, planID, callerID string) ([]PersonMet, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT ab.user_id::text, up.display_name, up.occupation,
			COALESCE((SELECT url FROM profile_photos ph WHERE ph.user_id = ab.user_id ORDER BY position, created_at LIMIT 1), ''),
			COALESCE((SELECT CASE c.status WHEN 'accepted' THEN 'connected' WHEN 'pending' THEN 'pending' ELSE 'none' END
			          FROM connections c
			          WHERE (c.requester_id = $2 AND c.recipient_id = ab.user_id) OR (c.requester_id = ab.user_id AND c.recipient_id = $2)
			          LIMIT 1), 'none')
		FROM attended_bookings ab
		JOIN user_profiles up ON up.user_id = ab.user_id
		WHERE ab.plan_id = $1 AND ab.user_id <> $2::uuid
		  AND NOT EXISTS (SELECT 1 FROM blocks b
			WHERE (b.user_id = $2 AND b.blocked_user_id = ab.user_id) OR (b.user_id = ab.user_id AND b.blocked_user_id = $2))
		ORDER BY up.display_name`, planID, callerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PersonMet
	for rows.Next() {
		var p PersonMet
		if err := rows.Scan(&p.UserID, &p.DisplayName, &p.Occupation, &p.PhotoURL, &p.State); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// eligibleInvitees returns how many of the ids attended the plan, are the
// caller's accepted connections, and aren't blocked either way.
func (r *Repository) eligibleInvitees(ctx context.Context, planID, callerID string, ids []string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM attended_bookings ab
		WHERE ab.plan_id = $1 AND ab.user_id = ANY($3::uuid[])
		  AND EXISTS (SELECT 1 FROM connections c WHERE c.status = 'accepted'::connection_status
		      AND ((c.requester_id = $2 AND c.recipient_id = ab.user_id) OR (c.requester_id = ab.user_id AND c.recipient_id = $2)))
		  AND NOT EXISTS (SELECT 1 FROM blocks b
		      WHERE (b.user_id = $2 AND b.blocked_user_id = ab.user_id) OR (b.user_id = ab.user_id AND b.blocked_user_id = $2))`,
		planID, callerID, ids).Scan(&n)
	return n, err
}

func (r *Repository) notifyInvitees(ctx context.Context, callerName, planID string, ids []string, roomID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, id := range ids {
		if err := eventbus.EnqueueNotifyUser(ctx, tx, eventbus.NotifyUserPayload{
			UserID: id, Title: "Meet again?", Body: callerName + " started a small group chat with you.",
			DeepLink: "hivemind://chat/" + roomID, Channel: "push",
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *Repository) displayName(ctx context.Context, userID string) string {
	var n string
	if err := r.pool.QueryRow(ctx, `SELECT display_name FROM user_profiles WHERE user_id = $1`, userID).Scan(&n); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "Someone"
	}
	if n == "" {
		return "Someone"
	}
	return n
}

func (s *Service) WithMeetAgain(g *idempotency.Guard, rooms RoomCreator) *Service {
	s.guard, s.rooms = g, rooms
	return s
}

func (s *Service) ListPeopleMet(ctx context.Context, planID, callerID string) ([]PersonMet, error) {
	if planID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	ok, err := s.repo.attended(ctx, planID, callerID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotAttendee
	}
	return s.repo.PeopleMet(ctx, planID, callerID)
}

func (s *Service) CreateMeetAgainGroup(ctx context.Context, planID, callerID string, invitees []string, key string) (string, error) {
	if planID == "" || callerID == "" || len(invitees) == 0 || len(invitees) > maxMeetAgainInvitees || s.rooms == nil {
		return "", ErrInvalidInput
	}
	seen := map[string]bool{callerID: true}
	for _, id := range invitees {
		if id == "" || seen[id] {
			return "", ErrInvalidInput
		}
		seen[id] = true
	}
	ok, err := s.repo.attended(ctx, planID, callerID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNotAttendee
	}
	n, err := s.repo.eligibleInvitees(ctx, planID, callerID, invitees)
	if err != nil {
		return "", err
	}
	if n != len(invitees) {
		return "", ErrInviteeRules
	}
	if s.guard != nil && key != "" {
		if err := s.guard.Reserve(ctx, "meetagain:"+callerID+":"+key); err != nil {
			return "", err
		}
	}
	roomID, err := s.rooms.CreateAdHocRoomWithMembers(ctx, append([]string{callerID}, invitees...))
	if err != nil {
		return "", err
	}
	// Best effort: the room exists either way, so a failed nudge isn't an error.
	_ = s.repo.notifyInvitees(ctx, s.repo.displayName(ctx, callerID), planID, invitees, roomID)
	return roomID, nil
}
