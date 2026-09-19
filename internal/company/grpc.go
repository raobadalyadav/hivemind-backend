package company

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

// Handler implements socialv1.CompanyServiceServer — every RPC is fully
// implemented (see flow.md §53).
type Handler struct {
	socialv1.UnimplementedCompanyServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) CreateCompany(ctx context.Context, req *socialv1.CreateCompanyRequest) (*socialv1.Company, error) {
	ownerID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	c, err := h.svc.CreateCompany(ctx, req.GetName(), req.GetBillingEmail(), ownerID)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to create company")
	}
	return &socialv1.Company{Id: c.ID, Name: c.Name, BillingEmail: c.BillingEmail}, nil
}

func (h *Handler) AddCompanyMember(ctx context.Context, req *socialv1.AddCompanyMemberRequest) (*socialv1.CompanyMember, error) {
	callerID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	m, err := h.svc.AddCompanyMember(ctx, req.GetCompanyId(), callerID, req.GetUserId())
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrForbidden:
			return nil, status.Error(codes.PermissionDenied, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to add company member")
		}
	}
	return &socialv1.CompanyMember{CompanyId: m.CompanyID, UserId: m.UserID, Role: m.Role}, nil
}

func (h *Handler) ListCompanyMembers(ctx context.Context, req *socialv1.ListCompanyMembersRequest) (*socialv1.ListCompanyMembersResponse, error) {
	callerID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	members, err := h.svc.ListCompanyMembers(ctx, req.GetCompanyId(), callerID)
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrForbidden:
			return nil, status.Error(codes.PermissionDenied, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to list company members")
		}
	}
	out := make([]*socialv1.CompanyMember, 0, len(members))
	for _, m := range members {
		out = append(out, &socialv1.CompanyMember{CompanyId: m.CompanyID, UserId: m.UserID, Role: m.Role})
	}
	return &socialv1.ListCompanyMembersResponse{Members: out}, nil
}

func (h *Handler) CreateTeamBooking(ctx context.Context, req *socialv1.CreateTeamBookingRequest) (*socialv1.CreateTeamBookingResponse, error) {
	callerID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	result, err := h.svc.CreateTeamBooking(ctx, req.GetCompanyId(), callerID, req.GetPlanId(), req.GetEmployeeUserIds())
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrForbidden:
			return nil, status.Error(codes.PermissionDenied, err.Error())
		case ErrNotAllMembers:
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to create team booking")
		}
	}
	resp := &socialv1.CreateTeamBookingResponse{BookingIds: result.BookingIDs}
	if result.Err != nil {
		resp.FailedEmployeeUserId = result.FailedEmployeeID
		resp.ErrorMessage = result.Err.Error()
	}
	return resp, nil
}
