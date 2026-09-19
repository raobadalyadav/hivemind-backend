package safety

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	socialv1 "github.com/hivemind/backend/gen/social/v1"
	"github.com/hivemind/backend/pkg/grpcmiddleware"
)

type Handler struct {
	socialv1.UnimplementedSafetyServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func safetyErr(err error, fallback string) error {
	switch err {
	case ErrInvalidInput, ErrNotConfirmed:
		return status.Error(codes.InvalidArgument, err.Error())
	case ErrPlanNotAllowed:
		return status.Error(codes.PermissionDenied, err.Error())
	case ErrNoContact:
		return status.Error(codes.NotFound, err.Error())
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

func contactToProto(c *Contact) *socialv1.EmergencyContact {
	return &socialv1.EmergencyContact{Name: c.Name, Email: c.Email, Phone: c.Phone, Relationship: c.Relationship}
}

func (h *Handler) GetSafetyCenter(ctx context.Context, _ *socialv1.GetSafetyCenterRequest) (*socialv1.SafetyCenter, error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	v, err := h.svc.SafetyCenter(ctx, uid)
	if err != nil {
		return nil, safetyErr(err, "failed to load safety center")
	}
	return &socialv1.SafetyCenter{
		VerificationStatus: v.VerificationStatus, HasEmergencyContact: v.HasContact, BlockedCount: v.BlockedCount,
		Guidelines: v.Guidelines, EmergencyNumber: v.EmergencyNumber, Disclaimer: v.Disclaimer,
	}, nil
}

func (h *Handler) SetEmergencyContact(ctx context.Context, req *socialv1.SetEmergencyContactRequest) (*socialv1.EmergencyContact, error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	c := Contact{Name: req.GetName(), Email: req.GetEmail(), Phone: req.GetPhone(), Relationship: req.GetRelationship()}
	if err := h.svc.SetContact(ctx, uid, c); err != nil {
		return nil, safetyErr(err, "failed to save contact")
	}
	saved, err := h.svc.GetContact(ctx, uid)
	if err != nil {
		return nil, safetyErr(err, "failed to load contact")
	}
	return contactToProto(saved), nil
}

func (h *Handler) GetEmergencyContact(ctx context.Context, _ *socialv1.GetEmergencyContactRequest) (*socialv1.EmergencyContact, error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	c, err := h.svc.GetContact(ctx, uid)
	if err != nil {
		return nil, safetyErr(err, "failed to load contact")
	}
	return contactToProto(c), nil
}

func (h *Handler) DeleteEmergencyContact(ctx context.Context, _ *socialv1.DeleteEmergencyContactRequest) (*socialv1.DeleteEmergencyContactResponse, error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.svc.DeleteContact(ctx, uid); err != nil {
		return nil, safetyErr(err, "failed to delete contact")
	}
	return &socialv1.DeleteEmergencyContactResponse{}, nil
}

func (h *Handler) TriggerSOS(ctx context.Context, req *socialv1.TriggerSOSRequest) (*socialv1.TriggerSOSResponse, error) {
	uid, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	sos := SOS{UserID: uid, PlanID: req.GetPlanId(), Note: req.GetNote()}
	if req.GetHasLocation() {
		lat, lng := req.GetLatitude(), req.GetLongitude()
		sos.Lat, sos.Lng = &lat, &lng
	}
	r, err := h.svc.TriggerSOS(ctx, sos, req.GetConfirmed())
	if err != nil {
		return nil, safetyErr(err, "failed to record SOS")
	}
	return &socialv1.TriggerSOSResponse{
		SosId: r.ID, ContactNotified: r.ContactNotified, EmergencyNumber: r.EmergencyNumber, Disclaimer: r.Disclaimer,
	}, nil
}
