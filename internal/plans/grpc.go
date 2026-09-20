package plans

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

// Handler implements socialv1.PlanServiceServer — every RPC is fully
// implemented (see PRD §13.4/§17).
type Handler struct {
	socialv1.UnimplementedPlanServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) CreatePlan(ctx context.Context, req *socialv1.CreatePlanRequest) (*socialv1.Plan, error) {
	// The host is always the authenticated caller — the request's host_id
	// field is ignored (it used to be trusted, letting anyone create a plan
	// hosted as someone else).
	hostID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	p := &Plan{
		Title:       req.GetTitle(),
		Description: req.GetDescription(),
		CategoryID:  req.GetCategoryId(),
		HostID:      hostID,
		CityID:      req.GetCityId(),
		VenueID:     req.GetVenueId(),
		Capacity:    req.GetCapacity(),

		JoinMode:            joinModeFromProto(req.GetJoinMode()),
		Visibility:          visibilityFromProto(req.GetVisibility()),
		CommunityID:         req.GetCommunityId(),
		RequiresEntitlement: req.GetRequiresEntitlement(),
		CoverMediaID:        req.GetCoverMediaId(),
	}
	if req.GetStartsAt() != nil {
		p.StartsAt = req.GetStartsAt().AsTime()
	}
	if req.GetEndsAt() != nil {
		p.EndsAt = req.GetEndsAt().AsTime()
	}
	if req.GetPrice() != nil {
		p.PriceMinor = req.GetPrice().GetMinorUnits()
		p.Currency = req.GetPrice().GetCurrency()
	}
	if req.GetLocation() != nil {
		lat, lng := req.GetLocation().GetLatitude(), req.GetLocation().GetLongitude()
		p.Latitude, p.Longitude = &lat, &lng
	}

	created, err := h.svc.CreatePlan(ctx, p, req.GetRecurrenceRule())
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrForbidden:
			return nil, status.Error(codes.PermissionDenied, "you must own or moderate that community")
		case ErrMediaUnavailable:
			return nil, status.Error(codes.Unimplemented, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to create plan")
		}
	}
	return toProto(created), nil
}

func (h *Handler) GetPlan(ctx context.Context, req *socialv1.GetPlanRequest) (*socialv1.Plan, error) {
	callerID, _ := grpcmiddleware.UserIDFromContext(ctx)
	role, _ := grpcmiddleware.RoleFromContext(ctx)
	p, err := h.svc.GetPlanAsUser(ctx, req.GetId(), callerID, role)
	if err != nil {
		return nil, status.Error(codes.NotFound, "plan not found")
	}
	return toProto(p), nil
}

func (h *Handler) SearchPlans(ctx context.Context, req *socialv1.SearchPlansRequest) (*socialv1.SearchPlansResponse, error) {
	f := SearchFilter{CityID: req.GetCityId(), CategoryID: req.GetCategoryId(), RadiusKM: req.GetRadiusKm()}
	if req.GetOrigin() != nil {
		lat, lng := req.GetOrigin().GetLatitude(), req.GetOrigin().GetLongitude()
		f.Latitude, f.Longitude = &lat, &lng
	}
	results, err := h.svc.SearchPlans(ctx, f)
	if err != nil {
		return nil, status.Error(codes.Internal, "search failed")
	}
	viewer, _ := grpcmiddleware.UserIDFromContext(ctx)
	if err := h.svc.Decorate(ctx, results, viewer); err != nil {
		return nil, status.Error(codes.Internal, "search failed")
	}
	out := make([]*socialv1.Plan, 0, len(results))
	for _, p := range results {
		out = append(out, toProto(p))
	}
	return &socialv1.SearchPlansResponse{Plans: out}, nil
}

func (h *Handler) JoinPlan(ctx context.Context, req *socialv1.JoinPlanRequest) (*socialv1.JoinPlanResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	bookingID, err := h.svc.JoinPlan(ctx, req.GetPlanId(), userID, req.GetIdempotencyKey())
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, mapBookingCreationError(err)
	}
	return &socialv1.JoinPlanResponse{BookingId: bookingID}, nil
}

func (h *Handler) LeavePlan(ctx context.Context, req *socialv1.LeavePlanRequest) (*socialv1.LeavePlanResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	if err := h.svc.LeavePlan(ctx, req.GetPlanId(), userID); err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to leave plan")
	}
	return &socialv1.LeavePlanResponse{}, nil
}

