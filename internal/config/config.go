package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is runtime configuration loaded from YAML + optional environment overrides.
type Config struct {
	DiscordToken         string
	DiscordApplicationID string
	// AllowedUserIDs restricts who may talk to the bot (DMs and guilds).
	// Empty means nobody is allowed (fail closed).
	AllowedUserIDs map[string]struct{}
	// AllowedGuildIDs restricts which Discord servers (guilds) the bot accepts.
	// Empty means any server the bot is in (user allowlist still applies).
	AllowedGuildIDs map[string]struct{}
	RequireMention  bool
	// Home is the assistant workspace (identity, memory, default tools cwd).
	// Default: $XDG_CONFIG_HOME/pi-bridge/home (or ~/.config/pi-bridge/home).
	Home string
	// WorkRoot is where the agent clones and keeps code checkouts.
	// Default: $XDG_CONFIG_HOME/pi-bridge/work (hidden product data).
	WorkRoot string
	// DefaultCWD is the working directory passed to pi. Defaults to Home.
	DefaultCWD string
	QueueSize  int
	Workers    int
	JobTimeout time.Duration
	PiBinary   string
	// Extra args after --mode rpc.
	PiArgs []string

	// SessionDir is where pi writes session JSONL files.
	SessionDir string
	// SessionIndexPath maps Discord threads -> session files.
	SessionIndexPath string
	// PersistSessions resumes pi sessions across restarts (default true).
	PersistSessions bool

	// ConfigPath is where file-backed settings were loaded from (if any).
	ConfigPath string
}

// fileConfig is the on-disk YAML shape.
type fileConfig struct {
	Discord fileDiscord `yaml:"discord"`
	Pi      filePi      `yaml:"pi"`
	Bridge  fileBridge  `yaml:"bridge"`
}

type fileDiscord struct {
	Token           string   `yaml:"token"`
	ApplicationID   string   `yaml:"application_id"`
	AllowedUserIDs  []string `yaml:"allowed_user_ids"`
	AllowedGuildIDs []string `yaml:"allowed_guild_ids"`
	RequireMention  *bool    `yaml:"require_mention"`
}

type filePi struct {
	Home            string   `yaml:"home"`
	WorkRoot        string   `yaml:"work_root"`
	CWD             string   `yaml:"cwd"`
	Binary          string   `yaml:"binary"`
	Args            []string `yaml:"args"`
	PersistSessions *bool    `yaml:"persist_sessions"`
	SessionDir      string   `yaml:"session_dir"`
	SessionIndex    string   `yaml:"session_index"`
}

type fileBridge struct {
	Workers    int    `yaml:"workers"`
	QueueSize  int    `yaml:"queue_size"`
	JobTimeout string `yaml:"job_timeout"` // Go duration, e.g. 10m
}

// Load reads configuration from YAML (preferred) or a legacy dotenv file,
// then applies environment variable overrides (env wins).
// Returns ErrMissingToken if no Discord token is set.
func Load() (Config, error) {
	path := resolveConfigPath()
	cfg := defaults()
	cfg.ConfigPath = path

	if path != "" {
		if err := loadFile(path, &cfg); err != nil && !os.IsNotExist(err) {
			return cfg, err
		}
	}

	applyEnvOverrides(&cfg)
	normalizePaths(&cfg)

	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	if cfg.QueueSize < 1 {
		cfg.QueueSize = 64
	}
	if cfg.JobTimeout <= 0 {
		cfg.JobTimeout = 30 * time.Minute
	}
	if cfg.DiscordToken == "" {
		return cfg, ErrMissingToken
	}
	return cfg, nil
}

// ErrMissingToken means the user still needs setup.
var ErrMissingToken = fmt.Errorf("discord token is not set")

// Dir returns the XDG-style config directory for pi-bridge:
// $XDG_CONFIG_HOME/pi-bridge, or ~/.config/pi-bridge when unset.
func Dir() string {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "pi-bridge")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "pi-bridge"
	}
	return filepath.Join(home, ".config", "pi-bridge")
}

