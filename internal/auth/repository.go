// Package auth implements PRD §13.1 Authentication & Account. SignUp/SignIn/
// RefreshToken/SignOut/RequestPasswordReset are all fully implemented, backed
// by real refresh-token rotation (refresh_tokens table) and device/session
// tracking (devices table) — see migrations 0015/0018.
package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pgUniqueViolation = "23505"

var (
	ErrUserExists          = errors.New("auth: email already registered")
	ErrInvalidCredentials  = errors.New("auth: invalid credentials")
	ErrRefreshTokenInvalid = errors.New("auth: refresh token invalid or expired")
)

type userRow struct {
	ID           string
	PasswordHash string
	Role         string
	Status       string
}

type refreshTokenRow struct {
	UserID    string
	DeviceID  *string
	ExpiresAt time.Time
	RevokedAt *time.Time
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
		`SELECT id, password_hash, role::text, status FROM users WHERE email = $1`, email,
	).Scan(&u.ID, &u.PasswordHash, &u.Role, &u.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	return &u, nil
}

func (r *Repository) GetUserByID(ctx context.Context, userID string) (*userRow, error) {
	var u userRow
	err := r.pool.QueryRow(ctx,
		`SELECT id, password_hash, role::text, status FROM users WHERE id = $1`, userID,
	).Scan(&u.ID, &u.PasswordHash, &u.Role, &u.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	return &u, nil
}

// UpsertDevice records/updates the caller's device (push token, platform) and
// returns its internal id, used to scope refresh tokens per PRD §13.1
// "session/device management". A blank deviceID means the caller didn't
// supply one — refresh tokens are then not tied to any device row.
func (r *Repository) UpsertDevice(ctx context.Context, userID, deviceID, platform string) (string, error) {
	if deviceID == "" {
		return "", nil
	}
	var id string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO devices (user_id, device_id, platform)
		VALUES ($1, $2, NULLIF($3,'')::device_platform)
		ON CONFLICT (user_id, device_id) DO UPDATE SET platform = EXCLUDED.platform
		RETURNING id`,
		userID, deviceID, platform,
	).Scan(&id)
	return id, err
}

func (r *Repository) StoreRefreshToken(ctx context.Context, userID, deviceID, tokenHash string, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (user_id, device_id, token_hash, expires_at)
		VALUES ($1, NULLIF($2,'')::uuid, $3, $4)`,
		userID, deviceID, tokenHash, expiresAt,
	)
	return err
}

// ConsumeRefreshToken looks up a non-revoked, non-expired token by hash and
// revokes it in the same statement — a refresh token is single-use, rotated
// on every RefreshToken call.
func (r *Repository) ConsumeRefreshToken(ctx context.Context, tokenHash string) (*refreshTokenRow, error) {
	var row refreshTokenRow
	err := r.pool.QueryRow(ctx, `
		UPDATE refresh_tokens SET revoked_at = now()
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()
		RETURNING user_id, device_id::text, expires_at`,
		tokenHash,
	).Scan(&row.UserID, &row.DeviceID, &row.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRefreshTokenInvalid
		}
		return nil, err
	}
	return &row, nil
}

// RevokeDeviceTokens revokes every live refresh token for the user's device —
// SignOut for that device. deviceID here is the client-supplied
// devices.device_id string (what SignOutRequest carries), not the internal
// devices.id UUID that refresh_tokens.device_id actually stores — the join
// resolves one to the other. If no matching device row exists, this is a
// no-op rather than an error: SignOut should be idempotent.
func (r *Repository) RevokeDeviceTokens(ctx context.Context, userID, deviceID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE refresh_tokens SET revoked_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL
		  AND device_id = (SELECT id FROM devices WHERE user_id = $1 AND device_id = $2)`,
		userID, deviceID,
	)
	return err
}

func (r *Repository) CreatePasswordResetToken(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO password_reset_tokens (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		userID, tokenHash, expiresAt,
	)
	return err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}
