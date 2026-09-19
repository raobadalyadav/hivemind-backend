package moderation

import (
	"context"
	"testing"
)

func TestScreener_Screen(t *testing.T) {
	s := NewScreener()
	ctx := context.Background()

	cases := []struct {
		name    string
		body    string
		wantSev string
	}{
		{"clean text", "Looking forward to this plan, see everyone there!", "safe"},
		{"bad word", "just kill yourself already", "severe"},
		{"excessive caps", "THIS IS AN ABSOLUTELY INSANE AMAZING EVENT YOU MUST COME", "review"},
		{"repeated chars", "wow this is great!!!!!!", "review"},
		{"unsolicited link", "http://spam.example.com/click-here", "review"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			severity, _ := s.Screen(ctx, tc.body)
			if severity != tc.wantSev {
				t.Errorf("Screen(%q) severity = %q, want %q", tc.body, severity, tc.wantSev)
			}
		})
	}
}
