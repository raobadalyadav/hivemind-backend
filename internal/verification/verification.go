// Package verification implements the blue tick: a live selfie holding a
// random pose, reviewed by an admin. There is no automatic face matching.
package verification

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hivemind/backend/pkg/media"
)

var (
	ErrInvalidInput     = errors.New("verification: invalid input")
	ErrAlreadyVerified  = errors.New("verification: you're already verified")
	ErrNoChallenge      = errors.New("verification: start again — your pose request expired")
	ErrMediaUnavailable = errors.New("verification: uploads are not configured")
)

// Poses is the fixed challenge list; the app shows a localized instruction
// for each key.
var Poses = []string{"peace", "thumbs_up", "wave", "touch_nose", "look_left"}

const challengeTTL = 15 * time.Minute

type State struct {
	Status       string // none | issued | pending | approved | rejected
	Challenge    string
	ExpiresAt    time.Time
	RejectReason string
	Verified     bool
}

type Service struct {
	pool  *pgxpool.Pool
	media media.Resolver
	now   func() time.Time
}

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool, now: time.Now} }

// WithMedia enables submitting selfies (attached by upload id).
func (s *Service) WithMedia(m media.Resolver) *Service {
	s.media = m
	return s
}

func randomPose() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(Poses))))
	if err != nil {
		return "", err
	}
	return Poses[n.Int64()], nil
}

func (s *Service) expireStale(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE verification_requests SET status = 'expired'
		WHERE user_id = $1 AND status = 'issued' AND expires_at < $2::timestamptz`, userID, s.now())
	return err
}

func (s *Service) Get(ctx context.Context, userID string) (*State, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	if err := s.expireStale(ctx, userID); err != nil {
		return nil, err
	}
	st := &State{Status: "none"}
	if err := s.pool.QueryRow(ctx, `SELECT selfie_verified_at IS NOT NULL FROM user_profiles WHERE user_id = $1`, userID).Scan(&st.Verified); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if st.Verified {
		st.Status = "approved"
		return st, nil
	}
	// The open request if any, otherwise the latest decision (so a rejection is visible).
	err := s.pool.QueryRow(ctx, `
		SELECT status, challenge, expires_at, reject_reason FROM verification_requests
		WHERE user_id = $1 AND status IN ('issued','pending','rejected')
		ORDER BY (status IN ('issued','pending')) DESC, created_at DESC LIMIT 1`, userID).Scan(&st.Status, &st.Challenge, &st.ExpiresAt, &st.RejectReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	return st, err
}

// Start issues (or re-shows) the pose to hold. One open request per user is
// enforced by a partial unique index, so a double tap can't create two.
func (s *Service) Start(ctx context.Context, userID string) (*State, error) {
	cur, err := s.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	if cur.Verified {
		return nil, ErrAlreadyVerified
	}
	if cur.Status == "issued" || cur.Status == "pending" {
		return cur, nil
	}
	pose, err := randomPose()
	if err != nil {
		return nil, err
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO verification_requests (user_id, challenge, status, expires_at) VALUES ($1, $2, 'issued', $3::timestamptz)
		ON CONFLICT DO NOTHING`, userID, pose, s.now().Add(challengeTTL)); err != nil {
		return nil, err
	}
	return s.Get(ctx, userID)
}

// Submit attaches the caller's own live selfie to their open challenge and
// sends it for review.
func (s *Service) Submit(ctx context.Context, userID, mediaID string) (*State, error) {
	if userID == "" || mediaID == "" {
		return nil, ErrInvalidInput
	}
	if s.media == nil {
		return nil, ErrMediaUnavailable
	}
	cur, err := s.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	if cur.Status != "issued" {
		return nil, ErrNoChallenge
	}
	assets, err := s.media.Claim(ctx, userID, []string{mediaID})
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			return nil, ErrInvalidInput
		}
		return nil, err
	}
	if assets[0].Kind != "image" {
		return nil, ErrInvalidInput
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE verification_requests SET status = 'pending', media_id = $2::uuid
		WHERE user_id = $1 AND status = 'issued' AND expires_at >= $3::timestamptz`, userID, mediaID, s.now())
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNoChallenge
	}
	return s.Get(ctx, userID)
}
