// Package users implements the account-management half of PRD §13.1 (signup
// itself lives in internal/auth). Every RPC is fully implemented.
package users

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type User struct {
	ID          string
	Email       string
	CityID      string
	AgeVerified bool
	Status      string
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Get(ctx context.Context, id string) (*User, error) {
	var u User
	err := r.pool.QueryRow(ctx,
		`SELECT id, email, COALESCE(city_id::text,''), age_verified, status FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Email, &u.CityID, &u.AgeVerified, &u.Status)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *Repository) UpdateCity(ctx context.Context, userID, cityID string) (*User, error) {
	var u User
	err := r.pool.QueryRow(ctx, `
		UPDATE users SET city_id = NULLIF($2,'')::uuid, updated_at = now()
		WHERE id = $1
		RETURNING id, email, COALESCE(city_id::text,''), age_verified, status`,
		userID, cityID,
	).Scan(&u.ID, &u.Email, &u.CityID, &u.AgeVerified, &u.Status)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// SoftDelete marks the account deleted without erasing the row — bookings,
// reviews, and moderation history reference users.id and must stay intact.
func (r *Repository) SoftDelete(ctx context.Context, userID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE users SET status = 'deleted', updated_at = now() WHERE id = $1`, userID,
	)
	return err
}

func (r *Repository) UpsertDevice(ctx context.Context, userID, deviceID, pushToken, platform string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO devices (user_id, device_id, push_token, platform)
		VALUES ($1, $2, NULLIF($3,''), NULLIF($4,'')::device_platform)
		ON CONFLICT (user_id, device_id)
		DO UPDATE SET push_token = EXCLUDED.push_token, platform = EXCLUDED.platform`,
		userID, deviceID, pushToken, platform,
	)
	return err
}

// UpdateLocation records the user's last known device location (flow.md
// §2.3/§9) for NEAR_YOU discovery — see internal/discovery's HomeFeedPlanIDs.
func (r *Repository) UpdateLocation(ctx context.Context, userID string, lat, lng float64) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE users SET last_location = ST_SetSRID(ST_MakePoint($3, $2), 4326)::geography,
			last_location_at = now(), updated_at = now()
		WHERE id = $1`,
		userID, lat, lng,
	)
	return err
}
