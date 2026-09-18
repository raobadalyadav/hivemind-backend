package payments

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
)

// Handler implements socialv1.PaymentServiceServer. CreateOrder is real;
// GetPayment/RefundPayment inherit
// socialv1.UnimplementedPaymentServiceServer — see PRD §13.13.
type Handler struct {
	socialv1.UnimplementedPaymentServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) CreateOrder(ctx context.Context, req *socialv1.CreateOrderRequest) (*socialv1.Order, error) {
	o := &Order{BookingID: req.GetBookingId()}
	if req.GetAmount() != nil {
		o.AmountMinor = req.GetAmount().GetMinorUnits()
		o.Currency = req.GetAmount().GetCurrency()
	}
	created, err := h.svc.CreateOrder(ctx, o)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to create order")
	}
	return &socialv1.Order{
		Id:             created.ID,
		BookingId:      created.BookingID,
		Amount:         &socialv1.Money{MinorUnits: created.AmountMinor, Currency: created.Currency},
		GatewayOrderId: created.GatewayOrderID,
		Status:         created.Status,
	}, nil
}