func (h *Handler) CancelPlan(ctx context.Context, req *socialv1.CancelPlanRequest) (*socialv1.Plan, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	role, _ := grpcmiddleware.RoleFromContext(ctx)
	p, err := h.svc.CancelPlan(ctx, req.GetPlanId(), userID, role, req.GetReason(), req.GetCancelFuture())
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrForbidden:
			return nil, status.Error(codes.PermissionDenied, err.Error())
		case ErrPlanNotFound:
			return nil, status.Error(codes.NotFound, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to cancel plan")
		}
	}
	return toProto(p), nil
}

func (h *Handler) SuggestPlanDraft(ctx context.Context, req *socialv1.SuggestPlanDraftRequest) (*socialv1.PlanDraft, error) {
	title, description, err := h.svc.SuggestPlanDraft(ctx, req.GetCategoryId())
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to suggest plan draft")
	}
	return &socialv1.PlanDraft{Title: title, Description: description}, nil
}

// mapBookingCreationError avoids importing internal/bookings just to switch
// on its sentinel errors (ErrPlanFull, idempotency.ErrDuplicateRequest) —
// that would defeat the point of the BookingCreator interface. The message
// is descriptive enough for the client without needing exact error codes.
func mapBookingCreationError(err error) error {
	// Booking access errors (invite-only, approval required, ...) already
	// carry a precise gRPC status — pass them through untouched.
	if _, ok := status.FromError(err); ok {
		return err
	}
	return status.Error(codes.FailedPrecondition, err.Error())
}

func toProto(p *Plan) *socialv1.Plan {
	out := &socialv1.Plan{
		Id:             p.ID,
		Title:          p.Title,
		Description:    p.Description,
		CategoryId:     p.CategoryID,
		HostId:         p.HostID,
		CityId:         p.CityID,
		VenueId:        p.VenueID,
		StartsAt:       timestamppb.New(p.StartsAt),
		EndsAt:         timestamppb.New(p.EndsAt),
		Capacity:       p.Capacity,
		ConfirmedCount: p.ConfirmedCount,
		Price:          &socialv1.Money{MinorUnits: p.PriceMinor, Currency: p.Currency},
		Status:         statusToProto(p.Status),

		JoinMode:            joinModeToProto(p.JoinMode),
		Visibility:          visibilityToProto(p.Visibility),
		CommunityId:         p.CommunityID,
		RequiresEntitlement: p.RequiresEntitlement,
		SeriesId:            p.SeriesID,
		CoverUrl:            p.CoverURL,
		CoverThumbUrl:       p.CoverThumbURL,
		SavedByMe:           p.SavedByMe,
		HostRatingAvg:       p.HostRatingAvg,
		HostRatingCount:     p.HostRatingCount,
	}
	if p.Latitude != nil && p.Longitude != nil {
		out.Location = &socialv1.GeoPoint{Latitude: *p.Latitude, Longitude: *p.Longitude}
	}
	return out
}

var statusFromProtoMap = map[string]socialv1.PlanStatus{
	"draft":     socialv1.PlanStatus_PLAN_STATUS_DRAFT,
	"published": socialv1.PlanStatus_PLAN_STATUS_PUBLISHED,
	"full":      socialv1.PlanStatus_PLAN_STATUS_FULL,
	"cancelled": socialv1.PlanStatus_PLAN_STATUS_CANCELLED,
	"completed": socialv1.PlanStatus_PLAN_STATUS_COMPLETED,
}

func statusToProto(s string) socialv1.PlanStatus {
	if v, ok := statusFromProtoMap[s]; ok {
		return v
	}
	return socialv1.PlanStatus_PLAN_STATUS_UNSPECIFIED
}

func joinModeFromProto(m socialv1.PlanJoinMode) string {
	switch m {
	case socialv1.PlanJoinMode_PLAN_JOIN_MODE_APPROVAL:
		return "approval"
	case socialv1.PlanJoinMode_PLAN_JOIN_MODE_INVITE_ONLY:
		return "invite_only"
	case socialv1.PlanJoinMode_PLAN_JOIN_MODE_OPEN:
		return "open"
	}
	return "" // unspecified → service default
}

func joinModeToProto(m string) socialv1.PlanJoinMode {
	switch m {
	case "approval":
		return socialv1.PlanJoinMode_PLAN_JOIN_MODE_APPROVAL
	case "invite_only":
		return socialv1.PlanJoinMode_PLAN_JOIN_MODE_INVITE_ONLY
	case "open":
		return socialv1.PlanJoinMode_PLAN_JOIN_MODE_OPEN
	}
	return socialv1.PlanJoinMode_PLAN_JOIN_MODE_UNSPECIFIED
}

func visibilityFromProto(v socialv1.PlanVisibility) string {
	switch v {
	case socialv1.PlanVisibility_PLAN_VISIBILITY_PRIVATE:
		return "private"
	case socialv1.PlanVisibility_PLAN_VISIBILITY_COMMUNITY:
		return "community"
	case socialv1.PlanVisibility_PLAN_VISIBILITY_PUBLIC:
		return "public"
	}
	return ""
}

