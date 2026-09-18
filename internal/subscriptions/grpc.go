package subscriptions

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
)

// Handler implements socialv1.SubscriptionServiceServer. GetCatalog is real;
// Subscribe/CancelSubscription/GetEntitlements inherit
// socialv1.UnimplementedSubscriptionServiceServer — see PRD §13.14.
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
