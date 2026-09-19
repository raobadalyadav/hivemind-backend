package chat

import (
	"context"
	"fmt"
	"sort"
)

// IcebreakerGenerator is a small, always-available interface — flow.md §42
// "AI Icebreaker" without an LLM call, per the PRD's rule-based-first
// non-goal. A future LLM-backed implementation would satisfy this same
// one-method interface, swapped in at cmd/api/main.go's wiring.
type IcebreakerGenerator interface {
	Generate(ctx context.Context, memberInterests [][]string) (string, error)
}

type TemplateIcebreaker struct{}

func NewTemplateIcebreaker() TemplateIcebreaker { return TemplateIcebreaker{} }

func (TemplateIcebreaker) Generate(ctx context.Context, memberInterests [][]string) (string, error) {
	shared := intersectAll(memberInterests)
	if len(shared) > 0 {
		return fmt.Sprintf("You all like %s — ask someone what got them into it!", shared[0]), nil
	}
	return "Introduce yourself and share what you're most excited about for this plan!", nil
}

// intersectAll returns interests present in every non-empty member slice.
// A room with fewer than 2 members, or where any member has zero recorded
// interests, has no meaningful intersection — returns nil rather than a
// misleading "shared interest" of everything.
func intersectAll(memberInterests [][]string) []string {
	if len(memberInterests) < 2 {
		return nil
	}
	counts := map[string]int{}
	for _, interests := range memberInterests {
		if len(interests) == 0 {
			return nil
		}
		seen := map[string]bool{}
		for _, i := range interests {
			if !seen[i] {
				counts[i]++
				seen[i] = true
			}
		}
	}
	var shared []string
	for interest, count := range counts {
		if count == len(memberInterests) {
			shared = append(shared, interest)
		}
	}
	sort.Strings(shared) // deterministic order — map iteration isn't
	return shared
}
