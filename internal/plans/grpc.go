package plans

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
)

// Handler implements socialv1.PlanServiceServer. CreatePlan/GetPlan are real;
// every other method inherits socialv1.UnimplementedPlanServiceServer's
// codes.Unimplemented response — see proto/social/v1/plan.proto for the
// deferred RPC list and PRD §13.4/§17 for the full spec.
type Handler struct {
	socialv1.UnimplementedPlanServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) CreatePlan(ctx context.Context, req *socialv1.CreatePlanRequest) (*socialv1.Plan, error) {
	p := &Plan{
		Title:       req.GetTitle(),
		Description: req.GetDescription(),
		CategoryID:  req.GetCategoryId(),
		HostID:      req.GetHostId(),
		CityID:      req.GetCityId(),
		VenueID:     req.GetVenueId(),
		Capacity:    req.GetCapacity(),
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

	created, err := h.svc.CreatePlan(ctx, p)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to create plan")
	}
	return toProto(created), nil
}

func (h *Handler) GetPlan(ctx context.Context, req *socialv1.GetPlanRequest) (*socialv1.Plan, error) {
	p, err := h.svc.GetPlan(ctx, req.GetId())
	if err != nil {
		return nil, status.Error(codes.NotFound, "plan not found")
	}
	return toProto(p), nil
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
