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
	return &Client{httpClient: &http.Client{}, baseURL: base, clientID: clientID, clientSecret: clientSecret}
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
