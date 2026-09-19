package security

import (
	"testing"
	"time"
)

func TestPass_RoundTripTamperExpiryWrongSecret(t *testing.T) {
	secret := []byte("s3cret")
	now := time.Now()
	p := SignPass(secret, "booking-1", now.Add(time.Hour))

	if id, err := VerifyPass(secret, p, now); err != nil || id != "booking-1" {
		t.Fatalf("valid pass: id=%q err=%v", id, err)
	}
	if _, err := VerifyPass(secret, "v1.booking-2"+p[len("v1.booking-1"):], now); err != ErrInvalidPass {
		t.Errorf("tampered booking id must be rejected, got %v", err)
	}
	if _, err := VerifyPass([]byte("other"), p, now); err != ErrInvalidPass {
		t.Errorf("wrong secret must be rejected, got %v", err)
	}
	if _, err := VerifyPass(secret, p, now.Add(2*time.Hour)); err != ErrExpiredPass {
		t.Errorf("expired pass must be rejected, got %v", err)
	}
	if _, err := VerifyPass(secret, "garbage", now); err != ErrInvalidPass {
		t.Errorf("garbage must be rejected, got %v", err)
	}
}
