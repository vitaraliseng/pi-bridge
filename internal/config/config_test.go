package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoadYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	in := Config{
		DiscordToken:         "tok-123",
		DiscordApplicationID: "app-9",
		AllowedUserIDs:       map[string]struct{}{"111": {}, "222": {}},
		AllowedGuildIDs:      map[string]struct{}{"g1": {}},
		RequireMention:       true,
		DefaultCWD:           "/tmp/work",
		QueueSize:            32,
		Workers:              2,
		JobTimeout:           5 * time.Minute,
		PiBinary:             "pi",
		PiArgs:               []string{"--foo"},
		SessionDir:           "/tmp/sessions",
		SessionIndexPath:     "/tmp/index.json",
		PersistSessions:      true,
	}
	if err := Save(path, in); err != nil {
		t.Fatal(err)
	}

	// Ensure mode is private-ish (best-effort; Windows may differ).
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("config perms too open: %o", st.Mode().Perm())
	}

	t.Setenv("PI_BRIDGE_CONFIG", path)
	t.Setenv("DISCORD_TOKEN", "") // force file value
	// Clear overrides that might exist in the environment.
	for _, k := range []string{
		"DISCORD_APPLICATION_ID", "ALLOWED_USER_IDS", "ALLOWED_GUILD_IDS",
		"PI_CWD", "PI_BINARY", "PI_ARGS", "WORKERS", "QUEUE_SIZE", "JOB_TIMEOUT",
	} {
		t.Setenv(k, "")
	}

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.DiscordToken != "tok-123" {
		t.Fatalf("token = %q", got.DiscordToken)
	}
	if _, ok := got.AllowedUserIDs["111"]; !ok {
		t.Fatalf("missing user 111: %#v", got.AllowedUserIDs)
	}
	if _, ok := got.AllowedUserIDs["222"]; !ok {
		t.Fatalf("missing user 222: %#v", got.AllowedUserIDs)
	}
	if _, ok := got.AllowedGuildIDs["g1"]; !ok {
		t.Fatalf("missing guild g1: %#v", got.AllowedGuildIDs)
	}
	if got.Workers != 2 || got.QueueSize != 32 {
		t.Fatalf("workers/queue = %d/%d", got.Workers, got.QueueSize)
	}
	if got.JobTimeout != 5*time.Minute {
		t.Fatalf("timeout = %v", got.JobTimeout)
	}
	if got.DefaultCWD != "/tmp/work" {
		t.Fatalf("cwd = %q", got.DefaultCWD)
	}
}

func TestEnvOverridesYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := Save(path, Config{
		DiscordToken:   "file-token",
		AllowedUserIDs: map[string]struct{}{"1": {}},
		Workers:        1,
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_BRIDGE_CONFIG", path)
	t.Setenv("DISCORD_TOKEN", "env-token")
	t.Setenv("ALLOWED_USER_IDS", "9,8")
	t.Setenv("WORKERS", "4")

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.DiscordToken != "env-token" {
		t.Fatalf("token = %q", got.DiscordToken)
	}
	if _, ok := got.AllowedUserIDs["9"]; !ok {
		t.Fatalf("env allowlist not applied: %#v", got.AllowedUserIDs)
	}
	if got.Workers != 4 {
		t.Fatalf("workers = %d", got.Workers)
	}
}

func TestLoadLegacyEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pi-bridge.env")
	content := "DISCORD_TOKEN=legacy\nALLOWED_USER_IDS=42,43\nREQUIRE_MENTION=false\nWORKERS=3\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_BRIDGE_CONFIG", path)
	for _, k := range []string{"DISCORD_TOKEN", "ALLOWED_USER_IDS", "REQUIRE_MENTION", "WORKERS"} {
		t.Setenv(k, "")
	}

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.DiscordToken != "legacy" {
		t.Fatalf("token = %q", got.DiscordToken)
	}
	if got.RequireMention {
		t.Fatal("expected require_mention false")
	}
	if _, ok := got.AllowedUserIDs["42"]; !ok {
		t.Fatalf("users = %#v", got.AllowedUserIDs)
	}
	if got.Workers != 3 {
		t.Fatalf("workers = %d", got.Workers)
	}
}

func TestSaveConvertsLegacyPathToYAML(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "config.env")
	if err := Save(legacy, Config{DiscordToken: "x", AllowedUserIDs: map[string]struct{}{"1": {}}}); err != nil {
		t.Fatal(err)
	}
	yamlPath := filepath.Join(dir, "config.yaml")
	if _, err := os.Stat(yamlPath); err != nil {
		t.Fatalf("expected yaml write at %s: %v", yamlPath, err)
	}
}
