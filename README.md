# Gua

Multi-tenant AI agent server that bridges messaging platforms with AI backends through a fully decoupled architecture.

Each user gets their own isolated AI session running in a separate runtime environment. No API keys required — use your existing AI subscription (Claude Max, etc.) and share it across users on any supported messaging platform.

## Comparison

| | **Gua** | **[weclaw](https://github.com/fastclaw-ai/weclaw)** | **[ccbot](https://github.com/six-ddc/ccbot)** |
|---|---|---|---|
| **Language** | Go | Go | Python |
| **Channel** | WeChat (extensible via interface) | WeChat | Telegram |
| **Agent** | Claude Code via MCP channel protocol | Claude/Codex/Kimi via ACP, CLI, or HTTP | Claude Code via tmux |
| **Communication** | MCP channel — persistent bidirectional Unix socket; bridge binary runs as MCP server inside Claude Code | ACP — JSON-RPC 2.0 over long-running subprocess (most efficient); CLI — spawns process per message with `--stream-json`; HTTP — API client | tmux `send-keys` for input; JSONL file polling for output (`~/.claude/projects/*.jsonl`, byte-offset incremental read) |
| **Streaming** | Event-driven push via MCP notifications (tool calls, permission requests) | ACP: JSON-RPC notifications; CLI: NDJSON stdout | File polling every 2s with byte-offset tracking |
| **Billing** | Subscription (Claude Code subscription, no per-message API cost) | ACP/CLI: subscription; HTTP: API token-based | Subscription (local Claude Code CLI) |
| **Multi-user** | Multi-tenant — one server serves many users, each with isolated tmux session | Single instance per account; multiple instances for multiple users | Multi-user — one Telegram bot serves many users via topic-to-window mapping |
| **Session isolation** | Per-user tmux window + separate MCP bridge | Per-conversation session ID; ACP maintains in-process state | Per-topic tmux window; `session_map.json` tracks mapping |
| **Permission handling** | Claude Code hooks (`PermissionRequest`, `Elicitation`) intercept before terminal display; routed through bridge to user for approval | ACP: auto-allow all; CLI: system prompt injection | Terminal parser detects permission prompts via regex; inline keyboard buttons for approval |
| **TUI menu handling** | `capture-pane` with boundary detection (separator + `Esc to`); arrow key navigation via `/select N` | Not handled | Full TUI support: regex `UIPattern` matching with top/bottom delimiters; inline keyboard for navigation |
| **Media** | Images, voice transcription, video, files | Text only | Text, voice transcription, screenshots |
| **Architecture** | Agent / Channel / Runtime fully decoupled via Go interfaces | Agent abstraction (ACP/CLI/HTTP) but channel hardcoded | Monolithic (tmux + Telegram tightly coupled) |
| **Extensibility** | New agent: implement `Agent` interface; new channel: implement `Channel` + `Presenter`; new runtime: implement `Runtime` | Agents pluggable (3 modes) but channel is WeChat-only | Claude Code only, Telegram only |
| **Persistence** | tmux session + `--continue` flag; respawn in same pane preserves state | CLI: session file; ACP: in-process lifetime | tmux persistence + state files (thread bindings, monitor offsets, session map) |

## Architecture

```
Channel (messaging)     Agent (AI backend)      Runtime (process container)
  ├── WeChat              ├── Claude Code          ├── tmux
  ├── Telegram (planned)  ├── Codex (planned)      ├── screen (planned)
  └── Discord (planned)   └── Gemini (planned)     └── container (planned)

                    ↕                ↕
               [Server orchestration]
            per-user session management
```

All three dimensions are **fully decoupled via Go interfaces**. Adding a new channel, agent, or runtime requires zero changes to existing code.

### Data Flow

```
User message (WeChat)
  → Channel.Start → InboundHandler
  → Server.handleInbound
    ├── Global command (/whosyourdaddy) → handle directly
    ├── Agent CLI command (/model) → Agent.RawInput → capture-pane → TUI menu → user
    ├── Control action (/yes, /cancel, /select N) → Agent.Control → SendInput/Hook reply
    └── Normal message → Agent.Send → MCP channel event → Claude Code
  → Claude Code processes → gua_reply tool call
  → Bridge → Unix socket → readBridgeLoop → pushResponse
  → ensureResponseLoop → Channel.Send → WeChat
```

### Permission Flow (Hook-based)

```
Claude Code wants to use Bash/Write/Edit
  → PermissionRequest hook fires (before terminal prompt)
  → gua-bridge --hook permission → connects to Unix socket
  → Gua pushes permission prompt to user via Channel
  → User replies /yes or /cancel
  → Hook returns decision → Claude Code continues or denies
```

## Quick Start

### Prerequisites

- Go 1.24+
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed and authenticated
- tmux

### Build

```bash
make build
```

### Setup (one-time per WeChat account)

```bash
./bin/gua-server setup --backend wechat
# Scan the QR code with WeChat
# Run again for additional accounts
```

### Start

```bash
./bin/gua-server start \
  --backend wechat \
  --agent claude \
  --work-dir /path/to/workspace \
  --bridge-bin ./bin/gua-bridge \
  --model sonnet
```

### User Commands

| Command | Effect |
|---|---|
| `/whosyourdaddy` | Activate yolo mode (`--dangerously-skip-permissions`) |
| `/imyourdaddy` | Restore safe mode |
| `/model` | Switch Claude Code model (TUI menu passthrough) |
| `/fast` | Toggle fast mode (TUI menu passthrough) |
| `/yes` | Confirm / allow |
| `/cancel` | Cancel / deny / exit menu |
| `/select N` | Select option N in a menu |

## Project Structure

```
gua/
├── types/              Shared types (MediaFile, Action)
├── utils/              Shared utilities (JSON, MIME, SyncValue)
├── config/             Base prompt (security rules)
├── runtime/            Process container interface
│   └── tmux/           tmux implementation
├── agent/              Agent interface
│   └── claude/         Claude Code implementation
│       ├── protocol/   Bridge socket protocol (envelope types)
│       ├── mcpserver/  Lightweight MCP JSON-RPC server
│       └── bridge/     Bridge binary (spawned by Claude Code as MCP server)
├── channel/            Channel interface + Presenter
│   └── wechat/         WeChat implementation
├── libc/wechat/        WeChat iLink protocol library
├── server/             Server orchestration (Channel ↔ Agent routing)
├── cmd/                CLI entry point
└── bot/                Reference implementations
    ├── weclaw/         WeChat + Claude (ACP/CLI/HTTP) — Go
    └── ccbot/          Telegram + Claude Code (tmux + JSONL) — Python
```

## Key Design Decisions

**MCP Channel Protocol**: Gua uses Claude Code's native MCP channel protocol for persistent, bidirectional communication. The bridge process runs inside Claude Code as an MCP server and communicates with Gua over a Unix socket. No subprocess spawning per message, no file polling.

**Hook-based Permission Relay**: Claude Code hooks (`PermissionRequest`, `Elicitation`) intercept tool permission prompts *before* they appear in the terminal. The hook process connects to Gua's socket, forwards the request to the user, and returns the decision. This eliminates terminal parsing for permissions.

**TUI Menu Passthrough**: Agent CLI commands (`/model`, `/fast`) are forwarded directly to the terminal via `tmux send-keys`. The resulting TUI menu is captured via `capture-pane` with boundary detection (separator line + `Esc to` indicator). Users navigate with `/select N`, `/yes`, `/cancel`.

**Three-layer Prompt Injection**: Init prompt = `config/base.md` (security rules) + `agent/claude/claude.md` (agent behavior) + `channel/wechat/wechat.md` (channel rules) + `presenter.MediaInstructions()` (media handling). Each layer is independently managed.

**Presenter Pattern**: Channel-specific rendering is encapsulated in a `Presenter` interface. WeChat gets plain text with `/select N` commands; a future Telegram channel could use inline keyboard buttons. Same agent output, different presentation.

## Acknowledgments

- [Tencent WeChat OpenClaw](https://www.npmjs.com/package/@tencent-weixin/openclaw-weixin-cli) — WeChat iLink Bot API
- [weclaw](https://github.com/fastclaw-ai/weclaw) — Pioneer WeChat AI agent bridge, inspired the iLink protocol implementation
- [ccbot](https://github.com/six-ddc/ccbot) — Telegram Claude Code bot, demonstrated tmux-based TUI handling and JSONL output monitoring

## License

[MIT](LICENSE)
