package communities

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

// Handler implements socialv1.CommunityServiceServer — every RPC is fully
// implemented (see PRD §13.8/§33).
type Handler struct {
	socialv1.UnimplementedCommunityServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) CreateCommunity(ctx context.Context, req *socialv1.CreateCommunityRequest) (*socialv1.Community, error) {
	ownerID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	c := &Community{
		Name:                req.GetName(),
		Description:         req.GetDescription(),
		CityID:              req.GetCityId(),
		OwnerID:             ownerID,
		MembershipType:      typeFromProto(req.GetMembershipType()),
		CoverImageURL:       req.GetCoverImageUrl(),
		Rules:               req.GetRules(),
		CategoryID:          req.GetCategoryId(),
		RequiredEntitlement: req.GetRequiredEntitlement(),
	}
	created, err := h.svc.CreateCommunity(ctx, c)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to create community")
	}
	return toProto(created), nil
}

func (h *Handler) GetCommunity(ctx context.Context, req *socialv1.GetCommunityRequest) (*socialv1.Community, error) {
	callerID, _ := grpcmiddleware.UserIDFromContext(ctx)
	c, err := h.svc.GetCommunity(ctx, req.GetId(), callerID)
	if err != nil {
		return nil, status.Error(codes.NotFound, "community not found")
	}
	return toProto(c), nil
}

func (h *Handler) JoinCommunity(ctx context.Context, req *socialv1.JoinCommunityRequest) (*socialv1.Membership, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	m, err := h.svc.JoinCommunity(ctx, req.GetCommunityId(), userID)
	if err != nil {
		return nil, communityErr(err, "failed to join community")
	}
	return &socialv1.Membership{CommunityId: m.CommunityID, UserId: m.UserID, Role: m.Role, Status: m.Status}, nil
}

func (h *Handler) ListCommunityPlans(ctx context.Context, req *socialv1.ListCommunityPlansRequest) (*socialv1.ListPlansResponse, error) {
	callerID, _ := grpcmiddleware.UserIDFromContext(ctx)
	ids, err := h.svc.ListCommunityPlans(ctx, req.GetCommunityId(), callerID)
	if err != nil {
		return nil, communityErr(err, "failed to list community plans")
	}
	return &socialv1.ListPlansResponse{PlanIds: ids}, nil
}

func toProto(c *Community) *socialv1.Community {
	return &socialv1.Community{
		Id:                  c.ID,
		Name:                c.Name,
		Description:         c.Description,
		CityId:              c.CityID,
		OwnerId:             c.OwnerID,
		MembershipType:      typeToProto(c.MembershipType),
		CoverImageUrl:       c.CoverImageURL,
		Rules:               c.Rules,
		CategoryId:          c.CategoryID,
		RequiredEntitlement: c.RequiredEntitlement,
		MemberCount:         c.MemberCount,
		PlanCount:           c.PlanCount,
	}
}

func typeFromProto(t socialv1.CommunityType) string {
	switch t {
	case socialv1.CommunityType_COMMUNITY_TYPE_PRIVATE:
		return "private"
	case socialv1.CommunityType_COMMUNITY_TYPE_APPROVAL:
		return "approval"
	case socialv1.CommunityType_COMMUNITY_TYPE_PAID:
		return "paid"
	case socialv1.CommunityType_COMMUNITY_TYPE_PUBLIC:
		return "public"
	}
	return ""
}

func typeToProto(t string) socialv1.CommunityType {
	switch t {
	case "private":
		return socialv1.CommunityType_COMMUNITY_TYPE_PRIVATE
	case "approval":
		return socialv1.CommunityType_COMMUNITY_TYPE_APPROVAL
	case "paid":
		return socialv1.CommunityType_COMMUNITY_TYPE_PAID
	case "public":
		return socialv1.CommunityType_COMMUNITY_TYPE_PUBLIC
	}
	return socialv1.CommunityType_COMMUNITY_TYPE_UNSPECIFIED
}