// DefaultConfigPath returns the preferred on-disk YAML config location.
func DefaultConfigPath() string {
	return filepath.Join(Dir(), "config.yaml")
}

// DefaultHomeDir is the default assistant workspace (agent cwd).
func DefaultHomeDir() string {
	return filepath.Join(Dir(), "home")
}

// DefaultWorkRoot is where the agent keeps code checkouts by default.
func DefaultWorkRoot() string {
	return filepath.Join(Dir(), "work")
}

// EnsureHome creates the assistant home and seeds AGENTS.md if missing.
// Safe to call on every start; does not overwrite existing files.
func EnsureHome(home string) error {
	if home == "" {
		home = DefaultHomeDir()
	}
	if err := os.MkdirAll(filepath.Join(home, "docs", "solutions"), 0o700); err != nil {
		return fmt.Errorf("create assistant home: %w", err)
	}
	agents := filepath.Join(home, "AGENTS.md")
	if _, err := os.Stat(agents); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.WriteFile(agents, []byte(defaultAgentsMD), 0o644); err != nil {
		return fmt.Errorf("seed AGENTS.md: %w", err)
	}
	return nil
}

// EnsureWorkRoot creates the code work root and a .sandboxes dir.
func EnsureWorkRoot(workRoot string) error {
	if workRoot == "" {
		workRoot = DefaultWorkRoot()
	}
	if err := os.MkdirAll(filepath.Join(workRoot, ".sandboxes"), 0o700); err != nil {
		return fmt.Errorf("create work root: %w", err)
	}
	return nil
}

// EnsureWorkspace prepares home + work roots for a running bot.
func EnsureWorkspace(cfg Config) error {
	if err := EnsureHome(cfg.Home); err != nil {
		return err
	}
	return EnsureWorkRoot(cfg.WorkRoot)
}

const defaultAgentsMD = `# Assistant home

You are the user's Discord-installed coding and personal assistant (pi-bridge).

This directory is your default workspace — identity, notes, and durable learnings live here.
Code checkouts live in the configured **work root** (sibling directory work/ under the pi-bridge config dir by default), not in this home.

## Guidelines

- Prefer working in this home for personal tasks, planning, and memory.
- Clone and manage repos under the work root; prefer an existing clone there over cloning again.
- New clones: <work_root>/<repo-name>. Throwaways: <work_root>/.sandboxes/<name>.
- Prefer git worktrees under an existing clone for parallel branches.
- Only edit trees outside home/work when the user clearly asks.
- Capture reusable learnings under docs/solutions/ so future sessions can reuse them.
- Keep secrets out of committed files; this home is local user data.
`

// Save writes configuration as YAML (mode 0600).
func Save(path string, cfg Config) error {
	if path == "" {
		path = DefaultConfigPath()
	}
	// Always prefer a .yaml extension for new writes when given a legacy path.
	if isLegacyEnvPath(path) {
		path = strings.TrimSuffix(path, filepath.Ext(path)) + ".yaml"
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		if filepath.Dir(path) != "." {
			return fmt.Errorf("create config dir: %w", err)
		}
	}

	normalizePaths(&cfg)
	fc := toFileConfig(cfg)
	b, err := yaml.Marshal(&fc)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	header := []byte("# pi-bridge config — keep this private (mode 0600)\n# Generated by: pi-bridge setup\n\n")
	out := append(header, b...)
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func defaults() Config {
	home := DefaultHomeDir()
	return Config{
		RequireMention:   true,
		Home:             home,
		WorkRoot:         DefaultWorkRoot(),
		DefaultCWD:       home,
		QueueSize:        64,
		Workers:          1,
		JobTimeout:       30 * time.Minute,
		PiBinary:         "pi",
		SessionDir:       filepath.Join(Dir(), "sessions"),
		SessionIndexPath: filepath.Join(Dir(), "session-index.json"),
		PersistSessions:  true,
	}
}

func loadFile(path string, cfg *Config) error {
	switch {
	case isYAMLPath(path):
		return loadYAMLFile(path, cfg)
	case isLegacyEnvPath(path):
		return loadLegacyEnvFile(path, cfg)
	default:
		// Unknown extension: try YAML first, then dotenv.
		if err := loadYAMLFile(path, cfg); err == nil {
			return nil
		}
		return loadLegacyEnvFile(path, cfg)
	}
}

func loadYAMLFile(path string, cfg *Config) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var fc fileConfig
	if err := yaml.Unmarshal(b, &fc); err != nil {
		return fmt.Errorf("parse config yaml: %w", err)
	}
	mergeFileConfig(cfg, fc)
	return nil
}

