// Package users implements the account-management half of PRD §13.1 (signup
// itself lives in internal/auth). Every RPC is fully implemented.
package users

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type User struct {
	ID            string
	Email         string
	CityID        string
	AgeVerified   bool
	Status        string
	DateOfBirth   string // YYYY-MM-DD, empty until the person sets it
	TermsAccepted bool   // agreed to CurrentTermsVersion
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
		`SELECT id, email, COALESCE(city_id::text,''), age_verified, status, COALESCE(to_char(date_of_birth,'YYYY-MM-DD'),''),
		        EXISTS (SELECT 1 FROM consents c WHERE c.user_id = users.id AND c.consent_type = 'terms:' || $2::text)
		 FROM users WHERE id = $1`, id, CurrentTermsVersion,
	).Scan(&u.ID, &u.Email, &u.CityID, &u.AgeVerified, &u.Status, &u.DateOfBirth, &u.TermsAccepted)
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
		RETURNING id, email, COALESCE(city_id::text,''), age_verified, status, COALESCE(to_char(date_of_birth,'YYYY-MM-DD'),'')`,
		userID, cityID,
	).Scan(&u.ID, &u.Email, &u.CityID, &u.AgeVerified, &u.Status, &u.DateOfBirth)
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

// ErrBirthdayLocked: the date of birth was already set.
var ErrBirthdayLocked = errors.New("users: your birthday is already set and can't be changed — contact support if it's wrong")

// SetBirthday stores the date of birth once and marks the person as a verified adult. It only ever writes
// when none is set, so it can't be used to change an age later.
func (r *Repository) SetBirthday(ctx context.Context, userID string, dob time.Time) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE users SET date_of_birth = $2::date, age_verified = true, updated_at = now()
		WHERE id = $1 AND date_of_birth IS NULL`, userID, dob.Format("2006-01-02"))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrBirthdayLocked
	}
	return nil
}

// CurrentTermsVersion identifies the Terms of Service + Privacy Policy text people must have accepted.
// Bump it when the documents change materially: everyone is asked again on their next launch.
const CurrentTermsVersion = "2026-09"

// AcceptTerms records the acceptance (idempotent).
func (r *Repository) AcceptTerms(ctx context.Context, userID, version string) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO consents (user_id, consent_type) VALUES ($1, 'terms:' || $2::text) ON CONFLICT DO NOTHING`, userID, version)
	return err
}

// exportQueries: each section of the personal-data export, run with $1 = the user id. They select whole
// rows (as JSON) so a column added later is exported too; secrets (push tokens, token hashes) are left out.
var exportQueries = []struct{ name, query string }{
	{"account", `SELECT id, email, recovery_email, date_of_birth, role, status, city_id, created_at FROM users WHERE id = $1`},
	{"profile", `SELECT * FROM user_profiles WHERE user_id = $1`},
	{"preferences", `SELECT * FROM user_preferences WHERE user_id = $1`},
	{"photos", `SELECT id, url, position, created_at FROM profile_photos WHERE user_id = $1`},
	{"emergency_contacts", `SELECT * FROM emergency_contacts WHERE user_id = $1`},
	{"sign_in_methods", `SELECT provider, email, created_at FROM oauth_identities WHERE user_id = $1`},
	{"devices", `SELECT platform, created_at FROM devices WHERE user_id = $1`},
	{"consents", `SELECT consent_type, granted_at FROM consents WHERE user_id = $1`},
	{"posts", `SELECT * FROM posts WHERE author_id = $1`},
	{"comments", `SELECT * FROM comments WHERE author_id = $1`},
	{"stories", `SELECT id, caption, media_type, created_at, expires_at FROM stories WHERE author_id = $1`},
	{"connections", `SELECT * FROM connections WHERE requester_id = $1 OR recipient_id = $1`},
	{"bookings", `SELECT * FROM bookings WHERE user_id = $1`},
	{"orders", `SELECT o.* FROM orders o JOIN bookings b ON b.id = o.booking_id WHERE b.user_id = $1`},
	{"credit_ledger", `SELECT * FROM credit_ledger WHERE user_id = $1`},
	{"reviews", `SELECT * FROM reviews WHERE user_id = $1`},
	{"saved_plans", `SELECT plan_id, created_at FROM plan_saves WHERE user_id = $1`},
	{"reports_filed", `SELECT id, subject_type, reason, created_at FROM reports WHERE reporter_id = $1`},
}

// ExportData returns everything the app holds about the person, as one JSON object (DPDP / GDPR access request).
func (r *Repository) ExportData(ctx context.Context, userID string) ([]byte, error) {
	out := make(map[string]json.RawMessage, len(exportQueries)+1)
	for _, e := range exportQueries {
		var rows []byte
		if err := r.pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(t)), '[]'::jsonb) FROM (`+e.query+`) t`, userID).Scan(&rows); err != nil {
			return nil, fmt.Errorf("export %s: %w", e.name, err)
		}
		out[e.name] = rows
	}
	out["exported_at"], _ = json.Marshal(time.Now().UTC())
	return json.MarshalIndent(out, "", "  ")
}
