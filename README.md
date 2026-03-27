# Gua

Multi-tenant AI agent server that bridges messaging platforms with AI backends through a fully decoupled architecture.

Each user gets their own isolated AI session. No API keys required — use your existing AI subscription (Claude Max, etc.) and share it across users on any supported messaging platform.

## Comparison

| | **Gua** | **[weclaw](https://github.com/fastclaw-ai/weclaw)** | **[ccbot](https://github.com/six-ddc/ccbot)** |
|---|---|---|---|
| **Language** | Go | Go | Python |
| **Channel** | Interface-based (WeChat, Telegram implemented) | WeChat | Telegram |
| **Agent** | Interface-based (Claude Code implemented) | Claude/Codex/Kimi via ACP, CLI, or HTTP | Claude Code |
| **Runtime** | Interface-based (tmux implemented) | N/A | tmux (hardcoded) |
| **Communication** | Agent-defined (Claude Code: MCP channel protocol, persistent bidirectional Unix socket) | ACP: JSON-RPC 2.0 over long-running subprocess; CLI: process per message with `--stream-json`; HTTP: API client | tmux `send-keys` for input; JSONL file polling for output (byte-offset incremental read) |
| **Billing** | Agent-dependent (Claude Code: subscription, no per-message API cost) | ACP/CLI: subscription; HTTP: API token-based | Subscription (local Claude Code CLI) |
| **Multi-user** | Multi-tenant — one server serves many users with isolated sessions | Single instance per account; multiple instances for multiple users | Multi-user — one Telegram bot serves many users via topic-to-window mapping |
| **Permission handling** | Agent-defined (Claude Code: hooks intercept before terminal; routed to user for approval) | ACP: auto-allow all; CLI: system prompt injection | Terminal regex detection; inline keyboard buttons |
| **TUI menu** | Agent-defined (Claude Code: `capture-pane` boundary detection; `/select N` navigation) | Not handled | Full TUI: regex `UIPattern` top/bottom delimiters; inline keyboard |
| **Media** | Channel-defined (WeChat: images, voice, video, files; Telegram: photos, documents, voice, video) | Text only | Text, voice, screenshots |
| **Sharing** | `/share` generates QR/invite via Channel interface | Manual | Manual |
| **Management** | CLI: `accounts` (bot accounts), `sessions` (user sessions) | None | None |
| **Architecture** | Agent / Channel / Runtime fully decoupled via interfaces | Agent abstraction (3 modes) but channel hardcoded | Monolithic (tmux + Telegram tightly coupled) |

## Architecture

```
Channel (messaging)     Agent (AI backend)      Runtime (process container)
  ├── WeChat ✓            ├── Claude Code ✓        ├── tmux ✓
  ├── Telegram ✓          ├── Codex                ├── screen
  └── Discord             └── Gemini               └── container

                    ↕                ↕
               [Server orchestration]
            per-user session management
```

All three dimensions are **fully decoupled via Go interfaces**. Adding a new channel, agent, or runtime requires zero changes to existing code.

- **New Agent** (e.g., Codex): implement `Agent` interface — different agents may use different communication methods (MCP, JSONL polling, API calls)
- **New Channel** (e.g., Discord): implement `Channel` + `Presenter` — different channels may offer inline keyboards, buttons, or text-only interaction
- **New Runtime** (e.g., containers): implement `Runtime` — different runtimes may use Docker, screen, or direct process management

## Quick Start

### Prerequisites

- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed and authenticated
- tmux

### Install (Linux & macOS)

```bash
curl -sSfL https://raw.githubusercontent.com/CMGS/gua/refs/heads/master/scripts/install.sh | bash
```

This will:
- Download and install `gua-server` + `gua-bridge` (`/usr/bin/` on Linux, `/usr/local/bin/` on macOS)
- Linux: create a systemd service (`gua.service`) managed via `systemctl`
- macOS: create a LaunchAgent (`com.gua.server`) managed via `launchctl`
- Set up work directory at `~/.gua/workspace/`

The script is idempotent — safe to re-run for upgrades (stops running service before installing).

For a specific version: `GUA_VERSION=0.2 curl ... | bash`

### Build from Source

Requires Go 1.24+.

```bash
make build

# Check version
./bin/gua-server --version
```

### Setup

**WeChat** (one-time per account):

```bash
./bin/gua-server setup --backend wechat
# Scan the QR code with WeChat
# Run again for additional accounts
```

**Telegram** (one-time):

```bash
./bin/gua-server setup --backend telegram
# Enter bot token from @BotFather
```

### Start

```bash
# Linux (systemd):
sudo systemctl start gua
sudo systemctl enable gua  # auto-start on boot

# macOS (launchctl):
launchctl load ~/Library/LaunchAgents/com.gua.server.plist
launchctl start com.gua.server

# Or run directly:
gua-server start \
  --backend wechat \
  --work-dir ~/.gua/workspace \
  --bridge-bin $(which gua-bridge) \
  --model sonnet \
  --prompt ./my-custom-rules.md  # optional: appended to init prompt

# Telegram:
gua-server start \
  --backend telegram \
  --work-dir ~/.gua/workspace \
  --bridge-bin $(which gua-bridge) \
  --model sonnet
```

### Account Management

```bash
# List registered bot accounts (~/.gua/<backend>/accounts/)
./bin/gua-server accounts list
./bin/gua-server accounts list --backend wechat

# Remove a bot account
./bin/gua-server accounts remove <account-id>
```

### Session Management

```bash
# List user chat sessions (<work-dir>/sessions/)
./bin/gua-server sessions list --work-dir /path/to/workspace

# Remove a user's session data
./bin/gua-server sessions remove <user-id> --work-dir /path/to/workspace
```

### In-Chat Commands

**Global** (handled by server, all channels):

| Command | Effect |
|---|---|
| `/whosyourdaddy` | Activate yolo mode (`--dangerously-skip-permissions`) |
| `/imyourdaddy` | Restore safe mode |
| `/share` | Send the bot's QR code / invite link for sharing with other users |
| `/close` | Close current session (next message starts a fresh session) |
| `/clean` | Clean up stale sessions from deleted threads/topics |
| `/rename <name>` | Rename session (and topic on Telegram) |
| `/respawn <dir>` | Switch session to a different working directory (fresh) |
| `/respawn <dir> --continue` | Same, resume most recent conversation |
| `/respawn <dir> --resume <id>` | Same, resume a specific session by ID |

**Agent CLI passthrough** (Claude Code specific, forwarded to terminal):

| Command | Effect |
|---|---|
| `/model` | Switch model (TUI menu) |
| `/fast` | Toggle fast mode (TUI menu) |

**Channel control** (parsed by Presenter, channel-specific UX):

| Command | WeChat | Telegram | Effect |
|---|---|---|---|
| `/yes` | Text | Text | Confirm / allow / enter |
| `/no` | Text | Text | Deny / reject |
| `/cancel` | Text | Text | Cancel / exit menu |
| `/select N` | Text | Text | Select option N |
| ✅ Allow / ❌ Deny | — | Inline keyboard | Permission approval |
| Option buttons | — | Inline keyboard | TUI menu selection |
| 是 / 好 / 可以 | Voice-friendly | — | Same as `/yes` |
| 不 / 不要 / 取消 | Voice-friendly | — | Same as `/no` |

Telegram uses inline keyboard buttons for permissions and TUI menus; text commands work as fallback. WeChat uses text-only commands with Chinese voice shortcuts.

## Project Structure

```
gua/
├── version/            Build version info (injected via ldflags)
├── types/              Shared types (MediaFile, Action)
├── utils/              Shared utilities (JSON, MIME, SyncValue, SyncQueue, TempFile)
├── config/             Base prompt (security rules)
├── runtime/            Process container interface
│   └── tmux/           tmux implementation
├── agent/              Agent interface
│   └── claude/         Claude Code implementation
│       ├── protocol/   Bridge socket protocol (envelope types)
│       ├── mcpserver/  Lightweight MCP JSON-RPC server
│       └── bridge/     Bridge binary (MCP server inside Claude Code)
├── channel/            Channel + Presenter interfaces
│   ├── wechat/         WeChat implementation
│   └── telegram/       Telegram implementation
├── libc/wechat/        WeChat iLink protocol library
├── server/             Server orchestration (Channel ↔ Agent routing)
├── cmd/                CLI entry point (setup, start, accounts, sessions)
├── scripts/            Install script (Linux systemd + macOS LaunchAgent)
└── bot/                Reference implementations
    ├── weclaw/         WeChat + Claude (ACP/CLI/HTTP) — Go
    └── ccbot/          Telegram + Claude Code (tmux + JSONL) — Python
```

## Design Decisions

**Interface-driven Architecture**: Server orchestration knows nothing about specific agents, channels, or runtimes. The `Agent` interface defines how to send messages and handle control actions. The `Channel` interface defines how to receive and deliver messages. The `Runtime` interface defines how to manage processes. Each implementation decides its own communication method, session model, and capabilities.

**Agent CLI Command Whitelist**: Each agent declares which CLI commands it supports via `CLICommands()`. The server forwards matching `/commands` directly to the agent's terminal; unrecognized commands are sent as normal messages through the agent's standard message protocol.

**Presenter Pattern**: Channel-specific rendering is encapsulated in a `Presenter` interface. The same agent response can be rendered as plain text with `/select N` commands (WeChat), inline keyboard buttons (Telegram), or rich embeds (Discord). The agent layer produces structured data; the presenter decides how to display it.

**Telegram Forum Topics**: Each Telegram topic maps to an isolated session. Users create topics via Telegram's native UI; each topic gets its own tmux window and Claude Code instance. `/close` kills the session; deleting a topic leaves the session orphaned until `/clean` is run. Stale callbacks from old inline keyboards are silently dropped via the `ActionOnly` flag.

**Four-layer Prompt**: Init prompt = `config/base.md` (security rules) + agent prompt (agent-specific behavior) + channel prompt (channel-specific rules) + `presenter.MediaInstructions()` (media handling) + optional user prompt (`--prompt` flag). Each layer is independently managed; user prompt has highest priority (appended last).

### Claude Code Implementation Details

The Claude Code agent uses these specific mechanisms (other agents may differ):

- **MCP Channel Protocol**: Persistent bidirectional communication via Unix socket bridge
- **Hook-based Permissions**: `PermissionRequest` and `Elicitation` hooks intercept prompts before terminal display
- **TUI Menu Capture**: `capture-pane` with separator + `Esc to` boundary detection for CLI command menus
- **Watch**: FIFO-based `pipe-pane` keeps the terminal output stream alive (interactive detection uses `capture-pane` instead)

## Acknowledgments

- [Tencent WeChat OpenClaw](https://www.npmjs.com/package/@tencent-weixin/openclaw-weixin-cli) — WeChat iLink Bot API
- [weclaw](https://github.com/fastclaw-ai/weclaw) — Pioneer WeChat AI agent bridge, inspired the iLink protocol implementation
- [ccbot](https://github.com/six-ddc/ccbot) — Telegram Claude Code bot, demonstrated tmux-based TUI handling and JSONL output monitoring

## License

[MIT](LICENSE)