func loadLegacyEnvFile(path string, cfg *Config) error {
	// Parse KEY=VAL into a map, then map known keys onto cfg.
	vals, err := parseEnvFile(path)
	if err != nil {
		return err
	}
	if v := vals["DISCORD_TOKEN"]; v != "" {
		cfg.DiscordToken = v
	}
	if v := vals["DISCORD_APPLICATION_ID"]; v != "" {
		cfg.DiscordApplicationID = v
	}
	if v := vals["ALLOWED_USER_IDS"]; v != "" {
		cfg.AllowedUserIDs = parseIDSet(v)
	}
	if v := vals["ALLOWED_GUILD_IDS"]; v != "" {
		cfg.AllowedGuildIDs = parseIDSet(v)
	}
	if v := vals["REQUIRE_MENTION"]; v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.RequireMention = b
		}
	}
	if v := vals["PI_HOME"]; v != "" {
		cfg.Home = v
	}
	if v := vals["PI_WORK_ROOT"]; v != "" {
		cfg.WorkRoot = v
	}
	if v := vals["PI_CWD"]; v != "" {
		cfg.DefaultCWD = v
	}
	if v := vals["PI_BINARY"]; v != "" {
		cfg.PiBinary = v
	}
	if v := vals["PI_ARGS"]; v != "" {
		cfg.PiArgs = strings.Fields(v)
	}
	if v := vals["PI_PERSIST_SESSIONS"]; v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.PersistSessions = b
		}
	}
	if v := vals["PI_SESSION_DIR"]; v != "" {
		cfg.SessionDir = v
	}
	if v := vals["PI_SESSION_INDEX"]; v != "" {
		cfg.SessionIndexPath = v
	}
	if v := vals["QUEUE_SIZE"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.QueueSize = n
		}
	}
	if v := vals["WORKERS"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Workers = n
		}
	}
	if v := vals["JOB_TIMEOUT"]; v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.JobTimeout = d
		}
	}
	return nil
}

