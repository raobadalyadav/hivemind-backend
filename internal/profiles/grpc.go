package profiles

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
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
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	p := &Profile{
		UserID:      userID,
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
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	u := &Update{
		UserID:     userID,
		Bio:        req.Bio,
		Occupation: req.Occupation,
		Gender:     req.Gender,
		Education:  req.Education,
		Interests:  req.GetInterests(),
		Languages:  req.GetLanguages(),
		Hobbies:    req.GetHobbies(),
	}
	updated, err := h.svc.UpdateProfile(ctx, u)
	if err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to update profile")
	}
	return toProto(updated), nil
}

func (h *Handler) SetPrivacy(ctx context.Context, req *socialv1.SetPrivacyRequest) (*socialv1.Profile, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	p, err := h.svc.SetPrivacy(ctx, userID, req.GetShowInParticipantPreviews())
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
		Gender:             p.Gender,
		Education:          p.Education,
		Hobbies:            p.Hobbies,
		Photos:             photosToProto(p.Photos),
		SelfieVerified:     p.SelfieVerified,
	}
}

func photosToProto(ps []Photo) []*socialv1.ProfilePhoto {
	out := make([]*socialv1.ProfilePhoto, 0, len(ps))
	for _, p := range ps {
		out = append(out, &socialv1.ProfilePhoto{Id: p.ID, Url: p.URL, Position: p.Position, ThumbUrl: p.ThumbURL, Width: p.Width, Height: p.Height})
	}
	return out
}

func prefsToProto(p *Prefs) *socialv1.UserPreferences {
	return &socialv1.UserPreferences{
		Intents: p.Intents, GroupPref: p.GroupPref, EnergyPref: p.EnergyPref,
		PlanningPref: p.PlanningPref, TimePref: p.TimePref, SettingPref: p.SettingPref,
	}
}

func mapErr(err error, msg string) error {
	switch err {
	case ErrInvalidInput:
		return status.Error(codes.InvalidArgument, err.Error())
	case ErrMediaUnavailable:
		return status.Error(codes.Unimplemented, err.Error())
	case ErrTooManyPhotos:
		return status.Error(codes.FailedPrecondition, err.Error())
	case ErrPhotoNotFound:
		return status.Error(codes.NotFound, err.Error())
	case ErrForbidden:
		return status.Error(codes.PermissionDenied, err.Error())
	}
	return status.Error(codes.Internal, msg)
}

func (h *Handler) AddProfilePhoto(ctx context.Context, req *socialv1.AddProfilePhotoRequest) (*socialv1.ProfilePhoto, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	p, err := h.svc.AddPhoto(ctx, userID, req.GetMediaId())
	if err != nil {
		return nil, mapErr(err, "failed to add photo")
	}
	return &socialv1.ProfilePhoto{Id: p.ID, Url: p.URL, Position: p.Position, ThumbUrl: p.ThumbURL, Width: p.Width, Height: p.Height}, nil
}

func (h *Handler) DeleteProfilePhoto(ctx context.Context, req *socialv1.DeleteProfilePhotoRequest) (*socialv1.DeleteProfilePhotoResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	if err := h.svc.DeletePhoto(ctx, userID, req.GetPhotoId()); err != nil {
		return nil, mapErr(err, "failed to delete photo")
	}
	return &socialv1.DeleteProfilePhotoResponse{}, nil
}

func (h *Handler) ReorderProfilePhotos(ctx context.Context, req *socialv1.ReorderProfilePhotosRequest) (*socialv1.ReorderProfilePhotosResponse, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	ps, err := h.svc.ReorderPhotos(ctx, userID, req.GetPhotoIds())
	if err != nil {
		return nil, mapErr(err, "failed to reorder photos")
	}
	return &socialv1.ReorderProfilePhotosResponse{Photos: photosToProto(ps)}, nil
}

func (h *Handler) ListInterestCatalog(ctx context.Context, _ *socialv1.ListInterestCatalogRequest) (*socialv1.ListInterestCatalogResponse, error) {
	in, cat, err := h.svc.InterestCatalog(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load catalog")
	}
	return &socialv1.ListInterestCatalogResponse{Interests: in, Categories: cat, MinSelectable: MinInterests, MaxSelectable: MaxInterests}, nil
}

func (h *Handler) SetSocialIntent(ctx context.Context, req *socialv1.SetSocialIntentRequest) (*socialv1.UserPreferences, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	p, err := h.svc.SetIntents(ctx, userID, req.GetIntents())
	if err != nil {
		return nil, mapErr(err, "failed to save intent")
	}
	return prefsToProto(p), nil
}

func (h *Handler) SetPersonality(ctx context.Context, req *socialv1.SetPersonalityRequest) (*socialv1.UserPreferences, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	p, err := h.svc.SetPersonality(ctx, userID, &Prefs{
		GroupPref: req.GetGroupPref(), EnergyPref: req.GetEnergyPref(), PlanningPref: req.GetPlanningPref(),
		TimePref: req.GetTimePref(), SettingPref: req.GetSettingPref(),
	})
	if err != nil {
		return nil, mapErr(err, "failed to save personality")
	}
	return prefsToProto(p), nil
}

func (h *Handler) GetMyPreferences(ctx context.Context, _ *socialv1.GetMyPreferencesRequest) (*socialv1.UserPreferences, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	p, err := h.svc.GetPrefs(ctx, userID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load preferences")
	}
	return prefsToProto(p), nil
}
