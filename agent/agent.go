package agent

import (
	"context"

	"github.com/CMGS/gua/types"
)

// PromptType indicates whether a response requires user interaction.
type PromptType int

const (
	PromptNone PromptType = iota

	PromptPermission  // permission approval needed
	PromptInteractive // terminal interactive prompt (numbered options, y/n)
)

// PermissionInfo carries details about a permission request from the agent.
type PermissionInfo struct {
	ToolName     string
	Description  string
	InputPreview string
}

// Message is sent to the agent for processing.
type Message struct {
	Text       string            // formatted text (includes media path annotations)
	MediaFiles []types.MediaFile // attached media files (local paths)
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
	Name() string
	Chat(ctx context.Context, userID string, msg Message) (*Response, error)
	Control(ctx context.Context, userID string, action types.Action) (*Response, bool, error)
	Restart(ctx context.Context, userID string, flags map[string]string) (restarted bool, err error)
	Close(userID string) error
	CloseAll() error
}