func visibilityToProto(v string) socialv1.PlanVisibility {
	switch v {
	case "private":
		return socialv1.PlanVisibility_PLAN_VISIBILITY_PRIVATE
	case "community":
		return socialv1.PlanVisibility_PLAN_VISIBILITY_COMMUNITY
	case "public":
		return socialv1.PlanVisibility_PLAN_VISIBILITY_PUBLIC
	}
	return socialv1.PlanVisibility_PLAN_VISIBILITY_UNSPECIFIED
}

var joinStatusToProto = map[string]socialv1.JoinRequestStatus{
	"pending":   socialv1.JoinRequestStatus_JOIN_REQUEST_STATUS_PENDING,
	"approved":  socialv1.JoinRequestStatus_JOIN_REQUEST_STATUS_APPROVED,
	"rejected":  socialv1.JoinRequestStatus_JOIN_REQUEST_STATUS_REJECTED,
	"cancelled": socialv1.JoinRequestStatus_JOIN_REQUEST_STATUS_CANCELLED,
}

var joinStatusFromProto = map[socialv1.JoinRequestStatus]string{
	socialv1.JoinRequestStatus_JOIN_REQUEST_STATUS_PENDING:   "pending",
	socialv1.JoinRequestStatus_JOIN_REQUEST_STATUS_APPROVED:  "approved",
	socialv1.JoinRequestStatus_JOIN_REQUEST_STATUS_REJECTED:  "rejected",
	socialv1.JoinRequestStatus_JOIN_REQUEST_STATUS_CANCELLED: "cancelled",
}

func joinRequestToProto(j *JoinRequest) *socialv1.PlanJoinRequest {
	return &socialv1.PlanJoinRequest{
		Id: j.ID, PlanId: j.PlanID, UserId: j.UserID,
		Status: joinStatusToProto[j.Status], Message: j.Message,
	}
}

func planErr(err error, fallback string) error {
	switch err {
	case ErrInvalidInput:
		return status.Error(codes.InvalidArgument, err.Error())
	case ErrForbidden:
		return status.Error(codes.PermissionDenied, err.Error())
	case ErrPlanNotFound, ErrRequestNotFound:
		return status.Error(codes.NotFound, err.Error())
	case ErrAlreadyDecided, ErrNotParticipant:
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, fallback)
	}
}

func (h *Handler) RequestToJoinPlan(ctx context.Context, req *socialv1.RequestToJoinPlanRequest) (*socialv1.PlanJoinRequest, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	jr, err := h.svc.RequestToJoinPlan(ctx, req.GetPlanId(), userID, req.GetMessage())
	if err != nil {
		return nil, planErr(err, "failed to request to join")
	}
	return joinRequestToProto(jr), nil
}

func (h *Handler) ListPlanJoinRequests(ctx context.Context, req *socialv1.ListPlanJoinRequestsRequest) (*socialv1.ListPlanJoinRequestsResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	role, _ := grpcmiddleware.RoleFromContext(ctx)
	list, err := h.svc.ListPlanJoinRequests(ctx, req.GetPlanId(), joinStatusFromProto[req.GetStatus()], userID, role)
	if err != nil {
		return nil, planErr(err, "failed to list join requests")
	}
	out := make([]*socialv1.PlanJoinRequest, 0, len(list))
	for _, j := range list {
		out = append(out, joinRequestToProto(j))
	}
	return &socialv1.ListPlanJoinRequestsResponse{Requests: out}, nil
}

func (h *Handler) RespondPlanJoinRequest(ctx context.Context, req *socialv1.RespondPlanJoinRequestRequest) (*socialv1.PlanJoinRequest, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	role, _ := grpcmiddleware.RoleFromContext(ctx)
	jr, err := h.svc.RespondPlanJoinRequest(ctx, req.GetRequestId(), userID, role, req.GetApprove())
	if err != nil {
		return nil, planErr(err, "failed to respond to join request")
	}
	return joinRequestToProto(jr), nil
}

func (h *Handler) InvitePlanUsers(ctx context.Context, req *socialv1.InvitePlanUsersRequest) (*socialv1.InvitePlanUsersResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	role, _ := grpcmiddleware.RoleFromContext(ctx)
	n, err := h.svc.InvitePlanUsers(ctx, req.GetPlanId(), userID, role, req.GetUserIds())
	if err != nil {
		return nil, planErr(err, "failed to invite users")
	}
	return &socialv1.InvitePlanUsersResponse{InvitedCount: int32(n)}, nil
}