func mergeFileConfig(cfg *Config, fc fileConfig) {
	if fc.Discord.Token != "" {
		cfg.DiscordToken = strings.TrimSpace(fc.Discord.Token)
	}
	if fc.Discord.ApplicationID != "" {
		cfg.DiscordApplicationID = strings.TrimSpace(fc.Discord.ApplicationID)
	}
	if fc.Discord.AllowedUserIDs != nil {
		cfg.AllowedUserIDs = sliceToSet(fc.Discord.AllowedUserIDs)
	}
	if fc.Discord.AllowedGuildIDs != nil {
		cfg.AllowedGuildIDs = sliceToSet(fc.Discord.AllowedGuildIDs)
	}
	if fc.Discord.RequireMention != nil {
		cfg.RequireMention = *fc.Discord.RequireMention
	}

	if fc.Pi.Home != "" {
		cfg.Home = fc.Pi.Home
	}
	if fc.Pi.WorkRoot != "" {
		cfg.WorkRoot = fc.Pi.WorkRoot
	}
	if fc.Pi.CWD != "" {
		cfg.DefaultCWD = fc.Pi.CWD
	}
	if fc.Pi.Binary != "" {
		cfg.PiBinary = fc.Pi.Binary
	}
	if fc.Pi.Args != nil {
		cfg.PiArgs = append([]string(nil), fc.Pi.Args...)
	}
	if fc.Pi.PersistSessions != nil {
		cfg.PersistSessions = *fc.Pi.PersistSessions
	}
	if fc.Pi.SessionDir != "" {
		cfg.SessionDir = fc.Pi.SessionDir
	}
	if fc.Pi.SessionIndex != "" {
		cfg.SessionIndexPath = fc.Pi.SessionIndex
	}

	if fc.Bridge.Workers > 0 {
		cfg.Workers = fc.Bridge.Workers
	}
	if fc.Bridge.QueueSize > 0 {
		cfg.QueueSize = fc.Bridge.QueueSize
	}
	if fc.Bridge.JobTimeout != "" {
		if d, err := time.ParseDuration(fc.Bridge.JobTimeout); err == nil {
			cfg.JobTimeout = d
		}
	}

	if containsArg(cfg.PiArgs, "--no-session") {
		cfg.PersistSessions = false
	}
}

func toFileConfig(cfg Config) fileConfig {
	require := cfg.RequireMention
	persist := cfg.PersistSessions
	fc := fileConfig{
		Discord: fileDiscord{
			Token:           cfg.DiscordToken,
			ApplicationID:   cfg.DiscordApplicationID,
			AllowedUserIDs:  setToSlice(cfg.AllowedUserIDs),
			AllowedGuildIDs: setToSlice(cfg.AllowedGuildIDs),
			RequireMention:  &require,
		},
		Pi: filePi{
			Home:            cfg.Home,
			WorkRoot:        cfg.WorkRoot,
			CWD:             cfg.DefaultCWD,
			Binary:          cfg.PiBinary,
			Args:            append([]string(nil), cfg.PiArgs...),
			PersistSessions: &persist,
			SessionDir:      cfg.SessionDir,
			SessionIndex:    cfg.SessionIndexPath,
		},
		Bridge: fileBridge{
			Workers:    cfg.Workers,
			QueueSize:  cfg.QueueSize,
			JobTimeout: cfg.JobTimeout.String(),
		},
	}
	if fc.Discord.AllowedUserIDs == nil {
		fc.Discord.AllowedUserIDs = []string{}
	}
	if fc.Discord.AllowedGuildIDs == nil {
		fc.Discord.AllowedGuildIDs = []string{}
	}
	if fc.Pi.Args == nil {
		fc.Pi.Args = []string{}
	}
	return fc
}

// applyEnvOverrides lets environment variables win over the file.
func applyEnvOverrides(cfg *Config) {
	if v := strings.TrimSpace(os.Getenv("DISCORD_TOKEN")); v != "" {
		cfg.DiscordToken = v
	}
	if v := strings.TrimSpace(os.Getenv("DISCORD_APPLICATION_ID")); v != "" {
		cfg.DiscordApplicationID = v
	}
	if v := strings.TrimSpace(os.Getenv("ALLOWED_USER_IDS")); v != "" {
		cfg.AllowedUserIDs = parseIDSet(v)
	}
	if v := strings.TrimSpace(os.Getenv("ALLOWED_GUILD_IDS")); v != "" {
		cfg.AllowedGuildIDs = parseIDSet(v)
	}
	if v := strings.TrimSpace(os.Getenv("REQUIRE_MENTION")); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.RequireMention = b
		}
	}
	if v := strings.TrimSpace(os.Getenv("PI_HOME")); v != "" {
		cfg.Home = v
	}
	if v := strings.TrimSpace(os.Getenv("PI_WORK_ROOT")); v != "" {
		cfg.WorkRoot = v
	}
	if v := strings.TrimSpace(os.Getenv("PI_CWD")); v != "" {
		cfg.DefaultCWD = v
	}
	if v := strings.TrimSpace(os.Getenv("PI_BINARY")); v != "" {
		cfg.PiBinary = v
	}
	if v := strings.TrimSpace(os.Getenv("PI_ARGS")); v != "" {
		cfg.PiArgs = strings.Fields(v)
		if containsArg(cfg.PiArgs, "--no-session") {
			cfg.PersistSessions = false
		}
	}
	if v := strings.TrimSpace(os.Getenv("PI_PERSIST_SESSIONS")); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.PersistSessions = b
		}
	}
	if v := strings.TrimSpace(os.Getenv("PI_SESSION_DIR")); v != "" {
		cfg.SessionDir = v
	}
	if v := strings.TrimSpace(os.Getenv("PI_SESSION_INDEX")); v != "" {
		cfg.SessionIndexPath = v
	}
	if v := strings.TrimSpace(os.Getenv("QUEUE_SIZE")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.QueueSize = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("WORKERS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Workers = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("JOB_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.JobTimeout = d
		}
	}
}

