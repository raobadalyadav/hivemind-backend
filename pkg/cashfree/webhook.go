package cashfree

import (
	"encoding/json"
	"math"
)

const (
	EventPaymentSuccess = "PAYMENT_SUCCESS_WEBHOOK"
	EventPaymentFailed  = "PAYMENT_FAILED_WEBHOOK"
	EventRefundStatus   = "REFUND_STATUS_WEBHOOK"
)

type webhookPayload struct {
	Type string `json:"type"`
	Data struct {
		Order struct {
			OrderID string `json:"order_id"`
		} `json:"order"`
		Payment struct {
			CFPaymentID   string  `json:"cf_payment_id"`
			PaymentStatus string  `json:"payment_status"`
			PaymentAmount float64 `json:"payment_amount"`
		} `json:"payment"`
		Refund struct {
			RefundID     string `json:"refund_id"`
			RefundStatus string `json:"refund_status"`
		} `json:"refund"`
	} `json:"data"`
}

// WebhookEvent is the subset of a Cashfree webhook payload internal/payments
// actually needs — deliberately not the raw Cashfree struct, so that
// package doesn't need to know Cashfree's JSON shape.
type WebhookEvent struct {
	Type         string
	OrderID      string
	CFPaymentID  string
	AmountMinor  int64
	RefundID     string // our refunds.id (only for refund events)
	RefundStatus string // SUCCESS | FAILED | CANCELLED | PENDING
}

// ParseWebhookEvent does not verify the signature — call
// Client.VerifyWebhookSignature on the same rawBody first.
func ParseWebhookEvent(rawBody []byte) (*WebhookEvent, error) {
	var p webhookPayload
	if err := json.Unmarshal(rawBody, &p); err != nil {
		return nil, err
	}
	return &WebhookEvent{
		Type:         p.Type,
		OrderID:      p.Data.Order.OrderID,
		CFPaymentID:  p.Data.Payment.CFPaymentID,
		AmountMinor:  int64(math.Round(p.Data.Payment.PaymentAmount * 100)), // rupees→paise: round, don't truncate (0.29×100 = 28.999…)
		RefundID:     p.Data.Refund.RefundID,
		RefundStatus: p.Data.Refund.RefundStatus,
	}, nil
}
