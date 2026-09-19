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
	ID              string
	PlanID          string
	PinnedMessageID string
}

type Location struct {
	Latitude, Longitude float64
	Label               string
}

type PollOption struct {
	ID        string
	Label     string
	VoteCount int32
}

type Poll struct {
	ID             string
	Question       string
	Options        []PollOption
	MyVoteOptionID string
	TotalVotes     int32
}

type Message struct {
	ID              string
	RoomID          string
	SenderID        string
	Body            string
	SentAt          time.Time
	Type            string // text | image | voice | poll | announcement | location
	MediaURLs       []string
	Location        *Location
	DurationSeconds int32
	Poll            *Poll
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

// CreateAdHocRoom creates a plan-less chat room (plan_id NULL) — the one
// room-creation path used by ephemeral groups (Who's Free/Activity Buddy,
// Smart Groups), distinct from GetOrCreateRoomForPlan's plan-scoped path.
func (r *Repository) CreateAdHocRoom(ctx context.Context) (*Room, error) {
	var id string
	err := r.pool.QueryRow(ctx, `INSERT INTO chat_rooms (plan_id) VALUES (NULL) RETURNING id`).Scan(&id)
	if err != nil {
		return nil, err
	}
	return &Room{ID: id}, nil
}

func (r *Repository) AddMember(ctx context.Context, roomID, userID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO chat_members (room_id, user_id) VALUES ($1, $2)
		ON CONFLICT (room_id, user_id) DO NOTHING`,
		roomID, userID,
	)
	return err
}

// MemberInterests is used by GenerateIcebreaker — one interests slice per
// room member (empty slice, not skipped, for a member with no profile
// interests set, so intersectAll still sees them as a member with zero
// overlap rather than silently excluding them).
func (r *Repository) MemberInterests(ctx context.Context, roomID string) ([][]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT COALESCE(up.interests, '{}')
		FROM chat_members cm
		LEFT JOIN user_profiles up ON up.user_id = cm.user_id
		WHERE cm.room_id = $1`,
		roomID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out [][]string
	for rows.Next() {
		var interests []string
		if err := rows.Scan(&interests); err != nil {
			return nil, err
		}
		out = append(out, interests)
	}
	return out, rows.Err()
}

func (r *Repository) IsMember(ctx context.Context, roomID, userID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM chat_members WHERE room_id = $1 AND user_id = $2)`,
		roomID, userID,
	).Scan(&exists)
	return exists, err
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
