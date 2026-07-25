package pi

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/vitaraliseng/pi-bridge/internal/sessionstore"
)

// Pool keeps long-lived pi clients keyed by session (e.g. Discord thread),
// and resumes persisted pi session files across process restarts.
type Pool struct {
	mu         sync.Mutex
	clients    map[string]*entry
	binary     string
	extraArgs  []string
	sessionDir string
	store      *sessionstore.Store
	persist    bool
	idleFor    time.Duration
	log        *slog.Logger
}

type entry struct {
	client   *Client
	cwd      string
	lastUsed time.Time
	cancel   context.CancelFunc
}

// PoolOptions configures session process reuse and persistence.
type PoolOptions struct {
	Binary     string
	// ExtraArgs are appended to every pi invocation (from PI_ARGS).
	// If they include --no-session, persistence is disabled.
	ExtraArgs  []string
	SessionDir string
	Store      *sessionstore.Store
	// Persist enables writing/resuming pi session files. Default true when Store is set.
	Persist   *bool
	IdleFor   time.Duration
	ReapEvery time.Duration
	Log       *slog.Logger
}

// NewPool creates an empty session pool.
func NewPool(opts PoolOptions) *Pool {
	if opts.IdleFor == 0 {
		opts.IdleFor = 30 * time.Minute
	}
	if opts.ReapEvery == 0 {
		opts.ReapEvery = time.Minute
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}

	persist := opts.Store != nil
	if opts.Persist != nil {
		persist = *opts.Persist
	}
	if containsArg(opts.ExtraArgs, "--no-session") {
		persist = false
	}

	p := &Pool{
		clients:    make(map[string]*entry),
		binary:     opts.Binary,
		extraArgs:  append([]string(nil), opts.ExtraArgs...),
		sessionDir: opts.SessionDir,
		store:      opts.Store,
		persist:    persist,
		idleFor:    opts.IdleFor,
		log:        opts.Log,
	}
	if p.persist && p.sessionDir != "" {
		_ = os.MkdirAll(p.sessionDir, 0o700)
	}
	go p.reapLoop(opts.ReapEvery)
	return p
}

