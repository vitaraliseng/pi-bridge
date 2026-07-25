package discord

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/cabewaldrop/pi-bridge/internal/pi"
	"github.com/cabewaldrop/pi-bridge/internal/queue"
)

// Config controls bot behavior.
type Config struct {
	Token string
	// AllowedGuildIDs restricts the bot to these servers. Empty = all.
	AllowedGuildIDs map[string]struct{}
	// RequireMention requires an @bot mention in top-level guild channels.
	// DMs always work. Existing bot threads always work.
	RequireMention bool
	// DefaultCWD is passed to pi as the working directory.
	DefaultCWD string
	// MaxMessageRunes caps outbound Discord messages.
	MaxMessageRunes int
}

// Bot bridges Discord messages into the job queue and posts replies.
type Bot struct {
	session *discordgo.Session
	queue   *queue.Memory
	cfg     Config
	log     *slog.Logger

	// Live message editing while streaming
	mu      sync.Mutex
	pending map[string]*liveReply // jobID -> live reply state
}

type liveReply struct {
	mu        sync.Mutex
	channelID string
	messageID string
	steps     []string // activity log (what the agent is doing)
	answer    strings.Builder
	dirty     bool
	flush     *time.Ticker
	done      chan struct{}
}

const maxProgressSteps = 8

// New creates a Discord bot bound to the given queue.
func New(cfg Config, q *queue.Memory, log *slog.Logger) (*Bot, error) {
	if log == nil {
		log = slog.Default()
	}
	if cfg.MaxMessageRunes == 0 {
		cfg.MaxMessageRunes = 1900
	}

	s, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		return nil, fmt.Errorf("discord session: %w", err)
	}
	// Message content intent is required to read message text.
	s.Identify.Intents = discordgo.IntentsGuilds |
		discordgo.IntentsGuildMessages |
		discordgo.IntentsDirectMessages |
		discordgo.IntentsMessageContent

	b := &Bot{
		session: s,
		queue:   q,
		cfg:     cfg,
		log:     log,
		pending: make(map[string]*liveReply),
	}
	s.AddHandler(b.onReady)
	s.AddHandler(b.onMessage)
	return b, nil
}

// Open connects to the Discord gateway.
func (b *Bot) Open() error {
	if err := b.session.Open(); err != nil {
		if strings.Contains(err.Error(), "4014") || strings.Contains(strings.ToLower(err.Error()), "disallowed intent") {
			return fmt.Errorf("%w\n\n%s", err, intentHelp)
		}
		return err
	}
	return nil
}

const intentHelp = `Message Content Intent is required but not enabled.
Fix:
  1. Open https://discord.com/developers/applications
  2. Select your app -> Bot
  3. Under Privileged Gateway Intents, enable MESSAGE CONTENT INTENT
  4. Save Changes, then restart pi-bridge`

// Close disconnects.
func (b *Bot) Close() error {
	return b.session.Close()
}

// --- Sink implementation for worker ---

// OnStart posts a placeholder reply message.
func (b *Bot) OnStart(_ context.Context, job queue.Job) error {
	msg, err := b.session.ChannelMessageSend(job.ChannelID, renderProgress([]string{"Starting…"}, "", true))
	if err != nil {
		return err
	}

	lr := &liveReply{
		channelID: job.ChannelID,
		messageID: msg.ID,
		steps:     []string{"Starting…"},
		dirty:     true,
		flush:     time.NewTicker(900 * time.Millisecond),
		done:      make(chan struct{}),
	}
	b.mu.Lock()
	b.pending[job.ID] = lr
	b.mu.Unlock()

	go b.flushLoop(lr)
	return nil
}

