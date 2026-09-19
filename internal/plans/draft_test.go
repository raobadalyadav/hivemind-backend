package plans

import (
	"context"
	"testing"
)

func TestTemplateDraftGenerator_Suggest(t *testing.T) {
	gen := NewTemplateDraftGenerator()
	ctx := context.Background()

	t.Run("known category", func(t *testing.T) {
		title, desc := gen.Suggest(ctx, "Sports")
		if title == "" || desc == "" {
			t.Error("expected non-empty title/description for a known category")
		}
	})

	t.Run("unknown category falls back to generic template", func(t *testing.T) {
		title, desc := gen.Suggest(ctx, "Underwater Basket Weaving")
		if title == "" || desc == "" {
			t.Error("expected non-empty fallback title/description")
		}
	})

	t.Run("empty category", func(t *testing.T) {
		title, desc := gen.Suggest(ctx, "")
		if title == "" || desc == "" {
			t.Error("expected non-empty fallback title/description for empty category")
		}
	})
}
