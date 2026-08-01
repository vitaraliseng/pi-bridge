package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// Client talks to a pi process in RPC mode over stdin/stdout JSONL.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	mu      sync.Mutex
	nextID  atomic.Uint64
	pending map[string]chan Response
	events  chan Event

	// serialize prompts against event consumption for this client
	runMu sync.Mutex
}

// Command is a JSON-RPC style command sent to pi.
type Command map[string]any

// Response is a command response from pi.
type Response struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Error   string          `json:"error,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Event is a streamed agent event.
type Event map[string]any

// StartOptions configures the pi subprocess.
type StartOptions struct {
	// Binary defaults to "pi".
	Binary string
	// CWD is the working directory for the agent tools.
	CWD string
	// Args are appended after ["--mode", "rpc"].
	// Default: ["--no-session"]
	Args []string
}

// Start launches pi --mode rpc.
func Start(ctx context.Context, opts StartOptions) (*Client, error) {
	binary := opts.Binary
	if binary == "" {
		binary = "pi"
	}
	args := []string{"--mode", "rpc"}
	if len(opts.Args) == 0 {
		args = append(args, "--no-session")
	} else {
		args = append(args, opts.Args...)
	}

	cmd := exec.CommandContext(ctx, binary, args...)
	if opts.CWD != "" {
		cmd.Dir = opts.CWD
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	// Keep stderr available for debugging; discard by default.
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("start pi: %w", err)
	}

	c := &Client{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		pending: make(map[string]chan Response),
		events:  make(chan Event, 256),
	}
	go c.readLoop()
	return c, nil
}

// Call sends a command and waits for its correlated response.
func (c *Client) Call(ctx context.Context, cmd Command) (Response, error) {
	id := fmt.Sprintf("req-%d", c.nextID.Add(1))
	cmd["id"] = id

	ch := make(chan Response, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	payload, err := json.Marshal(cmd)
	if err != nil {
		return Response{}, fmt.Errorf("marshal command: %w", err)
	}
	payload = append(payload, '\n')

	c.mu.Lock()
	_, err = c.stdin.Write(payload)
	c.mu.Unlock()
	if err != nil {
		return Response{}, fmt.Errorf("write command: %w", err)
	}

	select {
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case resp := <-ch:
		if !resp.Success {
			if resp.Error == "" {
				resp.Error = "unknown error"
			}
			return resp, fmt.Errorf("pi %s failed: %s", resp.Command, resp.Error)
		}
		return resp, nil
	}
}

// Image is a multimodal image for pi RPC prompt commands.
type Image struct {
	Type     string `json:"type"` // "image"
	Data     string `json:"data"` // base64
	MimeType string `json:"mimeType"`
}

// Prompt sends a user message. Fails if the agent is already streaming
// unless you use PromptWithBehavior.
func (c *Client) Prompt(ctx context.Context, message string, images []Image) error {
	cmd := Command{"type": "prompt", "message": message}
	if len(images) > 0 {
		cmd["images"] = images
	}
	_, err := c.Call(ctx, cmd)
	return err
}

// PromptWithBehavior queues a message while streaming.
// behavior is "steer" or "followUp".
func (c *Client) PromptWithBehavior(ctx context.Context, message, behavior string, images []Image) error {
	cmd := Command{
		"type":              "prompt",
		"message":           message,
		"streamingBehavior": behavior,
	}
	if len(images) > 0 {
		cmd["images"] = images
	}
	_, err := c.Call(ctx, cmd)
	return err
}

// Abort cancels the current agent operation.
func (c *Client) Abort(ctx context.Context) error {
	_, err := c.Call(ctx, Command{"type": "abort"})
	return err
}

// GetLastAssistantText returns the last assistant text, or empty string.
func (c *Client) GetLastAssistantText(ctx context.Context) (string, error) {
	resp, err := c.Call(ctx, Command{"type": "get_last_assistant_text"})
	if err != nil {
		return "", err
	}
	var data struct {
		Text *string `json:"text"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return "", fmt.Errorf("decode last text: %w", err)
	}
	if data.Text == nil {
		return "", nil
	}
	return *data.Text, nil
}

// State is a subset of pi get_state used by the session pool.
type State struct {
	SessionFile  string `json:"sessionFile"`
	SessionID    string `json:"sessionId"`
	SessionName  string `json:"sessionName"`
	MessageCount int    `json:"messageCount"`
	IsStreaming  bool   `json:"isStreaming"`
}

