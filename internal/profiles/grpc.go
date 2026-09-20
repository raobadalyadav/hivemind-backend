package profiles

import (
	"context"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

// Handler implements socialv1.ProfileServiceServer — every RPC is fully
// implemented (see PRD §13.2).
type Handler struct {
	socialv1.UnimplementedProfileServiceServer
	svc   *Service
	posts PostCounter
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
	caller, _ := grpcmiddleware.UserIDFromContext(ctx)
	if caller != "" && caller != req.GetUserId() {
		// A blocked (either way) or deactivated person's profile doesn't load.
		if ok, err := h.svc.repo.CanView(ctx, caller, req.GetUserId()); err == nil && !ok {
			return nil, status.Error(codes.NotFound, "profile not found")
		}
	}
	p, err := h.svc.GetProfile(ctx, req.GetUserId())
	if err != nil {
		return nil, status.Error(codes.NotFound, "profile not found")
	}
	out := toProto(p)
	if caller != p.UserID {
		out.ShowInParticipantPreviews = false // someone else's privacy settings aren't yours to read
		out.HideProfileViews = false
	}
	return out, nil
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
	p, err := h.svc.SetPrivacy(ctx, userID, req.ShowInParticipantPreviews, req.HideProfileViews)
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
		UserId:                    p.UserID,
		DisplayName:               p.DisplayName,
		Bio:                       p.Bio,
		Interests:                 p.Interests,
		Languages:                 p.Languages,
		Occupation:                p.Occupation,
		VerificationStatus:        p.VerificationStatus,
		Gender:                    p.Gender,
		Education:                 p.Education,
		Hobbies:                   p.Hobbies,
		Photos:                    photosToProto(p.Photos),
		SelfieVerified:            p.SelfieVerified,
		ShowInParticipantPreviews: p.ShowInPreviews,
		HideProfileViews:          p.HideProfileViews,
		Audit:                     &socialv1.Audit{CreatedAt: timestamppb.New(p.CreatedAt)},
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

func (h *Handler) GetMyStats(ctx context.Context, _ *socialv1.GetMyStatsRequest) (*socialv1.MyStats, error) {
	userID, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	st, err := h.svc.GetMyStats(ctx, userID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load stats")
	}
	return &socialv1.MyStats{PlansAttended: st.PlansAttended, PlansUpcoming: st.PlansUpcoming, Connections: st.Connections, Communities: st.Communities}, nil
}

// PostCounter counts the posts of an author that a viewer may open (implemented by social.Repository).
type PostCounter interface {
	CountVisibleByAuthor(ctx context.Context, authorID, viewerID string) (int32, error)
}

// WithPostCounter enables the posts count in GetUserStats.
func (h *Handler) WithPostCounter(c PostCounter) *Handler {
	h.posts = c
	return h
}

func (h *Handler) GetUserStats(ctx context.Context, req *socialv1.GetUserStatsRequest) (*socialv1.UserStats, error) {
	caller, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	st, err := h.svc.GetUserStats(ctx, caller, req.GetUserId())
	if err != nil {
		if err == ErrBlockedOrGone || err == pgx.ErrNoRows {
			return nil, status.Error(codes.NotFound, "profile not found")
		}
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to load stats")
	}
	out := &socialv1.UserStats{
		Connections: st.Connections, PlansAttended: st.PlansAttended, MutualConnections: st.MutualConnections,
		SharedCommunities: st.SharedCommunities, SharedCommunityNames: st.SharedCommunityNames,
		MemberSince: timestamppb.New(st.MemberSince), HostRatingAvg: st.HostRatingAvg, HostRatingCount: st.HostRatingCount,
		PlansHosted: st.PlansHosted,
	}
	if h.posts != nil {
		if n, err := h.posts.CountVisibleByAuthor(ctx, req.GetUserId(), caller); err == nil {
			out.Posts = n
		}
	}
	return out, nil
}

func (h *Handler) RecordProfileView(ctx context.Context, req *socialv1.RecordProfileViewRequest) (*socialv1.RecordProfileViewResponse, error) {
	caller, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "auth required")
	}
	if err := h.svc.RecordProfileView(ctx, caller, req.GetUserId()); err != nil {
		if err == ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to record view")
	}
	return &socialv1.RecordProfileViewResponse{}, nil
}
