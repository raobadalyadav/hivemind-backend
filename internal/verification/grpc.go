package verification

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

type Handler struct {
	socialv1.UnimplementedVerificationServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func vErr(err error, fallback string) error {
	switch err {
	case ErrInvalidInput:
		return status.Error(codes.InvalidArgument, err.Error())
	case ErrAlreadyVerified, ErrNoChallenge:
		return status.Error(codes.FailedPrecondition, err.Error())
	case ErrMediaUnavailable:
		return status.Error(codes.Unimplemented, err.Error())
	}
	return status.Error(codes.Internal, fallback)
}

var statusToProto = map[string]socialv1.VerificationStatus{
	"issued":   socialv1.VerificationStatus_VERIFICATION_STATUS_ISSUED,
	"pending":  socialv1.VerificationStatus_VERIFICATION_STATUS_PENDING,
	"approved": socialv1.VerificationStatus_VERIFICATION_STATUS_APPROVED,
	"rejected": socialv1.VerificationStatus_VERIFICATION_STATUS_REJECTED,
}

func toProto(s *State) *socialv1.VerificationState {
	out := &socialv1.VerificationState{Status: statusToProto[s.Status], Challenge: s.Challenge, RejectReason: s.RejectReason, Verified: s.Verified}
	if !s.ExpiresAt.IsZero() && (s.Status == "issued") {
		out.ExpiresAt = timestamppb.New(s.ExpiresAt)
	}
	return out
}

func (h *Handler) run(ctx context.Context, fn func(uid string) (*State, error), fallback string) (*socialv1.VerificationState, error) {
	uid, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	st, err := fn(uid)
	if err != nil {
		return nil, vErr(err, fallback)
	}
	return toProto(st), nil
}

func (h *Handler) GetVerification(ctx context.Context, _ *socialv1.GetVerificationRequest) (*socialv1.VerificationState, error) {
	return h.run(ctx, func(uid string) (*State, error) { return h.svc.Get(ctx, uid) }, "failed to load verification")
}

func (h *Handler) StartSelfieVerification(ctx context.Context, _ *socialv1.StartSelfieVerificationRequest) (*socialv1.VerificationState, error) {
	return h.run(ctx, func(uid string) (*State, error) { return h.svc.Start(ctx, uid) }, "failed to start verification")
}

func (h *Handler) SubmitSelfie(ctx context.Context, req *socialv1.SubmitSelfieRequest) (*socialv1.VerificationState, error) {
	return h.run(ctx, func(uid string) (*State, error) { return h.svc.Submit(ctx, uid, req.GetMediaId()) }, "failed to submit selfie")
}
