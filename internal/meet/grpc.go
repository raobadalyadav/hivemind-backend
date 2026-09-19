package meet

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

type Handler struct {
	socialv1.UnimplementedMeetServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func meetErr(err error, fallback string) error {
	switch err {
	case ErrInvalidInput:
		return status.Error(codes.InvalidArgument, err.Error())
	case ErrTargetNotFound:
		return status.Error(codes.NotFound, err.Error())
	case ErrRateLimited:
		return status.Error(codes.ResourceExhausted, err.Error())
	case ErrAlreadyBoosted, ErrInsufficientCredits:
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	return status.Error(codes.Internal, fallback)
}

func caller(ctx context.Context) (string, error) {
	id, ok := grpcmiddleware.UserIDFromContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "auth required")
	}
	return id, nil
}

func (h *Handler) GetDeck(ctx context.Context, req *socialv1.GetDeckRequest) (*socialv1.GetDeckResponse, error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	cards, err := h.svc.GetDeck(ctx, uid, int(req.GetLimit()), int(req.GetMinAge()), int(req.GetMaxAge()))
	if err != nil {
		return nil, meetErr(err, "failed to load deck")
	}
	out := &socialv1.GetDeckResponse{}
	for _, c := range cards {
		pc := &socialv1.MeetCard{
			UserId: c.UserID, DisplayName: c.DisplayName, Age: c.Age, CityName: c.CityName, Bio: c.Bio,
			Occupation: c.Occupation, Education: c.Education, Interests: c.Interests, Hobbies: c.Hobbies,
			Intents: c.Intents, SelfieVerified: c.Verified, SharedInterests: c.Shared, WavedAtYou: c.WavedAtYou,
			Boosted: c.Boosted, CommonCommunities: c.CommonCommunities,
		}
		for _, p := range c.Photos {
			pc.Photos = append(pc.Photos, &socialv1.MeetPhoto{Url: p.URL, ThumbUrl: p.ThumbURL, Width: p.Width, Height: p.Height})
		}
		out.Cards = append(out.Cards, pc)
	}
	return out, nil
}

var actionFromProto = map[socialv1.SwipeAction]string{
	socialv1.SwipeAction_SWIPE_ACTION_PASS:  "pass",
	socialv1.SwipeAction_SWIPE_ACTION_WAVE:  "wave",
	socialv1.SwipeAction_SWIPE_ACTION_SUPER: "super",
}

func (h *Handler) Swipe(ctx context.Context, req *socialv1.SwipeRequest) (*socialv1.SwipeResponse, error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	r, err := h.svc.Swipe(ctx, uid, req.GetTargetId(), actionFromProto[req.GetAction()])
	if err != nil {
		return nil, meetErr(err, "failed to record swipe")
	}
	return &socialv1.SwipeResponse{Matched: r.Matched, RoomId: r.RoomID, TargetName: r.TargetName}, nil
}

func (h *Handler) ListWaves(ctx context.Context, req *socialv1.ListWavesRequest) (*socialv1.ListWavesResponse, error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	list, err := h.svc.ListWaves(ctx, uid, req.GetDirection() == socialv1.WaveDirection_WAVE_DIRECTION_SENT)
	if err != nil {
		return nil, meetErr(err, "failed to list waves")
	}
	out := &socialv1.ListWavesResponse{}
	for _, w := range list {
		out.Waves = append(out.Waves, &socialv1.Wave{
			UserId: w.UserID, DisplayName: w.DisplayName, Age: w.Age, PhotoUrl: w.PhotoURL, SelfieVerified: w.Verified,
			Super: w.Super, Matched: w.Matched, RoomId: w.RoomID, CreatedAt: timestamppb.New(w.CreatedAt),
		})
	}
	return out, nil
}

func boostToProto(b *Boost) *socialv1.BoostStatus {
	out := &socialv1.BoostStatus{
		Active: b.Active, FreeAvailable: b.FreeAvailable, CostMinor: BoostCostMinor, Currency: Currency,
		BalanceMinor: b.BalanceMinor, DurationMinutes: int32(BoostDuration.Minutes()),
	}
	if b.Active {
		out.EndsAt = timestamppb.New(b.EndsAt)
	}
	if !b.FreeAgainAt.IsZero() {
		out.FreeAgainAt = timestamppb.New(b.FreeAgainAt)
	}
	return out
}

func (h *Handler) GetBoostStatus(ctx context.Context, _ *socialv1.GetBoostStatusRequest) (*socialv1.BoostStatus, error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	b, err := h.svc.BoostStatus(ctx, uid)
	if err != nil {
		return nil, meetErr(err, "failed to load boost")
	}
	return boostToProto(b), nil
}

func (h *Handler) ActivateBoost(ctx context.Context, _ *socialv1.ActivateBoostRequest) (*socialv1.BoostStatus, error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	b, err := h.svc.ActivateBoost(ctx, uid)
	if err != nil {
		return nil, meetErr(err, "failed to activate boost")
	}
	return boostToProto(b), nil
}
