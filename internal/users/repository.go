// Package users implements the account-management half of PRD §13.1 (signup
// itself lives in internal/auth). GetUser is the fully working vertical
// slice; UpdateUser/DeleteAccount/RegisterDevice are typed stubs.
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
		`SELECT id, email, COALESCE(city_id::text,''), age_verified FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Email, &u.CityID, &u.AgeVerified)
	if err != nil {
		return nil, err
	}
	return &u, nil
}
