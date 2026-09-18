// Package moderation implements PRD §13.16 Trust & Safety. SubmitReport/
// GetCase are the fully working vertical slice; ResolveCase/BlockUser are
// typed stubs.
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
