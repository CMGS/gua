package claude

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/CMGS/gua/agent"
	"github.com/CMGS/gua/agent/claude/protocol"
	"github.com/CMGS/gua/runtime"
	"github.com/CMGS/gua/types"
)

func (c *ClaudeCode) getSession(userID string) (*userSession, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	sess, ok := c.sessions[userID]
	return sess, ok
}

func (c *ClaudeCode) getUserFlag(userID, key string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if flags, ok := c.userFlags[userID]; ok {
		return flags[key]
	}
	return ""
}

func permissionResponse(perm *protocol.Permission) *agent.Response {
	return &agent.Response{
		Prompt:     agent.PromptPermission,
		PromptText: perm.Prompt,
		Options:    runtime.ExtractOptions(perm.Prompt),
		Permission: &agent.PermissionInfo{
			ToolName:     perm.ToolName,
			Description:  perm.Description,
			InputPreview: perm.InputPreview,
		},
	}
}

func interactiveResponse(prompt string) *agent.Response {
	return &agent.Response{
		Prompt:     agent.PromptInteractive,
		PromptText: prompt,
		Options:    runtime.ExtractOptions(prompt),
	}
}

func elicitationResponse(elicit *protocol.Elicitation) *agent.Response {
	return &agent.Response{
		Prompt: agent.PromptElicitation,
		Permission: &agent.PermissionInfo{
			ToolName:    elicit.ServerName,
			Description: elicit.Message,
		},
	}
}

func actionToKeys(action types.Action, prompt string) []string {
	lower := strings.ToLower(prompt)
	hasYN := strings.Contains(lower, "y/n")

	switch action.Type {
	case types.ActionConfirm:
		if hasYN {
			return []string{"y", "Enter"}
		}
		return []string{"Enter"}
	case types.ActionDeny:
		if strings.Contains(prompt, "Esc to cancel") {
			return []string{"Escape"}
		}
		if hasYN {
			return []string{"n", "Enter"}
		}
		return []string{"C-c"}
	case types.ActionSelect:
		return []string{action.Value, "Enter"}
	default:
		return nil
	}
}

// claudeLineFilter filters terminal lines specific to Claude Code's TUI.
func claudeLineFilter(line string) (keep bool, interactive bool) {
	switch {
	case strings.Contains(line, "Claude Code v"),
		strings.Contains(line, "Listening for channel messages from:"),
		strings.Contains(line, "Experimental · inbound messages"),
		strings.HasPrefix(line, "/private/tmp/"),
		strings.HasPrefix(line, "/tmp/"),
		strings.HasPrefix(line, "← "),
		strings.HasPrefix(line, "● "):
		return false, false
	case strings.HasPrefix(line, "❯ "):
		trimmed := strings.TrimSpace(strings.TrimPrefix(line, "❯ "))
		return trimmed != "", false
	}

	// Strip Claude box-drawing decorations.
	stripped := strings.TrimLeft(line, "⎿│└├┌─> ")
	stripped = strings.TrimSpace(stripped)
	if stripped == "" || strings.HasPrefix(stripped, "/") {
		return false, false
	}

	return true, false
}

func hasExistingSession(workDir string) bool {
	_, err := os.Stat(filepath.Join(workDir, ".mcp.json"))
	return err == nil
}
