package profiles

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
)

// Handler implements socialv1.ProfileServiceServer — every RPC is fully
// implemented (see PRD §13.2).
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

func (h *Handler) UpdateProfile(ctx context.Context, req *socialv1.UpdateProfileRequest) (*socialv1.Profile, error) {
	p := &Profile{
		UserID:     req.GetUserId(),
		Bio:        req.GetBio(),
		Interests:  req.GetInterests(),
		Languages:  req.GetLanguages(),
		Occupation: req.GetOccupation(),
	}
	updated, err := h.svc.UpdateProfile(ctx, p)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to update profile")
	}
	return toProto(updated), nil
}

func (h *Handler) SetPrivacy(ctx context.Context, req *socialv1.SetPrivacyRequest) (*socialv1.Profile, error) {
	p, err := h.svc.SetPrivacy(ctx, req.GetUserId(), req.GetShowInParticipantPreviews())
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to set privacy")
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
