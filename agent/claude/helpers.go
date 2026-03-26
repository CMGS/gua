package claude

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/CMGS/gua/agent"
	"github.com/CMGS/gua/agent/claude/protocol"
	"github.com/CMGS/gua/channel"
	"github.com/CMGS/gua/runtime"
	"github.com/CMGS/gua/types"
	"github.com/CMGS/gua/utils"
)

// CC CLI commands that trigger TUI menus via passthrough.
var ccCLICommands = []string{"/model", "/fast"}

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

func tuiMenuResponse(menu string) *agent.Response {
	return &agent.Response{
		Prompt:     agent.PromptTUIMenu,
		PromptText: menu,
		Options:    extractCCMenuOptions(menu),
	}
}

// extractCCMenuOptions extracts options from a CC TUI menu.
// Numbered options ("1.","2.") become select values.
// "Enter to" presence adds "confirm" signal.
func extractCCMenuOptions(menu string) []string {
	opts := runtime.ExtractOptions(menu)
	if strings.Contains(menu, "Enter to") {
		opts = append(opts, channel.OptionConfirm)
	}
	return opts
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

// tuiMenuSelectKeys maps a select action to TUI navigation keys.
// Cursor menus (❯) use arrow keys; text menus use number+Enter.
func tuiMenuSelectKeys(action types.Action, prompt string) []string {
	if current := findCurrentOption(prompt); current > 0 {
		target, _ := strconv.Atoi(action.Value)
		if target <= 0 || target == current {
			return nil
		}
		delta := target - current
		key, n := "Down", delta
		if delta < 0 {
			key, n = "Up", -delta
		}
		keys := make([]string, n)
		for i := range keys {
			keys[i] = key
		}
		return keys
	}
	return []string{action.Value, "Enter"}
}

// extractTUIMenu extracts CC TUI menu content from capture-pane output.
// Bottom boundary: line containing "Esc to " (always present in CC menus).
// Top boundary: separator line (─────) above it.
// Returns content between boundaries (inclusive of bottom, exclusive of separator).
func extractTUIMenu(pane string) string {
	lines := strings.Split(pane, "\n")

	// Find bottom boundary: last "Esc to " line.
	escLine := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "Esc to ") {
			escLine = i
			break
		}
	}
	if escLine < 0 {
		return ""
	}

	// Find top boundary: separator above the Esc line.
	// CC uses both pure separators (─────) and decorated ones (──Title── ...).
	sepLine := -1
	for i := escLine - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if runtime.IsSeparatorLine(trimmed) || strings.HasPrefix(trimmed, "──") {
			sepLine = i
			break
		}
	}
	if sepLine < 0 || sepLine >= escLine-1 {
		return ""
	}

	// Extract and clean menu content between separator and Esc line (inclusive).
	var kept []string
	for _, raw := range lines[sepLine+1 : escLine+1] {
		line := strings.TrimRight(raw, "\r ")
		if line != "" {
			kept = append(kept, line)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// captureStatusText extracts the CC command result from pane output.
// Scans from bottom to find the last "❯ /command" line, then extracts
// the "⎿  <result>" line below it.
func captureStatusText(pane string) string {
	lines := strings.Split(pane, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "❯") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "❯"))
		if !strings.HasPrefix(rest, "/") {
			continue
		}
		// Found command line — look below for ⎿ response.
		for j := i + 1; j < len(lines); j++ {
			resp := strings.TrimSpace(lines[j])
			if !strings.HasPrefix(resp, "⎿") {
				continue
			}
			stripped := strings.TrimSpace(strings.TrimPrefix(resp, "⎿"))
			if stripped != "" {
				return stripped
			}
		}
		return ""
	}
	return ""
}

// findCurrentOption extracts the currently selected option number from a TUI
// cursor menu (❯ indicator). Returns 0 if no cursor found.
func findCurrentOption(prompt string) int {
	for line := range strings.SplitSeq(prompt, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "❯") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "❯"))
		if n := runtime.OptionLineNumber(rest); n != "" {
			num, _ := strconv.Atoi(n)
			return num
		}
	}
	return 0
}

// claudeLineFilter filters terminal lines specific to Claude Code's TUI.
// Returns (keep, interactive) — interactive=true triggers prompt detection.
func claudeLineFilter(line string) (keep bool, interactive bool) {
	switch {
	case strings.Contains(line, "Claude Code v"),
		strings.Contains(line, "Listening for channel messages from:"),
		strings.Contains(line, "Experimental · inbound messages"),
		strings.HasPrefix(line, utils.TempDir()),
		strings.HasPrefix(line, "← "),
		strings.HasPrefix(line, "● "),
		strings.HasPrefix(line, "⏺ "),
		strings.Contains(line, "[ctx left:"):
		return false, false

	// CC spinner/progress noise.
	case strings.Contains(line, "Tab to amend"),
		strings.Contains(line, "Update available"),
		strings.Trim(line, "✳✻✽✶✢·⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏ ") == "":
		return false, false

	// CC interactive indicators (moved from runtime — agent-specific).
	case strings.Contains(line, "Enter to confirm"),
		strings.Contains(line, "Esc to "):
		return true, true

	case strings.HasPrefix(line, "❯"):
		rest := strings.TrimSpace(strings.TrimPrefix(line, "❯"))
		return rest != "" && !strings.HasPrefix(rest, "/"), false
	}

	// Filter box-drawing response lines (⎿ etc.) — previous command output, not menu.
	if len(line) >= 3 && strings.ContainsAny(line[:min(len(line), 4)], "⎿│└├┌") {
		return false, false
	}

	stripped := strings.TrimSpace(line)
	if stripped == "" || strings.HasPrefix(stripped, "/") {
		return false, false
	}

	return true, false
}

// claudeAutoConfirm decides how to respond to CC startup prompts.
// Most prompts: Enter (select option 1).
// Prompts where option 1 is "exit/no": Down + Enter (select option 2).
func claudeAutoConfirm(prompt string) []string {
	lower := strings.ToLower(prompt)
	if strings.Contains(lower, "no, exit") || strings.Contains(lower, "exit and fix") {
		return []string{"Down", "Enter"}
	}
	return []string{"Enter"}
}

func hookEntry(command string) []map[string]any {
	return []map[string]any{{
		"matcher": "",
		"hooks": []map[string]any{{
			"type":    "command",
			"command": command,
			"timeout": hookTimeoutMS,
		}},
	}}
}

func hasExistingSession(workDir string) bool {
	_, err := os.Stat(filepath.Join(workDir, ".mcp.json"))
	return err == nil
}

// cleanupWorkdir removes gua's injected config from a workdir.
// Called on Close to avoid polluting user project directories.
func cleanupWorkdir(workDir string) {
	utils.UnmergeJSONFile(
		filepath.Join(workDir, ".mcp.json"),
		[]string{"mcpServers", "gua"},
	)
	utils.UnmergeJSONFile(
		filepath.Join(workDir, ".claude", "settings.local.json"),
		[]string{"hooks", "PermissionRequest"},
		[]string{"hooks", "Elicitation"},
	)
}
