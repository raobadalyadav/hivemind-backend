// Package company implements flow.md §53 Corporate Mode — team bookings
// billed to a company rather than individually. Every RPC is fully
// implemented.
package company

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Company struct {
	ID           string
	Name         string
	BillingEmail string
}

type Member struct {
	CompanyID string
	UserID    string
	Role      string
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Create inserts the company and adds the creator as its first member
// (role 'owner') in one transaction — same shape as
// communities.Repository.Create.
func (r *Repository) Create(ctx context.Context, name, billingEmail, ownerUserID string) (*Company, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	c := &Company{Name: name, BillingEmail: billingEmail}
	if err := tx.QueryRow(ctx, `
		INSERT INTO companies (name, billing_email) VALUES ($1, $2) RETURNING id`,
		name, billingEmail,
	).Scan(&c.ID); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO company_members (company_id, user_id, role) VALUES ($1, $2, 'owner')`,
		c.ID, ownerUserID,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// AddMember is idempotent — ON CONFLICT DO NOTHING, same pattern as
// communities.Repository.Join.
func (r *Repository) AddMember(ctx context.Context, companyID, userID string) (*Member, error) {
	m := &Member{CompanyID: companyID, UserID: userID, Role: "member"}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO company_members (company_id, user_id, role) VALUES ($1, $2, 'member')
		ON CONFLICT (company_id, user_id) DO NOTHING`,
		companyID, userID,
	)
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (r *Repository) ListMembers(ctx context.Context, companyID string, limit int) ([]*Member, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT company_id::text, user_id::text, role FROM company_members
		WHERE company_id = $1 ORDER BY joined_at LIMIT $2`,
		companyID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.CompanyID, &m.UserID, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

func (r *Repository) IsOwner(ctx context.Context, companyID, userID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM company_members WHERE company_id = $1 AND user_id = $2 AND role = 'owner')`,
		companyID, userID,
	).Scan(&exists)
	return exists, err
}

func (r *Repository) IsMember(ctx context.Context, companyID, userID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM company_members WHERE company_id = $1 AND user_id = $2)`,
		companyID, userID,
	).Scan(&exists)
	return exists, err
}

// AllAreMembers reports whether every userID is a member of companyID —
// CreateTeamBooking uses this to reject up front rather than letting an
// owner book an arbitrary, non-member user_id on the company's behalf.
func (r *Repository) AllAreMembers(ctx context.Context, companyID string, userIDs []string) (bool, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`SELECT COUNT(DISTINCT user_id) FROM company_members WHERE company_id = $1 AND user_id = ANY($2::uuid[])`,
		companyID, userIDs,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count == len(uniqueStrings(userIDs)), nil
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// TagBookingCompany attributes an already-created booking to a company for
// billing reconciliation — a single UPDATE run right after
// bookings.Service.CreateBookingForPlan returns, deliberately outside that
// call's own transaction since bookings.Service is treated as an opaque
// adapter (same convention plans.Service already follows for it).
func (r *Repository) TagBookingCompany(ctx context.Context, bookingID, companyID string) error {
	_, err := r.pool.Exec(ctx, `UPDATE bookings SET company_id = $2 WHERE id = $1`, bookingID, companyID)
	return err
}
