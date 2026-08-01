package discord

import (
	"strings"
	"testing"
)

func TestStatusEmbed(t *testing.T) {
	emb := statusEmbed("Reading `bot.go`")
	if emb.Color != colorWorking {
		t.Fatalf("color: %d", emb.Color)
	}
	if emb.Description != "🔄 Reading `bot.go`" {
		t.Fatalf("description: %q", emb.Description)
	}
	if emb.Title != "" {
		t.Fatalf("expected no title, got %q", emb.Title)
	}
}

func TestStatusEmbedDefaultAndTruncate(t *testing.T) {
	if got := statusEmbed("").Description; got != "🔄 Thinking…" {
		t.Fatalf("default: %q", got)
	}
	long := strings.Repeat("x", 300)
	got := statusEmbed(long).Description
	if !strings.HasPrefix(got, "🔄 ") || !strings.HasSuffix(got, "…") {
		t.Fatalf("truncate: %q", got)
	}
	if r := []rune(got); len(r) > 204 {
		t.Fatalf("too long: %d", len(r))
	}
}
