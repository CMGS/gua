package agent

import (
	"context"

	"github.com/CMGS/gua/types"
)

// Agent is an AI backend (Claude Code, Codex, etc.).
type Agent interface {
	Name() string
	// Send sends a message to the user's agent session. Non-blocking.
	// Responses arrive asynchronously via the channel returned by Subscribe.
	Send(ctx context.Context, userID string, msg Message) error
	// Control sends a control action (confirm/deny/select). Non-blocking.
	// Returns handled=true if there was an active prompt that consumed the action.
	// Returns handled=false if no prompt was active (caller should treat input as normal message).
	Control(ctx context.Context, userID string, action types.Action) (handled bool, err error)
	// Subscribe returns a channel that receives all responses for a user.
	// Created on first call, persists until Close.
	Subscribe(userID string) <-chan *Response
	// Restart restarts the user's session with new flags.
	Restart(ctx context.Context, userID string, flags map[string]string) (bool, error)
	// RawInput sends text directly to the agent's terminal (bypasses MCP).
	RawInput(ctx context.Context, userID string, input string) error
	// RespawnSession switches the user's session to a different working directory.
	// Idempotent: no-op if already running in the given workDir.
	// resumeOpt: "" = fresh session, "continue" = most recent, "<id>" = resume specific session.
	RespawnSession(ctx context.Context, userID, workDir, resumeOpt string) (changed bool, err error)
	// CLICommands returns the agent's CLI command whitelist (e.g. /model, /fast).
	CLICommands() []string
	Close(userID string) error
	CloseAll() error
}
