package subscriptions

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

// Handler implements socialv1.SubscriptionServiceServer — every RPC is
// fully implemented (see PRD §13.14).
type Handler struct {
	socialv1.UnimplementedSubscriptionServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) GetCatalog(ctx context.Context, req *socialv1.GetCatalogRequest) (*socialv1.GetCatalogResponse, error) {
	products, err := h.svc.GetCatalog(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load catalog")
	}
	out := make([]*socialv1.SubscriptionProduct, 0, len(products))
	for _, p := range products {
		out = append(out, &socialv1.SubscriptionProduct{
			Id:       p.ID,
			Name:     p.Name,
			Price:    &socialv1.Money{MinorUnits: p.PriceMinor, Currency: p.Currency},
			Interval: p.Interval,
		})
	}
	return &socialv1.GetCatalogResponse{Products: out}, nil
}

func (h *Handler) Subscribe(ctx context.Context, req *socialv1.SubscribeRequest) (*socialv1.Subscription, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	sub, err := h.svc.Subscribe(ctx, userID, req.GetProductId(), req.GetStoreReceipt())
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrSubscriptionNotFound:
			return nil, status.Error(codes.NotFound, "product not found")
		default:
			return nil, status.Error(codes.Internal, "failed to subscribe")
		}
	}
	return toProto(sub), nil
}

func (h *Handler) CancelSubscription(ctx context.Context, req *socialv1.CancelSubscriptionRequest) (*socialv1.Subscription, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	sub, err := h.svc.CancelSubscription(ctx, req.GetSubscriptionId(), userID)
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrForbidden:
			return nil, status.Error(codes.PermissionDenied, err.Error())
		case ErrSubscriptionNotFound:
			return nil, status.Error(codes.NotFound, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to cancel subscription")
		}
	}
	return toProto(sub), nil
}

func (h *Handler) GetEntitlements(ctx context.Context, req *socialv1.GetEntitlementsRequest) (*socialv1.GetEntitlementsResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	keys, err := h.svc.GetEntitlements(ctx, userID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load entitlements")
	}
	return &socialv1.GetEntitlementsResponse{Entitlements: keys}, nil
}

func toProto(s *Subscription) *socialv1.Subscription {
	return &socialv1.Subscription{Id: s.ID, UserId: s.UserID, ProductId: s.ProductID, Status: s.Status}
}
