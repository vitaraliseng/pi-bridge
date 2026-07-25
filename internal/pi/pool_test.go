package pi

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/vitaraliseng/pi-bridge/internal/sessionstore"
)

func TestSessionName(t *testing.T) {
	cases := map[string]string{
		"discord:123456": "discord-123456",
		"discord/abc":    "discord-abc",
		"":               "session",
		"@@@":            "session",
	}
	for in, want := range cases {
		if got := SessionName(in); got != want {
			t.Fatalf("SessionName(%q)=%q want %q", in, got, want)
		}
	}
}

func TestBuildArgsNewAndResume(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	p := &Pool{
		persist:    true,
		sessionDir: "/tmp/sessions",
		log:        log,
	}

	newArgs := p.buildArgs(sessionstore.Entry{}, "discord-1")
	if !containsArg(newArgs, "--session-dir") || !containsArg(newArgs, "--name") {
		t.Fatalf("new session args = %v", newArgs)
	}

	dir := t.TempDir()
	file := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(file, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resumeArgs := p.buildArgs(sessionstore.Entry{SessionFile: file}, "discord-1")
	if !containsArg(resumeArgs, "--session") {
		t.Fatalf("resume args = %v", resumeArgs)
	}
}

func TestBuildArgsEphemeral(t *testing.T) {
	p := &Pool{persist: false, log: slog.Default()}
	args := p.buildArgs(sessionstore.Entry{}, "x")
	if !containsArg(args, "--no-session") {
		t.Fatalf("expected --no-session, got %v", args)
	}
}