// OnProgress records activity / streamed text for the live reply.
func (b *Bot) OnProgress(_ context.Context, job queue.Job, p pi.Progress) {
	b.mu.Lock()
	lr := b.pending[job.ID]
	b.mu.Unlock()
	if lr == nil {
		return
	}

	lr.mu.Lock()
	defer lr.mu.Unlock()

	switch p.Kind {
	case pi.ProgressDelta:
		lr.answer.WriteString(p.Delta)
		lr.dirty = true
	case pi.ProgressStatus, pi.ProgressToolStart, pi.ProgressToolEnd:
		msg := strings.TrimSpace(p.Message)
		if msg == "" {
			return
		}
		// Dedup consecutive identical status lines.
		if n := len(lr.steps); n > 0 && lr.steps[n-1] == msg {
			return
		}
		lr.steps = append(lr.steps, msg)
		if len(lr.steps) > maxProgressSteps*2 {
			// Keep memory bounded; render shows only the last N.
			lr.steps = lr.steps[len(lr.steps)-maxProgressSteps:]
		}
		lr.dirty = true
	}
}

// OnComplete writes the final answer and stops streaming edits.
func (b *Bot) OnComplete(_ context.Context, job queue.Job, text string) error {
	b.finish(job.ID, text, false)
	return nil
}

// OnError reports a failure in the Discord channel.
func (b *Bot) OnError(_ context.Context, job queue.Job, err error) {
	b.finish(job.ID, fmt.Sprintf("**error:** %v", err), true)
}

func (b *Bot) finish(jobID, text string, isErr bool) {
	b.mu.Lock()
	lr := b.pending[jobID]
	delete(b.pending, jobID)
	b.mu.Unlock()

	if lr == nil {
		return
	}
	close(lr.done)
	lr.flush.Stop()

	body := strings.TrimSpace(text)
	if body == "" && !isErr {
		body = "_(empty response)_"
	}
	// Final message is the answer only (progress was ephemeral).
	chunks := chunkRunes(body, b.cfg.MaxMessageRunes)
	if len(chunks) == 0 {
		return
	}
	if _, err := b.session.ChannelMessageEdit(lr.channelID, lr.messageID, chunks[0]); err != nil {
		_, _ = b.session.ChannelMessageSend(lr.channelID, chunks[0])
	}
	for i := 1; i < len(chunks); i++ {
		_, _ = b.session.ChannelMessageSend(lr.channelID, chunks[i])
	}
}

func (b *Bot) flushLoop(lr *liveReply) {
	for {
		select {
		case <-lr.done:
			return
		case <-lr.flush.C:
			lr.mu.Lock()
			if !lr.dirty {
				lr.mu.Unlock()
				continue
			}
			steps := append([]string(nil), lr.steps...)
			answer := lr.answer.String()
			lr.dirty = false
			lr.mu.Unlock()

			preview := renderProgress(steps, answer, true)
			if utf8.RuneCountInString(preview) > b.cfg.MaxMessageRunes {
				preview = string([]rune(preview)[:b.cfg.MaxMessageRunes-1]) + "…"
			}
			_, _ = b.session.ChannelMessageEdit(lr.channelID, lr.messageID, preview)
		}
	}
}

// renderProgress builds the live Discord message body.
func renderProgress(steps []string, answer string, streaming bool) string {
	var b strings.Builder

	if n := len(steps); n > 0 {
		b.WriteString("**Progress**\n")
		start := 0
		if n > maxProgressSteps {
			start = n - maxProgressSteps
			b.WriteString("_…earlier steps omitted_\n")
		}
		for _, s := range steps[start:] {
			b.WriteString("• ")
			b.WriteString(s)
			b.WriteByte('\n')
		}
	}

	answer = strings.TrimSpace(answer)
	if answer != "" {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(answer)
		if streaming {
			b.WriteString(" ▍")
		}
		return b.String()
	}

	if b.Len() == 0 {
		return "_working…_"
	}
	b.WriteString("\n_working…_")
	return b.String()
}

// --- Discord event handlers ---

func (b *Bot) onReady(_ *discordgo.Session, r *discordgo.Ready) {
	b.log.Info("discord ready", "user", r.User.Username)
}

