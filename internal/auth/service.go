package auth

import (
	"context"
	"errors"
	"time"

	"github.com/hivemind/backend/pkg/security"
)

var (
	ErrUnderage     = errors.New("auth: must be 18 or older")
	ErrInvalidInput = errors.New("auth: invalid input")
	ErrUserInactive = errors.New("auth: account is suspended or deleted")
)

const (
	minAgeYears           = 18
	refreshTokenTTL       = 30 * 24 * time.Hour
	passwordResetTokenTTL = time.Hour
)

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

func (s *Service) SignUp(ctx context.Context, email, password, deviceID, platform string, dob time.Time) (*Tokens, error) {
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
	return s.issueSession(ctx, userID, "user", deviceID, platform)
}

func (s *Service) SignIn(ctx context.Context, email, password, deviceID, platform string) (*Tokens, error) {
	if email == "" || password == "" {
		return nil, ErrInvalidInput
	}
	u, err := s.repo.GetUserByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	if u.Status != "active" {
		return nil, ErrUserInactive
	}
	if !security.VerifyPassword(u.PasswordHash, password) {
		return nil, ErrInvalidCredentials
	}
	return s.issueSession(ctx, u.ID, u.Role, deviceID, platform)
}

// RefreshToken rotates the refresh token: the old one is consumed
// (single-use) and a new access+refresh pair is issued, tied to the same
// device the original token was scoped to.
func (s *Service) RefreshToken(ctx context.Context, refreshToken string) (*Tokens, error) {
	if refreshToken == "" {
		return nil, ErrInvalidInput
	}
	row, err := s.repo.ConsumeRefreshToken(ctx, security.HashToken(refreshToken))
	if err != nil {
		return nil, err
	}
	u, err := s.repo.GetUserByID(ctx, row.UserID)
	if err != nil {
		return nil, err
	}
	if u.Status != "active" {
		return nil, ErrUserInactive
	}
	deviceID := ""
	if row.DeviceID != nil {
		deviceID = *row.DeviceID
	}
	return s.issueTokens(ctx, u.ID, u.Role, deviceID)
}

func (s *Service) SignOut(ctx context.Context, userID, deviceID string) error {
	if userID == "" {
		return ErrInvalidInput
	}
	return s.repo.RevokeDeviceTokens(ctx, userID, deviceID)
}

// RequestPasswordReset always looks like it succeeded even for an unknown
// email — PRD-standard practice to avoid leaking which emails are
// registered. The token is only logged (no real email sender) — same
// non-goal carried over from the scaffold.
func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	if email == "" {
		return ErrInvalidInput
	}
	u, err := s.repo.GetUserByEmail(ctx, email)
	if err != nil {
		if err == ErrInvalidCredentials {
			return nil // don't reveal whether the email exists
		}
		return err
	}

	token, err := security.GenerateOpaqueToken()
	if err != nil {
		return err
	}
	return s.repo.CreatePasswordResetToken(ctx, u.ID, security.HashToken(token), time.Now().Add(passwordResetTokenTTL))
}

func (s *Service) issueSession(ctx context.Context, userID, role, deviceID, platform string) (*Tokens, error) {
	deviceUUID, err := s.repo.UpsertDevice(ctx, userID, deviceID, platform)
	if err != nil {
		return nil, err
	}
	return s.issueTokens(ctx, userID, role, deviceUUID)
}

func (s *Service) issueTokens(ctx context.Context, userID, role, deviceUUID string) (*Tokens, error) {
	access, expiresAt, err := s.issuer.Issue(userID, role)
	if err != nil {
		return nil, err
	}

	refresh, err := security.GenerateOpaqueToken()
	if err != nil {
		return nil, err
	}
	if err := s.repo.StoreRefreshToken(ctx, userID, deviceUUID, security.HashToken(refresh), time.Now().Add(refreshTokenTTL)); err != nil {
		return nil, err
	}

	return &Tokens{UserID: userID, AccessToken: access, RefreshToken: refresh, ExpiresAt: expiresAt}, nil
}
