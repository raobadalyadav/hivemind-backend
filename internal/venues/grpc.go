package venues

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

// Handler implements socialv1.VenueServiceServer — every RPC is fully
// implemented (see PRD §13.12).
type Handler struct {
	socialv1.UnimplementedVenueServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) CreateVenue(ctx context.Context, req *socialv1.CreateVenueRequest) (*socialv1.Venue, error) {
	ownerID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	v := &Venue{
		OwnerHostID: ownerID,
		CityID:      req.GetCityId(),
		Name:        req.GetName(),
		Address:     req.GetAddress(),
		Capacity:    req.GetCapacity(),
	}
	if req.GetLocation() != nil {
		lat, lng := req.GetLocation().GetLatitude(), req.GetLocation().GetLongitude()
		v.Latitude, v.Longitude = &lat, &lng
	}
	created, err := h.svc.CreateVenue(ctx, v)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to create venue")
	}
	return toProto(created), nil
}

func (h *Handler) GetVenue(ctx context.Context, req *socialv1.GetVenueRequest) (*socialv1.Venue, error) {
	v, err := h.svc.GetVenue(ctx, req.GetId())
	if err != nil {
		return nil, status.Error(codes.NotFound, "venue not found")
	}
	return toProto(v), nil
}

func (h *Handler) ListMyVenues(ctx context.Context, req *socialv1.ListMyVenuesRequest) (*socialv1.ListMyVenuesResponse, error) {
	ownerID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	list, err := h.svc.ListMyVenues(ctx, ownerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list venues")
	}
	out := make([]*socialv1.Venue, 0, len(list))
	for _, v := range list {
		out = append(out, toProto(v))
	}
	return &socialv1.ListMyVenuesResponse{Venues: out}, nil
}

func (h *Handler) UpdateVenue(ctx context.Context, req *socialv1.UpdateVenueRequest) (*socialv1.Venue, error) {
	callerID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	v, err := h.svc.UpdateVenue(ctx, req.GetId(), callerID, req.GetName(), req.GetAddress(), req.GetCapacity())
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrForbidden:
			return nil, status.Error(codes.PermissionDenied, err.Error())
		case ErrVenueNotFound:
			return nil, status.Error(codes.NotFound, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to update venue")
		}
	}
	return toProto(v), nil
}

func (h *Handler) GetVenueDashboard(ctx context.Context, req *socialv1.GetVenueDashboardRequest) (*socialv1.VenueDashboard, error) {
	callerID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	d, err := h.svc.GetVenueDashboard(ctx, req.GetVenueId(), callerID)
	if err != nil {
		switch err {
		case ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case ErrForbidden:
			return nil, status.Error(codes.PermissionDenied, err.Error())
		case ErrVenueNotFound:
			return nil, status.Error(codes.NotFound, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to load venue dashboard")
		}
	}
	return &socialv1.VenueDashboard{
		TotalBookings:  d.TotalBookings,
		TotalAttendees: d.TotalAttendees,
		GrossRevenue:   &socialv1.Money{MinorUnits: d.GrossRevenueMinor, Currency: "INR"},
		AvgRating:      d.AvgRating,
	}, nil
}

func toProto(v *Venue) *socialv1.Venue {
	out := &socialv1.Venue{
		Id:          v.ID,
		OwnerHostId: v.OwnerHostID,
		CityId:      v.CityID,
		Name:        v.Name,
		Address:     v.Address,
		Capacity:    v.Capacity,
	}
	if v.Latitude != nil && v.Longitude != nil {
		out.Location = &socialv1.GeoPoint{Latitude: *v.Latitude, Longitude: *v.Longitude}
	}
	return out
}
