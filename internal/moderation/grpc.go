package moderation

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
)

// Handler implements socialv1.ModerationServiceServer. SubmitReport/GetCase
// are real; ResolveCase/BlockUser inherit
// socialv1.UnimplementedModerationServiceServer — see PRD §13.16.
type Handler struct {
	socialv1.UnimplementedModerationServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) SubmitReport(ctx context.Context, req *socialv1.SubmitReportRequest) (*socialv1.ModerationCase, error) {
	c := &Case{
		ReporterID:  req.GetReporterId(),
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
	}
}
