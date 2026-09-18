// Package auth implements PRD §13.1 Authentication & Account. SignUp/SignIn
// are the fully working vertical slice; RefreshToken/SignOut/
// RequestPasswordReset are typed stubs.
package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pgUniqueViolation = "23505"

var ErrUserExists = errors.New("auth: email already registered")
var ErrInvalidCredentials = errors.New("auth: invalid credentials")

type userRow struct {
	ID           string
	PasswordHash string
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// CreateUser inserts the account and its (initially empty) profile in one
// transaction, so a user never exists without a profile row.
func (r *Repository) CreateUser(ctx context.Context, email, passwordHash string, ageVerified bool) (string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var userID string
	err = tx.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, age_verified) VALUES ($1, $2, $3) RETURNING id`,
		email, passwordHash, ageVerified,
	).Scan(&userID)
	if err != nil {
		if isUniqueViolation(err) {
			return "", ErrUserExists
		}
		return "", err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO user_profiles (user_id, display_name) VALUES ($1, '')`, userID,
	); err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return userID, nil
}

func (r *Repository) GetUserByEmail(ctx context.Context, email string) (*userRow, error) {
	var u userRow
	err := r.pool.QueryRow(ctx,
		`SELECT id, password_hash FROM users WHERE email = $1`, email,
	).Scan(&u.ID, &u.PasswordHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	return &u, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}
