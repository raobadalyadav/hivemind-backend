// Package connections implements PRD §13.9 Connections (Phase 2) — every RPC
// is fully implemented.
package connections

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrConnectionNotFound = errors.New("connections: connection not found")

type Connection struct {
	ID           string
	RequesterID  string
	RecipientID  string
	OriginPlanID string
	Status       string
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Create(ctx context.Context, c *Connection) (*Connection, error) {
	out := *c
	out.Status = "pending"
	err := r.pool.QueryRow(ctx, `
		INSERT INTO connections (requester_id, recipient_id, origin_plan_id)
		VALUES ($1, $2, NULLIF($3,'')::uuid)
		RETURNING id`,
		c.RequesterID, c.RecipientID, c.OriginPlanID,
	).Scan(&out.ID)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *Repository) Get(ctx context.Context, id string) (*Connection, error) {
	var c Connection
	err := r.pool.QueryRow(ctx, `
		SELECT id, requester_id, recipient_id, COALESCE(origin_plan_id::text,''), status
		FROM connections WHERE id = $1`, id,
	).Scan(&c.ID, &c.RequesterID, &c.RecipientID, &c.OriginPlanID, &c.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrConnectionNotFound
		}
		return nil, err
	}
	return &c, nil
}

// Respond updates status — caller ownership (only the recipient may
// accept/reject) is enforced in service.go, not here.
func (r *Repository) Respond(ctx context.Context, id string, accept bool) (*Connection, error) {
	newStatus := "rejected"
	if accept {
		newStatus = "accepted"
	}
	var c Connection
	err := r.pool.QueryRow(ctx, `
		UPDATE connections SET status = $2::connection_status, updated_at = now()
		WHERE id = $1
		RETURNING id, requester_id, recipient_id, COALESCE(origin_plan_id::text,''), status`,
		id, newStatus,
	).Scan(&c.ID, &c.RequesterID, &c.RecipientID, &c.OriginPlanID, &c.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrConnectionNotFound
		}
		return nil, err
	}
	return &c, nil
}

func (r *Repository) ListForUser(ctx context.Context, userID string, limit int) ([]*Connection, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, requester_id, recipient_id, COALESCE(origin_plan_id::text,''), status
		FROM connections WHERE requester_id = $1 OR recipient_id = $1
		ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Connection
	for rows.Next() {
		var c Connection
		if err := rows.Scan(&c.ID, &c.RequesterID, &c.RecipientID, &c.OriginPlanID, &c.Status); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}