func (h *Handler) RevokePlanInvite(ctx context.Context, req *socialv1.RevokePlanInviteRequest) (*socialv1.RevokePlanInviteResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	role, _ := grpcmiddleware.RoleFromContext(ctx)
	if err := h.svc.RevokePlanInvite(ctx, req.GetPlanId(), req.GetUserId(), userID, role); err != nil {
		return nil, planErr(err, "failed to revoke invite")
	}
	return &socialv1.RevokePlanInviteResponse{}, nil
}

func (h *Handler) GetPlanParticipants(ctx context.Context, req *socialv1.GetPlanParticipantsRequest) (*socialv1.GetPlanParticipantsResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	role, _ := grpcmiddleware.RoleFromContext(ctx)
	ps, err := h.svc.GetPlanParticipants(ctx, req.GetPlanId(), userID, role)
	if err != nil {
		return nil, planErr(err, "failed to load participants")
	}
	cards := make([]*socialv1.ParticipantCard, 0, len(ps.Cards))
	for _, c := range ps.Cards {
		cards = append(cards, &socialv1.ParticipantCard{
			UserId: c.UserID, DisplayName: c.DisplayName, Occupation: c.Occupation, Interests: c.Interests,
			PhotoUrl: c.PhotoURL, SharedInterestCount: c.SharedInterestCount,
			MutualConnectionCount: c.MutualConnectionCount, SharedCommunityCount: c.SharedCommunityCount,
		})
	}
	return &socialv1.GetPlanParticipantsResponse{
		Participants: cards, TotalAttending: ps.TotalAttending, HiddenCount: ps.HiddenCount,
		ConnectionsAttending: ps.ConnectionsAttending,
	}, nil
}

func (h *Handler) SetParticipantVisibility(ctx context.Context, req *socialv1.SetParticipantVisibilityRequest) (*socialv1.SetParticipantVisibilityResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	if err := h.svc.SetParticipantVisibility(ctx, req.GetPlanId(), userID, req.GetVisible()); err != nil {
		return nil, planErr(err, "failed to update visibility")
	}
	return &socialv1.SetParticipantVisibilityResponse{}, nil
}

func (h *Handler) ListUpcomingPlans(ctx context.Context, req *socialv1.ListUpcomingPlansRequest) (*socialv1.ListUpcomingPlansResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	f := UpcomingFilter{CategoryID: req.GetCategoryId(), CityID: req.GetCityId(), FreeOnly: req.GetFreeOnly(), Limit: int(req.GetLimit())}
	if req.GetFrom() != nil {
		f.From = req.GetFrom().AsTime()
	}
	if req.GetTo() != nil {
		f.To = req.GetTo().AsTime()
	}
	plans, err := h.svc.ListUpcomingPlans(ctx, userID, f)
	if err != nil {
		return nil, planErr(err, "failed to list plans")
	}
	if err := h.svc.Decorate(ctx, plans, userID); err != nil {
		return nil, planErr(err, "failed to list plans")
	}
	out := &socialv1.ListUpcomingPlansResponse{}
	for _, p := range plans {
		out.Plans = append(out.Plans, toProto(p))
	}
	return out, nil
}

func (h *Handler) SavePlan(ctx context.Context, req *socialv1.SavePlanRequest) (*socialv1.SavePlanResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	if err := h.svc.SavePlan(ctx, req.GetPlanId(), userID); err != nil {
		return nil, planNotFoundOrErr(err, "failed to save plan")
	}
	return &socialv1.SavePlanResponse{}, nil
}

func (h *Handler) UnsavePlan(ctx context.Context, req *socialv1.UnsavePlanRequest) (*socialv1.UnsavePlanResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	if err := h.svc.UnsavePlan(ctx, req.GetPlanId(), userID); err != nil {
		return nil, planNotFoundOrErr(err, "failed to unsave plan")
	}
	return &socialv1.UnsavePlanResponse{}, nil
}

func (h *Handler) ListSavedPlans(ctx context.Context, _ *socialv1.ListSavedPlansRequest) (*socialv1.ListSavedPlansResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	plans, err := h.svc.ListSavedPlans(ctx, userID)
	if err != nil {
		return nil, planNotFoundOrErr(err, "failed to list saved plans")
	}
	out := &socialv1.ListSavedPlansResponse{}
	for _, p := range plans {
		out.Plans = append(out.Plans, toProto(p))
	}
	return out, nil
}

func planNotFoundOrErr(err error, msg string) error {
	switch err {
	case ErrPlanNotFound:
		return status.Error(codes.NotFound, "plan not found")
	case ErrInvalidInput:
		return status.Error(codes.InvalidArgument, err.Error())
	}
	return status.Error(codes.Internal, msg)
}
