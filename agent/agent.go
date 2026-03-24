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
	PromptElicitation // MCP elicitation request (accept/decline)
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
	PromptText string          // raw prompt content (from runtime capture)
	Options    []string        // available options ["1","2","3"] or ["yes","no"]
	Permission *PermissionInfo // set when Prompt is PromptPermission or PromptElicitation
}

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
	Close(userID string) error
	CloseAll() error
}
