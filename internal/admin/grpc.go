package admin

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

// Handler implements socialv1.AdminServiceServer — every RPC is fully
// implemented (see PRD §13.18/§23). RBAC is enforced by
// pkg/grpcmiddleware's adminMethods gate (checked before any handler here
// runs), not by this package.
type Handler struct {
	socialv1.UnimplementedAdminServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) ListUsers(ctx context.Context, req *socialv1.ListUsersRequest) (*socialv1.ListUsersResponse, error) {
	ids, err := h.svc.ListUsers(ctx, req.GetQuery())
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list users")
	}
	return &socialv1.ListUsersResponse{UserIds: ids}, nil
}

func (h *Handler) SuspendUser(ctx context.Context, req *socialv1.SuspendUserRequest) (*socialv1.SuspendUserResponse, error) {
	// actor_id comes from the authenticated admin's own token, not the
	// request — otherwise one admin could frame another in the audit log.
	actorID, _ := grpcmiddleware.UserIDFromContext(ctx)
	err := h.svc.SuspendUser(ctx, req.GetUserId(), req.GetReason(), actorID)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to suspend user")
	}
	return &socialv1.SuspendUserResponse{}, nil
}

func (h *Handler) ListReports(ctx context.Context, req *socialv1.ListReportsRequest) (*socialv1.ListReportsResponse, error) {
	ids, err := h.svc.ListReports(ctx, req.GetStatus())
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list reports")
	}
	return &socialv1.ListReportsResponse{CaseIds: ids}, nil
}

func (h *Handler) OverrideBookingStatus(ctx context.Context, req *socialv1.OverrideBookingStatusRequest) (*socialv1.OverrideBookingStatusResponse, error) {
	actorID, _ := grpcmiddleware.UserIDFromContext(ctx)
	err := h.svc.OverrideBookingStatus(ctx, req.GetBookingId(), req.GetNewStatus(), req.GetReason(), actorID)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.InvalidArgument, "invalid booking id or status value")
	}
	return &socialv1.OverrideBookingStatusResponse{}, nil
}

func (h *Handler) GetDashboardStats(ctx context.Context, req *socialv1.GetDashboardStatsRequest) (*socialv1.GetDashboardStatsResponse, error) {
	stats, err := h.svc.GetDashboardStats(ctx, req.GetCityId())
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load dashboard stats")
	}
	return &socialv1.GetDashboardStatsResponse{
		Dau:           stats.DAU,
		Mau:           stats.MAU,
		BookingsToday: stats.BookingsToday,
		GmvMinorUnits: stats.GMVMinorUnits,
	}, nil
}

func (h *Handler) ApproveHost(ctx context.Context, req *socialv1.ApproveHostRequest) (*socialv1.ApproveHostResponse, error) {
	actorID, _ := grpcmiddleware.UserIDFromContext(ctx)
	if err := h.svc.ApproveHost(ctx, req.GetUserId(), actorID); err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to approve host")
	}
	return &socialv1.ApproveHostResponse{}, nil
}

func (h *Handler) MarkPayoutProcessed(ctx context.Context, req *socialv1.MarkPayoutProcessedRequest) (*socialv1.MarkPayoutProcessedResponse, error) {
	actorID, _ := grpcmiddleware.UserIDFromContext(ctx)
	if err := h.svc.MarkPayoutProcessed(ctx, req.GetPayoutId(), actorID); err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to mark payout processed")
	}
	return &socialv1.MarkPayoutProcessedResponse{}, nil
}

func (h *Handler) AdminGrantCredit(ctx context.Context, req *socialv1.AdminGrantCreditRequest) (*socialv1.AdminGrantCreditResponse, error) {
	actorID, _ := grpcmiddleware.UserIDFromContext(ctx)
	if err := h.svc.AdminGrantCredit(ctx, req.GetUserId(), req.GetAmountMinor(), req.GetReason(), actorID); err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to grant credit")
	}
	return &socialv1.AdminGrantCreditResponse{}, nil
}

func (h *Handler) CreateCoupon(ctx context.Context, req *socialv1.CreateCouponRequest) (*socialv1.Coupon, error) {
	var expiresAt *time.Time
	if req.GetExpiresAt() != nil {
		t := req.GetExpiresAt().AsTime()
		expiresAt = &t
	}
	c, err := h.svc.CreateCoupon(ctx, req.GetCode(), req.GetDiscountType(), req.GetDiscountValue(), req.GetMaxUses(), expiresAt)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to create coupon")
	}
	return couponToProto(c), nil
}

func (h *Handler) ListCoupons(ctx context.Context, req *socialv1.ListCouponsRequest) (*socialv1.ListCouponsResponse, error) {
	list, err := h.svc.ListCoupons(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list coupons")
	}
	out := make([]*socialv1.Coupon, 0, len(list))
	for _, c := range list {
		out = append(out, couponToProto(c))
	}
	return &socialv1.ListCouponsResponse{Coupons: out}, nil
}

func (h *Handler) DeactivateCoupon(ctx context.Context, req *socialv1.DeactivateCouponRequest) (*socialv1.DeactivateCouponResponse, error) {
	if err := h.svc.DeactivateCoupon(ctx, req.GetId()); err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to deactivate coupon")
	}
	return &socialv1.DeactivateCouponResponse{}, nil
}

func (h *Handler) CreateCity(ctx context.Context, req *socialv1.CreateCityRequest) (*socialv1.City, error) {
	actorID, _ := grpcmiddleware.UserIDFromContext(ctx)
	c, err := h.svc.CreateCity(ctx, req.GetName(), req.GetState(), req.GetCountry(), actorID)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to create city")
	}
	return cityToProto(c), nil
}

func (h *Handler) UpdateCityStatus(ctx context.Context, req *socialv1.UpdateCityStatusRequest) (*socialv1.UpdateCityStatusResponse, error) {
	actorID, _ := grpcmiddleware.UserIDFromContext(ctx)
	if err := h.svc.UpdateCityStatus(ctx, req.GetCityId(), req.GetStatus(), actorID); err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to update city status")
	}
	return &socialv1.UpdateCityStatusResponse{}, nil
}

func cityToProto(c *City) *socialv1.City {
	return &socialv1.City{
		Id:      c.ID,
		Name:    c.Name,
		State:   c.State,
		Country: c.Country,
		Status:  c.Status,
	}
}

func couponToProto(c *Coupon) *socialv1.Coupon {
	return &socialv1.Coupon{
		Id:            c.ID,
		Code:          c.Code,
		DiscountType:  c.DiscountType,
		DiscountValue: c.DiscountValue,
		MaxUses:       c.MaxUses,
		UsesCount:     c.UsesCount,
		Active:        c.Active,
	}
}
