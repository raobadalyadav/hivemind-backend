package bookings

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

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
		// Access errors (invite-only, approval, community, premium) carry
		// their own gRPC status.
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
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

func waitlistErr(err error, fallback string) error {
	switch {
	case errors.Is(err, ErrInvalidInput):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ErrPlanNotFound):
		return status.Error(codes.NotFound, "plan not found or not published")
	case errors.Is(err, ErrAlreadyBooked):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, ErrNotFull):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		if _, ok := status.FromError(err); ok {
			return err
		}
		return status.Error(codes.Internal, fallback)
	}
}

func waitlistToProto(w *WaitlistStatus) *socialv1.WaitlistStatus {
	out := &socialv1.WaitlistStatus{PlanId: w.PlanID, Position: w.Position, TotalWaiting: w.TotalWaiting}
	switch w.State {
	case WaitlistWaiting:
		out.State = socialv1.WaitlistState_WAITLIST_STATE_WAITING
	case WaitlistOffered:
		out.State = socialv1.WaitlistState_WAITLIST_STATE_OFFERED
	default:
		out.State = socialv1.WaitlistState_WAITLIST_STATE_NONE
	}
	if w.OfferExpiresAt != nil {
		out.OfferExpiresAt = timestamppb.New(*w.OfferExpiresAt)
	}
	return out
}

func (h *Handler) JoinWaitlist(ctx context.Context, req *socialv1.JoinWaitlistRequest) (*socialv1.WaitlistStatus, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	w, err := h.svc.JoinWaitlist(ctx, req.GetPlanId(), userID)
	if err != nil {
		return nil, waitlistErr(err, "failed to join waitlist")
	}
	return waitlistToProto(w), nil
}

func (h *Handler) LeaveWaitlist(ctx context.Context, req *socialv1.LeaveWaitlistRequest) (*socialv1.LeaveWaitlistResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	if err := h.svc.LeaveWaitlist(ctx, req.GetPlanId(), userID); err != nil {
		return nil, waitlistErr(err, "failed to leave waitlist")
	}
	return &socialv1.LeaveWaitlistResponse{}, nil
}

func (h *Handler) GetWaitlistStatus(ctx context.Context, req *socialv1.GetWaitlistStatusRequest) (*socialv1.WaitlistStatus, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	w, err := h.svc.GetWaitlistStatus(ctx, req.GetPlanId(), userID)
	if err != nil {
		return nil, waitlistErr(err, "failed to load waitlist status")
	}
	return waitlistToProto(w), nil
}

func (h *Handler) ListMyWaitlist(ctx context.Context, req *socialv1.ListMyWaitlistRequest) (*socialv1.ListMyWaitlistResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	list, err := h.svc.ListMyWaitlist(ctx, userID)
	if err != nil {
		return nil, waitlistErr(err, "failed to list waitlist")
	}
	out := make([]*socialv1.WaitlistStatus, 0, len(list))
	for _, w := range list {
		out = append(out, waitlistToProto(w))
	}
	return &socialv1.ListMyWaitlistResponse{Entries: out}, nil
}

func (h *Handler) ListMyBookings(ctx context.Context, req *socialv1.ListMyBookingsRequest) (*socialv1.ListMyBookingsResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	tab := TabUpcoming
	switch req.GetTab() {
	case socialv1.BookingTab_BOOKING_TAB_PAST:
		tab = TabPast
	case socialv1.BookingTab_BOOKING_TAB_CANCELLED:
		tab = TabCancelled
	}
	list, err := h.svc.ListMyBookings(ctx, userID, tab)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list bookings")
	}
	out := make([]*socialv1.BookingSummary, 0, len(list))
	for _, s := range list {
		b := s.Booking
		out = append(out, &socialv1.BookingSummary{
			Booking:   toProto(&b),
			PlanTitle: s.PlanTitle,
			StartsAt:  timestamppb.New(s.StartsAt),
			EndsAt:    timestamppb.New(s.EndsAt),
			Reviewed:  s.Reviewed,
		})
	}
	return &socialv1.ListMyBookingsResponse{Bookings: out}, nil
}

func passErr(err error, fallback string) error {
	switch {
	case errors.Is(err, ErrInvalidInput), errors.Is(err, ErrInvalidPass):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ErrForbidden):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, ErrBookingNotFound):
		return status.Error(codes.NotFound, "booking not found")
	case errors.Is(err, ErrPassUnavailable), errors.Is(err, ErrOutsideWindow):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, ErrPassDisabled):
		return status.Error(codes.Unavailable, err.Error())
	default:
		return status.Error(codes.Internal, fallback)
	}
}

func (h *Handler) GetPass(ctx context.Context, req *socialv1.GetPassRequest) (*socialv1.Pass, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	info, payload, validUntil, err := h.svc.GetPass(ctx, req.GetBookingId(), userID)
	if err != nil {
		return nil, passErr(err, "failed to build pass")
	}
	return &socialv1.Pass{
		BookingId:       info.BookingID,
		PlanId:          info.PlanID,
		PlanTitle:       info.PlanTitle,
		PlanDescription: info.PlanDesc,
		VenueName:       info.VenueName,
		VenueAddress:    info.VenueAddress,
		StartsAt:        timestamppb.New(info.StartsAt),
		EndsAt:          timestamppb.New(info.EndsAt),
		HostName:        info.HostName,
		Payload:         payload,
		ValidUntil:      timestamppb.New(validUntil),
		Status:          statusToProto(info.Status),
	}, nil
}

func (h *Handler) ScanPass(ctx context.Context, req *socialv1.ScanPassRequest) (*socialv1.ScanPassResult, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	role, _ := grpcmiddleware.RoleFromContext(ctx)
	b, name, err := h.svc.ScanPass(ctx, req.GetPayload(), userID, role)
	if err != nil {
		return nil, passErr(err, "failed to scan pass")
	}
	return &socialv1.ScanPassResult{Booking: toProto(b), AttendeeName: name}, nil
}
