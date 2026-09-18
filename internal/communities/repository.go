// Package communities implements PRD §13.8 Communities (Phase 2) — every RPC
// is fully implemented.
package communities

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Community struct {
	ID          string
	Name        string
	Description string
	CityID      string
	OwnerID     string
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Create inserts the community and adds the owner as its first member (role
// 'owner') in one transaction.
func (r *Repository) Create(ctx context.Context, c *Community) (*Community, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	out := *c
	if err := tx.QueryRow(ctx, `
		INSERT INTO communities (name, description, city_id, owner_id)
		VALUES ($1, $2, NULLIF($3,'')::uuid, $4::uuid)
		RETURNING id`,
		c.Name, c.Description, c.CityID, c.OwnerID,
	).Scan(&out.ID); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO community_members (community_id, user_id, role) VALUES ($1, $2, 'owner')`,
		out.ID, c.OwnerID,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *Repository) Get(ctx context.Context, id string) (*Community, error) {
	var c Community
	err := r.pool.QueryRow(ctx, `
		SELECT id, name, description, COALESCE(city_id::text,''), owner_id::text
		FROM communities WHERE id = $1`, id,
	).Scan(&c.ID, &c.Name, &c.Description, &c.CityID, &c.OwnerID)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

type Membership struct {
	CommunityID string
	UserID      string
	Role        string
}

// Join is idempotent — ON CONFLICT DO NOTHING, same pattern as
// chat.Repository.AddMember — a repeated join isn't an error.
func (r *Repository) Join(ctx context.Context, communityID, userID string) (*Membership, error) {
	m := &Membership{CommunityID: communityID, UserID: userID, Role: "member"}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO community_members (community_id, user_id, role) VALUES ($1, $2, 'member')
		ON CONFLICT (community_id, user_id) DO NOTHING`,
		communityID, userID,
	)
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (r *Repository) ListPlanIDs(ctx context.Context, communityID string, limit int) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT p.id FROM community_events ce
		JOIN plans p ON p.id = ce.plan_id
		WHERE ce.community_id = $1 AND p.status = 'published'
		ORDER BY p.starts_at
		LIMIT $2`,
		communityID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
