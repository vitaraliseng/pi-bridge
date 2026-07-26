package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/vitaraliseng/pi-bridge/internal/config"
	"github.com/vitaraliseng/pi-bridge/internal/discord"
	"github.com/vitaraliseng/pi-bridge/internal/pi"
	"github.com/vitaraliseng/pi-bridge/internal/queue"
	"github.com/vitaraliseng/pi-bridge/internal/sessionstore"
	"github.com/vitaraliseng/pi-bridge/internal/setup"
	"github.com/vitaraliseng/pi-bridge/internal/worker"
)

// Filled by GoReleaser ldflags on release builds.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(log, os.Args[1:]); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, args []string) error {
	cmd := "run"
	if len(args) > 0 {
		cmd = args[0]
	}

	switch cmd {
	case "setup", "init", "configure":
		res, err := setup.Run(setup.Options{})
		if err != nil {
			return err
		}
		if !res.StartNow {
			fmt.Println("Config saved. Start later with: pi-bridge")
			return nil
		}
		// Fall through to bot after setup.
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		return runBot(log, cfg)

	case "help", "-h", "--help":
		printHelp()
		return nil

	case "version", "-v", "--version":
		fmt.Printf("pi-bridge %s (commit %s, built %s)\n", version, commit, date)
		return nil

	case "run", "start":
		// continue below
	default:
		// Allow unknown args only if they look like flags; otherwise help.
		if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
			printHelp()
			return fmt.Errorf("unknown command %q", args[0])
		}
	}

	cfg, err := config.Load()
	if errors.Is(err, config.ErrMissingToken) {
		fmt.Println("No Discord token found — starting setup wizard.")
		fmt.Println()
		res, werr := setup.Run(setup.Options{})
		if werr != nil {
			return werr
		}
		if !res.StartNow {
			fmt.Println("Config saved. Start later with: pi-bridge")
			return nil
		}
		cfg, err = config.Load()
	}
	if err != nil {
		return err
	}
	return runBot(log, cfg)
}

func runBot(log *slog.Logger, cfg config.Config) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	q := queue.NewMemory(cfg.QueueSize)

	var store *sessionstore.Store
	if cfg.PersistSessions {
		var err error
		store, err = sessionstore.Open(cfg.SessionIndexPath)
		if err != nil {
			return fmt.Errorf("session index: %w", err)
		}
		log.Info("session persistence enabled",
			"index", cfg.SessionIndexPath,
			"sessions", cfg.SessionDir,
		)
	} else {
		log.Info("session persistence disabled (ephemeral pi sessions)")
	}

	persist := cfg.PersistSessions
	pool := pi.NewPool(pi.PoolOptions{
		Binary:     cfg.PiBinary,
		ExtraArgs:  cfg.PiArgs,
		SessionDir: cfg.SessionDir,
		Store:      store,
		Persist:    &persist,
		Log:        log,
	})
	defer pool.Close()

	if len(cfg.AllowedUserIDs) == 0 {
		log.Warn("discord.allowed_user_ids is empty — bot will ignore all messages (fail closed). Run: pi-bridge setup")
	} else {
		log.Info("user allowlist enabled", "users", len(cfg.AllowedUserIDs))
	}
	if len(cfg.AllowedGuildIDs) > 0 {
		log.Info("guild allowlist enabled", "guilds", len(cfg.AllowedGuildIDs))
	}

	bot, err := discord.New(discord.Config{
		Token:           cfg.DiscordToken,
		AllowedUserIDs:  cfg.AllowedUserIDs,
		AllowedGuildIDs: cfg.AllowedGuildIDs,
		RequireMention:  cfg.RequireMention,
		DefaultCWD:      cfg.DefaultCWD,
	}, q, log)
	if err != nil {
		return fmt.Errorf("discord bot: %w", err)
	}

	if err := bot.Open(); err != nil {
		return fmt.Errorf("discord open: %w", err)
	}
	defer bot.Close()

	var wg sync.WaitGroup
	for i := 0; i < cfg.Workers; i++ {
		w := &worker.Worker{
			ID:      fmt.Sprintf("w%d", i+1),
			Queue:   q,
			Pool:    pool,
			Sink:    bot,
			Timeout: cfg.JobTimeout,
			Log:     log,
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := w.Run(ctx); err != nil && ctx.Err() == nil {
				log.Error("worker stopped", "worker", w.ID, "err", err)
			}
		}()
	}

	log.Info("pi-bridge running",
		"workers", cfg.Workers,
		"cwd", cfg.DefaultCWD,
		"queue", cfg.QueueSize,
		"config", cfg.ConfigPath,
	)
	fmt.Println()
	fmt.Println("Bot is online. In Discord, try:  @your-bot hello")
	fmt.Println("Press Ctrl+C to stop.")
	fmt.Println()

	<-ctx.Done()
	log.Info("shutting down")
	q.Close()
	wg.Wait()
	return nil
}

func printHelp() {
	fmt.Printf(`pi-bridge — Discord front-end for the pi coding agent (%s)

Usage:
  pi-bridge           Start the bot (launches setup if unconfigured)
  pi-bridge setup     Re-run the guided Discord setup wizard
  pi-bridge version   Print version
  pi-bridge help      Show this help

Install:
  brew install vitaraliseng/tap/pi-bridge   # after first release + tap setup
  go install github.com/vitaraliseng/pi-bridge/cmd/pi-bridge@latest

Config is YAML (lists for user/guild IDs). Discovery order:
  $PI_BRIDGE_CONFIG
  ./pi-bridge.yaml
  $XDG_CONFIG_HOME/pi-bridge/config.yaml  (or ~/Library/Application Support/pi-bridge on macOS)
  legacy: ./pi-bridge.env or config.env (still read; setup rewrites to .yaml)

Environment variables always override the config file.
`, version)
}
