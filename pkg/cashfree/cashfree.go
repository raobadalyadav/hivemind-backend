// Package cashfree is a thin client for Cashfree's Payment Gateway Orders
// API (https://www.cashfree.com/docs/api-reference/payments/latest/orders/create-order)
// — hand-rolled over net/http rather than a vendored SDK, since the surface
// used here (create order, verify webhook signature) is two REST calls and
// one HMAC check, not worth a dependency.
package cashfree

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const apiVersion = "2023-08-01"

type Client struct {
	httpClient   *http.Client
	baseURL      string
	clientID     string
	clientSecret string
}

// NewClient — sandbox selects Cashfree's sandbox host; production apps set
// it false once real (not test-mode) credentials are configured.
func NewClient(clientID, clientSecret string, sandbox bool) *Client {
	base := "https://api.cashfree.com/pg"
	if sandbox {
		base = "https://sandbox.cashfree.com/pg"
	}
	return &Client{httpClient: &http.Client{Timeout: 20 * time.Second}, baseURL: base, clientID: clientID, clientSecret: clientSecret}
}

type customerDetails struct {
	CustomerID    string `json:"customer_id"`
	CustomerPhone string `json:"customer_phone"`
	CustomerEmail string `json:"customer_email,omitempty"`
}

type createOrderRequest struct {
	OrderID         string          `json:"order_id"`
	OrderAmount     float64         `json:"order_amount"`
	OrderCurrency   string          `json:"order_currency"`
	CustomerDetails customerDetails `json:"customer_details"`
}

type createOrderResponse struct {
	CFOrderID        string `json:"cf_order_id"`
	OrderID          string `json:"order_id"`
	PaymentSessionID string `json:"payment_session_id"`
	OrderStatus      string `json:"order_status"`
	Message          string `json:"message"`
}

// CreateOrder — orderID is our own orders.id (Cashfree's order_id field,
// distinct from the cf_order_id it assigns); amountMinor is paise, Cashfree
// wants a decimal rupee amount. Returns Cashfree's cf_order_id (stored as
// orders.gateway_order_id) and the payment_session_id the mobile client's
// Checkout SDK needs — that one is short-lived and never persisted.
func (c *Client) CreateOrder(ctx context.Context, orderID string, amountMinor int64, currency, customerID, customerPhone, customerEmail string) (cfOrderID, paymentSessionID string, err error) {
	body := createOrderRequest{
		OrderID:       orderID,
		OrderAmount:   float64(amountMinor) / 100.0,
		OrderCurrency: currency,
		CustomerDetails: customerDetails{
			CustomerID:    customerID,
			CustomerPhone: customerPhone,
			CustomerEmail: customerEmail,
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/orders", bytes.NewReader(payload))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-version", apiVersion)
	req.Header.Set("x-client-id", c.clientID)
	req.Header.Set("x-client-secret", c.clientSecret)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	var out createOrderResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", err
	}
	if resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("cashfree: create order failed (%d): %s", resp.StatusCode, out.Message)
	}
	return out.CFOrderID, out.PaymentSessionID, nil
}

// VerifyWebhookSignature per Cashfree's documented scheme:
// signedPayload = timestamp + rawBody; expected = base64(HMAC-SHA256(signedPayload, secret)).
func (c *Client) VerifyWebhookSignature(rawBody []byte, signature, timestamp string) bool {
	mac := hmac.New(sha256.New, []byte(c.clientSecret))
	mac.Write([]byte(timestamp))
	mac.Write(rawBody)
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

// do sends an authenticated JSON request and decodes the JSON answer into out (which may be nil).
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body *bytes.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	} else {
		body = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-version", apiVersion)
	req.Header.Set("x-client-id", c.clientID)
	req.Header.Set("x-client-secret", c.clientSecret)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw := new(bytes.Buffer)
	if _, err := raw.ReadFrom(http.MaxBytesReader(nil, resp.Body, 1<<20)); err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw.Bytes(), &e)
		return fmt.Errorf("cashfree: %s %s failed (%d): %s", method, path, resp.StatusCode, e.Message)
	}
	if out != nil {
		return json.Unmarshal(raw.Bytes(), out)
	}
	return nil
}

// Refund asks Cashfree to return amountMinor of an order's payment. refundID is OUR id for this attempt (Cashfree
// dedupes on it, so a retry of the same attempt never refunds twice); orderID is our orders.id, which is what
// we sent as Cashfree's order_id. Returns Cashfree's cf_refund_id.
func (c *Client) Refund(ctx context.Context, orderID, refundID string, amountMinor int64, note string) (cfRefundID string, err error) {
	var out struct {
		CFRefundID   string `json:"cf_refund_id"`
		RefundStatus string `json:"refund_status"`
	}
	err = c.do(ctx, http.MethodPost, "/orders/"+url.PathEscape(orderID)+"/refunds", map[string]any{
		"refund_amount": float64(amountMinor) / 100.0,
		"refund_id":     refundID,
		"refund_note":   note,
		"refund_speed":  "STANDARD",
	}, &out)
	if err != nil {
		return "", err
	}
	if out.RefundStatus == "CANCELLED" {
		return "", fmt.Errorf("cashfree: refund %s was cancelled", refundID)
	}
	return out.CFRefundID, nil
}

// GatewayPayment is one payment attempt on an order, as Cashfree reports it.
type GatewayPayment struct {
	CFPaymentID string  `json:"cf_payment_id"`
	Status      string  `json:"payment_status"` // SUCCESS | FAILED | PENDING | NOT_ATTEMPTED | USER_DROPPED | VOID | CANCELLED
	Amount      float64 `json:"payment_amount"`
}

// OrderPayments lists the payment attempts on our order id — the server-side truth about whether it was paid.
func (c *Client) OrderPayments(ctx context.Context, orderID string) ([]GatewayPayment, error) {
	var out []GatewayPayment
	if err := c.do(ctx, http.MethodGet, "/orders/"+url.PathEscape(orderID)+"/payments", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}
