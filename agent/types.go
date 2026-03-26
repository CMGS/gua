package agent

import (
	"errors"

	"github.com/CMGS/gua/types"
)

// ErrNoSession is returned when an operation requires an active session but none exists.
var ErrNoSession = errors.New("no active session")

// PromptType indicates whether a response requires user interaction.
type PromptType int

const (
	PromptNone PromptType = iota

	PromptPermission  // permission approval needed
	PromptInteractive // terminal interactive prompt (numbered options, y/n)
	PromptElicitation // MCP elicitation request (accept/decline)
	PromptTUIMenu     // TUI cursor menu from agent CLI command (/model, /fast)
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