func communityErr(err error, fallback string) error {
	switch err {
	case ErrInvalidInput:
		return status.Error(codes.InvalidArgument, err.Error())
	case ErrForbidden:
		return status.Error(codes.PermissionDenied, err.Error())
	case ErrEntitlementRequired:
		return status.Error(codes.PermissionDenied, err.Error())
	case ErrCommunityNotFound, ErrRequestNotFound:
		return status.Error(codes.NotFound, err.Error())
	case ErrAlreadyDecided, ErrOwnerCannotLeave:
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, fallback)
	}
}

var reqStatusToProto = map[string]socialv1.JoinRequestStatus{
	"pending":   socialv1.JoinRequestStatus_JOIN_REQUEST_STATUS_PENDING,
	"approved":  socialv1.JoinRequestStatus_JOIN_REQUEST_STATUS_APPROVED,
	"rejected":  socialv1.JoinRequestStatus_JOIN_REQUEST_STATUS_REJECTED,
	"cancelled": socialv1.JoinRequestStatus_JOIN_REQUEST_STATUS_CANCELLED,
}

var reqStatusFromProto = map[socialv1.JoinRequestStatus]string{
	socialv1.JoinRequestStatus_JOIN_REQUEST_STATUS_PENDING:   "pending",
	socialv1.JoinRequestStatus_JOIN_REQUEST_STATUS_APPROVED:  "approved",
	socialv1.JoinRequestStatus_JOIN_REQUEST_STATUS_REJECTED:  "rejected",
	socialv1.JoinRequestStatus_JOIN_REQUEST_STATUS_CANCELLED: "cancelled",
}

func requestToProto(j *JoinRequest) *socialv1.CommunityJoinRequest {
	return &socialv1.CommunityJoinRequest{Id: j.ID, CommunityId: j.CommunityID, UserId: j.UserID, Status: reqStatusToProto[j.Status]}
}

func (h *Handler) ListCommunities(ctx context.Context, req *socialv1.ListCommunitiesRequest) (*socialv1.ListCommunitiesResponse, error) {
	list, err := h.svc.ListCommunities(ctx, req.GetCityId(), req.GetCategoryId(), req.GetQuery())
	if err != nil {
		return nil, communityErr(err, "failed to list communities")
	}
	out := make([]*socialv1.Community, 0, len(list))
	for _, c := range list {
		out = append(out, toProto(c))
	}
	return &socialv1.ListCommunitiesResponse{Communities: out}, nil
}

func (h *Handler) LeaveCommunity(ctx context.Context, req *socialv1.LeaveCommunityRequest) (*socialv1.LeaveCommunityResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	if err := h.svc.LeaveCommunity(ctx, req.GetCommunityId(), userID); err != nil {
		return nil, communityErr(err, "failed to leave community")
	}
	return &socialv1.LeaveCommunityResponse{}, nil
}

func (h *Handler) ListCommunityJoinRequests(ctx context.Context, req *socialv1.ListCommunityJoinRequestsRequest) (*socialv1.ListCommunityJoinRequestsResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	list, err := h.svc.ListJoinRequests(ctx, req.GetCommunityId(), reqStatusFromProto[req.GetStatus()], userID)
	if err != nil {
		return nil, communityErr(err, "failed to list join requests")
	}
	out := make([]*socialv1.CommunityJoinRequest, 0, len(list))
	for _, j := range list {
		out = append(out, requestToProto(j))
	}
	return &socialv1.ListCommunityJoinRequestsResponse{Requests: out}, nil
}

func (h *Handler) RespondCommunityJoinRequest(ctx context.Context, req *socialv1.RespondCommunityJoinRequestRequest) (*socialv1.CommunityJoinRequest, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	jr, err := h.svc.RespondJoinRequest(ctx, req.GetRequestId(), userID, req.GetApprove())
	if err != nil {
		return nil, communityErr(err, "failed to respond to join request")
	}
	return requestToProto(jr), nil
}

func (h *Handler) InviteToCommunity(ctx context.Context, req *socialv1.InviteToCommunityRequest) (*socialv1.InviteToCommunityResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	if err := h.svc.InviteToCommunity(ctx, req.GetCommunityId(), userID, req.GetUserId()); err != nil {
		return nil, communityErr(err, "failed to invite user")
	}
	return &socialv1.InviteToCommunityResponse{}, nil
}
