package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidPass = errors.New("security: invalid pass")
	ErrExpiredPass = errors.New("security: pass expired")
)

// SignPass returns a stateless digital-pass payload (flow.md §18) the client
// renders as a QR code: "v1.<bookingID>.<expUnix>.<b64url(HMAC-SHA256)>".
// No DB column is needed — the signature proves the server issued it, and
// ScanPass still re-checks the booking, host and time window server-side.
func SignPass(secret []byte, bookingID string, exp time.Time) string {
	body := "v1." + bookingID + "." + strconv.FormatInt(exp.Unix(), 10)
	return body + "." + passMAC(secret, body)
}

// VerifyPass checks the signature (constant-time) and expiry, and returns
// the booking id the pass was issued for.
func VerifyPass(secret []byte, payload string, now time.Time) (string, error) {
	i := strings.LastIndex(payload, ".")
	if i < 0 {
		return "", ErrInvalidPass
	}
	body, sig := payload[:i], payload[i+1:]
	if !hmac.Equal([]byte(passMAC(secret, body)), []byte(sig)) {
		return "", ErrInvalidPass
	}
	parts := strings.Split(body, ".")
	if len(parts) != 3 || parts[0] != "v1" || parts[1] == "" {
		return "", ErrInvalidPass
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return "", ErrInvalidPass
	}
	if now.Unix() > exp {
		return "", ErrExpiredPass
	}
	return parts[1], nil
}

func passMAC(secret []byte, body string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
