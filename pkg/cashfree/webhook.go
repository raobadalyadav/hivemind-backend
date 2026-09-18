package cashfree

import "encoding/json"

const EventPaymentSuccess = "PAYMENT_SUCCESS_WEBHOOK"

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
	} `json:"data"`
}

// WebhookEvent is the subset of a Cashfree webhook payload internal/payments
// actually needs — deliberately not the raw Cashfree struct, so that
// package doesn't need to know Cashfree's JSON shape.
type WebhookEvent struct {
	Type        string
	OrderID     string
	CFPaymentID string
	AmountMinor int64
}

// ParseWebhookEvent does not verify the signature — call
// Client.VerifyWebhookSignature on the same rawBody first.
func ParseWebhookEvent(rawBody []byte) (*WebhookEvent, error) {
	var p webhookPayload
	if err := json.Unmarshal(rawBody, &p); err != nil {
		return nil, err
	}
	return &WebhookEvent{
		Type:        p.Type,
		OrderID:     p.Data.Order.OrderID,
		CFPaymentID: p.Data.Payment.CFPaymentID,
		AmountMinor: int64(p.Data.Payment.PaymentAmount * 100),
	}, nil
}
