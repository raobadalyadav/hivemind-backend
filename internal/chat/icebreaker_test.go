package chat

import (
	"context"
	"testing"
)

func TestTemplateIcebreaker_Generate(t *testing.T) {
	gen := NewTemplateIcebreaker()
	ctx := context.Background()

	t.Run("shared interest", func(t *testing.T) {
		text, err := gen.Generate(ctx, [][]string{{"hiking", "coffee"}, {"coffee", "reading"}})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if text != "You all like coffee — ask someone what got them into it!" {
			t.Errorf("unexpected icebreaker text: %q", text)
		}
	})

	t.Run("no overlap falls back to generic prompt", func(t *testing.T) {
		text, err := gen.Generate(ctx, [][]string{{"hiking"}, {"reading"}})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if text == "" {
			t.Error("expected a non-empty fallback icebreaker")
		}
	})

	t.Run("single member has no intersection", func(t *testing.T) {
		text, err := gen.Generate(ctx, [][]string{{"hiking"}})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if text == "" {
			t.Error("expected a non-empty fallback icebreaker")
		}
	})
}
