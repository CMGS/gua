# Gua

Multi-tenant AI agent server that bridges messaging platforms (WeChat, Telegram, Discord) with AI backends (Claude Code, Codex, Gemini) through a fully decoupled architecture.

Each user gets their own isolated AI session running in a separate runtime environment. No API keys required — use your existing subscription (Claude Max, Codex, etc.) and share it with the whole family via WeChat.

## Why Gua?

| Feature | Gua | [weclaw](https://github.com/fastclaw-ai/weclaw) | [ccbot](https://github.com/six-ddc/ccbot) |
|---|---|---|---|
| **Rich media** | Images, voice transcription, video, files | Text only | Text only |
| **AI integration** | MCP channel protocol (persistent session) | ACP/CLI subprocess (slow per-message spawn) | CLI subprocess |
| **Multi-tenant** | Each user gets an isolated tmux session | Single bot, shared agent | Single user |
| **Permission modes** | Safe mode + `/whosyourdaddy` yolo mode | Fixed permissions | Fixed permissions |
| **Session persistence** | `--continue` auto-resumes conversations | Manual session management | No persistence |
| **Architecture** | Agent / Channel / Runtime fully decoupled | Monolithic | Monolithic |
| **Extensible agents** | Claude Code, Codex, Gemini (interface-based) | Claude, Codex (hardcoded) | Claude only |
| **Extensible channels** | WeChat, Telegram, Discord (interface-based) | WeChat only | WeChat only |
| **Extensible runtime** | tmux, screen, containers (interface-based) | tmux (hardcoded) | None |

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

All three dimensions are **fully decoupled via Go interfaces**. Adding a new channel, agent, or runtime requires zero changes to existing code — just implement the interface.

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

Each WeChat account runs as an independent bot with its own Claude Code session in a tmux window.

### Commands

| Command | Effect |
|---|---|
| `/whosyourdaddy` | Activate yolo mode (`--dangerously-skip-permissions`) |
| `/imyourdaddy` | Restore safe mode (default `--allowedTools`) |

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
│       ├── protocol/   MCP bridge socket protocol
│       ├── mcpserver/  Lightweight MCP JSON-RPC server
│       └── bridge/     Bridge binary (spawned by Claude Code)
├── channel/            Channel interface
│   └── wechat/         WeChat implementation
├── libc/wechat/        WeChat iLink protocol library
├── server/             Server orchestration
└── cmd/                CLI entry point
```

## Key Design Decisions

**MCP Channel Protocol**: Instead of spawning a subprocess per message (slow) or using ACP (complex), Gua uses Claude Code's native MCP channel protocol for persistent, bidirectional communication. The bridge process runs alongside Claude Code and forwards messages over a Unix socket.

**Multi-tenant via tmux**: Each user gets a dedicated tmux window running their own Claude Code instance. Sessions persist across restarts via `--continue`. Switching between safe/yolo mode respawns the process in the same pane — no session data lost.

**Three-layer prompt injection**: Init prompt = `config/base.md` (security rules) + `agent/claude/claude.md` (agent-specific) + `channel/wechat/wechat.md` (channel-specific) + `presenter.MediaInstructions()` (runtime media rules). Each layer is independently managed.

**Presenter pattern**: Channel-specific rendering (how to format prompts, media annotations, error messages) is encapsulated in a `Presenter` interface. WeChat gets plain text with `/yes` `/no` commands; Telegram could get inline keyboard buttons — same agent output, different presentation.

## Acknowledgments

This project would not be possible without:

- [Tencent WeChat OpenClaw](https://www.npmjs.com/package/@tencent-weixin/openclaw-weixin-cli) — WeChat's official iLink Bot API that enables AI agent integration with WeChat
- [weclaw](https://github.com/fastclaw-ai/weclaw) — Pioneer WeChat AI agent bridge that inspired the iLink protocol implementation
- [ccbot](https://github.com/six-ddc/ccbot) — WeChat Claude Code bot that demonstrated the MCP channel approach

## License

MIT
