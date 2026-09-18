// Package chat implements PRD §13.7 Chat. CreateRoom/SendMessage are the
// fully working vertical slice; ListMessages/ReportMessage are typed stubs.
// Live delivery goes through the separate WebSocket gateway described in
// PRD §16, which reads persisted rows written here.
package chat

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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

func (r *Repository) CreateRoom(ctx context.Context, planID string) (*Room, error) {
	var id string
	err := r.pool.QueryRow(ctx,
		`INSERT INTO chat_rooms (plan_id) VALUES ($1) RETURNING id`, planID,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return &Room{ID: id, PlanID: planID}, nil
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
