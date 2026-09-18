package users

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
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
	u, err := h.svc.GetUser(ctx, req.GetUserId())
	if err != nil {
		return nil, status.Error(codes.NotFound, "user not found")
	}
	return toProto(u), nil
}

func (h *Handler) UpdateUser(ctx context.Context, req *socialv1.UpdateUserRequest) (*socialv1.User, error) {
	u, err := h.svc.UpdateUser(ctx, req.GetUserId(), req.GetCityId())
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to update user")
	}
	return toProto(u), nil
}

func (h *Handler) DeleteAccount(ctx context.Context, req *socialv1.DeleteAccountRequest) (*socialv1.DeleteAccountResponse, error) {
	if err := h.svc.DeleteAccount(ctx, req.GetUserId()); err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to delete account")
	}
	return &socialv1.DeleteAccountResponse{}, nil
}

func (h *Handler) RegisterDevice(ctx context.Context, req *socialv1.RegisterDeviceRequest) (*socialv1.RegisterDeviceResponse, error) {
	err := h.svc.RegisterDevice(ctx, req.GetUserId(), req.GetDeviceId(), req.GetPushToken(), req.GetPlatform())
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to register device")
	}
	return &socialv1.RegisterDeviceResponse{}, nil
}

func toProto(u *User) *socialv1.User {
	return &socialv1.User{
		Id:          u.ID,
		Email:       u.Email,
		CityId:      u.CityID,
		AgeVerified: u.AgeVerified,
	}
}
