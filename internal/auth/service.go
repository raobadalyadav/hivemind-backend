package auth

import (
	"context"
	"errors"
	"time"

	"github.com/hivemind/backend/pkg/oauth"
	"github.com/hivemind/backend/pkg/security"
)

var (
	ErrUnderage            = errors.New("auth: must be 18 or older")
	ErrInvalidInput        = errors.New("auth: invalid input")
	ErrUserInactive        = errors.New("auth: account is suspended or deleted")
	ErrOAuthTokenInvalid   = errors.New("auth: id token failed verification")
	ErrProviderUnavailable = errors.New("auth: this sign-in provider is not configured")
)

const (
	minAgeYears           = 18
	refreshTokenTTL       = 30 * 24 * time.Hour
	recoveryCodeTTL       = 15 * time.Minute
	purposeVerifyEmail    = "verify_recovery_email"
	purposeAccountRecover = "account_recovery"
)

type Tokens struct {
	UserID       string
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

type Service struct {
	repo           *Repository
	issuer         *security.TokenIssuer
	googleVerifier *oauth.Verifier
	appleVerifier  *oauth.Verifier
}

func NewService(repo *Repository, issuer *security.TokenIssuer, googleVerifier, appleVerifier *oauth.Verifier) *Service {
	return &Service{repo: repo, issuer: issuer, googleVerifier: googleVerifier, appleVerifier: appleVerifier}
}

func (s *Service) SignInWithGoogle(ctx context.Context, idToken, deviceID, platform string, dob time.Time) (*Tokens, error) {
	if s.googleVerifier == nil {
		return nil, ErrProviderUnavailable
	}
	return s.signInWithProvider(ctx, s.googleVerifier, "google", idToken, deviceID, platform, dob)
}

func (s *Service) SignInWithApple(ctx context.Context, idToken, deviceID, platform string, dob time.Time) (*Tokens, error) {
	if s.appleVerifier == nil {
		return nil, ErrProviderUnavailable
	}
	return s.signInWithProvider(ctx, s.appleVerifier, "apple", idToken, deviceID, platform, dob)
}

// signInWithProvider verifies the ID token, finds-or-creates the account by
// (provider, provider_user_id), and issues a session. The 18+ age gate only
// runs on first creation — a client only needs to send date_of_birth on the
// very first sign-in for a new account.
func (s *Service) signInWithProvider(ctx context.Context, verifier *oauth.Verifier, providerName, idToken, deviceID, platform string, dob time.Time) (*Tokens, error) {
	if idToken == "" {
		return nil, ErrInvalidInput
	}
	providerUserID, email, err := verifier.Verify(idToken)
	if err != nil {
		return nil, ErrOAuthTokenInvalid
	}

	u, err := s.repo.FindByProviderIdentity(ctx, providerName, providerUserID)
	switch {
	case err == nil:
		// existing account, nothing to create
	case errors.Is(err, ErrUserNotFound):
		if !dob.IsZero() && time.Since(dob).Hours()/24/365.25 < minAgeYears {
			return nil, ErrUnderage
		}
		userID, createErr := s.repo.CreateUserFromOAuth(ctx, email, !dob.IsZero(), providerName, providerUserID, email)
		if createErr != nil {
			return nil, createErr
		}
		u = &userRow{ID: userID, Role: "user", Status: "active"}
	default:
		return nil, err
	}

	if u.Status != "active" {
		return nil, ErrUserInactive
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

// AddRecoveryEmail claims the email as this account's (unverified) recovery
// contact and generates a verification code. TODO(phase1+): wire a real
// email sender — the code is only persisted here, not transmitted, same
// non-goal carried over from earlier passes.
func (s *Service) AddRecoveryEmail(ctx context.Context, userID, email string) error {
	if userID == "" || email == "" {
		return ErrInvalidInput
	}
	if err := s.repo.SetPendingRecoveryEmail(ctx, userID, email); err != nil {
		return err
	}
	return s.sendRecoveryCode(ctx, userID, purposeVerifyEmail)
}

func (s *Service) VerifyRecoveryEmail(ctx context.Context, userID, code string) error {
	if userID == "" || code == "" {
		return ErrInvalidInput
	}
	ownerID, err := s.repo.ConsumeRecoveryCode(ctx, security.HashToken(code), purposeVerifyEmail)
	if err != nil {
		return err
	}
	if ownerID != userID {
		return ErrRecoveryCodeInvalid
	}
	return s.repo.MarkRecoveryEmailVerified(ctx, userID)
}

// RequestAccountRecovery always looks like it succeeded even for an unknown
// email — avoids leaking which emails are registered, same practice as the
// scaffold's earlier password-reset flow.
func (s *Service) RequestAccountRecovery(ctx context.Context, recoveryEmail string) error {
	if recoveryEmail == "" {
		return ErrInvalidInput
	}
	u, err := s.repo.FindByVerifiedRecoveryEmail(ctx, recoveryEmail)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil
		}
		return err
	}
	return s.sendRecoveryCode(ctx, u.ID, purposeAccountRecover)
}

// RecoverAccount is the actual "I lost my Google/Apple account" path: the
// code (only ever generated for the verified recovery email's owner)
// authorizes linking a fresh OAuth identity to the existing account and
// issuing a new session. providerName is "google" or "apple" — the service
// picks its own configured verifier rather than making the gRPC handler
// resolve one, since only the service holds them.
func (s *Service) RecoverAccount(ctx context.Context, code, providerName, idToken, deviceID, platform string) (*Tokens, error) {
	if code == "" || idToken == "" {
		return nil, ErrInvalidInput
	}
	var verifier *oauth.Verifier
	switch providerName {
	case "google":
		verifier = s.googleVerifier
	case "apple":
		verifier = s.appleVerifier
	default:
		return nil, ErrInvalidInput
	}
	if verifier == nil {
		return nil, ErrProviderUnavailable
	}
	userID, err := s.repo.ConsumeRecoveryCode(ctx, security.HashToken(code), purposeAccountRecover)
	if err != nil {
		return nil, err
	}

	providerUserID, email, err := verifier.Verify(idToken)
	if err != nil {
		return nil, ErrOAuthTokenInvalid
	}
	if err := s.repo.LinkOAuthIdentity(ctx, userID, providerName, providerUserID, email); err != nil {
		return nil, err
	}

	u, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.issueSession(ctx, u.ID, u.Role, deviceID, platform)
}

func (s *Service) sendRecoveryCode(ctx context.Context, userID, purpose string) error {
	code, err := security.GenerateOpaqueToken()
	if err != nil {
		return err
	}
	return s.repo.CreateRecoveryCode(ctx, userID, security.HashToken(code), purpose, time.Now().Add(recoveryCodeTTL))
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
