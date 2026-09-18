// Package profiles implements PRD §13.2 Profile & Identity — every RPC is
// fully implemented.
package profiles

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Profile struct {
	UserID             string
	DisplayName        string
	Bio                string
	Interests          []string
	Languages          []string
	Occupation         string
	VerificationStatus string
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Create upserts the profile row created (empty) during signup — see
// internal/auth.Repository.CreateUser — with the caller-supplied fields.
func (r *Repository) Create(ctx context.Context, p *Profile) (*Profile, error) {
	out := *p
	err := r.pool.QueryRow(ctx, `
		UPDATE user_profiles SET display_name = $2, interests = $3, updated_at = now()
		WHERE user_id = $1
		RETURNING display_name, interests, languages, occupation, verification_status`,
		p.UserID, p.DisplayName, p.Interests,
	).Scan(&out.DisplayName, &out.Interests, &out.Languages, &out.Occupation, &out.VerificationStatus)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *Repository) Get(ctx context.Context, userID string) (*Profile, error) {
	var p Profile
	p.UserID = userID
	err := r.pool.QueryRow(ctx, `
		SELECT display_name, bio, interests, languages, occupation, verification_status
		FROM user_profiles WHERE user_id = $1`, userID,
	).Scan(&p.DisplayName, &p.Bio, &p.Interests, &p.Languages, &p.Occupation, &p.VerificationStatus)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *Repository) Update(ctx context.Context, p *Profile) (*Profile, error) {
	out := *p
	out.UserID = p.UserID
	err := r.pool.QueryRow(ctx, `
		UPDATE user_profiles SET bio = $2, interests = $3, languages = $4, occupation = $5, updated_at = now()
		WHERE user_id = $1
		RETURNING display_name, verification_status`,
		p.UserID, p.Bio, p.Interests, p.Languages, p.Occupation,
	).Scan(&out.DisplayName, &out.VerificationStatus)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *Repository) SetPrivacy(ctx context.Context, userID string, showInPreviews bool) (*Profile, error) {
	var p Profile
	p.UserID = userID
	err := r.pool.QueryRow(ctx, `
		UPDATE user_profiles SET show_in_participant_previews = $2, updated_at = now()
		WHERE user_id = $1
		RETURNING display_name, bio, interests, languages, occupation, verification_status`,
		userID, showInPreviews,
	).Scan(&p.DisplayName, &p.Bio, &p.Interests, &p.Languages, &p.Occupation, &p.VerificationStatus)
	if err != nil {
		return nil, err
	}
	return &p, nil
}
