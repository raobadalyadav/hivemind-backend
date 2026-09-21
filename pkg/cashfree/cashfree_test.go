package cashfree

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestClient_VerifyWebhookSignature_Valid(t *testing.T) {
	secret := "test-secret-key"
	c := NewClient("id", secret, true)

	body := []byte(`{"type":"PAYMENT_SUCCESS_WEBHOOK","data":{}}`)
	timestamp := "1617695238078"

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write(body)
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	if !c.VerifyWebhookSignature(body, sig, timestamp) {
		t.Error("expected valid signature to verify")
	}
}

func TestClient_VerifyWebhookSignature_RejectsTamperedBody(t *testing.T) {
	secret := "test-secret-key"
	c := NewClient("id", secret, true)

	original := []byte(`{"type":"PAYMENT_SUCCESS_WEBHOOK","data":{}}`)
	timestamp := "1617695238078"

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write(original)
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	tampered := []byte(`{"type":"PAYMENT_SUCCESS_WEBHOOK","data":{"evil":true}}`)
	if c.VerifyWebhookSignature(tampered, sig, timestamp) {
		t.Error("expected tampered body to fail verification")
	}
}

func TestClient_VerifyWebhookSignature_RejectsWrongSecret(t *testing.T) {
	c := NewClient("id", "the-real-secret", true)

	body := []byte(`{"type":"PAYMENT_SUCCESS_WEBHOOK"}`)
	timestamp := "123"

	mac := hmac.New(sha256.New, []byte("attacker-guessed-secret"))
	mac.Write([]byte(timestamp))
	mac.Write(body)
	forgedSig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	if c.VerifyWebhookSignature(body, forgedSig, timestamp) {
		t.Error("expected signature forged with wrong secret to fail verification")
	}
}

func TestParseWebhookEvent(t *testing.T) {
	body := []byte(`{
		"type": "PAYMENT_SUCCESS_WEBHOOK",
		"data": {
			"order": {"order_id": "order-123"},
			"payment": {"cf_payment_id": "cfpay-456", "payment_status": "SUCCESS", "payment_amount": 599.00}
		}
	}`)

	event, err := ParseWebhookEvent(body)
	if err != nil {
		t.Fatalf("ParseWebhookEvent: %v", err)
	}
	if event.Type != EventPaymentSuccess {
		t.Errorf("expected type %q, got %q", EventPaymentSuccess, event.Type)
	}
	if event.OrderID != "order-123" {
		t.Errorf("expected order_id 'order-123', got %q", event.OrderID)
	}
	if event.CFPaymentID != "cfpay-456" {
		t.Errorf("expected cf_payment_id 'cfpay-456', got %q", event.CFPaymentID)
	}
	if event.AmountMinor != 59900 {
		t.Errorf("expected amount_minor 59900 (paise), got %d", event.AmountMinor)
	}
}

func TestParseWebhookEvent_RefundAndRounding(t *testing.T) {
	ev, err := ParseWebhookEvent([]byte(`{"type":"REFUND_STATUS_WEBHOOK","data":{"refund":{"refund_id":"r-1","refund_status":"SUCCESS"},"order":{"order_id":"o-1"}}}`))
	if err != nil || ev.Type != EventRefundStatus || ev.RefundID != "r-1" || ev.RefundStatus != "SUCCESS" {
		t.Fatalf("refund event: %+v %v", ev, err)
	}
	ev, _ = ParseWebhookEvent([]byte(`{"type":"PAYMENT_SUCCESS_WEBHOOK","data":{"order":{"order_id":"o"},"payment":{"cf_payment_id":"1","payment_amount":0.29}}}`))
	if ev.AmountMinor != 29 {
		t.Fatalf("0.29 rupees is 29 paise, got %d", ev.AmountMinor)
	}
}
