// Package chat implements PRD §13.7 Chat — every RPC is fully implemented.
// Live delivery goes through the separate WebSocket gateway described in
// PRD §16, which reads persisted rows written here. Room/membership creation
// is driven by cmd/worker's BOOKING_CONFIRMED handler, not by clients
// calling CreateRoom directly — see service.go.
package chat

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrRoomNotFound    = errors.New("chat: room not found")
	ErrNotAMember      = errors.New("chat: sender is not a member of this room")
	ErrMessageNotFound = errors.New("chat: message not found")
)

type Room struct {
	ID     string
	PlanID string
}

type Message struct {
	ID       string
	RoomID   string
	SenderID string
	Body     string
	SentAt   time.Time
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// GetOrCreateRoomForPlan is idempotent — chat_rooms.plan_id is UNIQUE, so
// repeated calls for the same plan (e.g. one per booking) return the same
// room instead of erroring.
func (r *Repository) GetOrCreateRoomForPlan(ctx context.Context, planID string) (*Room, error) {
	var id string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO chat_rooms (plan_id) VALUES ($1)
		ON CONFLICT (plan_id) DO UPDATE SET plan_id = EXCLUDED.plan_id
		RETURNING id`,
		planID,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return &Room{ID: id, PlanID: planID}, nil
}

func (r *Repository) AddMember(ctx context.Context, roomID, userID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO chat_members (room_id, user_id) VALUES ($1, $2)
		ON CONFLICT (room_id, user_id) DO NOTHING`,
		roomID, userID,
	)
	return err
}

func (r *Repository) IsMember(ctx context.Context, roomID, userID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM chat_members WHERE room_id = $1 AND user_id = $2)`,
		roomID, userID,
	).Scan(&exists)
	return exists, err
}

func (r *Repository) SendMessage(ctx context.Context, m *Message) (*Message, error) {
	out := *m
	err := r.pool.QueryRow(ctx, `
		INSERT INTO messages (room_id, sender_id, body) VALUES ($1, $2, $3)
		RETURNING id, sent_at`,
		m.RoomID, m.SenderID, m.Body,
	).Scan(&out.ID, &out.SentAt)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *Repository) ListMessages(ctx context.Context, roomID string, limit int) ([]*Message, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, room_id, sender_id, body, sent_at FROM messages
		WHERE room_id = $1 ORDER BY sent_at DESC LIMIT $2`,
		roomID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.RoomID, &m.SenderID, &m.Body, &m.SentAt); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

// MessageSubjectID resolves a message id to the (subject_id, subject-owner)
// pair ReportMessage needs to file against internal/moderation — chat stays
// unaware of moderation's schema beyond this lookup.
func (r *Repository) MessageExists(ctx context.Context, messageID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM messages WHERE id = $1)`, messageID,
	).Scan(&exists)
	return exists, err
}
