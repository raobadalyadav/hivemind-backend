// Package connections implements PRD §13.9 Connections (Phase 2).
// RequestConnection/ListConnections are the fully working vertical slice;
// RespondConnection is a typed stub.
package connections

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

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
