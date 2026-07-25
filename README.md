# pi-bridge

Discord front-end for [pi](https://github.com/earendil-works/pi-coding-agent) using a simple in-memory job queue.

```text
Discord mention/DM/thread
        │
   in-memory channel queue
        │
   worker(s) → pi --mode rpc
        │
   stream reply back to Discord
```

## Prerequisites

- Go 1.22+
- `pi` on your `PATH` and authenticated (same as interactive use)
- A Discord bot token with **Message Content Intent** enabled

## Install

### Homebrew (after first release)

```bash
brew install vitaraliseng/tap/pi-bridge
```

### Go

```bash
go install github.com/vitaraliseng/pi-bridge/cmd/pi-bridge@latest
```

### From a GitHub Release

Binaries for macOS/Linux/Windows are attached to each [release](https://github.com/vitaraliseng/pi-bridge/releases).

### From this repo

```bash
cd pi-bridge
go install ./cmd/pi-bridge
# or: make install
```

Ensure `$(go env GOPATH)/bin` is on your `PATH`.

### Releasing (maintainers)

Tags matching `v*` trigger GoReleaser via GitHub Actions:

```bash
git tag v0.1.0
git push origin v0.1.0
```

That builds multi-arch binaries and publishes a GitHub Release.  
To also update Homebrew, create `vitaraliseng/homebrew-tap` and add a repo secret
`HOMEBREW_TAP_TOKEN` (classic PAT with `repo` scope).

## Setup (guided)

```bash
pi-bridge setup
# or just:
pi-bridge
```

If no token is configured, an interactive wizard walks you through Discord setup:

1. Open the Discord Developer Portal so you can create an application
2. Open the Bot page after you paste the Application ID
3. Prompt for the token (hidden input) and validate it
4. Help create a server if needed, then open the invite link
5. Save config to your user config dir (mode `600`)
6. Optionally start the bot immediately

Config is stored at (first existing file wins on load):

- `$PI_BRIDGE_CONFIG`
- `./pi-bridge.env`
- macOS: `~/Library/Application Support/pi-bridge/config.env`
- Linux: `~/.config/pi-bridge/config.env`

## Run

```bash
pi-bridge
```

Optional env vars (override the config file):

| Variable | Default | Meaning |
|---|---|---|
| `DISCORD_TOKEN` | _(required)_ | Bot token |
| `DISCORD_APPLICATION_ID` | | Application ID (from setup) |
| `ALLOWED_GUILD_IDS` | all | Comma-separated server IDs |
| `REQUIRE_MENTION` | `true` | Guild messages must @mention the bot |
| `PI_CWD` | process cwd | Working directory for pi tools |
| `PI_BINARY` | `pi` | Path to pi executable |
| `PI_ARGS` | _(none)_ | Extra args after `--mode rpc` |
| `PI_PERSIST_SESSIONS` | `true` | Resume pi sessions across restarts |
| `PI_SESSION_DIR` | user config `…/pi-bridge/sessions` | Where pi JSONL sessions are stored |
| `PI_SESSION_INDEX` | user config `…/session-index.json` | Discord thread → session file map |
| `WORKERS` | `1` | Concurrent job workers |
| `QUEUE_SIZE` | `64` | In-memory queue capacity |
| `JOB_TIMEOUT` | `10m` | Per-job timeout |
| `PI_BRIDGE_CONFIG` | auto | Path to dotenv config file |

### Session memory

By default each Discord thread maps to a **persisted pi session**:

- live process while idle &lt; 30 minutes
- session file on disk forever (until you delete it)
- after `pi-bridge` restart, the same thread resumes the same session file

Disable with `PI_PERSIST_SESSIONS=false` or `PI_ARGS=--no-session`.

## Always-on (laptop sleep/shutdown)

`pi-bridge` is a normal process. If this laptop sleeps or powers off, the bot goes offline and cannot reply.

**Session memory survives restarts** (files on disk), but **someone has to be running `pi-bridge`** for Discord to work.

Options:

| Approach | Good when |
|---|---|
| Leave the laptop awake + `pi-bridge` in a terminal/tmux | Dev / casual use |
| macOS `launchd` user agent | Laptop mostly open; auto-restart on login/crash |
| Small always-on host (VPS, mini PC, Raspberry Pi) | You want it 24/7 |
| Cloud VM (Fly, Railway, a $5 VPS) | Reliable remote uptime |

### Quick local auto-start (macOS launchd)

```bash
# create ~/Library/LaunchAgents/com.pi-bridge.plist  (see deploy/com.pi-bridge.plist)
launchctl load ~/Library/LaunchAgents/com.pi-bridge.plist
launchctl start com.pi-bridge
```

Note: launchd on a laptop still stops when the machine is off/asleep. For true 24/7, run on a server that stays powered.

### VPS sketch

1. Install `go`, `pi`, and your model API keys on the server
2. `go install` pi-bridge (or copy the binary)
3. Copy `config.env` (token) with mode `600`
4. Run under `systemd` / Docker / `tmux`
5. Set `PI_CWD` to whatever repo tree the bot should work in on that host

## Usage

1. In a guild channel: `@Bot review this function`  
   → bot opens a thread and replies there
2. Continue chatting in that thread (no mention required)
3. DM the bot directly for a private session

Each Discord channel/thread maps to one pi session (`SessionKey = discord:<channelID>`). Live processes are reaped after 30 minutes idle; the session file remains for resume.

## Layout

```text
cmd/pi-bridge/      entrypoint (installed binary name)
internal/
  config/           env config
  discord/          gateway handlers + reply sink
  pi/               RPC client + session pool
  queue/            in-memory channel queue
  sessionstore/     Discord thread → pi session file index
  setup/            interactive Discord setup wizard
  worker/           job runner
deploy/             example launchd plist
```

## Next steps

- GitHub PR webhook producer into the same queue
- Redis/NATS when you outgrow a single process
- Discord buttons for Stop (`abort`) / New session
