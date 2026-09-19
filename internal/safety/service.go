package safety

import (
	"context"
	"fmt"
	"html"
	"net/mail"
	"strings"
	"time"
	"unicode"
)

// EmailSender is satisfied by *email.Client. May be nil (no provider
// configured) — SOS then records a 'failed' delivery instead of pretending.
type EmailSender interface {
	Send(ctx context.Context, to, subject, htmlBody string) error
}

// Disclaimer is shown with every SOS result and the Safety Center. It is
// constant on purpose: the platform does not dispatch emergency services.
const Disclaimer = "HiveMind does not contact police, ambulance or fire services. " +
	"If you are in danger, call your local emergency number now."

var Guidelines = []string{
	"Meet in public places and tell someone where you're going.",
	"Trust your instincts — leave if something feels wrong.",
	"Keep payments and personal details inside the app.",
	"Report or block anyone who makes you uncomfortable.",
}

const (
	sosDedupe   = time.Minute
	sendTimeout = 5 * time.Second
)

type Service struct {
	repo            *Repository
	email           EmailSender
	emergencyNumber string
	now             func() time.Time
}

func NewService(repo *Repository, email EmailSender, emergencyNumber string) *Service {
	if emergencyNumber == "" {
		emergencyNumber = "112"
	}
	return &Service{repo: repo, email: email, emergencyNumber: emergencyNumber, now: time.Now}
}

func validPhone(p string) bool {
	if p == "" {
		return true
	}
	digits := 0
	for _, r := range p {
		switch {
		case unicode.IsDigit(r):
			digits++
		case r == '+' || r == ' ' || r == '-':
		default:
			return false
		}
	}
	return digits >= 7 && digits <= 15
}

func (s *Service) SetContact(ctx context.Context, userID string, c Contact) error {
	c.Name, c.Relationship = strings.TrimSpace(c.Name), strings.TrimSpace(c.Relationship)
	addr, err := mail.ParseAddress(strings.TrimSpace(c.Email))
	if userID == "" || c.Name == "" || len(c.Name) > 100 || err != nil || len(c.Relationship) > 50 || !validPhone(c.Phone) {
		return ErrInvalidInput
	}
	c.Email = addr.Address
	return s.repo.SetContact(ctx, userID, c)
}

func (s *Service) GetContact(ctx context.Context, userID string) (*Contact, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	return s.repo.GetContact(ctx, userID)
}

func (s *Service) DeleteContact(ctx context.Context, userID string) error {
	if userID == "" {
		return ErrInvalidInput
	}
	return s.repo.DeleteContact(ctx, userID)
}

type CenterView struct {
	*Center
	Guidelines      []string
	EmergencyNumber string
	Disclaimer      string
}

func (s *Service) SafetyCenter(ctx context.Context, userID string) (*CenterView, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	c, err := s.repo.Center(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &CenterView{Center: c, Guidelines: Guidelines, EmergencyNumber: s.emergencyNumber, Disclaimer: Disclaimer}, nil
}

type SOSResult struct {
	ID              string
	ContactNotified bool
	EmergencyNumber string
	Disclaimer      string
}

// TriggerSOS records the alert and, if the user has an emergency contact and
// an email provider is configured, emails them. It never fails the user's
// request just because delivery failed — the outcome is reported honestly in
// ContactNotified, and the response always carries the real emergency number.
func (s *Service) TriggerSOS(ctx context.Context, sos SOS, confirmed bool) (*SOSResult, error) {
	if sos.UserID == "" || len(sos.Note) > 500 {
		return nil, ErrInvalidInput
	}
	if !confirmed {
		return nil, ErrNotConfirmed
	}
	if sos.Lat != nil && (*sos.Lat < -90 || *sos.Lat > 90 || sos.Lng == nil || *sos.Lng < -180 || *sos.Lng > 180) {
		return nil, ErrInvalidInput
	}
	var plan *PlanInfo
	if sos.PlanID != "" {
		p, err := s.repo.PlanContext(ctx, sos.PlanID, sos.UserID)
		if err != nil {
			return nil, err
		}
		plan = p
	}

	res := &SOSResult{EmergencyNumber: s.emergencyNumber, Disclaimer: Disclaimer}
	id, fresh, err := s.repo.RecordSOS(ctx, sos, sosDedupe, s.now())
	if err != nil {
		return nil, err
	}
	res.ID = id
	if !fresh { // a repeat within the dedupe window: don't email the contact again
		var delivery string
		_ = s.repo.pool.QueryRow(ctx, `SELECT contact_delivery FROM sos_events WHERE id = $1`, id).Scan(&delivery)
		res.ContactNotified = delivery == "sent"
		return res, nil
	}

	contact, err := s.repo.GetContact(ctx, sos.UserID)
	if err != nil { // ErrNoContact (or a read failure): stays 'no_contact'
		return res, nil
	}
	if s.email == nil {
		_ = s.repo.SetDelivery(ctx, id, "failed", "email provider not configured")
		return res, nil
	}
	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	name := s.repo.DisplayName(ctx, sos.UserID)
	if err := s.email.Send(sendCtx, contact.Email, name+" sent an SOS alert", s.sosBody(name, contact, sos, plan)); err != nil {
		_ = s.repo.SetDelivery(ctx, id, "failed", err.Error())
		return res, nil
	}
	_ = s.repo.SetDelivery(ctx, id, "sent", "")
	res.ContactNotified = true
	return res, nil
}

func (s *Service) sosBody(name string, c *Contact, sos SOS, plan *PlanInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<p>Hi %s,</p><p><b>%s</b> pressed the SOS button in HiveMind and listed you as an emergency contact.</p>",
		html.EscapeString(c.Name), html.EscapeString(name))
	if plan != nil {
		fmt.Fprintf(&b, "<p>Plan: %s%s, starting %s.</p>", html.EscapeString(plan.Title),
			venueSuffix(plan.Venue), plan.StartsAt.UTC().Format("2 Jan 2006 15:04 MST"))
	}
	if sos.Lat != nil && sos.Lng != nil {
		fmt.Fprintf(&b, `<p>Last shared location: <a href="https://maps.google.com/?q=%.6f,%.6f">open in maps</a>.</p>`, *sos.Lat, *sos.Lng)
	}
	if sos.Note != "" {
		fmt.Fprintf(&b, "<p>Their note: %s</p>", html.EscapeString(sos.Note))
	}
	fmt.Fprintf(&b, "<p>Please try to reach them. %s</p>", html.EscapeString(Disclaimer))
	return b.String()
}

func venueSuffix(v string) string {
	if v == "" {
		return ""
	}
	return " at " + html.EscapeString(v)
}
