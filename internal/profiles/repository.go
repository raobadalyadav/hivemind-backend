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
	Gender             string
	Education          string
	Hobbies            []string
	Photos             []Photo
	SelfieVerified     bool
}

// Update is a partial update: nil pointer / nil slice = leave unchanged.
// ponytail: an empty repeated field can't be told apart from an omitted one
// over proto3, so interests/languages/hobbies can be replaced but not
// cleared; add StringList wrappers if clearing is ever needed.
type Update struct {
	UserID     string
	Bio        *string
	Occupation *string
	Gender     *string
	Education  *string
	Interests  []string
	Languages  []string
	Hobbies    []string
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

const profileCols = `display_name, bio, interests, languages, occupation, verification_status,
	COALESCE(gender,''), education, hobbies, selfie_verified_at IS NOT NULL`

func scanProfile(row interface{ Scan(...any) error }, p *Profile) error {
	return row.Scan(&p.DisplayName, &p.Bio, &p.Interests, &p.Languages, &p.Occupation,
		&p.VerificationStatus, &p.Gender, &p.Education, &p.Hobbies, &p.SelfieVerified)
}

// Create fills in the (empty) profile row created during signup — see
// internal/auth.Repository.CreateUser.
func (r *Repository) Create(ctx context.Context, p *Profile) (*Profile, error) {
	out := Profile{UserID: p.UserID}
	err := scanProfile(r.pool.QueryRow(ctx, `
		UPDATE user_profiles SET display_name = $2, interests = $3, updated_at = now()
		WHERE user_id = $1
		RETURNING `+profileCols,
		p.UserID, p.DisplayName, p.Interests,
	), &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *Repository) Get(ctx context.Context, userID string) (*Profile, error) {
	p := Profile{UserID: userID}
	if err := scanProfile(r.pool.QueryRow(ctx,
		`SELECT `+profileCols+` FROM user_profiles WHERE user_id = $1`, userID), &p); err != nil {
		return nil, err
	}
	photos, err := r.ListPhotos(ctx, userID)
	if err != nil {
		return nil, err
	}
	p.Photos = photos
	return &p, nil
}

func (r *Repository) Update(ctx context.Context, u *Update) (*Profile, error) {
	p := Profile{UserID: u.UserID}
	err := scanProfile(r.pool.QueryRow(ctx, `
		UPDATE user_profiles SET
			bio        = COALESCE($2, bio),
			occupation = COALESCE($3, occupation),
			gender     = COALESCE(NULLIF($4::text,''), gender),
			education  = COALESCE($5, education),
			interests  = COALESCE($6::text[], interests),
			languages  = COALESCE($7::text[], languages),
			hobbies    = COALESCE($8::text[], hobbies),
			updated_at = now()
		WHERE user_id = $1
		RETURNING `+profileCols,
		u.UserID, u.Bio, u.Occupation, u.Gender, u.Education, u.Interests, u.Languages, u.Hobbies,
	), &p)
	if err != nil {
		return nil, err
	}
	photos, err := r.ListPhotos(ctx, u.UserID)
	if err != nil {
		return nil, err
	}
	p.Photos = photos
	return &p, nil
}

func (r *Repository) SetPrivacy(ctx context.Context, userID string, showInPreviews bool) (*Profile, error) {
	p := Profile{UserID: userID}
	err := scanProfile(r.pool.QueryRow(ctx, `
		UPDATE user_profiles SET show_in_participant_previews = $2, updated_at = now()
		WHERE user_id = $1
		RETURNING `+profileCols, userID, showInPreviews), &p)
	if err != nil {
		return nil, err
	}
	return &p, nil
}
