// Package users implements the account-management half of PRD §13.1 (signup
// itself lives in internal/auth). Every RPC is fully implemented.
package users

import (
	"context"
	"errors"

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

// ErrActiveCommitments: the account can't be erased while other people depend on it.
var ErrActiveCommitments = errors.New("users: cancel your upcoming bookings, hosted plans and communities before deleting your account")

// eraseStatements remove what the person made or holds; each runs with $1 = the user id.
// Kept: bookings, payments, ledger, reviews, moderation and audit history (money and safety records
// other people and the law depend on), and messages already sent (they show as "Deleted user").
var eraseStatements = []string{
	`DELETE FROM refresh_tokens WHERE user_id = $1`,
	`DELETE FROM recovery_codes WHERE user_id = $1`,
	`DELETE FROM devices WHERE user_id = $1`,
	`DELETE FROM oauth_identities WHERE user_id = $1`,
	`DELETE FROM notifications WHERE user_id = $1 OR actor_id = $1`,
	`DELETE FROM notification_preferences WHERE user_id = $1`,
	`DELETE FROM post_saves WHERE user_id = $1`,
	`DELETE FROM plan_saves WHERE user_id = $1`,
	`DELETE FROM likes WHERE user_id = $1`,
	`DELETE FROM story_likes WHERE user_id = $1`,
	`DELETE FROM story_views WHERE viewer_id = $1`,
	`DELETE FROM profile_views WHERE viewer_id = $1 OR viewed_id = $1`,
	`DELETE FROM comments WHERE author_id = $1`,
	`DELETE FROM posts WHERE author_id = $1`,
	`DELETE FROM stories WHERE author_id = $1`,
	`DELETE FROM profile_photos WHERE user_id = $1`,
	`DELETE FROM profile_boosts WHERE user_id = $1`,
	`DELETE FROM emergency_contacts WHERE user_id = $1`,
	`DELETE FROM user_preferences WHERE user_id = $1`,
	`DELETE FROM availability_windows WHERE user_id = $1`,
	`DELETE FROM waitlist_entries WHERE user_id = $1`,
	`DELETE FROM plan_join_requests WHERE user_id = $1`,
	`DELETE FROM plan_invites WHERE user_id = $1`,
	`DELETE FROM community_join_requests WHERE user_id = $1`,
	`DELETE FROM community_members WHERE user_id = $1`,
	`DELETE FROM chat_members WHERE user_id = $1`,
	`DELETE FROM connections WHERE requester_id = $1 OR recipient_id = $1`,
	`DELETE FROM verification_requests WHERE user_id = $1`,
	`UPDATE user_profiles SET display_name = 'Deleted user', bio = '', occupation = '', education = '',
	     interests = '{}', languages = '{}', hobbies = '{}', gender = NULL, selfie_verified_at = NULL,
	     show_in_participant_previews = false, hide_profile_views = true, updated_at = now() WHERE user_id = $1`,
	`UPDATE users SET status = 'deleted', email = 'deleted+' || id::text || '@deleted.invalid',
	     recovery_email = NULL, recovery_email_verified_at = NULL, date_of_birth = NULL, city_id = NULL,
	     last_location = NULL, last_location_at = NULL, updated_at = now() WHERE id = $1`,
}

// EraseAccount deletes the person's data and frees their e-mail / sign-in identity, keeping only the
// records that must outlive them (see eraseStatements). Everything happens in one transaction.
func (r *Repository) EraseAccount(ctx context.Context, userID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var busy bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM bookings b JOIN plans p ON p.id = b.plan_id
		               WHERE b.user_id = $1 AND b.status = 'confirmed' AND GREATEST(p.ends_at, p.starts_at) > now())
		    OR EXISTS (SELECT 1 FROM plans WHERE host_id = $1 AND status = 'published' AND GREATEST(ends_at, starts_at) > now())
		    OR EXISTS (SELECT 1 FROM communities WHERE owner_id = $1)`, userID).Scan(&busy); err != nil {
		return err
	}
	if busy {
		return ErrActiveCommitments
	}
	for _, q := range eraseStatements {
		if _, err := tx.Exec(ctx, q, userID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
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
