package bookings

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/idempotency"
)

// Handler implements socialv1.BookingServiceServer. CreateBooking/GetBooking
// are real; QuoteBooking/CancelBooking/CheckIn inherit
// socialv1.UnimplementedBookingServiceServer's codes.Unimplemented response —
// see proto/social/v1/booking.proto and PRD §13.6/§33.
type Handler struct {
	socialv1.UnimplementedBookingServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) CreateBooking(ctx context.Context, req *socialv1.CreateBookingRequest) (*socialv1.Booking, error) {
	b := &Booking{
		PlanID:         req.GetPlanId(),
		UserID:         req.GetUserId(),
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
	case err == idempotency.ErrDuplicateRequest:
		return nil, status.Error(codes.AlreadyExists, "booking already requested")
	default:
		return nil, status.Error(codes.Internal, "failed to create booking")
	}
}

func (h *Handler) GetBooking(ctx context.Context, req *socialv1.GetBookingRequest) (*socialv1.Booking, error) {
	b, err := h.svc.GetBooking(ctx, req.GetId())
	if err != nil {
		return nil, status.Error(codes.NotFound, "booking not found")
	}
	return toProto(b), nil
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
