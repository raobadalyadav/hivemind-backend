package users

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

// Handler implements socialv1.UserServiceServer — every RPC is fully
// implemented (see PRD §13.1).
type Handler struct {
	socialv1.UnimplementedUserServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) GetUser(ctx context.Context, req *socialv1.GetUserRequest) (*socialv1.User, error) {
	caller, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	// The user record carries the email: it is yours (or staff's) to read, nobody else's.
	// Other people's public data lives on their profile (ProfileService.GetProfile).
	role, _ := grpcmiddleware.RoleFromContext(ctx)
	id := req.GetUserId()
	if id == "" {
		id = caller
	}
	if id != caller && !grpcmiddleware.IsAdminRole(role) {
		return nil, status.Error(codes.NotFound, "user not found")
	}
	u, err := h.svc.GetUser(ctx, id)
	if err != nil {
		return nil, status.Error(codes.NotFound, "user not found")
	}
	return toProto(u), nil
}

func (h *Handler) UpdateUser(ctx context.Context, req *socialv1.UpdateUserRequest) (*socialv1.User, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	u, err := h.svc.UpdateUser(ctx, userID, req.GetCityId())
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to update user")
	}
	return toProto(u), nil
}

func (h *Handler) DeleteAccount(ctx context.Context, req *socialv1.DeleteAccountRequest) (*socialv1.DeleteAccountResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	if err := h.svc.DeleteAccount(ctx, userID); err != nil {
		if err == ErrActiveCommitments {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to delete account")
	}
	return &socialv1.DeleteAccountResponse{}, nil
}

func (h *Handler) RegisterDevice(ctx context.Context, req *socialv1.RegisterDeviceRequest) (*socialv1.RegisterDeviceResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	err := h.svc.RegisterDevice(ctx, userID, req.GetDeviceId(), req.GetPushToken(), req.GetPlatform())
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to register device")
	}
	return &socialv1.RegisterDeviceResponse{}, nil
}

func (h *Handler) UpdateLocation(ctx context.Context, req *socialv1.UpdateLocationRequest) (*socialv1.UpdateLocationResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	if req.GetLocation() == nil {
		return nil, status.Error(codes.InvalidArgument, "location is required")
	}
	err := h.svc.UpdateLocation(ctx, userID, req.GetLocation().GetLatitude(), req.GetLocation().GetLongitude())
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to update location")
	}
	return &socialv1.UpdateLocationResponse{}, nil
}

func toProto(u *User) *socialv1.User {
	return &socialv1.User{
		Id:          u.ID,
		Email:       u.Email,
		CityId:      u.CityID,
		AgeVerified: u.AgeVerified,
	}
}
