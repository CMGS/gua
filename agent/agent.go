package agent

import (
	"context"

	"github.com/CMGS/gua/backend"
)

// Message is sent to the agent for processing.
type Message struct {
	Text       string              // formatted text (includes media path annotations)
	MediaFiles []backend.MediaFile // attached media files (local paths)
}

// PromptType indicates whether a response requires user interaction.
type PromptType int

const (
	PromptNone        PromptType = iota
	PromptPermission              // permission approval needed
	PromptInteractive             // terminal interactive prompt (numbered options, y/n)
)

// PermissionInfo carries details about a permission request from the agent.
type PermissionInfo struct {
	ToolName     string
	Description  string
	InputPreview string
}

// Response is the agent's reply.
type Response struct {
	Text       string          // reply text
	Files      []string        // file paths extracted from the reply
	Prompt     PromptType      // whether this response is a prompt requiring user action
	PromptText string          // raw prompt content (from tmux capture)
	Options    []string        // available options ["1","2","3"] or ["yes","no"]
	Permission *PermissionInfo // only when Prompt == PromptPermission
}

// Agent is an AI backend (Claude Code, Codex, etc.).
type Agent interface {
	// Name returns the agent identifier (e.g. "claude", "codex").
	Name() string
	// Chat sends a message to the user's session and returns the reply.
	// The agent manages per-user sessions internally.
	Chat(ctx context.Context, userID string, msg Message) (*Response, error)
	// Control handles out-of-band user confirmations such as /yes or /no.
	// Returns handled=false when the input should fall back to normal chat.
	Control(ctx context.Context, userID, input string) (resp *Response, handled bool, err error)
	// Close terminates a user's session.
	Close(userID string) error
	// CloseAll terminates all sessions.
	CloseAll() error
}
