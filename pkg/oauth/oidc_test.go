package oauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// jwkRSA is the minimal JWK fields keyfunc/golang-jwt need to verify an
// RS256 signature — hand-built here instead of pulling in jwkset's
// marshaling helpers, since a mock JWKS endpoint only needs to be valid,
// not produced by the same library that will later parse it.
type jwkRSA struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func b64url(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// startMockJWKS serves the given RSA public key as a JWKS document and
// returns the server URL — a stand-in for Google/Apple's real JWKS endpoint.
func startMockJWKS(t *testing.T, kid string, pub *rsa.PublicKey) string {
	t.Helper()
	eBytes := []byte{1, 0, 1} // 65537 as 3 bytes, the standard RSA public exponent
	jwk := jwkRSA{
		Kty: "RSA",
		Kid: kid,
		Use: "sig",
		Alg: "RS256",
		N:   b64url(pub.N.Bytes()),
		E:   b64url(eBytes),
	}
	body, err := json.Marshal(map[string]any{"keys": []jwkRSA{jwk}})
	if err != nil {
		t.Fatalf("marshal jwks: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func signTestToken(t *testing.T, key *rsa.PrivateKey, kid, issuer, audience, subject, email string, expiresIn time.Duration) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims{
		Email: email,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audience},
			Subject:   subject,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(expiresIn)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	})
	token.Header["kid"] = kid
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("sign test token: %v", err)
	}
	return signed
}

func TestVerifier_Verify_ValidToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	jwksURL := startMockJWKS(t, "test-kid", &key.PublicKey)

	ctx := context.Background()
	v, err := newVerifier(ctx, []string{"https://accounts.google.com"}, "test-client-id", jwksURL)
	if err != nil {
		t.Fatalf("newVerifier: %v", err)
	}

	tok := signTestToken(t, key, "test-kid", "https://accounts.google.com", "test-client-id", "user-sub-123", "user@example.com", time.Hour)

	sub, email, err := v.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if sub != "user-sub-123" {
		t.Errorf("expected sub 'user-sub-123', got %q", sub)
	}
	if email != "user@example.com" {
		t.Errorf("expected email 'user@example.com', got %q", email)
	}
}

func TestVerifier_Verify_RejectsWrongAudience(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	jwksURL := startMockJWKS(t, "test-kid", &key.PublicKey)

	ctx := context.Background()
	v, err := newVerifier(ctx, []string{"https://accounts.google.com"}, "expected-client-id", jwksURL)
	if err != nil {
		t.Fatalf("newVerifier: %v", err)
	}

	tok := signTestToken(t, key, "test-kid", "https://accounts.google.com", "wrong-client-id", "user-sub-123", "user@example.com", time.Hour)

	if _, _, err := v.Verify(tok); err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken for wrong audience, got %v", err)
	}
}

func TestVerifier_Verify_RejectsWrongIssuer(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	jwksURL := startMockJWKS(t, "test-kid", &key.PublicKey)

	ctx := context.Background()
	v, err := newVerifier(ctx, []string{"https://accounts.google.com"}, "test-client-id", jwksURL)
	if err != nil {
		t.Fatalf("newVerifier: %v", err)
	}

	tok := signTestToken(t, key, "test-kid", "https://evil.example.com", "test-client-id", "user-sub-123", "user@example.com", time.Hour)

	if _, _, err := v.Verify(tok); err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken for wrong issuer, got %v", err)
	}
}

func TestVerifier_Verify_RejectsExpiredToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	jwksURL := startMockJWKS(t, "test-kid", &key.PublicKey)

	ctx := context.Background()
	v, err := newVerifier(ctx, []string{"https://accounts.google.com"}, "test-client-id", jwksURL)
	if err != nil {
		t.Fatalf("newVerifier: %v", err)
	}

	tok := signTestToken(t, key, "test-kid", "https://accounts.google.com", "test-client-id", "user-sub-123", "user@example.com", -time.Hour)

	if _, _, err := v.Verify(tok); err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken for expired token, got %v", err)
	}
}

func TestVerifier_Verify_RejectsTokenSignedByDifferentKey(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	attackerKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate attacker key: %v", err)
	}
	// JWKS only publishes the legitimate key.
	jwksURL := startMockJWKS(t, "test-kid", &key.PublicKey)

	ctx := context.Background()
	v, err := newVerifier(ctx, []string{"https://accounts.google.com"}, "test-client-id", jwksURL)
	if err != nil {
		t.Fatalf("newVerifier: %v", err)
	}

	// Token forged with a different private key but claiming the same kid.
	tok := signTestToken(t, attackerKey, "test-kid", "https://accounts.google.com", "test-client-id", "user-sub-123", "user@example.com", time.Hour)

	if _, _, err := v.Verify(tok); err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken for forged signature, got %v", err)
	}
}
