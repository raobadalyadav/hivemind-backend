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
// implemented (see PRD §13.1 and service.go for the refresh-token rotation
// design).
type Handler struct {
	socialv1.UnimplementedAuthServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) SignUp(ctx context.Context, req *socialv1.SignUpRequest) (*socialv1.AuthTokens, error) {
	var dob time.Time
	if req.GetDateOfBirth() != "" {
		parsed, err := time.Parse("2006-01-02", req.GetDateOfBirth())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid date_of_birth, expected YYYY-MM-DD")
		}
		dob = parsed
	}

	tokens, err := h.svc.SignUp(ctx, req.GetEmail(), req.GetPassword(), req.GetDeviceId(), "", dob)
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrUnderage:
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		case ErrUserExists:
			return nil, status.Error(codes.AlreadyExists, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to sign up")
		}
	}
	return toProto(tokens), nil
}

func (h *Handler) SignIn(ctx context.Context, req *socialv1.SignInRequest) (*socialv1.AuthTokens, error) {
	tokens, err := h.svc.SignIn(ctx, req.GetEmail(), req.GetPassword(), req.GetDeviceId(), "")
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Unauthenticated, "invalid credentials")
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
	userID, _ := grpcmiddleware.UserIDFromContext(ctx)
	if err := h.svc.SignOut(ctx, userID, req.GetDeviceId()); err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to sign out")
	}
	return &socialv1.SignOutResponse{}, nil
}

func (h *Handler) RequestPasswordReset(ctx context.Context, req *socialv1.RequestPasswordResetRequest) (*socialv1.RequestPasswordResetResponse, error) {
	if err := h.svc.RequestPasswordReset(ctx, req.GetEmail()); err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to request password reset")
	}
	return &socialv1.RequestPasswordResetResponse{}, nil
}

func toProto(t *Tokens) *socialv1.AuthTokens {
	return &socialv1.AuthTokens{
		UserId:        t.UserID,
		AccessToken:   t.AccessToken,
		RefreshToken:  t.RefreshToken,
		ExpiresAtUnix: t.ExpiresAt.Unix(),
	}
}
