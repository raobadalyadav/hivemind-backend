package host

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

// Handler implements socialv1.HostServiceServer — every RPC is fully
// implemented (see PRD §13.11/§21).
type Handler struct {
	socialv1.UnimplementedHostServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) ApplyForHost(ctx context.Context, req *socialv1.ApplyForHostRequest) (*socialv1.HostStatus, error) {
	hostID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	a, err := h.svc.ApplyForHost(ctx, hostID, req.GetIdDocumentUrl(), req.GetBankAccountNumber(), req.GetBankIfsc())
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to apply for host status")
	}
	role, _ := grpcmiddleware.RoleFromContext(ctx)
	return toStatusProto(a, role), nil
}

func (h *Handler) GetHostStatus(ctx context.Context, req *socialv1.GetHostStatusRequest) (*socialv1.HostStatus, error) {
	hostID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	a, err := h.svc.GetHostStatus(ctx, hostID)
	if err != nil {
		return nil, status.Error(codes.NotFound, "no host application found")
	}
	role, _ := grpcmiddleware.RoleFromContext(ctx)
	return toStatusProto(a, role), nil
}

func (h *Handler) RequestPayout(ctx context.Context, req *socialv1.RequestPayoutRequest) (*socialv1.Payout, error) {
	hostID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	p, err := h.svc.RequestPayout(ctx, hostID)
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrHostNotApproved:
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		case ErrNoPayoutBalance:
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		case ErrPayoutAccountNotFound:
			return nil, status.Error(codes.NotFound, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to request payout")
		}
	}
	return toPayoutProto(p), nil
}

func (h *Handler) ListPayouts(ctx context.Context, req *socialv1.ListPayoutsRequest) (*socialv1.ListPayoutsResponse, error) {
	hostID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	list, err := h.svc.ListPayouts(ctx, hostID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list payouts")
	}
	out := make([]*socialv1.Payout, 0, len(list))
	for _, p := range list {
		out = append(out, toPayoutProto(p))
	}
	return &socialv1.ListPayoutsResponse{Payouts: out}, nil
}

func (h *Handler) GetHostDashboard(ctx context.Context, req *socialv1.GetHostDashboardRequest) (*socialv1.HostDashboard, error) {
	hostID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	d, err := h.svc.GetHostDashboard(ctx, hostID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load host dashboard")
	}
	return &socialv1.HostDashboard{
		TotalBookings:  d.TotalBookings,
		TotalAttendees: d.TotalAttendees,
		GrossRevenue:   &socialv1.Money{MinorUnits: d.GrossRevenueMinor, Currency: "INR"},
		AvgRating:      d.AvgRating,
	}, nil
}

func toStatusProto(a *PayoutAccount, role string) *socialv1.HostStatus {
	return &socialv1.HostStatus{
		UserId:               a.HostID,
		Role:                 role,
		PayoutAccountStatus:  a.Status,
		IdDocumentUrl:        a.IDDocumentURL,
	}
}

func toPayoutProto(p *Payout) *socialv1.Payout {
	return &socialv1.Payout{
		Id:     p.ID,
		Amount: &socialv1.Money{MinorUnits: p.AmountMinor, Currency: "INR"},
		Status: p.Status,
	}
}
