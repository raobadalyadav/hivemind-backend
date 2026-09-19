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
	Severity    string
	AutoFlagged bool
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

// CreateAutoFlagged is what the ContentScreener path calls for a 'review'
// severity result — same shape as Create (a report + a case referencing
// it, in one transaction), so GetCase/Resolve's existing JOIN on reports
// keeps working unchanged for auto-flagged cases too. actorID (the
// content's own author) becomes reports.reporter_id since that column is
// NOT NULL with no "system" reporter concept in this schema — the
// auto_flagged column is what distinguishes this from a human report, not
// who's recorded as reporter_id.
func (r *Repository) CreateAutoFlagged(ctx context.Context, subjectType, subjectID, actorID, severity, reason string) (string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var reportID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO reports (reporter_id, subject_type, subject_id, reason)
		VALUES ($1, $2, $3, $4) RETURNING id`,
		actorID, subjectType, subjectID, reason,
	).Scan(&reportID); err != nil {
		return "", err
	}

	var caseID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO moderation_cases (report_id, status, severity, auto_flagged)
		VALUES ($1, 'open', $2::case_severity, true)
		RETURNING id`,
		reportID, severity,
	).Scan(&caseID); err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return caseID, nil
}

func (r *Repository) Get(ctx context.Context, id string) (*Case, error) {
	var c Case
	err := r.pool.QueryRow(ctx, `
		SELECT mc.id, r.reporter_id, r.subject_type, r.subject_id::text, r.reason,
			mc.status, mc.resolution, mc.severity::text, mc.auto_flagged
		FROM moderation_cases mc
		JOIN reports r ON r.id = mc.report_id
		WHERE mc.id = $1`, id,
	).Scan(&c.ID, &c.ReporterID, &c.SubjectType, &c.SubjectID, &c.Reason, &c.Status, &c.Resolution, &c.Severity, &c.AutoFlagged)
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

// TrustSignals are the raw inputs to DeriveBadges — never returned to a
// client directly (PRD §22: expose badges/policy outcomes, not a number).
type TrustSignals struct {
	NoShowCount      int
	ReportCount      int
	AttendedCount    int
	HostAvgRating    float64
	VerificationTier string
	TenureDays       int
}

// GetTrustSignals aggregates from existing event tables live — no cached
// column, same convention as avg_rating elsewhere in this codebase.
func (r *Repository) GetTrustSignals(ctx context.Context, userID string) (*TrustSignals, error) {
	var sig TrustSignals
	err := r.pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM bookings WHERE user_id = $1 AND status = 'no_show'::booking_status),
			(SELECT COUNT(*) FROM reports WHERE subject_type = 'user' AND subject_id = $1),
			(SELECT COUNT(*) FROM bookings WHERE user_id = $1 AND status = 'attended'::booking_status),
			COALESCE((SELECT AVG(rv.rating) FROM reviews rv JOIN plans p ON p.id = rv.plan_id WHERE p.host_id = $1), 0),
			COALESCE((SELECT verification_status FROM user_profiles WHERE user_id = $1), 'unverified'),
			COALESCE((SELECT EXTRACT(DAY FROM now() - created_at)::int FROM users WHERE id = $1), 0)`,
		userID,
	).Scan(&sig.NoShowCount, &sig.ReportCount, &sig.AttendedCount, &sig.HostAvgRating, &sig.VerificationTier, &sig.TenureDays)
	if err != nil {
		return nil, err
	}
	return &sig, nil
}

func (r *Repository) BlockUser(ctx context.Context, userID, blockedUserID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO blocks (user_id, blocked_user_id) VALUES ($1, $2)
		ON CONFLICT (user_id, blocked_user_id) DO NOTHING`,
		userID, blockedUserID,
	)
	return err
}

type BlockedUser struct {
	UserID      string
	DisplayName string
}

func (r *Repository) ListBlocked(ctx context.Context, userID string) ([]BlockedUser, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT b.blocked_user_id::text, COALESCE(up.display_name, '')
		FROM blocks b LEFT JOIN user_profiles up ON up.user_id = b.blocked_user_id
		WHERE b.user_id = $1 ORDER BY b.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BlockedUser
	for rows.Next() {
		var u BlockedUser
		if err := rows.Scan(&u.UserID, &u.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UnblockUser is idempotent: removing a block that isn't there is not an error.
// It only ever touches the caller's own block row.
func (r *Repository) UnblockUser(ctx context.Context, userID, blockedUserID string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM blocks WHERE user_id = $1 AND blocked_user_id = $2`, userID, blockedUserID)
	return err
}
