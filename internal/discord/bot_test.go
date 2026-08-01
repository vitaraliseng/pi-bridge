package discord

import (
	"strings"
	"testing"
	"time"
)

func TestMarkSeenDedupes(t *testing.T) {
	t.Parallel()
	b := &Bot{seen: make(map[string]time.Time)}

	if !b.markSeen("m1") {
		t.Fatal("first sighting should process")
	}
	if b.markSeen("m1") {
		t.Fatal("duplicate should be dropped")
	}
	if !b.markSeen("m2") {
		t.Fatal("different id should process")
	}
}

func TestThreadNameTruncates(t *testing.T) {
	t.Parallel()
	name := threadName(strings.Repeat("a", 120), "user")
	if n := len([]rune(name)); n > 100 {
		t.Fatalf("thread name too long: %d runes (%q)", n, name)
	}
}
