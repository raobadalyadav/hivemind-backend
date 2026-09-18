package profiles

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
)

// Handler implements socialv1.ProfileServiceServer. CreateProfile/GetProfile
// are real; UpdateProfile/SetPrivacy inherit
// socialv1.UnimplementedProfileServiceServer — see PRD §13.2.
type Handler struct {
	socialv1.UnimplementedProfileServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) CreateProfile(ctx context.Context, req *socialv1.CreateProfileRequest) (*socialv1.Profile, error) {
	p := &Profile{
		UserID:      req.GetUserId(),
		DisplayName: req.GetDisplayName(),
		Interests:   req.GetInterests(),
	}
	created, err := h.svc.CreateProfile(ctx, p)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to create profile")
	}
	return toProto(created), nil
}

func (h *Handler) GetProfile(ctx context.Context, req *socialv1.GetProfileRequest) (*socialv1.Profile, error) {
	p, err := h.svc.GetProfile(ctx, req.GetUserId())
	if err != nil {
		return nil, status.Error(codes.NotFound, "profile not found")
	}
	return toProto(p), nil
}

func toProto(p *Profile) *socialv1.Profile {
	return &socialv1.Profile{
		UserId:             p.UserID,
		DisplayName:        p.DisplayName,
		Bio:                p.Bio,
		Interests:          p.Interests,
		Languages:          p.Languages,
		Occupation:         p.Occupation,
		VerificationStatus: p.VerificationStatus,
	}
}
