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

var (
	ErrAlreadyDecided = errors.New("connections: request was already decided differently")
	ErrUserNotFound   = errors.New("connections: user not found")
	ErrBadOriginPlan  = errors.New("connections: you and that user did not attend the origin plan together")
)

// Create is idempotent and race-safe: an advisory lock on the unordered pair
// serialises A→B and B→A requests (the UNIQUE(requester,recipient) key only
// guards one direction). An existing row in either direction is returned
// as-is; a pending request from the other side means both want it, so it is
// accepted. Blocks in either direction look like a missing user.
func (r *Repository) Create(ctx context.Context, c *Connection) (*Connection, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	lo, hi := c.RequesterID, c.RecipientID
	if lo > hi {
		lo, hi = hi, lo
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "conn:"+lo+":"+hi); err != nil {
		return nil, err
	}

	var visible bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM users WHERE id = $2)
		   AND NOT EXISTS(SELECT 1 FROM blocks WHERE (user_id = $1 AND blocked_user_id = $2) OR (user_id = $2 AND blocked_user_id = $1))`,
		c.RequesterID, c.RecipientID,
	).Scan(&visible); err != nil {
		return nil, err
	}
	if !visible {
		return nil, ErrUserNotFound
	}

	if c.OriginPlanID != "" {
		var both bool
		if err := tx.QueryRow(ctx, `
			SELECT (SELECT count(DISTINCT user_id) FROM plan_participants
			        WHERE plan_id = $1 AND status = 'confirmed' AND user_id IN ($2, $3)) = 2`,
			c.OriginPlanID, c.RequesterID, c.RecipientID,
		).Scan(&both); err != nil {
			return nil, err
		}
		if !both {
			return nil, ErrBadOriginPlan
		}
	}

	var out Connection
	err = tx.QueryRow(ctx, `
		SELECT id, requester_id, recipient_id, COALESCE(origin_plan_id::text,''), status
		FROM connections
		WHERE (requester_id = $1 AND recipient_id = $2) OR (requester_id = $2 AND recipient_id = $1)`,
		c.RequesterID, c.RecipientID,
	).Scan(&out.ID, &out.RequesterID, &out.RecipientID, &out.OriginPlanID, &out.Status)
	switch {
	case err == nil:
		if out.Status == "pending" && out.RecipientID == c.RequesterID { // mutual intent
			if err := tx.QueryRow(ctx, `
				UPDATE connections SET status = 'accepted'::connection_status, updated_at = now()
				WHERE id = $1 RETURNING status`, out.ID).Scan(&out.Status); err != nil {
				return nil, err
			}
		}
	case errors.Is(err, pgx.ErrNoRows):
		out = *c
		if err := tx.QueryRow(ctx, `
			INSERT INTO connections (requester_id, recipient_id, origin_plan_id)
			VALUES ($1, $2, NULLIF($3,'')::uuid)
			RETURNING id, status`,
			c.RequesterID, c.RecipientID, c.OriginPlanID,
		).Scan(&out.ID, &out.Status); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	return &out, tx.Commit(ctx)
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

// Respond only transitions a request out of 'pending'. Repeating the same
// decision returns the row; the opposite decision is ErrAlreadyDecided (an
// accepted connection can no longer be flipped to rejected, or vice versa).
// Caller ownership (only the recipient may respond) is enforced in service.go.
func (r *Repository) Respond(ctx context.Context, id string, accept bool) (*Connection, error) {
	newStatus := "rejected"
	if accept {
		newStatus = "accepted"
	}
	var c Connection
	err := r.pool.QueryRow(ctx, `
		UPDATE connections SET status = $2::connection_status, updated_at = now()
		WHERE id = $1 AND status = 'pending'::connection_status
		RETURNING id, requester_id, recipient_id, COALESCE(origin_plan_id::text,''), status`,
		id, newStatus,
	).Scan(&c.ID, &c.RequesterID, &c.RecipientID, &c.OriginPlanID, &c.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		cur, gerr := r.Get(ctx, id)
		if gerr != nil {
			return nil, gerr
		}
		if cur.Status == newStatus {
			return cur, nil
		}
		return nil, ErrAlreadyDecided
	}
	if err != nil {
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
