package referral

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

type Handler struct {
	socialv1.UnimplementedReferralServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func refErr(err error, fallback string) error {
	switch err {
	case ErrInvalidInput, ErrSelfReferral:
		return status.Error(codes.InvalidArgument, err.Error())
	case ErrCodeNotFound:
		return status.Error(codes.NotFound, err.Error())
	case ErrAlreadyReferred:
		return status.Error(codes.AlreadyExists, err.Error())
	case ErrWindowClosed, ErrCapReached:
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	return status.Error(codes.Internal, fallback)
}

func (h *Handler) GetMyReferralCode(ctx context.Context, _ *socialv1.GetMyReferralCodeRequest) (*socialv1.ReferralCode, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	code, err := h.svc.GetMyCode(ctx, userID)
	if err != nil {
		return nil, refErr(err, "failed to get code")
	}
	return &socialv1.ReferralCode{Code: code, RewardMinor: RewardMinor, Currency: Currency}, nil
}

func (h *Handler) ApplyReferralCode(ctx context.Context, req *socialv1.ApplyReferralCodeRequest) (*socialv1.ApplyReferralCodeResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	if err := h.svc.ApplyCode(ctx, userID, req.GetCode()); err != nil {
		return nil, refErr(err, "failed to apply code")
	}
	return &socialv1.ApplyReferralCodeResponse{RewardMinor: RewardMinor, Currency: Currency}, nil
}

func (h *Handler) GetReferralStats(ctx context.Context, _ *socialv1.GetReferralStatsRequest) (*socialv1.ReferralStats, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	n, earned, err := h.svc.Stats(ctx, userID)
	if err != nil {
		return nil, refErr(err, "failed to load stats")
	}
	return &socialv1.ReferralStats{ReferralCount: n, ReferralCap: Cap, TotalEarnedMinor: earned, Currency: Currency}, nil
}
