// Package referral implements flow.md §50: a user shares a code, a new user
// applies it during their first week, and both get a platform credit.
package referral

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hivemind/backend/pkg/eventbus"
)

const (
	// RewardMinor is what EACH side receives: ₹100 in paise.
	RewardMinor = 10000
	Currency    = "INR"
	// Cap bounds how much one referrer can earn — a cheap brake on farming.
	Cap = 20
	// SignupWindow is how long after signup a code may still be applied.
	SignupWindow = 7 * 24 * time.Hour
)

var (
	ErrInvalidInput    = errors.New("referral: invalid input")
	ErrCodeNotFound    = errors.New("referral: code not found")
	ErrSelfReferral    = errors.New("referral: you can't use your own code")
	ErrAlreadyReferred = errors.New("referral: you've already used a referral code")
	ErrWindowClosed    = errors.New("referral: codes can only be applied within 7 days of signing up")
	ErrCapReached      = errors.New("referral: this code has reached its referral limit")
)

// unambiguous alphabet: no 0/O, 1/I/L
const alphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

type Repository struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, now: time.Now}
}

func newCode() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b), nil
}

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// GetOrCreateCode is idempotent: ON CONFLICT (user_id) DO NOTHING keeps the
// first code; a collision on the code itself (23505) retries with a new one.
func (r *Repository) GetOrCreateCode(ctx context.Context, userID string) (string, error) {
	for i := 0; i < 5; i++ {
		var code string
		err := r.pool.QueryRow(ctx, `SELECT code FROM referral_codes WHERE user_id = $1`, userID).Scan(&code)
		if err == nil {
			return code, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		c, err := newCode()
		if err != nil {
			return "", err
		}
		if _, err := r.pool.Exec(ctx,
			`INSERT INTO referral_codes (user_id, code) VALUES ($1, $2) ON CONFLICT (user_id) DO NOTHING`, userID, c); err != nil {
			if isUnique(err) {
				continue
			}
			return "", err
		}
	}
	return "", errors.New("referral: could not allocate a code")
}

// Apply does everything in one transaction: resolve the referrer (locking
// their code row so the cap check is race-free), enforce the rules, insert
// the referral (referee_id UNIQUE = at most once) and both ledger rows.
func (r *Repository) Apply(ctx context.Context, refereeID, code string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var referrerID string
	err = tx.QueryRow(ctx, `SELECT user_id::text FROM referral_codes WHERE code = $1 FOR UPDATE`, code).Scan(&referrerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCodeNotFound
	}
	if err != nil {
		return err
	}
	if referrerID == refereeID {
		return ErrSelfReferral
	}

	var createdAt time.Time
	if err := tx.QueryRow(ctx, `SELECT created_at FROM users WHERE id = $1`, refereeID).Scan(&createdAt); err != nil {
		return err
	}
	if r.now().Sub(createdAt) > SignupWindow {
		return ErrWindowClosed
	}

	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM referrals WHERE referrer_id = $1`, referrerID).Scan(&n); err != nil {
		return err
	}
	if n >= Cap {
		return ErrCapReached
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO referrals (referrer_id, referee_id, code, reward_minor) VALUES ($1, $2, $3, $4)`,
		referrerID, refereeID, code, RewardMinor); err != nil {
		if isUnique(err) {
			return ErrAlreadyReferred
		}
		return err
	}
	for _, l := range []struct{ user, reason string }{
		{referrerID, "referral_referrer"}, {refereeID, "referral_referee"},
	} {
		if _, err := tx.Exec(ctx,
			`INSERT INTO credit_ledger (user_id, amount_minor, reason) VALUES ($1, $2, $3)`,
			l.user, RewardMinor, l.reason); err != nil {
			return err
		}
	}
	for _, id := range []string{referrerID, refereeID} {
		if err := eventbus.EnqueueNotifyUser(ctx, tx, eventbus.NotifyUserPayload{
			UserID: id, Title: "Referral reward", Body: "₹100 credit has been added to your account.",
			DeepLink: "hivemind://wallet", Channel: "in_app",
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *Repository) Stats(ctx context.Context, userID string) (count int32, earned int64, err error) {
	err = r.pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(sum(reward_minor), 0) FROM referrals WHERE referrer_id = $1`, userID).Scan(&count, &earned)
	return
}

func normalize(code string) string { return strings.ToUpper(strings.TrimSpace(code)) }
