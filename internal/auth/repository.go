// Package auth implements PRD §13.1 Authentication & Account, per flow.md
// §2.1: Google/Apple OAuth only, no password. Account recovery is a
// verified recovery email (see migration 0019) that lets a user re-link a
// new OAuth identity if they lose access to Google/Apple.
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
	ErrUserNotFound        = errors.New("auth: user not found")
	ErrRefreshTokenInvalid = errors.New("auth: refresh token invalid or expired")
	ErrRecoveryCodeInvalid = errors.New("auth: recovery code invalid, expired, or already used")
	ErrEmailAlreadyUsed    = errors.New("auth: email already in use as a recovery email")
)

type userRow struct {
	ID     string
	Role   string
	Status string
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

func (r *Repository) FindByProviderIdentity(ctx context.Context, provider, providerUserID string) (*userRow, error) {
	var u userRow
	err := r.pool.QueryRow(ctx, `
		SELECT u.id, u.role::text, u.status
		FROM users u
		JOIN oauth_identities oi ON oi.user_id = u.id
		WHERE oi.provider = $1::oauth_provider AND oi.provider_user_id = $2`,
		provider, providerUserID,
	).Scan(&u.ID, &u.Role, &u.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &u, nil
}

// CreateUserFromOAuth inserts the account, its (initially empty) profile,
// and the OAuth identity that created it, all in one transaction — a user
// never exists without a profile row or a way to sign back in.
func (r *Repository) CreateUserFromOAuth(ctx context.Context, email string, ageVerified bool, provider, providerUserID, providerEmail string) (string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var userID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO users (email, age_verified) VALUES ($1, $2) RETURNING id`,
		email, ageVerified,
	).Scan(&userID); err != nil {
		return "", err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO user_profiles (user_id, display_name) VALUES ($1, '')`, userID,
	); err != nil {
		return "", err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO oauth_identities (user_id, provider, provider_user_id, email)
		VALUES ($1, $2::oauth_provider, $3, $4)`,
		userID, provider, providerUserID, providerEmail,
	); err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return userID, nil
}

// LinkOAuthIdentity attaches a new provider identity to an existing user —
// used both when a signed-in user links a second provider and by
// RecoverAccount when re-linking after losing access to the original one.
func (r *Repository) LinkOAuthIdentity(ctx context.Context, userID, provider, providerUserID, email string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO oauth_identities (user_id, provider, provider_user_id, email)
		VALUES ($1, $2::oauth_provider, $3, $4)
		ON CONFLICT (provider, provider_user_id) DO UPDATE SET user_id = EXCLUDED.user_id`,
		userID, provider, providerUserID, email,
	)
	return err
}

func (r *Repository) GetUserByID(ctx context.Context, userID string) (*userRow, error) {
	var u userRow
	err := r.pool.QueryRow(ctx,
		`SELECT id, role::text, status FROM users WHERE id = $1`, userID,
	).Scan(&u.ID, &u.Role, &u.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &u, nil
}

// UpsertDevice records/updates the caller's device (platform) and returns
// its internal id, used to scope refresh tokens per PRD §13.1 "session/
// device management". A blank deviceID means the caller didn't supply one.
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

// RevokeDeviceTokens revokes every live refresh token for the user's device
// — SignOut for that device. deviceID is the client-supplied
// devices.device_id string, not the internal devices.id UUID that
// refresh_tokens.device_id actually stores — the join resolves one to the
// other. No matching device row is a no-op, not an error: SignOut should be
// idempotent.
func (r *Repository) RevokeDeviceTokens(ctx context.Context, userID, deviceID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE refresh_tokens SET revoked_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL
		  AND device_id = (SELECT id FROM devices WHERE user_id = $1 AND device_id = $2)`,
		userID, deviceID,
	)
	return err
}

// SetPendingRecoveryEmail claims users.recovery_email immediately
// (unverified — recovery_email_verified_at stays NULL until
// VerifyRecoveryEmail). ponytail: this means two accounts can briefly race
// for the same email before one verifies; acceptable for pre-launch scope,
// revisit with a partial-unique-index-on-verified approach if abuse shows up.
func (r *Repository) SetPendingRecoveryEmail(ctx context.Context, userID, email string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE users SET recovery_email = $2, recovery_email_verified_at = NULL, updated_at = now() WHERE id = $1`,
		userID, email,
	)
	if isUniqueViolation(err) {
		return ErrEmailAlreadyUsed
	}
	return err
}

func (r *Repository) MarkRecoveryEmailVerified(ctx context.Context, userID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE users SET recovery_email_verified_at = now(), updated_at = now() WHERE id = $1`, userID,
	)
	return err
}

func (r *Repository) CreateRecoveryCode(ctx context.Context, userID, codeHash, purpose string, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO recovery_codes (user_id, code_hash, purpose, expires_at) VALUES ($1, $2, $3, $4)`,
		userID, codeHash, purpose, expiresAt,
	)
	return err
}

// ConsumeRecoveryCode looks up a non-expired, unused code for the given
// purpose and marks it used in the same statement (single-use, like refresh
// tokens). Returns the owning user id.
func (r *Repository) ConsumeRecoveryCode(ctx context.Context, codeHash, purpose string) (string, error) {
	var userID string
	err := r.pool.QueryRow(ctx, `
		UPDATE recovery_codes SET used_at = now()
		WHERE code_hash = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > now()
		RETURNING user_id`,
		codeHash, purpose,
	).Scan(&userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrRecoveryCodeInvalid
		}
		return "", err
	}
	return userID, nil
}

func (r *Repository) FindByVerifiedRecoveryEmail(ctx context.Context, email string) (*userRow, error) {
	var u userRow
	err := r.pool.QueryRow(ctx,
		`SELECT id, role::text, status FROM users
		 WHERE recovery_email = $1 AND recovery_email_verified_at IS NOT NULL`,
		email,
	).Scan(&u.ID, &u.Role, &u.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &u, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}
