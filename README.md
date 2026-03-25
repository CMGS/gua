# Gua

Multi-tenant AI agent server that bridges messaging platforms with AI backends through a fully decoupled architecture.

Each user gets their own isolated AI session. No API keys required — use your existing AI subscription (Claude Max, etc.) and share it across users on any supported messaging platform.

## Comparison

| | **Gua** | **[weclaw](https://github.com/fastclaw-ai/weclaw)** | **[ccbot](https://github.com/six-ddc/ccbot)** |
|---|---|---|---|
| **Language** | Go | Go | Python |
| **Channel** | Interface-based (WeChat implemented) | WeChat | Telegram |
| **Agent** | Interface-based (Claude Code implemented) | Claude/Codex/Kimi via ACP, CLI, or HTTP | Claude Code |
| **Runtime** | Interface-based (tmux implemented) | N/A | tmux (hardcoded) |
| **Communication** | Agent-defined (Claude Code: MCP channel protocol, persistent bidirectional Unix socket) | ACP: JSON-RPC 2.0 over long-running subprocess; CLI: process per message with `--stream-json`; HTTP: API client | tmux `send-keys` for input; JSONL file polling for output (byte-offset incremental read) |
| **Billing** | Agent-dependent (Claude Code: subscription, no per-message API cost) | ACP/CLI: subscription; HTTP: API token-based | Subscription (local Claude Code CLI) |
| **Multi-user** | Multi-tenant — one server serves many users with isolated sessions | Single instance per account; multiple instances for multiple users | Multi-user — one Telegram bot serves many users via topic-to-window mapping |
| **Permission handling** | Agent-defined (Claude Code: hooks intercept before terminal; routed to user for approval) | ACP: auto-allow all; CLI: system prompt injection | Terminal regex detection; inline keyboard buttons |
| **TUI menu** | Agent-defined (Claude Code: `capture-pane` boundary detection; `/select N` navigation) | Not handled | Full TUI: regex `UIPattern` top/bottom delimiters; inline keyboard |
| **Media** | Channel-defined (WeChat: images, voice, video, files) | Text only | Text, voice, screenshots |
| **Sharing** | `/share` generates QR/invite via Channel interface | Manual | Manual |
| **Management** | CLI: `accounts` (bot accounts), `sessions` (user sessions) | None | None |
| **Architecture** | Agent / Channel / Runtime fully decoupled via interfaces | Agent abstraction (3 modes) but channel hardcoded | Monolithic (tmux + Telegram tightly coupled) |

## Architecture

```
Channel (messaging)     Agent (AI backend)      Runtime (process container)
  ├── WeChat ✓            ├── Claude Code ✓        ├── tmux ✓
  ├── Telegram            ├── Codex                ├── screen
  └── Discord             └── Gemini               └── container

                    ↕                ↕
               [Server orchestration]
            per-user session management
```

All three dimensions are **fully decoupled via Go interfaces**. Adding a new channel, agent, or runtime requires zero changes to existing code.

- **New Agent** (e.g., Codex): implement `Agent` interface — different agents may use different communication methods (MCP, JSONL polling, API calls)
- **New Channel** (e.g., Telegram): implement `Channel` + `Presenter` — different channels may offer inline keyboards, buttons, or text-only interaction
- **New Runtime** (e.g., containers): implement `Runtime` — different runtimes may use Docker, screen, or direct process management

## Quick Start

### Prerequisites

- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed and authenticated
- tmux

### Install (Linux)

```bash
curl -sSfL https://raw.githubusercontent.com/CMGS/gua/refs/heads/master/scripts/install.sh | bash
```

This will:
- Download and install `gua-server` + `gua-bridge` to `/usr/bin/`
- Create a systemd service (`gua.service`)
- Set up work directory at `~/.gua/workspace/`

For a specific version: `GUA_VERSION=0.2 curl ... | bash`

### Install (macOS)

Download from [releases](https://github.com/CMGS/gua/releases) and extract manually:

```bash
# Apple Silicon
curl -sSfL https://github.com/CMGS/gua/releases/download/v0.1/gua_0.1_Darwin_arm64.tar.gz | tar xz
sudo mv gua-server gua-bridge /usr/local/bin/
```

### Build from Source

Requires Go 1.24+.

```bash
make build

# Check version
./bin/gua-server --version
```

### Setup (one-time per WeChat account)

```bash
./bin/gua-server setup --backend wechat
# Scan the QR code with WeChat
# Run again for additional accounts
```

### Start

```bash
# If installed via install.sh:
sudo systemctl start gua
sudo systemctl enable gua  # auto-start on boot

# Or run directly:
gua-server start \
  --backend wechat \
  --agent claude \
  --work-dir ~/.gua/workspace \
  --bridge-bin /usr/bin/gua-bridge \
  --model sonnet \
  --prompt ./my-custom-rules.md  # optional: appended to init prompt
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

**Agent CLI passthrough** (Claude Code specific, forwarded to terminal):

| Command | Effect |
|---|---|
| `/model` | Switch model (TUI menu) |
| `/fast` | Toggle fast mode (TUI menu) |

**Channel control** (WeChat specific, parsed by Presenter):

| Command | Effect |
|---|---|
| `/yes` | Confirm / allow / enter |
| `/no` | Deny / reject (for permissions) |
| `/cancel` | Cancel / exit menu (for TUI menus) |
| `/select N` | Select option N in a menu |
| 是 / 好 / 可以 | Voice-friendly confirm (same as `/yes`) |
| 不 / 不要 / 取消 | Voice-friendly deny (same as `/no`) |

`/no` and `/cancel` both map to the same action (deny); the distinction is UX — prompts show `/no` for permission approval and `/cancel` for TUI menu navigation. Voice commands only take effect when there's an active prompt; otherwise they're sent as normal messages.

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
│   └── wechat/         WeChat implementation
├── libc/wechat/        WeChat iLink protocol library
├── server/             Server orchestration (Channel ↔ Agent routing)
├── cmd/                CLI entry point (setup, start, accounts, sessions)
├── scripts/            Install scripts (Linux systemd setup)
└── bot/                Reference implementations
    ├── weclaw/         WeChat + Claude (ACP/CLI/HTTP) — Go
    └── ccbot/          Telegram + Claude Code (tmux + JSONL) — Python
```

## Design Decisions

**Interface-driven Architecture**: Server orchestration knows nothing about specific agents, channels, or runtimes. The `Agent` interface defines how to send messages and handle control actions. The `Channel` interface defines how to receive and deliver messages. The `Runtime` interface defines how to manage processes. Each implementation decides its own communication method, session model, and capabilities.

**Agent CLI Command Whitelist**: Each agent declares which CLI commands it supports via `CLICommands()`. The server forwards matching `/commands` directly to the agent's terminal; unrecognized commands are sent as normal messages through the agent's standard message protocol.

**Presenter Pattern**: Channel-specific rendering is encapsulated in a `Presenter` interface. The same agent response can be rendered as plain text with `/select N` commands (WeChat), inline keyboard buttons (Telegram), or rich embeds (Discord). The agent layer produces structured data; the presenter decides how to display it.

**Four-layer Prompt**: Init prompt = `config/base.md` (security rules) + agent prompt (agent-specific behavior) + channel prompt (channel-specific rules) + `presenter.MediaInstructions()` (media handling) + optional user prompt (`--prompt` flag). Each layer is independently managed; user prompt has highest priority (appended last).

### Claude Code Implementation Details

The Claude Code agent uses these specific mechanisms (other agents may differ):

- **MCP Channel Protocol**: Persistent bidirectional communication via Unix socket bridge
- **Hook-based Permissions**: `PermissionRequest` and `Elicitation` hooks intercept prompts before terminal display
- **TUI Menu Capture**: `capture-pane` with separator + `Esc to` boundary detection for CLI command menus
- **Watch**: FIFO-based `pipe-pane` streaming for runtime interactive prompt detection

## Acknowledgments

- [Tencent WeChat OpenClaw](https://www.npmjs.com/package/@tencent-weixin/openclaw-weixin-cli) — WeChat iLink Bot API
- [weclaw](https://github.com/fastclaw-ai/weclaw) — Pioneer WeChat AI agent bridge, inspired the iLink protocol implementation
- [ccbot](https://github.com/six-ddc/ccbot) — Telegram Claude Code bot, demonstrated tmux-based TUI handling and JSONL output monitoring

## License

[MIT](LICENSE)
