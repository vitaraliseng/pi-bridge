package sessionstore

import (
	"path/filepath"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set("discord:1", Entry{SessionFile: "/tmp/a.jsonl", CWD: "/proj", Name: "discord-1"}); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := s2.Get("discord:1")
	if !ok {
		t.Fatal("missing entry")
	}
	if e.SessionFile != "/tmp/a.jsonl" || e.Name != "discord-1" {
		t.Fatalf("unexpected entry: %+v", e)
	}
}

func TestDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Set("k", Entry{SessionFile: "f"})
	if err := s.Delete("k"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("k"); ok {
		t.Fatal("expected deleted")
	}
}
