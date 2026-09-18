package bookings

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
	"github.com/hivemind/backend/pkg/idempotency"
)

// Handler implements socialv1.BookingServiceServer — every RPC is fully
// implemented (see PRD §13.6/§33).
type Handler struct {
	socialv1.UnimplementedBookingServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) CreateBooking(ctx context.Context, req *socialv1.CreateBookingRequest) (*socialv1.Booking, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	b := &Booking{
		PlanID:         req.GetPlanId(),
		UserID:         userID,
		IdempotencyKey: req.GetIdempotencyKey(),
	}
	created, err := h.svc.CreateBooking(ctx, b)
	switch {
	case err == nil:
		return toProto(created), nil
	case err == ErrInvalidInput:
		return nil, status.Error(codes.InvalidArgument, err.Error())
	case err == ErrPlanFull:
		return nil, status.Error(codes.FailedPrecondition, "plan is full")
	case err == ErrAlreadyBooked:
		return nil, status.Error(codes.AlreadyExists, err.Error())
	case err == ErrPlanNotFound:
		return nil, status.Error(codes.NotFound, "plan not found or not published")
	case err == idempotency.ErrDuplicateRequest:
		return nil, status.Error(codes.AlreadyExists, "booking already requested")
	default:
		return nil, status.Error(codes.Internal, "failed to create booking")
	}
}

func (h *Handler) GetBooking(ctx context.Context, req *socialv1.GetBookingRequest) (*socialv1.Booking, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	role, _ := grpcmiddleware.RoleFromContext(ctx)
	b, err := h.svc.GetBookingAsUser(ctx, req.GetId(), userID, role)
	if err != nil {
		if err == ErrForbidden {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
		return nil, status.Error(codes.NotFound, "booking not found")
	}
	return toProto(b), nil
}

func (h *Handler) QuoteBooking(ctx context.Context, req *socialv1.QuoteBookingRequest) (*socialv1.BookingQuote, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	q, err := h.svc.QuoteBooking(ctx, req.GetPlanId(), userID)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.NotFound, "plan not found")
	}
	return &socialv1.BookingQuote{
		Price:      &socialv1.Money{MinorUnits: q.PriceMinor, Currency: q.Currency},
		ServiceFee: &socialv1.Money{MinorUnits: q.ServiceFeeMinor, Currency: q.Currency},
		Total:      &socialv1.Money{MinorUnits: q.TotalMinor, Currency: q.Currency},
		Eligible:   q.Eligible,
	}, nil
}

func (h *Handler) CancelBooking(ctx context.Context, req *socialv1.CancelBookingRequest) (*socialv1.Booking, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	role, _ := grpcmiddleware.RoleFromContext(ctx)
	b, err := h.svc.CancelBookingAsUser(ctx, req.GetId(), userID, role, req.GetReason())
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrForbidden:
			return nil, status.Error(codes.PermissionDenied, err.Error())
		default:
			return nil, status.Error(codes.NotFound, "booking not found or not cancellable")
		}
	}
	return toProto(b), nil
}

func (h *Handler) CheckIn(ctx context.Context, req *socialv1.CheckInRequest) (*socialv1.CheckInResult, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	role, _ := grpcmiddleware.RoleFromContext(ctx)
	b, err := h.svc.CheckIn(ctx, req.GetBookingId(), userID, role)
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrForbidden:
			return nil, status.Error(codes.PermissionDenied, err.Error())
		default:
			return nil, status.Error(codes.NotFound, "booking not found or not checkable-in")
		}
	}
	return &socialv1.CheckInResult{Booking: toProto(b)}, nil
}

func toProto(b *Booking) *socialv1.Booking {
	return &socialv1.Booking{
		Id:     b.ID,
		PlanId: b.PlanID,
		UserId: b.UserID,
		Status: statusToProto(b.Status),
		Price:  &socialv1.Money{MinorUnits: b.PriceMinor, Currency: b.Currency},
	}
}

var statusFromProtoMap = map[string]socialv1.BookingStatus{
	"initiated":       socialv1.BookingStatus_BOOKING_INITIATED,
	"payment_pending": socialv1.BookingStatus_BOOKING_PAYMENT_PENDING,
	"confirmed":       socialv1.BookingStatus_BOOKING_CONFIRMED,
	"cancelled":       socialv1.BookingStatus_BOOKING_CANCELLED,
	"refunded":        socialv1.BookingStatus_BOOKING_REFUNDED,
	"no_show":         socialv1.BookingStatus_BOOKING_NO_SHOW,
	"attended":        socialv1.BookingStatus_BOOKING_ATTENDED,
}

func statusToProto(s string) socialv1.BookingStatus {
	if v, ok := statusFromProtoMap[s]; ok {
		return v
	}
	return socialv1.BookingStatus_BOOKING_STATUS_UNSPECIFIED
}
