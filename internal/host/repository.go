// Package host implements PRD §13.11 Host Marketplace + §21 host payouts —
// every RPC is fully implemented. Host verification and payouts are one
// package: both operate on payout_accounts, and splitting them would be an
// artificial boundary.
package host

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrPayoutAccountNotFound = errors.New("host: payout account not found")
	ErrPayoutNotFound        = errors.New("host: payout not found")
)

type PayoutAccount struct {
	ID            string
	HostID        string
	Status        string
	IDDocumentURL string
}

type Payout struct {
	ID              string
	PayoutAccountID string
	AmountMinor     int64
	Status          string
}

type Dashboard struct {
	TotalBookings     int64
	TotalAttendees    int64
	GrossRevenueMinor int64
	AvgRating         float64
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

type bankDetails struct {
	AccountNumber string `json:"account_number"`
	IFSC          string `json:"ifsc"`
}

// ApplyForHost upserts the host's payout_accounts row — idempotent via the
// UNIQUE(host_id) constraint (migration 0023), status always stays
// 'pending' here; only admin.ApproveHost flips it to 'active'.
func (r *Repository) ApplyForHost(ctx context.Context, hostID, idDocumentURL, bankAccountNumber, bankIFSC string) (*PayoutAccount, error) {
	details, err := json.Marshal(bankDetails{AccountNumber: bankAccountNumber, IFSC: bankIFSC})
	if err != nil {
		return nil, err
	}
	var a PayoutAccount
	a.HostID = hostID
	err = r.pool.QueryRow(ctx, `
		INSERT INTO payout_accounts (host_id, id_document_url, bank_details, status)
		VALUES ($1, $2, $3, 'pending')
		ON CONFLICT (host_id) DO UPDATE SET id_document_url = $2, bank_details = $3
		RETURNING id, status, id_document_url`,
		hostID, idDocumentURL, details,
	).Scan(&a.ID, &a.Status, &a.IDDocumentURL)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *Repository) GetPayoutAccountByHostID(ctx context.Context, hostID string) (*PayoutAccount, error) {
	var a PayoutAccount
	a.HostID = hostID
	err := r.pool.QueryRow(ctx,
		`SELECT id, status, id_document_url FROM payout_accounts WHERE host_id = $1`, hostID,
	).Scan(&a.ID, &a.Status, &a.IDDocumentURL)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPayoutAccountNotFound
		}
		return nil, err
	}
	return &a, nil
}

// GetPayoutBalance computes the host's current payable balance live: net
// captured revenue (captured payments minus processed refunds) minus prior
// payouts. Two correlated subqueries, not a join+group-by — a join would
// fan out rows across refunds and double-count payments.amount_minor.
func (r *Repository) GetPayoutBalance(ctx context.Context, hostID string) (netCapturedMinor, priorPayoutsMinor int64, err error) {
	err = r.pool.QueryRow(ctx, `
		SELECT
			COALESCE((
				SELECT SUM(p.amount_minor) FROM payments p
				JOIN orders o ON o.id = p.order_id
				JOIN bookings b ON b.id = o.booking_id
				JOIN plans pl ON pl.id = b.plan_id
				WHERE pl.host_id = $1 AND p.status = 'captured'::payment_status
			), 0)
			-
			COALESCE((
				SELECT SUM(r.amount_minor) FROM refunds r
				JOIN payments p ON p.id = r.payment_id
				JOIN orders o ON o.id = p.order_id
				JOIN bookings b ON b.id = o.booking_id
				JOIN plans pl ON pl.id = b.plan_id
				WHERE pl.host_id = $1 AND r.status = 'processed'::refund_status
			), 0) AS net_captured,
			COALESCE((
				SELECT SUM(po.amount_minor) FROM payouts po
				JOIN payout_accounts pa ON pa.id = po.payout_account_id
				WHERE pa.host_id = $1 AND po.status IN ('pending'::payout_status, 'processed'::payout_status)
			), 0) AS prior_payouts`,
		hostID,
	).Scan(&netCapturedMinor, &priorPayoutsMinor)
	return netCapturedMinor, priorPayoutsMinor, err
}

func (r *Repository) CreatePayout(ctx context.Context, payoutAccountID string, amountMinor int64) (*Payout, error) {
	var p Payout
	p.PayoutAccountID = payoutAccountID
	p.AmountMinor = amountMinor
	p.Status = "pending"
	err := r.pool.QueryRow(ctx, `
		INSERT INTO payouts (payout_account_id, amount_minor, status)
		VALUES ($1, $2, 'pending')
		RETURNING id`,
		payoutAccountID, amountMinor,
	).Scan(&p.ID)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *Repository) ListPayouts(ctx context.Context, hostID string, limit int) ([]*Payout, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT po.id, po.payout_account_id, po.amount_minor, po.status
		FROM payouts po JOIN payout_accounts pa ON pa.id = po.payout_account_id
		WHERE pa.host_id = $1 ORDER BY po.created_at DESC LIMIT $2`,
		hostID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Payout
	for rows.Next() {
		var p Payout
		if err := rows.Scan(&p.ID, &p.PayoutAccountID, &p.AmountMinor, &p.Status); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// GetDashboard: same query shape as internal/venues' GetDashboard, keyed on
// pl.host_id instead of pl.venue_id.
func (r *Repository) GetDashboard(ctx context.Context, hostID string) (*Dashboard, error) {
	var d Dashboard
	err := r.pool.QueryRow(ctx, `
		SELECT
			COUNT(DISTINCT b.id) FILTER (WHERE b.status IN ('confirmed'::booking_status,'attended'::booking_status)),
			COUNT(DISTINCT b.user_id) FILTER (WHERE b.status = 'attended'::booking_status),
			COALESCE(SUM(p.amount_minor) FILTER (WHERE p.status = 'captured'::payment_status), 0),
			COALESCE((SELECT AVG(rv.rating) FROM reviews rv JOIN plans pl2 ON pl2.id = rv.plan_id WHERE pl2.host_id = $1), 0)
		FROM plans pl
		LEFT JOIN bookings b ON b.plan_id = pl.id
		LEFT JOIN orders o ON o.booking_id = b.id
		LEFT JOIN payments p ON p.order_id = o.id
		WHERE pl.host_id = $1`,
		hostID,
	).Scan(&d.TotalBookings, &d.TotalAttendees, &d.GrossRevenueMinor, &d.AvgRating)
	if err != nil {
		return nil, err
	}
	return &d, nil
}
