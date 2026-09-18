// Package communities implements PRD §13.8 Communities (Phase 2).
// CreateCommunity/GetCommunity are the fully working vertical slice;
// JoinCommunity/ListCommunityPlans are typed stubs.
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
