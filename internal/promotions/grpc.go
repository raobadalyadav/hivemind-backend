package promotions

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

// Handler implements socialv1.PromotionServiceServer — every RPC is fully
// implemented (see PRD §13.11).
type Handler struct {
	socialv1.UnimplementedPromotionServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) PurchasePromotion(ctx context.Context, req *socialv1.PurchasePromotionRequest) (*socialv1.PromotedListing, error) {
	callerID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	created, sessionID, err := h.svc.PurchasePromotion(ctx, req.GetPlanId(), callerID, req.GetCustomerPhone(), req.GetDurationDays())
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrForbidden:
			return nil, status.Error(codes.PermissionDenied, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to purchase promotion")
		}
	}
	out := toProto(created)
	out.PaymentSessionId = sessionID
	return out, nil
}

func (h *Handler) ListMyPromotedListings(ctx context.Context, req *socialv1.ListMyPromotedListingsRequest) (*socialv1.ListMyPromotedListingsResponse, error) {
	callerID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	list, err := h.svc.ListMyPromotedListings(ctx, callerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list promoted listings")
	}
	out := make([]*socialv1.PromotedListing, 0, len(list))
	for _, l := range list {
		out = append(out, toProto(l))
	}
	return &socialv1.ListMyPromotedListingsResponse{Listings: out}, nil
}

func toProto(l *PromotedListing) *socialv1.PromotedListing {
	return &socialv1.PromotedListing{
		Id:       l.ID,
		PlanId:   l.PlanID,
		Amount:   &socialv1.Money{MinorUnits: l.AmountMinor, Currency: l.Currency},
		Status:   l.Status,
		StartsAt: timestamppb.New(l.StartsAt),
		EndsAt:   timestamppb.New(l.EndsAt),
	}
}
