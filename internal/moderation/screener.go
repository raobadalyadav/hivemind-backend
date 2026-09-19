package moderation

import (
	"context"
	"strings"
)

// ContentScreener is declared here and duplicated locally in chat/social
// (same no-cross-import convention as ReportSubmitter) — satisfied by
// *Screener, wired in cmd/api/main.go. Returns primitives, not a shared
// struct type, so the interface can be declared independently in each
// consumer package (a locally-declared struct type wouldn't satisfy an
// identically-shaped interface declared in a different package — Go
// requires exact return-type identity, not just structural equivalence).
type ContentScreener interface {
	Screen(ctx context.Context, body string) (severity, reason string)
}

// defaultBadWords is a small curated list, not a real classifier — matches
// the PRD's explicit "rule-based, not heavy ML" non-goal for this phase.
// ponytail: hardcoded list; swap for a config-driven or vendor list if
// false-negative complaints show up with real content.
var defaultBadWords = []string{
	"kill yourself", "nigger", "faggot",
}

// Screener never errors and never needs a nil-check — unlike EmailSender/
// PushSender/GatewayClient (truly-optional external services), this is a
// pure function with no external dependency, so it's always available.
type Screener struct {
	badWords []string
}

func NewScreener() *Screener {
	return &Screener{badWords: defaultBadWords}
}

func (s *Screener) Screen(ctx context.Context, body string) (severity, reason string) {
	lower := strings.ToLower(body)
	for _, w := range s.badWords {
		if strings.Contains(lower, w) {
			return "severe", "prohibited language"
		}
	}

	if len(body) > 20 {
		upper := 0
		for _, r := range body {
			if r >= 'A' && r <= 'Z' {
				upper++
			}
		}
		if float64(upper)/float64(len(body)) > 0.7 {
			return "review", "excessive caps"
		}
	}

	if hasRepeatedRun(body, 6) {
		return "review", "repeated characters"
	}

	if strings.HasPrefix(strings.TrimSpace(lower), "http") {
		return "review", "unsolicited link"
	}

	return "safe", ""
}

// hasRepeatedRun reports whether body contains the same rune repeated n or
// more times consecutively (e.g. "!!!!!!" or "aaaaaa").
func hasRepeatedRun(body string, n int) bool {
	runs := []rune(body)
	count := 1
	for i := 1; i < len(runs); i++ {
		if runs[i] == runs[i-1] {
			count++
			if count >= n {
				return true
			}
		} else {
			count = 1
		}
	}
	return false
}
