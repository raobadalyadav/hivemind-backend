package auth

import (
	"context"
	"errors"
	"time"

	"github.com/hivemind/backend/pkg/security"
)

var ErrUnderage = errors.New("auth: must be 18 or older")
var ErrInvalidInput = errors.New("auth: invalid input")

const minAgeYears = 18

type Tokens struct {
	UserID       string
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

type Service struct {
	repo   *Repository
	issuer *security.TokenIssuer
}

func NewService(repo *Repository, issuer *security.TokenIssuer) *Service {
	return &Service{repo: repo, issuer: issuer}
}

func (s *Service) SignUp(ctx context.Context, email, password string, dob time.Time) (*Tokens, error) {
	if email == "" || password == "" {
		return nil, ErrInvalidInput
	}
	if !dob.IsZero() && time.Since(dob).Hours()/24/365.25 < minAgeYears {
		return nil, ErrUnderage
	}

	hash, err := security.HashPassword(password)
	if err != nil {
		return nil, err
	}

	userID, err := s.repo.CreateUser(ctx, email, hash, !dob.IsZero())
	if err != nil {
		return nil, err
	}
	return s.issueTokens(userID)
}

func (s *Service) SignIn(ctx context.Context, email, password string) (*Tokens, error) {
	if email == "" || password == "" {
		return nil, ErrInvalidInput
	}
	u, err := s.repo.GetUserByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	if !security.VerifyPassword(u.PasswordHash, password) {
		return nil, ErrInvalidCredentials
	}
	return s.issueTokens(u.ID)
}

func (s *Service) issueTokens(userID string) (*Tokens, error) {
	access, expiresAt, err := s.issuer.Issue(userID)
	if err != nil {
		return nil, err
	}
	// Refresh tokens are a separate long-lived credential in a real system
	// (rotated, revocable). TODO(phase1): back this with a refresh_tokens
	// table instead of reusing the access token issuer.
	refresh, _, err := s.issuer.Issue(userID)
	if err != nil {
		return nil, err
	}
	return &Tokens{UserID: userID, AccessToken: access, RefreshToken: refresh, ExpiresAt: expiresAt}, nil
}
