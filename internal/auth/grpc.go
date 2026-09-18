package auth

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
)

// Handler implements socialv1.AuthServiceServer. SignUp/SignIn are real;
// RefreshToken/SignOut/RequestPasswordReset inherit
// socialv1.UnimplementedAuthServiceServer — see PRD §13.1.
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

	tokens, err := h.svc.SignUp(ctx, req.GetEmail(), req.GetPassword(), dob)
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
	tokens, err := h.svc.SignIn(ctx, req.GetEmail(), req.GetPassword())
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Unauthenticated, "invalid credentials")
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
