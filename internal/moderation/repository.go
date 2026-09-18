// Package moderation implements PRD §13.16 Trust & Safety — every RPC is
// fully implemented.
package moderation

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Case struct {
	ID          string
	ReporterID  string
	SubjectType string
	SubjectID   string
	Reason      string
	Status      string
	Resolution  string
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Create inserts the report and opens a moderation case referencing it in
// one transaction — a report never exists without a case to triage it.
func (r *Repository) Create(ctx context.Context, c *Case) (*Case, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var reportID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO reports (reporter_id, subject_type, subject_id, reason)
		VALUES ($1, $2, $3, $4) RETURNING id`,
		c.ReporterID, c.SubjectType, c.SubjectID, c.Reason,
	).Scan(&reportID); err != nil {
		return nil, err
	}

	out := *c
	out.Status = "open"
	if err := tx.QueryRow(ctx, `
		INSERT INTO moderation_cases (report_id, status) VALUES ($1, 'open')
		RETURNING id`, reportID,
	).Scan(&out.ID); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *Repository) Get(ctx context.Context, id string) (*Case, error) {
	var c Case
	err := r.pool.QueryRow(ctx, `
		SELECT mc.id, r.reporter_id, r.subject_type, r.subject_id::text, r.reason,
			mc.status, mc.resolution
		FROM moderation_cases mc
		JOIN reports r ON r.id = mc.report_id
		WHERE mc.id = $1`, id,
	).Scan(&c.ID, &c.ReporterID, &c.SubjectType, &c.SubjectID, &c.Reason, &c.Status, &c.Resolution)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *Repository) Resolve(ctx context.Context, caseID, resolution string) (*Case, error) {
	var c Case
	c.ID = caseID
	err := r.pool.QueryRow(ctx, `
		UPDATE moderation_cases SET status = 'resolved', resolution = $2, updated_at = now()
		WHERE id = $1
		RETURNING (SELECT reporter_id FROM reports WHERE id = moderation_cases.report_id),
			(SELECT subject_type FROM reports WHERE id = moderation_cases.report_id),
			(SELECT subject_id::text FROM reports WHERE id = moderation_cases.report_id),
			(SELECT reason FROM reports WHERE id = moderation_cases.report_id),
			status, resolution`,
		caseID, resolution,
	).Scan(&c.ReporterID, &c.SubjectType, &c.SubjectID, &c.Reason, &c.Status, &c.Resolution)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *Repository) BlockUser(ctx context.Context, userID, blockedUserID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO blocks (user_id, blocked_user_id) VALUES ($1, $2)
		ON CONFLICT (user_id, blocked_user_id) DO NOTHING`,
		userID, blockedUserID,
	)
	return err
}
