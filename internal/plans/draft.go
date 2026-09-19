package plans

import (
	"context"
	"strings"
)

// DraftGenerator is a small, always-available (never nil, never errors)
// interface — flow.md §43 "AI Plan Creator" without an LLM call, per the
// PRD's explicit rule-based-first non-goal for this phase. A future
// LLM-backed implementation would satisfy this same one-method interface
// and get swapped in at cmd/api/main.go's wiring, with no caller changes.
type DraftGenerator interface {
	Suggest(ctx context.Context, categoryName string) (title, description string)
}

type TemplateDraftGenerator struct{}

func NewTemplateDraftGenerator() TemplateDraftGenerator { return TemplateDraftGenerator{} }

// draftTemplates is a small curated map, not a config table — nothing has
// asked for admin-editable templates yet.
var draftTemplates = map[string]struct{ Title, Description string }{
	"sports":     {"Weekend Pickup Game", "Casual, all-levels-welcome game — bring water and good energy!"},
	"food":       {"Foodie Meetup", "Exploring a new spot together — come hungry and ready to chat."},
	"music":      {"Live Music Night", "Catching a show together — great way to meet fellow music lovers."},
	"outdoors":   {"Outdoor Adventure", "A relaxed outdoor hangout — dress for the weather and bring curiosity."},
	"networking": {"Casual Networking Meetup", "Low-pressure meetup to swap ideas and make new connections."},
}

func (TemplateDraftGenerator) Suggest(ctx context.Context, categoryName string) (title, description string) {
	if tmpl, ok := draftTemplates[strings.ToLower(categoryName)]; ok {
		return tmpl.Title, tmpl.Description
	}
	if categoryName == "" {
		return "New Plan", "Join us for a get-together!"
	}
	return "New " + categoryName + " Plan", "Join us for a " + categoryName + " get-together!"
}
