package availability

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

// Handler implements socialv1.AvailabilityServiceServer — every RPC is
// fully implemented (see flow.md §27/§28).
type Handler struct {
	socialv1.UnimplementedAvailabilityServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) SetAvailability(ctx context.Context, req *socialv1.SetAvailabilityRequest) (*socialv1.AvailabilityWindow, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	w := &Window{UserID: userID, ActivityType: req.GetActivityType()}
	if req.GetStartsAt() != nil {
		w.StartsAt = req.GetStartsAt().AsTime()
	}
	if req.GetEndsAt() != nil {
		w.EndsAt = req.GetEndsAt().AsTime()
	}
	created, err := h.svc.SetAvailability(ctx, w)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to set availability")
	}
	return toProto(created), nil
}

func (h *Handler) CancelAvailability(ctx context.Context, req *socialv1.CancelAvailabilityRequest) (*socialv1.CancelAvailabilityResponse, error) {
	callerID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	if err := h.svc.CancelAvailability(ctx, req.GetWindowId(), callerID); err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrForbidden:
			return nil, status.Error(codes.PermissionDenied, err.Error())
		case ErrWindowNotFound:
			return nil, status.Error(codes.NotFound, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to cancel availability")
		}
	}
	return &socialv1.CancelAvailabilityResponse{}, nil
}

func (h *Handler) FindActivityMatches(ctx context.Context, req *socialv1.FindActivityMatchesRequest) (*socialv1.FindActivityMatchesResponse, error) {
	callerID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	candidates, err := h.svc.FindActivityMatches(ctx, req.GetWindowId(), callerID)
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrForbidden:
			return nil, status.Error(codes.PermissionDenied, err.Error())
		case ErrWindowNotFound:
			return nil, status.Error(codes.NotFound, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to find activity matches")
		}
	}
	out := make([]*socialv1.CandidateMatch, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, &socialv1.CandidateMatch{WindowId: c.WindowID, UserId: c.UserID})
	}
	return &socialv1.FindActivityMatchesResponse{Candidates: out}, nil
}

func (h *Handler) AcceptActivityMatch(ctx context.Context, req *socialv1.AcceptActivityMatchRequest) (*socialv1.ActivityGroup, error) {
	callerID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	roomID, err := h.svc.AcceptActivityMatch(ctx, req.GetMyWindowId(), req.GetTheirWindowId(), callerID)
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrForbidden:
			return nil, status.Error(codes.PermissionDenied, err.Error())
		case ErrWindowNotFound:
			return nil, status.Error(codes.NotFound, err.Error())
		case ErrNotOpen:
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to accept activity match")
		}
	}
	return &socialv1.ActivityGroup{ChatRoomId: roomID}, nil
}

func toProto(w *Window) *socialv1.AvailabilityWindow {
	return &socialv1.AvailabilityWindow{
		Id:           w.ID,
		ActivityType: w.ActivityType,
		StartsAt:     timestamppb.New(w.StartsAt),
		EndsAt:       timestamppb.New(w.EndsAt),
		Status:       w.Status,
	}
}
