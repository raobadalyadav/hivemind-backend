package payments

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
)

// Seller is who issues receipts and the GST rate on the platform fee.
type Seller struct {
	Name, GSTIN, Address string
	FeeGSTPercent        int
}

// WithSeller sets who receipts are issued by.
func (s *Service) WithSeller(x Seller) *Service {
	s.seller = x
	return s
}

// Receipt is the record of one captured payment: what the host charged, the platform's service fee with its GST
// split out, and what was actually charged to the payer (credits and coupons aren't part of it).
type Receipt struct {
	Number                                          string
	IssuedAt                                        time.Time
	PlanTitle, BuyerEmail                           string
	PriceMinor, FeeMinor, FeeBaseMinor, FeeGSTMinor int64
	PaidMinor                                       int64
	Currency                                        string
	GSTPercent                                      float64
	SellerName, SellerGSTIN, SellerAddress          string
}

// financialYear is the Indian FY label ("2026-27") for a date: April to March.
func financialYear(t time.Time) string {
	y := t.Year()
	if t.Month() < time.April {
		y--
	}
	return fmt.Sprintf("%d-%02d", y, (y+1)%100)
}

// splitGST separates a GST-inclusive amount into its base and tax (rounded to the paisa; base + tax = amount).
func splitGST(inclusiveMinor int64, percent int) (base, gst int64) {
	if percent <= 0 || inclusiveMinor <= 0 {
		return inclusiveMinor, 0
	}
	base = int64(math.Round(float64(inclusiveMinor) * 100 / float64(100+percent)))
	return base, inclusiveMinor - base
}

// GetReceipt returns the receipt for a booking the caller paid for, issuing it (with the next number) on first
// request. Bookings paid entirely with credits have no payment and so no receipt.
func (s *Service) GetReceipt(ctx context.Context, bookingID, callerID string) (*Receipt, error) {
	if bookingID == "" || callerID == "" {
		return nil, ErrInvalidInput
	}
	ownerID, totalMinor, currency, _, err := s.bookings.GetBookingCharge(ctx, bookingID)
	if err != nil {
		return nil, err
	}
	if ownerID != callerID {
		return nil, ErrPaymentNotFound // don't reveal other people's bookings
	}
	payment, err := s.repo.FindCapturedPaymentForBooking(ctx, bookingID)
	if err != nil {
		return nil, err
	}
	var price int64
	var title, email string
	if err := s.repo.pool.QueryRow(ctx, `
		SELECT b.price_minor, p.title, u.email FROM bookings b JOIN plans p ON p.id = b.plan_id JOIN users u ON u.id = b.user_id
		WHERE b.id = $1`, bookingID).Scan(&price, &title, &email); err != nil {
		return nil, err
	}
	fee := totalMinor - price
	base, gst := splitGST(fee, s.seller.FeeGSTPercent)

	now := time.Now()
	r := &Receipt{PlanTitle: title, BuyerEmail: email, PriceMinor: price, FeeMinor: fee, FeeBaseMinor: base, FeeGSTMinor: gst,
		PaidMinor: payment.AmountMinor, Currency: currency, GSTPercent: float64(s.seller.FeeGSTPercent),
		SellerName: s.seller.Name, SellerGSTIN: s.seller.GSTIN, SellerAddress: s.seller.Address}
	err = s.repo.pool.QueryRow(ctx, `
		INSERT INTO receipts (payment_id, number, issued_at, price_minor, fee_minor, fee_base_minor, fee_gst_minor, gst_percent, paid_minor)
		VALUES ($1, 'HM/' || $2::text || '/' || lpad(nextval('receipt_number_seq')::text, 6, '0'), $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (payment_id) DO NOTHING
		RETURNING number, issued_at`,
		payment.ID, financialYear(now), now, price, fee, base, gst, s.seller.FeeGSTPercent, payment.AmountMinor).Scan(&r.Number, &r.IssuedAt)
	if errors.Is(err, pgx.ErrNoRows) { // already issued: return what was recorded then, unchanged
		err = s.repo.pool.QueryRow(ctx, `SELECT number, issued_at, price_minor, fee_minor, fee_base_minor, fee_gst_minor, gst_percent::float8, paid_minor FROM receipts WHERE payment_id = $1`, payment.ID).
			Scan(&r.Number, &r.IssuedAt, &r.PriceMinor, &r.FeeMinor, &r.FeeBaseMinor, &r.FeeGSTMinor, &r.GSTPercent, &r.PaidMinor)
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}
