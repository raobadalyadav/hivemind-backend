// Package oauth verifies Google and Apple Sign-In ID tokens. Both providers
// issue standard OIDC ID tokens (JWTs signed by the provider's rotating
// JWKS), so one generic verifier serves both instead of pulling in Google's
// full API SDK just to validate a token.
package oauth

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

var ErrInvalidToken = errors.New("oauth: invalid id token")

type claims struct {
	Email string `json:"email"`
	jwt.RegisteredClaims
}

type Verifier struct {
	validIssuers []string
	audience     string
	kf           keyfunc.Keyfunc
}

func newVerifier(ctx context.Context, issuers []string, audience, jwksURL string) (*Verifier, error) {
	kf, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("oauth: fetch jwks from %s: %w", jwksURL, err)
	}
	return &Verifier{validIssuers: issuers, audience: audience, kf: kf}, nil
}

// NewGoogleVerifier's clientID is the OAuth client ID Google issues the app
// — it's the token's "aud" claim. Google ID tokens use either issuer form
// depending on token generation, so both are accepted.
func NewGoogleVerifier(ctx context.Context, clientID string) (*Verifier, error) {
	return newVerifier(ctx,
		[]string{"accounts.google.com", "https://accounts.google.com"},
		clientID,
		"https://www.googleapis.com/oauth2/v3/certs",
	)
}

// NewAppleVerifier's bundleID is the app's bundle identifier — Apple's
// token "aud" claim.
func NewAppleVerifier(ctx context.Context, bundleID string) (*Verifier, error) {
	return newVerifier(ctx,
		[]string{"https://appleid.apple.com"},
		bundleID,
		"https://appleid.apple.com/auth/keys",
	)
}

// Verify validates the ID token's signature (against the provider's live,
// cached JWKS), issuer, audience, and expiry, returning the provider's
// stable user id (the "sub" claim) and the account's email.
func (v *Verifier) Verify(idToken string) (providerUserID, email string, err error) {
	var c claims
	token, err := jwt.ParseWithClaims(idToken, &c, v.kf.Keyfunc, jwt.WithAudience(v.audience))
	if err != nil || !token.Valid {
		return "", "", ErrInvalidToken
	}
	if !slices.Contains(v.validIssuers, c.Issuer) {
		return "", "", ErrInvalidToken
	}
	if c.Subject == "" {
		return "", "", ErrInvalidToken
	}
	return c.Subject, c.Email, nil
}
