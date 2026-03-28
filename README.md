# Gua

Multi-tenant AI agent server that bridges messaging platforms with AI backends through a fully decoupled architecture.

Each user gets their own isolated AI session. No API keys required — use your existing AI subscription (Claude Max, etc.) and share it across users on any supported messaging platform.

## Comparison

| | **Gua** | **[weclaw](https://github.com/fastclaw-ai/weclaw)** | **[ccbot](https://github.com/six-ddc/ccbot)** |
|---|---|---|---|
| **Language** | Go | Go | Python |
| **Channel** | Interface-based (WeChat, Telegram implemented) | WeChat | Telegram |
| **Agent** | Interface-based (Claude Code, Codex implemented) | Claude/Codex/Kimi via ACP, CLI, or HTTP | Claude Code |
| **Runtime** | Interface-based (tmux implemented) | N/A | tmux (hardcoded) |
| **Communication** | Agent-defined (Claude Code: MCP channel protocol via Unix socket; Codex: JSON-RPC 2.0 via `codex mcp-server` over FIFO pipes) | ACP: JSON-RPC 2.0 over long-running subprocess; CLI: process per message with `--stream-json`; HTTP: API client | tmux `send-keys` for input; JSONL file polling for output (byte-offset incremental read) |
| **Billing** | Agent-dependent (Claude Code: Anthropic subscription; Codex: OpenAI/ChatGPT subscription) | ACP/CLI: subscription; HTTP: API token-based | Subscription (local Claude Code CLI) |
| **Multi-user** | Multi-tenant — one server serves many users with isolated sessions | Single instance per account; multiple instances for multiple users | Multi-user — one Telegram bot serves many users via topic-to-window mapping |
| **Permission handling** | Agent-defined (Claude Code: hooks intercept before terminal; Codex: MCP elicitation/create requests; both routed to user for approval) | ACP: auto-allow all; CLI: system prompt injection | Terminal regex detection; inline keyboard buttons |
| **TUI menu** | Agent-defined (Claude Code: `capture-pane` boundary detection, `/select N` navigation; Codex: no TUI) | Not handled | Full TUI: regex `UIPattern` top/bottom delimiters; inline keyboard |
| **Media** | Channel-defined (WeChat: images, voice, video, files; Telegram: photos, documents, voice, video) | Text only | Text, voice, screenshots |
| **Sharing** | `/share` generates QR/invite via Channel interface | Manual | Manual |
| **Management** | CLI: `accounts` (bot accounts), `sessions` (user sessions) | None | None |
| **Architecture** | Agent / Channel / Runtime fully decoupled via interfaces | Agent abstraction (3 modes) but channel hardcoded | Monolithic (tmux + Telegram tightly coupled) |

## Architecture

```
Channel (messaging)     Agent (AI backend)      Runtime (process container)
  ├── WeChat ✓            ├── Claude Code ✓        ├── tmux ✓
  ├── Telegram ✓          ├── Codex ✓              ├── screen
  └── Discord             └── Gemini               └── container

                    ↕                ↕
               [Server orchestration]
            per-user session management
```

All three dimensions are **fully decoupled via Go interfaces**. Adding a new channel, agent, or runtime requires zero changes to existing code.

- **New Agent** (e.g., Gemini): implement `Agent` interface — different agents may use different communication methods (MCP, JSONL polling, API calls)
- **New Channel** (e.g., Discord): implement `Channel` + `Presenter` — different channels may offer inline keyboards, buttons, or text-only interaction
- **New Runtime** (e.g., containers): implement `Runtime` — different runtimes may use Docker, screen, or direct process management

## Quick Start

### Prerequisites

- tmux
- At least one AI agent CLI installed and authenticated:
  - [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) for Claude agent
  - [Codex CLI](https://github.com/openai/codex) for Codex agent

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

# Or run directly (Claude Code + WeChat):
gua-server start \
  --backend wechat \
  --agent claude \
  --work-dir ~/.gua/workspace \
  --bridge-bin $(which gua-bridge) \
  --model sonnet \
  --prompt ./my-custom-rules.md  # optional: appended to init prompt

# Claude Code + Telegram:
gua-server start \
  --backend telegram \
  --agent claude \
  --work-dir ~/.gua/workspace \
  --bridge-bin $(which gua-bridge) \
  --model sonnet

# Codex + WeChat:
gua-server start \
  --backend wechat \
  --agent codex \
  --work-dir ~/.gua/workspace

# Codex + Telegram:
gua-server start \
  --backend telegram \
  --agent codex \
  --work-dir ~/.gua/workspace
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

### Agent Comparison

| Feature | Claude Code | Codex |
|---|---|---|
| **Subscription** | Anthropic (Claude Max) | OpenAI (ChatGPT Plus) |
| **Communication** | MCP channel protocol via Unix socket bridge | JSON-RPC 2.0 via `codex mcp-server` over FIFO pipes |
| **Permission handling** | Hook-based (`PermissionRequest` / `Elicitation`) | MCP `elicitation/create` requests |
| **Yolo mode** (`/whosyourdaddy`) | `--dangerously-skip-permissions` flag | `approval-policy: "never"` |
| **TUI menu** | `capture-pane` boundary detection + `/select N` | Not supported (no TUI) |
| **CLI passthrough** (`/model`, `/fast`) | Forwarded to terminal via `RawInput` | Not supported (sent as regular messages) |
| **Session resume** (`--continue`, `--resume`) | Supported | Not supported (threadId-based) |
| **Tmux observability** | Full terminal visible (interactive TUI) | JSON-RPC traffic mirrored via `tee /dev/stderr` |
| **Required flags** | `--bridge-bin` | None |
| **Init prompt file** | `CLAUDE.md` | `CODEX.md` |

### Channel Comparison

| Feature | WeChat | Telegram |
|---|---|---|
| **Markdown** | Not supported (plain text only) | Supported |
| **Media types** | Image, voice, video, file | Photo, document, voice, video |
| **Permission UX** | Text commands (`/yes`, `/no`) | Inline keyboard buttons + text fallback |
| **TUI menu UX** | Text commands (`/select N`) | Inline keyboard buttons + text fallback |
| **Voice shortcuts** | 是/好/可以 = `/yes`, 不/不要/取消 = `/no` | Not supported |
| **Session model** | Per-user (single session per WeChat user) | Per-topic (forum topics, each topic = isolated session) |
| **Sharing** | `/share` → QR code image | Not supported |
| **Thread cleanup** | Not needed | `/clean` removes orphaned sessions from deleted topics |
| **Multi-account** | Multiple bot accounts with hot-reload (fsnotify) | Single bot token |

### In-Chat Commands

**Global** (handled by server, works with all agents and channels):

| Command | Effect |
|---|---|
| `/whosyourdaddy` | Activate yolo mode (skip permission prompts) |
| `/imyourdaddy` | Restore safe mode (require permission approval) |
| `/share` | Send the bot's QR code / invite link |
| `/close` | Close current session (next message starts fresh) |
| `/clean` | Clean up stale sessions from deleted threads/topics |
| `/rename <name>` | Rename session (and Telegram topic) |
| `/respawn <dir>` | Switch to a different working directory |
| `/respawn <dir> --continue` | Same, resume most recent conversation (Claude Code only) |
| `/respawn <dir> --resume <id>` | Same, resume specific session (Claude Code only) |

**Channel control** (parsed by Presenter):

| Input | WeChat | Telegram | Effect |
|---|---|---|---|
| `/yes` `/y` `/ok` | Text | Text | Confirm / approve |
| `/no` `/n` `/cancel` | Text | Text | Deny / reject |
| `/select N` | Text | Text | Select option N |
| Inline buttons | — | Keyboard | Permission / menu selection |
| 是 / 好 / 可以 | Voice-friendly | — | Same as `/yes` |
| 不 / 不要 / 取消 | Voice-friendly | — | Same as `/no` |

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
│   ├── claude/         Claude Code implementation
│   │   ├── protocol/   Bridge socket protocol (envelope types)
│   │   ├── mcpserver/  Lightweight MCP JSON-RPC server
│   │   └── bridge/     Bridge binary (MCP server inside Claude Code)
│   └── codex/          Codex implementation (JSON-RPC over FIFO pipes)
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

**Four-layer Prompt**: Init prompt = `config/base.md` (security rules) + agent prompt (agent-specific behavior) + channel prompt (channel-specific rules) + `presenter.MediaInstructions()` (media handling) + optional user prompt (`--prompt` flag). Each layer is independently managed; user prompt has highest priority (appended last). The assembled prompt is written to the session workdir as `CLAUDE.md` or `CODEX.md` depending on the agent — each agent only reads its own instruction file.

### Implementation Notes

**Claude Code**: Uses a custom bridge binary (`gua-bridge`) that runs as an MCP server inside Claude Code. The bridge connects back to gua via a persistent Unix socket, enabling bidirectional communication. Permission prompts are intercepted by hooks before reaching the terminal. TUI menus (e.g., `/model`) are captured via `capture-pane` boundary detection and rendered as channel-specific controls.

**Codex**: Runs `codex mcp-server` as a long-lived subprocess with stdin/stdout redirected through FIFO named pipes. First message creates a new thread via the `codex` MCP tool; subsequent messages continue via `codex-reply` with `threadId`. Permission prompts arrive as `elicitation/create` server-initiated JSON-RPC requests. No bridge binary needed — communication is direct over FIFOs.

## Acknowledgments

- [Tencent WeChat OpenClaw](https://www.npmjs.com/package/@tencent-weixin/openclaw-weixin-cli) — WeChat iLink Bot API
- [weclaw](https://github.com/fastclaw-ai/weclaw) — Pioneer WeChat AI agent bridge, inspired the iLink protocol implementation
- [ccbot](https://github.com/six-ddc/ccbot) — Telegram Claude Code bot, demonstrated tmux-based TUI handling and JSONL output monitoring

## License

[MIT](LICENSE)
