package moderation

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

// Handler implements socialv1.ModerationServiceServer — every RPC is fully
// implemented (see PRD §13.16).
type Handler struct {
	socialv1.UnimplementedModerationServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) SubmitReport(ctx context.Context, req *socialv1.SubmitReportRequest) (*socialv1.ModerationCase, error) {
	reporterID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	c := &Case{
		ReporterID:  reporterID,
		SubjectType: req.GetSubjectType(),
		SubjectID:   req.GetSubjectId(),
		Reason:      req.GetReason(),
	}
	created, err := h.svc.SubmitReport(ctx, c)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to submit report")
	}
	return toProto(created), nil
}

func (h *Handler) GetCase(ctx context.Context, req *socialv1.GetCaseRequest) (*socialv1.ModerationCase, error) {
	c, err := h.svc.GetCase(ctx, req.GetId())
	if err != nil {
		return nil, status.Error(codes.NotFound, "case not found")
	}
	return toProto(c), nil
}

// ResolveCase is gated to admin/moderator roles via pkg/grpcmiddleware's
// adminMethods — it's not self-referential (acts on someone else's report)
// and has no other authorization check.
func (h *Handler) ResolveCase(ctx context.Context, req *socialv1.ResolveCaseRequest) (*socialv1.ModerationCase, error) {
	c, err := h.svc.ResolveCase(ctx, req.GetCaseId(), req.GetResolution())
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.NotFound, "case not found")
	}
	return toProto(c), nil
}

func (h *Handler) GetTrustBadges(ctx context.Context, req *socialv1.GetTrustBadgesRequest) (*socialv1.TrustBadges, error) {
	badges, err := h.svc.GetTrustBadges(ctx, req.GetUserId())
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to load trust badges")
	}
	return &socialv1.TrustBadges{Badges: badges}, nil
}

func (h *Handler) BlockUser(ctx context.Context, req *socialv1.BlockUserRequest) (*socialv1.BlockUserResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	if err := h.svc.BlockUser(ctx, userID, req.GetBlockedUserId()); err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to block user")
	}
	return &socialv1.BlockUserResponse{}, nil
}

var statusToProtoMap = map[string]socialv1.CaseStatus{
	"open":         socialv1.CaseStatus_OPEN,
	"under_review": socialv1.CaseStatus_UNDER_REVIEW,
	"resolved":     socialv1.CaseStatus_RESOLVED,
	"appealed":     socialv1.CaseStatus_APPEALED,
}

func toProto(c *Case) *socialv1.ModerationCase {
	st, ok := statusToProtoMap[c.Status]
	if !ok {
		st = socialv1.CaseStatus_CASE_STATUS_UNSPECIFIED
	}
	return &socialv1.ModerationCase{
		Id:          c.ID,
		ReporterId:  c.ReporterID,
		SubjectType: c.SubjectType,
		SubjectId:   c.SubjectID,
		Reason:      c.Reason,
		Status:      st,
		Resolution:  c.Resolution,
		Severity:    c.Severity,
		AutoFlagged: c.AutoFlagged,
	}
}
