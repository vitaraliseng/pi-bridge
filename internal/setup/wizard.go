package setup

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/vitaraliseng/pi-bridge/internal/config"
)

// Discord bot permission bitfield for pi-bridge.
// View Channels | Send Messages | Embed Links | Attach Files |
// Read Message History | Create Public Threads | Send Messages in Threads
const botPermissions = (1 << 10) | // View Channels
	(1 << 11) | // Send Messages
	(1 << 14) | // Embed Links
	(1 << 15) | // Attach Files
	(1 << 16) | // Read Message History
	(1 << 35) | // Create Public Threads
	(1 << 38) // Send Messages in Threads

const (
	urlApplications = "https://discord.com/developers/applications"
)

// Result is what the wizard produces.
type Result struct {
	Config     config.Config
	ConfigPath string
	StartNow   bool
}

// Options controls wizard I/O (useful for tests).
type Options struct {
	In  io.Reader
	Out io.Writer
	// OpenURL opens a browser. Defaults to the OS handler.
	OpenURL func(string) error
	// HTTPClient validates the bot token.
	HTTPClient *http.Client
	// ConfigPath overrides where settings are saved.
	ConfigPath string
}

// Run walks the user through Discord app setup.
func Run(opts Options) (Result, error) {
	usingStdin := false
	if opts.In == nil {
		opts.In = os.Stdin
		usingStdin = true
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.OpenURL == nil {
		opts.OpenURL = openBrowser
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if opts.ConfigPath == "" {
		opts.ConfigPath = config.DefaultConfigPath()
	}

	in := bufio.NewReader(opts.In)
	out := opts.Out

	banner(out)
	fmt.Fprintln(out, "This wizard will help you create a Discord app, grab a bot token,")
	fmt.Fprintln(out, "invite the bot to your server, and save a local config file.")
	fmt.Fprintln(out)

	// Offer reconfigure if a token already exists at the target path or in the env.
	if existing, ok := loadExisting(opts.ConfigPath); ok {
		fmt.Fprintf(out, "Found an existing token (%s)\n", displayPath(existing.ConfigPath))
		if !confirm(in, out, "Run setup again and overwrite it?", false) {
			return Result{Config: existing, ConfigPath: existing.ConfigPath, StartNow: true}, nil
		}
		fmt.Fprintln(out)
	}

	// --- Step 1: Application ---
	step(out, 1, 5, "Create a Discord application")
	fmt.Fprintln(out, "  I'll open the Discord Developer Portal.")
	fmt.Fprintln(out, "  Click \"New Application\", name it (e.g. pi-bridge), then Create.")
	fmt.Fprintln(out)
	if confirm(in, out, "Open the applications page now?", true) {
		openOrPrint(out, opts.OpenURL, urlApplications)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  On the app's General Information page, copy the Application ID.")
	appID := promptRequired(in, out, "Paste Application ID")
	fmt.Fprintln(out)

	// --- Step 2: Bot + intents ---
	step(out, 2, 5, "Create the bot and enable intents")
	botURL := fmt.Sprintf("https://discord.com/developers/applications/%s/bot", appID)
	fmt.Fprintln(out, "  Opening your app's Bot page…")
	openOrPrint(out, opts.OpenURL, botURL)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  On that page:")
	fmt.Fprintln(out, "    1. Click \"Reset Token\" / \"Add Bot\" if needed, then Copy the token")
	fmt.Fprintln(out, "    2. Under Privileged Gateway Intents, enable:")
	fmt.Fprintln(out, "         • MESSAGE CONTENT INTENT  ← required")
	fmt.Fprintln(out, "    3. Save Changes")
	fmt.Fprintln(out)

	var token string
	for {
		var err error
		token, err = promptSecret(in, out, "Paste bot token", usingStdin)
		if err != nil {
			return Result{}, err
		}
		if token == "" {
			fmt.Fprintln(out, "  Token can't be empty.")
			continue
		}
		fmt.Fprint(out, "  Validating token with Discord… ")
		username, vErr := validateToken(opts.HTTPClient, token)
		if vErr != nil {
			fmt.Fprintf(out, "failed\n  %v\n", vErr)
			if !confirm(in, out, "Try a different token?", true) {
				return Result{}, fmt.Errorf("token validation failed: %w", vErr)
			}
			continue
		}
		fmt.Fprintf(out, "ok (logged in as %s)\n", username)
		break
	}
	ownerID, ownerName := fetchAppOwner(opts.HTTPClient, token)
	if ownerID != "" {
		if ownerName != "" {
			fmt.Fprintf(out, "  App owner: %s (%s)\n", ownerName, ownerID)
		} else {
			fmt.Fprintf(out, "  App owner id: %s\n", ownerID)
		}
	}
	fmt.Fprintln(out)

	// --- Step 3: Invite ---
	step(out, 3, 5, "Invite the bot to your server")
	fmt.Fprintln(out, "  The bot needs at least one Discord server to live in.")
	fmt.Fprintln(out, "  A private server just for you is free and fine.")
	fmt.Fprintln(out)
	if confirm(in, out, "Do you already have a server you can add the bot to?", true) {
		fmt.Fprintln(out, "  Great — we'll open the invite next.")
	} else {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  Create one in the Discord app (or https://discord.com/app):")
		fmt.Fprintln(out, "    1. Left sidebar → + (Add a Server)")
		fmt.Fprintln(out, "    2. Create My Own → For me and my friends")
		fmt.Fprintln(out, "    3. Name it anything (e.g. pi-lab) → Create")
		fmt.Fprintln(out)
		if confirm(in, out, "Open Discord in the browser so you can create a server?", true) {
			openOrPrint(out, opts.OpenURL, "https://discord.com/app")
		}
		pause(in, out, "Press Enter once your server exists…")
	}
	fmt.Fprintln(out)

	invite := inviteURL(appID, botPermissions)
	fmt.Fprintln(out, "  Permissions included:")
	fmt.Fprintln(out, "    View Channels, Send Messages, Read Message History,")
	fmt.Fprintln(out, "    Create Public Threads, Send Messages in Threads,")
	fmt.Fprintln(out, "    Embed Links, Attach Files")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  Opening the invite page…")
	fmt.Fprintf(out, "  (link: %s)\n\n", invite)
	openOrPrint(out, opts.OpenURL, invite)
	fmt.Fprintln(out, "  Choose your server in the dropdown and click Continue → Authorize.")
	pause(in, out, "Press Enter once the bot is in your server…")
	fmt.Fprintln(out)

	// --- Step 4: Who may use the bot (required) ---
	step(out, 4, 5, "Who is allowed to use the bot")
	fmt.Fprintln(out, "  pi-bridge runs a coding agent on THIS machine with your files and tools.")
	fmt.Fprintln(out, "  Only listed Discord user IDs can talk to it (DMs and servers).")
	fmt.Fprintln(out, "  Empty allowlist = nobody (fail closed).")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  How to copy a user ID:")
	fmt.Fprintln(out, "    1. Discord → User Settings → Advanced → enable Developer Mode")
	fmt.Fprintln(out, "    2. Right-click your avatar → Copy User ID")
	fmt.Fprintln(out)

	defaultUsers := ownerID
	if defaultUsers == "" {
		defaultUsers = ""
	}
	var allowedUsers map[string]struct{}
	for {
		label := "Allowed user ID(s), comma-separated"
		var raw string
		if defaultUsers != "" {
			raw = promptDefault(in, out, label, defaultUsers)
		} else {
			raw = promptRequired(in, out, label)
		}
		allowedUsers = parseIDList(raw)
		if len(allowedUsers) > 0 {
			break
		}
		fmt.Fprintln(out, "  At least one user ID is required.")
		defaultUsers = ""
	}
	fmt.Fprintln(out)

	// --- Step 5: Preferences ---
	step(out, 5, 5, "Optional preferences")
	home := config.DefaultHomeDir()
	work := config.DefaultWorkRoot()
	cfg := config.Config{
		DiscordToken:         token,
		DiscordApplicationID: appID,
		AllowedUserIDs:       allowedUsers,
		RequireMention:       true,
		Home:                 home,
		WorkRoot:             work,
		DefaultCWD:           home,
	}

	fmt.Fprintln(out, `  A Discord "server" is also called a guild.`)
	fmt.Fprintln(out, "  Restricting guilds is optional; user allowlist already blocks strangers.")
	if confirm(in, out, "Also restrict to specific server (guild) ID(s)?", false) {
		raw := prompt(in, out, "Server/guild ID(s), comma-separated (right-click server icon → Copy Server ID)")
		cfg.AllowedGuildIDs = parseIDList(raw)
	}

	fmt.Fprintln(out, "  Assistant home = brain (AGENTS, memory). Work root = code clones.")
	fmt.Fprintln(out, "  Both default under XDG config (hidden); override work root if you prefer.")
	cwd := promptDefault(in, out, "Agent workspace / cwd (pi.cwd)", cfg.DefaultCWD)
	if cwd != "" {
		cfg.DefaultCWD = cwd
	}
	wr := promptDefault(in, out, "Code work root (pi.work_root)", cfg.WorkRoot)
	if wr != "" {
		cfg.WorkRoot = wr
	}
	cfg.RequireMention = confirm(in, out, "Require @mention in top-level channels?", true)
	fmt.Fprintln(out)

	// --- Save ---
	path := opts.ConfigPath
	// Always write YAML (Save rewrites legacy .env paths to .yaml).
	if !strings.HasSuffix(strings.ToLower(path), ".yaml") && !strings.HasSuffix(strings.ToLower(path), ".yml") {
		path = strings.TrimSuffix(path, filepath.Ext(path)) + ".yaml"
	}
	fmt.Fprintf(out, "Saving YAML config to %s (mode 600)…\n", path)
	if err := config.Save(path, cfg); err != nil {
		return Result{}, err
	}
	if err := config.EnsureWorkspace(cfg); err != nil {
		return Result{}, err
	}
	cfg.ConfigPath = path
	// Point Load() at the YAML we just wrote (env still overrides if set).
	_ = os.Setenv("PI_BRIDGE_CONFIG", path)

	fmt.Fprintln(out)
	fmt.Fprintln(out, "✓ Setup complete!")
	fmt.Fprintln(out)
	fmt.Fprintf(out, "Config:          %s\n", path)
	fmt.Fprintf(out, "Assistant home:  %s\n", cfg.Home)
	fmt.Fprintf(out, "Work root:       %s\n", cfg.WorkRoot)
	fmt.Fprintf(out, "Agent cwd:       %s\n", cfg.DefaultCWD)
	fmt.Fprintf(out, "Allowed users:   %s\n", joinIDs(cfg.AllowedUserIDs))
	if len(cfg.AllowedGuildIDs) > 0 {
		fmt.Fprintf(out, "Allowed servers (guilds): %s\n", joinIDs(cfg.AllowedGuildIDs))
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "How to chat:")
	fmt.Fprintln(out, "  • DM the bot (allowed users only) — replies in the DM (no threads; Discord limit)")
	fmt.Fprintln(out, "  • Or in a server channel:  @your-bot hello  (opens a thread)")
	fmt.Fprintln(out, "  • Follow-ups in a server thread keep that thread's pi session")
	fmt.Fprintln(out, "  Tip: you must share a server with the bot before Discord allows DMs.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Re-run setup anytime with:  pi-bridge setup")
	fmt.Fprintln(out)

	start := confirm(in, out, "Start pi-bridge now?", true)
	return Result{Config: cfg, ConfigPath: path, StartNow: start}, nil
}

func banner(out io.Writer) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "┌──────────────────────────────────────────┐")
	fmt.Fprintln(out, "│           pi-bridge setup wizard         │")
	fmt.Fprintln(out, "│   Discord ↔ pi coding agent bridge       │")
	fmt.Fprintln(out, "└──────────────────────────────────────────┘")
	fmt.Fprintln(out)
}

func step(out io.Writer, n, total int, title string) {
	fmt.Fprintf(out, "── Step %d/%d · %s ──\n\n", n, total, title)
}

func confirm(in *bufio.Reader, out io.Writer, question string, def bool) bool {
	hint := "y/N"
	if def {
		hint = "Y/n"
	}
	fmt.Fprintf(out, "%s [%s]: ", question, hint)
	line, _ := in.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return def
	}
	return line == "y" || line == "yes"
}

func prompt(in *bufio.Reader, out io.Writer, label string) string {
	fmt.Fprintf(out, "%s: ", label)
	line, _ := in.ReadString('\n')
	return strings.TrimSpace(line)
}

func promptDefault(in *bufio.Reader, out io.Writer, label, def string) string {
	fmt.Fprintf(out, "%s [%s]: ", label, def)
	line, _ := in.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func promptRequired(in *bufio.Reader, out io.Writer, label string) string {
	for {
		v := prompt(in, out, label)
		if v != "" {
			return v
		}
		fmt.Fprintln(out, "  This value is required.")
	}
}

func promptSecret(in *bufio.Reader, out io.Writer, label string, hide bool) (string, error) {
	fmt.Fprintf(out, "%s: ", label)

	// Prefer hidden input only for real interactive stdin (not piped tests).
	if hide {
		fd := int(os.Stdin.Fd())
		if term.IsTerminal(fd) {
			b, err := term.ReadPassword(fd)
			fmt.Fprintln(out)
			if err != nil {
				return "", fmt.Errorf("read token: %w", err)
			}
			return strings.TrimSpace(string(b)), nil
		}
	}

	// Fallback for pipes / tests.
	line, err := in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// loadExisting returns a config if a known config file or environment already has a token.
func loadExisting(targetPath string) (config.Config, bool) {
	candidates := existingConfigCandidates(targetPath)
	for _, p := range candidates {
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			continue
		}
		_ = os.Setenv("PI_BRIDGE_CONFIG", p)
		if cfg, err := config.Load(); err == nil && cfg.DiscordToken != "" {
			return cfg, true
		}
	}

	// Env-only configuration (no file yet).
	if tok := strings.TrimSpace(os.Getenv("DISCORD_TOKEN")); tok != "" {
		cfg, err := config.Load()
		if err == nil {
			return cfg, true
		}
		return config.Config{DiscordToken: tok, ConfigPath: targetPath}, true
	}
	return config.Config{}, false
}

func existingConfigCandidates(targetPath string) []string {
	var out []string
	add := func(p string) {
		if p == "" {
			return
		}
		for _, e := range out {
			if e == p {
				return
			}
		}
		out = append(out, p)
	}

	add(targetPath)
	if targetPath != "" {
		base := strings.TrimSuffix(targetPath, filepath.Ext(targetPath))
		add(base + ".yaml")
		add(base + ".yml")
		add(base + ".env")
	}

	// When using the default location, also probe other historical defaults.
	if targetPath == config.DefaultConfigPath() {
		add(filepath.Join(config.Dir(), "config.env"))
		add(filepath.Join(config.Dir(), "config.yml"))
		if dir, err := os.UserConfigDir(); err == nil && dir != "" {
			legacy := filepath.Join(dir, "pi-bridge")
			if legacy != config.Dir() {
				add(filepath.Join(legacy, "config.yaml"))
				add(filepath.Join(legacy, "config.yml"))
				add(filepath.Join(legacy, "config.env"))
			}
		}
		if home, err := os.UserHomeDir(); err == nil {
			add(filepath.Join(home, ".pi-bridge.yaml"))
			add(filepath.Join(home, ".pi-bridge.env"))
		}
	}
	return out
}

func pause(in *bufio.Reader, out io.Writer, msg string) {
	fmt.Fprint(out, msg)
	_, _ = in.ReadString('\n')
}

func inviteURL(appID string, permissions int64) string {
	q := url.Values{}
	q.Set("client_id", appID)
	q.Set("permissions", fmt.Sprintf("%d", permissions))
	q.Set("scope", "bot")
	return "https://discord.com/api/oauth2/authorize?" + q.Encode()
}

func validateToken(client *http.Client, token string) (string, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://discord.com/api/v10/users/@me", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bot "+token)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("discord api: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discord returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var me struct {
		Username string `json:"username"`
		ID       string `json:"id"`
	}
	if err := json.Unmarshal(body, &me); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if me.Username == "" {
		me.Username = me.ID
	}
	return me.Username, nil
}

// fetchAppOwner best-effort reads the Discord application owner for allowlist defaults.
func fetchAppOwner(client *http.Client, token string) (id, username string) {
	if client == nil || token == "" {
		return "", ""
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://discord.com/api/v10/oauth2/applications/@me", nil)
	if err != nil {
		return "", ""
	}
	req.Header.Set("Authorization", "Bot "+token)
	resp, err := client.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", ""
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var app struct {
		Owner struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"owner"`
		// Team-owned apps expose owner under team; ignore for now.
	}
	if err := json.Unmarshal(body, &app); err != nil {
		return "", ""
	}
	return app.Owner.ID, app.Owner.Username
}

func parseIDList(raw string) map[string]struct{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := make(map[string]struct{})
	for _, id := range strings.Split(raw, ",") {
		id = strings.TrimSpace(id)
		if id != "" {
			out[id] = struct{}{}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func joinIDs(ids map[string]struct{}) string {
	if len(ids) == 0 {
		return ""
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func openOrPrint(out io.Writer, open func(string) error, rawURL string) {
	if err := open(rawURL); err != nil {
		fmt.Fprintf(out, "  Could not open a browser: %v\n", err)
		fmt.Fprintf(out, "  Open this URL manually:\n  %s\n", rawURL)
		return
	}
}

func openBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	return cmd.Start()
}

func displayPath(path string) string {
	if path == "" {
		return config.DefaultConfigPath()
	}
	return path
}