// GetState returns the current pi session state.
func (c *Client) GetState(ctx context.Context) (State, error) {
	resp, err := c.Call(ctx, Command{"type": "get_state"})
	if err != nil {
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(resp.Data, &st); err != nil {
		return State{}, fmt.Errorf("decode state: %w", err)
	}
	return st, nil
}

// SetSessionName sets the display name for the active pi session.
func (c *Client) SetSessionName(ctx context.Context, name string) error {
	_, err := c.Call(ctx, Command{"type": "set_session_name", "name": name})
	return err
}

// RunPrompt sends a prompt and waits until the agent fully settles.
// onProgress is optional and receives status/tool/text updates.
func (c *Client) RunPrompt(ctx context.Context, message string, images []Image, onProgress func(Progress)) (string, error) {
	c.runMu.Lock()
	defer c.runMu.Unlock()

	// Drain any stale events from a previous run.
	c.drainEvents()

	if err := c.Prompt(ctx, message, images); err != nil {
		// If already streaming, queue as follow-up.
		if err2 := c.PromptWithBehavior(ctx, message, "followUp", images); err2 != nil {
			return "", err
		}
	}

	emit := func(p Progress) {
		if onProgress != nil {
			onProgress(p)
		}
	}

	for {
		select {
		case <-ctx.Done():
			// Best-effort abort with a hard cap — never block shutdown/job cancel
			// waiting for pi (e.g. long-running bash that ignores abort briefly).
			c.abortBestEffort()
			return "", ctx.Err()
		case ev, ok := <-c.events:
			if !ok {
				return "", io.EOF
			}
			if err := c.handleEvent(ctx, ev, emit); err != nil {
				return "", err
			}
			if typ, _ := ev["type"].(string); typ == "agent_settled" {
				return c.GetLastAssistantText(ctx)
			}
		}
	}
}

const abortWait = 2 * time.Second

func (c *Client) abortBestEffort() {
	ctx, cancel := context.WithTimeout(context.Background(), abortWait)
	defer cancel()
	_ = c.Abort(ctx)
}

func (c *Client) handleEvent(ctx context.Context, ev Event, emit func(Progress)) error {
	switch ev["type"] {
	case "agent_start":
		emit(Progress{Kind: ProgressStatus, Message: "Starting…"})

	case "turn_start":
		emit(Progress{Kind: ProgressStatus, Message: "Planning next step…"})

	case "tool_execution_start":
		name, _ := ev["toolName"].(string)
		args := asMap(ev["args"])
		emit(Progress{
			Kind:    ProgressToolStart,
			Message: FormatToolStart(name, args),
		})

	case "tool_execution_end":
		name, _ := ev["toolName"].(string)
		isErr, _ := ev["isError"].(bool)
		emit(Progress{
			Kind:    ProgressToolEnd,
			Message: FormatToolEnd(name, isErr),
		})

	case "compaction_start":
		emit(Progress{Kind: ProgressStatus, Message: "Compacting conversation context…"})

	case "compaction_end":
		emit(Progress{Kind: ProgressStatus, Message: "Context compacted"})

	case "auto_retry_start":
		emit(Progress{Kind: ProgressStatus, Message: "Retrying after a temporary error…"})

	case "message_update":
		ame, _ := ev["assistantMessageEvent"].(map[string]any)
		if ame == nil {
			return nil
		}
		switch ame["type"] {
		case "text_delta":
			if delta, ok := ame["delta"].(string); ok && delta != "" {
				emit(Progress{Kind: ProgressDelta, Delta: delta})
			}
		case "thinking_start":
			emit(Progress{Kind: ProgressStatus, Message: "Thinking…"})
		case "thinking_delta":
			// Avoid spamming every thought token; thinking_start is enough.
		case "toolcall_start":
			emit(Progress{Kind: ProgressStatus, Message: "Preparing a tool call…"})
		}

	case "extension_ui_request":
		// Headless: auto-cancel dialogs so the agent does not block.
		id, _ := ev["id"].(string)
		if id == "" {
			return nil
		}
		emit(Progress{Kind: ProgressStatus, Message: "Skipped an interactive prompt (headless mode)"})
		_, err := c.Call(ctx, Command{
			"type": "extension_ui_response",
			"id":   id,
			//nolint:misspell // pi RPC field name uses British spelling
			"cancelled": true,
		})
		return err
	}
	return nil
}

func asMap(v any) map[string]any {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func (c *Client) drainEvents() {
	for {
		select {
		case <-c.events:
		default:
			return
		}
	}
}

func (c *Client) readLoop() {
	defer close(c.events)

	reader := bufio.NewReader(c.stdout)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			// Strip trailing \n and optional \r
			if line[len(line)-1] == '\n' {
				line = line[:len(line)-1]
			}
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			if len(line) > 0 {
				c.dispatch(line)
			}
		}
		if err != nil {
			return
		}
	}
}

func (c *Client) dispatch(line []byte) {
	var envelope map[string]any
	if err := json.Unmarshal(line, &envelope); err != nil {
		return
	}

	if typ, _ := envelope["type"].(string); typ == "response" {
		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			return
		}
		c.mu.Lock()
		ch := c.pending[resp.ID]
		c.mu.Unlock()
		if ch != nil {
			select {
			case ch <- resp:
			default:
			}
		}
		return
	}

	select {
	case c.events <- Event(envelope):
	default:
		// Drop if consumer is too slow; prefer progress over deadlock.
	}
}

// Close terminates the pi process.
// It does not wait forever: if Wait stalls, the process is killed.
func (c *Client) Close() error {
	_ = c.stdin.Close()
	if c.cmd.Process == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() {
		done <- c.cmd.Wait()
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		_ = c.cmd.Process.Kill()
		return <-done
	}
}
