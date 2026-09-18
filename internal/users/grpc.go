package users

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
)

// Handler implements socialv1.UserServiceServer. GetUser is real;
// UpdateUser/DeleteAccount/RegisterDevice inherit
// socialv1.UnimplementedUserServiceServer — see PRD §13.1.
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
	return &socialv1.User{
		Id:          u.ID,
		Email:       u.Email,
		CityId:      u.CityID,
		AgeVerified: u.AgeVerified,
	}, nil
}