func (b *Bot) onMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author == nil || m.Author.Bot {
		return
	}
	if m.GuildID != "" && len(b.cfg.AllowedGuildIDs) > 0 {
		if _, ok := b.cfg.AllowedGuildIDs[m.GuildID]; !ok {
			return
		}
	}

	content := strings.TrimSpace(m.Content)
	if content == "" {
		return
	}

	ch, err := s.State.Channel(m.ChannelID)
	if err != nil {
		ch, err = s.Channel(m.ChannelID)
		if err != nil {
			b.log.Warn("resolve channel failed", "err", err)
			return
		}
	}
	isThread := ch.Type == discordgo.ChannelTypeGuildPublicThread ||
		ch.Type == discordgo.ChannelTypeGuildPrivateThread ||
		ch.Type == discordgo.ChannelTypeGuildNewsThread
	isDM := ch.Type == discordgo.ChannelTypeDM || ch.Type == discordgo.ChannelTypeGroupDM

	mentioned := userMentioned(m, s.State.User.ID)

	// Top-level guild channel: require mention and open a thread.
	if !isDM && !isThread {
		if b.cfg.RequireMention && !mentioned {
			return
		}
		if !mentioned {
			return
		}
		content = stripMentions(content, s.State.User.ID)
		if content == "" {
			content = "Hello!"
		}
		thread, err := s.MessageThreadStartComplex(m.ChannelID, m.ID, &discordgo.ThreadStart{
			Name:                threadName(content, m.Author.Username),
			AutoArchiveDuration: 60,
		})
		if err != nil {
			b.log.Error("create thread failed", "err", err)
			_, _ = s.ChannelMessageSend(m.ChannelID, "Could not start a thread for this chat.")
			return
		}
		b.enqueue(thread.ID, m, content)
		return
	}

	// DMs and existing threads: respond in-place.
	if mentioned {
		content = stripMentions(content, s.State.User.ID)
	}
	if content == "" {
		return
	}
	b.enqueue(m.ChannelID, m, content)
}

func (b *Bot) enqueue(channelID string, m *discordgo.MessageCreate, prompt string) {
	job := queue.Job{
		ID:         uuid.NewString(),
		SessionKey: "discord:" + channelID,
		CWD:        b.cfg.DefaultCWD,
		Prompt:     prompt,
		CreatedAt:  time.Now(),
		ChannelID:  channelID,
		MessageID:  m.ID,
		UserID:     m.Author.ID,
	}
	if err := b.queue.Enqueue(context.Background(), job); err != nil {
		b.log.Error("enqueue failed", "err", err)
		_, _ = b.session.ChannelMessageSend(channelID, "Queue is full or shutting down; try again.")
		return
	}
	b.log.Info("enqueued", "job", job.ID, "session", job.SessionKey, "user", m.Author.Username)
}

func userMentioned(m *discordgo.MessageCreate, botID string) bool {
	for _, u := range m.Mentions {
		if u.ID == botID {
			return true
		}
	}
	return false
}

func stripMentions(content, botID string) string {
	content = strings.ReplaceAll(content, "<@"+botID+">", "")
	content = strings.ReplaceAll(content, "<@!"+botID+">", "")
	return strings.TrimSpace(content)
}

func threadName(content, username string) string {
	name := content
	if name == "" {
		name = "chat with " + username
	}
	// Discord thread name max is 100 chars.
	runes := []rune(name)
	if len(runes) > 90 {
		name = string(runes[:90]) + "…"
	}
	return name
}

func chunkRunes(s string, max int) []string {
	if max < 1 {
		max = 1900
	}
	runes := []rune(s)
	if len(runes) == 0 {
		return nil
	}
	var out []string
	for len(runes) > 0 {
		n := max
		if n > len(runes) {
			n = len(runes)
		}
		out = append(out, string(runes[:n]))
		runes = runes[n:]
	}
	return out
}