// Acquire returns a client for sessionKey, starting or resuming one if needed.
// If cwd changes for an existing live process, the process is replaced (session file kept).
func (p *Pool) Acquire(ctx context.Context, sessionKey, cwd string) (*Client, error) {
	if sessionKey == "" {
		return nil, fmt.Errorf("session key required")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if e, ok := p.clients[sessionKey]; ok {
		if e.cwd == cwd || cwd == "" {
			e.lastUsed = time.Now()
			return e.client, nil
		}
		// CWD changed; recycle process but keep persisted session mapping.
		e.cancel()
		_ = e.client.Close()
		delete(p.clients, sessionKey)
	}

	client, cancel, err := p.startClient(ctx, sessionKey, cwd)
	if err != nil {
		return nil, err
	}

	p.clients[sessionKey] = &entry{
		client:   client,
		cwd:      cwd,
		lastUsed: time.Now(),
		cancel:   cancel,
	}
	return client, nil
}

func (p *Pool) startClient(ctx context.Context, sessionKey, cwd string) (*Client, context.CancelFunc, error) {
	name := SessionName(sessionKey)
	var meta sessionstore.Entry
	if p.store != nil {
		meta, _ = p.store.Get(sessionKey)
	}

	args := p.buildArgs(meta, name)
	runCtx, cancel := context.WithCancel(context.Background())
	client, err := Start(runCtx, StartOptions{
		Binary: p.binary,
		CWD:    cwd,
		Args:   args,
	})
	if err != nil {
		cancel()
		// If resume failed at process start, try a fresh session once.
		if p.persist && meta.SessionFile != "" {
			p.log.Warn("failed to resume session; starting fresh",
				"session", sessionKey,
				"file", meta.SessionFile,
				"err", err,
			)
			if p.store != nil {
				_ = p.store.Delete(sessionKey)
			}
			args = p.buildArgs(sessionstore.Entry{}, name)
			runCtx, cancel = context.WithCancel(context.Background())
			client, err = Start(runCtx, StartOptions{
				Binary: p.binary,
				CWD:    cwd,
				Args:   args,
			})
			if err != nil {
				cancel()
				return nil, nil, err
			}
		} else {
			return nil, nil, err
		}
	}

	if p.persist {
		if err := p.rememberSession(ctx, sessionKey, cwd, name, client, meta.SessionFile != ""); err != nil {
			p.log.Warn("could not record session file", "session", sessionKey, "err", err)
		}
	}
	return client, cancel, nil
}

func (p *Pool) buildArgs(meta sessionstore.Entry, name string) []string {
	args := append([]string(nil), p.extraArgs...)

	if !p.persist {
		if !containsArg(args, "--no-session") {
			args = append(args, "--no-session")
		}
		return args
	}

	// Persistence on: never pass --no-session unless user forced it (handled above).
	if p.sessionDir != "" && !hasFlag(args, "--session-dir") {
		args = append(args, "--session-dir", p.sessionDir)
	}

	if meta.SessionFile != "" && fileExists(meta.SessionFile) && !hasFlag(args, "--session") {
		args = append(args, "--session", meta.SessionFile)
		p.log.Info("resuming pi session", "file", meta.SessionFile, "name", name)
		return args
	}

	if !hasFlag(args, "--name") && !hasFlag(args, "-n") {
		args = append(args, "--name", name)
	}
	p.log.Info("starting new pi session", "name", name)
	return args
}

func (p *Pool) rememberSession(ctx context.Context, sessionKey, cwd, name string, client *Client, resumed bool) error {
	// Best-effort name for listings.
	_ = client.SetSessionName(ctx, name)

	st, err := client.GetState(ctx)
	if err != nil {
		return err
	}
	if st.SessionFile == "" {
		return fmt.Errorf("pi returned empty sessionFile")
	}
	if p.store == nil {
		return nil
	}
	action := "bound"
	if resumed {
		action = "resumed"
	}
	p.log.Info("session "+action,
		"key", sessionKey,
		"file", st.SessionFile,
		"messages", st.MessageCount,
	)
	return p.store.Set(sessionKey, sessionstore.Entry{
		SessionFile: st.SessionFile,
		CWD:         cwd,
		Name:        name,
	})
}

// Close shuts down all live processes (session files remain on disk).
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, e := range p.clients {
		e.cancel()
		_ = e.client.Close()
		delete(p.clients, key)
	}
}

func (p *Pool) reapLoop(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for range t.C {
		p.reap()
	}
}

func (p *Pool) reap() {
	p.mu.Lock()
	defer p.mu.Unlock()
	cutoff := time.Now().Add(-p.idleFor)
	for key, e := range p.clients {
		if e.lastUsed.Before(cutoff) {
			p.log.Info("reaping idle session process", "session", key)
			e.cancel()
			_ = e.client.Close()
			delete(p.clients, key)
		}
	}
}

// SessionName turns a session key into a filesystem/display-friendly name.
func SessionName(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return "session"
	}
	// discord:123 -> discord-123
	key = strings.ReplaceAll(key, ":", "-")
	re := regexp.MustCompile(`[^a-zA-Z0-9._-]+`)
	key = re.ReplaceAllString(key, "-")
	key = strings.Trim(key, "-")
	if key == "" {
		return "session"
	}
	if len(key) > 80 {
		key = key[:80]
	}
	return key
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func hasFlag(args []string, flag string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == flag {
			return true
		}
		if strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// DefaultSessionDir returns the standard directory for pi session files.
func DefaultSessionDir() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "pi-bridge", "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "pi-bridge-sessions"
	}
	return filepath.Join(home, ".pi-bridge", "sessions")
}

// DefaultSessionIndexPath returns the path of the session key index.
func DefaultSessionIndexPath() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "pi-bridge", "session-index.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "session-index.json"
	}
	return filepath.Join(home, ".pi-bridge", "session-index.json")
}
