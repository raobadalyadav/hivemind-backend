package auth

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

// Handler implements socialv1.AuthServiceServer — every RPC is fully
// implemented (see PRD §13.1 and service.go for the OAuth + recovery design).
type Handler struct {
	socialv1.UnimplementedAuthServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) SignInWithGoogle(ctx context.Context, req *socialv1.OAuthSignInRequest) (*socialv1.AuthTokens, error) {
	return h.signIn(ctx, h.svc.SignInWithGoogle, req)
}

func (h *Handler) SignInWithApple(ctx context.Context, req *socialv1.OAuthSignInRequest) (*socialv1.AuthTokens, error) {
	return h.signIn(ctx, h.svc.SignInWithApple, req)
}

func (h *Handler) signIn(ctx context.Context, fn func(context.Context, string, string, string, time.Time) (*Tokens, error), req *socialv1.OAuthSignInRequest) (*socialv1.AuthTokens, error) {
	var dob time.Time
	if req.GetDateOfBirth() != "" {
		parsed, err := time.Parse("2006-01-02", req.GetDateOfBirth())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid date_of_birth, expected YYYY-MM-DD")
		}
		dob = parsed
	}

	tokens, err := fn(ctx, req.GetIdToken(), req.GetDeviceId(), "", dob)
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrUnderage:
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		case ErrOAuthTokenInvalid:
			return nil, status.Error(codes.Unauthenticated, err.Error())
		case ErrProviderUnavailable:
			return nil, status.Error(codes.Unimplemented, err.Error())
		case ErrUserInactive:
			return nil, status.Error(codes.PermissionDenied, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to sign in")
		}
	}
	return toProto(tokens), nil
}

func (h *Handler) RefreshToken(ctx context.Context, req *socialv1.RefreshTokenRequest) (*socialv1.AuthTokens, error) {
	tokens, err := h.svc.RefreshToken(ctx, req.GetRefreshToken())
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Unauthenticated, "invalid or expired refresh token")
	}
	return toProto(tokens), nil
}

func (h *Handler) SignOut(ctx context.Context, req *socialv1.SignOutRequest) (*socialv1.SignOutResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	if err := h.svc.SignOut(ctx, userID, req.GetDeviceId()); err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to sign out")
	}
	return &socialv1.SignOutResponse{}, nil
}

func (h *Handler) AddRecoveryEmail(ctx context.Context, req *socialv1.AddRecoveryEmailRequest) (*socialv1.AddRecoveryEmailResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	if err := h.svc.AddRecoveryEmail(ctx, userID, req.GetEmail()); err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrEmailAlreadyUsed:
			return nil, status.Error(codes.AlreadyExists, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to add recovery email")
		}
	}
	return &socialv1.AddRecoveryEmailResponse{}, nil
}

func (h *Handler) VerifyRecoveryEmail(ctx context.Context, req *socialv1.VerifyRecoveryEmailRequest) (*socialv1.VerifyRecoveryEmailResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	if err := h.svc.VerifyRecoveryEmail(ctx, userID, req.GetCode()); err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrRecoveryCodeInvalid:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to verify recovery email")
		}
	}
	return &socialv1.VerifyRecoveryEmailResponse{}, nil
}

func (h *Handler) RequestAccountRecovery(ctx context.Context, req *socialv1.RequestAccountRecoveryRequest) (*socialv1.RequestAccountRecoveryResponse, error) {
	if err := h.svc.RequestAccountRecovery(ctx, req.GetRecoveryEmail()); err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to request account recovery")
	}
	return &socialv1.RequestAccountRecoveryResponse{}, nil
}

func (h *Handler) RecoverAccount(ctx context.Context, req *socialv1.RecoverAccountRequest) (*socialv1.AuthTokens, error) {
	var providerName string
	switch req.GetProvider() {
	case socialv1.OAuthProvider_OAUTH_PROVIDER_GOOGLE:
		providerName = "google"
	case socialv1.OAuthProvider_OAUTH_PROVIDER_APPLE:
		providerName = "apple"
	default:
		return nil, status.Error(codes.InvalidArgument, "provider must be specified")
	}

	tokens, err := h.svc.RecoverAccount(ctx, req.GetCode(), providerName, req.GetIdToken(), req.GetDeviceId(), "")
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrRecoveryCodeInvalid:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrOAuthTokenInvalid:
			return nil, status.Error(codes.Unauthenticated, err.Error())
		case ErrProviderUnavailable:
			return nil, status.Error(codes.Unimplemented, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to recover account")
		}
	}
	return toProto(tokens), nil
}

func toProto(t *Tokens) *socialv1.AuthTokens {
	return &socialv1.AuthTokens{
		UserId:        t.UserID,
		AccessToken:   t.AccessToken,
		RefreshToken:  t.RefreshToken,
		ExpiresAtUnix: t.ExpiresAt.Unix(),
	}
}
