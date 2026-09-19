package payments

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

// Handler implements socialv1.PaymentServiceServer — every RPC is fully
// implemented (see PRD §13.13).
type Handler struct {
	socialv1.UnimplementedPaymentServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) CreateOrder(ctx context.Context, req *socialv1.CreateOrderRequest) (*socialv1.Order, error) {
	callerID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	o := &Order{BookingID: req.GetBookingId()}
	if req.GetAmount() != nil {
		o.AmountMinor = req.GetAmount().GetMinorUnits()
		o.Currency = req.GetAmount().GetCurrency()
	}
	created, sessionID, err := h.svc.CreateOrder(ctx, o, callerID, req.GetCustomerPhone(), req.GetPromoCode(), req.GetUseCredits())
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrForbidden:
			return nil, status.Error(codes.PermissionDenied, err.Error())
		case ErrCouponInvalid:
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to create order")
		}
	}
	return &socialv1.Order{
		Id:               created.ID,
		BookingId:        created.BookingID,
		Amount:           &socialv1.Money{MinorUnits: created.AmountMinor, Currency: created.Currency},
		GatewayOrderId:   created.GatewayOrderID,
		Status:           created.Status,
		PaymentSessionId: sessionID,
	}, nil
}

func (h *Handler) GetPayment(ctx context.Context, req *socialv1.GetPaymentRequest) (*socialv1.Payment, error) {
	p, err := h.svc.GetPayment(ctx, req.GetId())
	if err != nil {
		return nil, status.Error(codes.NotFound, "payment not found")
	}
	return &socialv1.Payment{
		Id:      p.ID,
		OrderId: p.OrderID,
		Amount:  &socialv1.Money{MinorUnits: p.AmountMinor, Currency: p.Currency},
		Status:  p.Status,
	}, nil
}

func (h *Handler) GetMyCreditBalance(ctx context.Context, req *socialv1.GetMyCreditBalanceRequest) (*socialv1.CreditBalance, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	balance, err := h.svc.GetMyCreditBalance(ctx, userID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load credit balance")
	}
	return &socialv1.CreditBalance{BalanceMinor: balance}, nil
}

func (h *Handler) RefundPayment(ctx context.Context, req *socialv1.RefundPaymentRequest) (*socialv1.Refund, error) {
	var amount int64
	var currency string
	if req.GetAmount() != nil {
		amount = req.GetAmount().GetMinorUnits()
		currency = req.GetAmount().GetCurrency()
	}
	r, err := h.svc.RefundPayment(ctx, req.GetPaymentId(), amount, req.GetReason())
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrPaymentNotFound:
			return nil, status.Error(codes.NotFound, err.Error())
		case ErrPaymentNotRefundable:
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to refund payment")
		}
	}
	return &socialv1.Refund{
		Id:        r.ID,
		PaymentId: r.PaymentID,
		Amount:    &socialv1.Money{MinorUnits: r.AmountMinor, Currency: currency},
		Status:    r.Status,
	}, nil
}
