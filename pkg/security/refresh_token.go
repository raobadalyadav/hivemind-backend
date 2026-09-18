package security

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// GenerateOpaqueToken returns a high-entropy random token suitable for a
// refresh token — unlike the access token, it's not a JWT: it's looked up
// by its hash in the refresh_tokens table, which is what makes revocation
// possible (a JWT can't be revoked without a blocklist).
func GenerateOpaqueToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken returns a SHA-256 hex digest for storage/lookup. The token
// itself is already high-entropy random (not a low-entropy secret an
// attacker could brute-force offline), so a fast hash is appropriate here —
// unlike HashPassword, which deliberately uses slow bcrypt.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