func resolveConfigPath() string {
	if p := strings.TrimSpace(os.Getenv("PI_BRIDGE_CONFIG")); p != "" {
		return p
	}

	// Prefer local project files, then XDG config, then legacy locations.
	candidates := []string{
		"pi-bridge.yaml",
		"pi-bridge.yml",
		"pi-bridge.env",
		DefaultConfigPath(),
		filepath.Join(Dir(), "config.yml"),
		filepath.Join(Dir(), "config.env"),
	}
	// macOS Application Support and other os.UserConfigDir paths (pre-XDG installs).
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		legacy := filepath.Join(dir, "pi-bridge")
		if legacy != Dir() {
			candidates = append(candidates,
				filepath.Join(legacy, "config.yaml"),
				filepath.Join(legacy, "config.yml"),
				filepath.Join(legacy, "config.env"),
			)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".pi-bridge.yaml"),
			filepath.Join(home, ".pi-bridge.env"),
		)
	}

	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	// Default write target when nothing exists yet.
	return DefaultConfigPath()
}

// normalizePaths fills Home/WorkRoot/CWD defaults after file + env merge.
func normalizePaths(cfg *Config) {
	cfg.Home = expandPath(cfg.Home)
	cfg.WorkRoot = expandPath(cfg.WorkRoot)
	cfg.DefaultCWD = expandPath(cfg.DefaultCWD)
	cfg.SessionDir = expandPath(cfg.SessionDir)
	cfg.SessionIndexPath = expandPath(cfg.SessionIndexPath)

	if cfg.Home == "" {
		cfg.Home = DefaultHomeDir()
	}
	if cfg.WorkRoot == "" {
		cfg.WorkRoot = DefaultWorkRoot()
	}
	if cfg.DefaultCWD == "" {
		cfg.DefaultCWD = cfg.Home
	}
	if cfg.SessionDir == "" {
		cfg.SessionDir = filepath.Join(Dir(), "sessions")
	}
	if cfg.SessionIndexPath == "" {
		cfg.SessionIndexPath = filepath.Join(Dir(), "session-index.json")
	}
}

func expandPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return p
	}
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~"+string(os.PathSeparator)) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func parseEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := make(map[string]string)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		out[key] = val
	}
	return out, sc.Err()
}

func parseIDSet(raw string) map[string]struct{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	return sliceToSet(strings.Split(raw, ","))
}

func sliceToSet(ids []string) map[string]struct{} {
	if len(ids) == 0 {
		return map[string]struct{}{}
	}
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			out[id] = struct{}{}
		}
	}
	return out
}

func setToSlice(ids map[string]struct{}) []string {
	if len(ids) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func isYAMLPath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}

func isLegacyEnvPath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".env" || strings.HasSuffix(strings.ToLower(path), ".env")
}


