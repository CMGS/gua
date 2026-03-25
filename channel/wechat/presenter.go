package wechat

import (
	"fmt"
	"strings"

	"github.com/CMGS/gua/types"
	"github.com/CMGS/gua/utils"
)

type presenter struct{}

// FormatPrompt renders a prompt for WeChat.
// When toolName is set (permission/elicitation), shows approval hints.
// When toolName is empty (TUI menu), shows navigation hints.
func (p *presenter) FormatPrompt(promptText string, options []string, toolName, description string) string {
	if promptText != "" {
		return formatPromptText(promptText, options, toolName)
	}

	if toolName != "" {
		text := "需要确认 " + toolName
		if description != "" {
			text += ": " + description
		}
		return text + "\n回复 /yes 允许，或 /no 拒绝。"
	}

	return "正在等待确认。回复 /yes 或 /cancel。"
}

func formatPromptText(promptText string, options []string, toolName string) string {
	var b strings.Builder
	if toolName != "" {
		fmt.Fprintf(&b, "需要确认:\n\n%s\n\n", promptText)
	} else {
		fmt.Fprintf(&b, "%s\n\n", promptText)
	}

	if hints := optionHints(options); hints != "" {
		fmt.Fprintf(&b, "%s\n", hints)
	}

	if toolName != "" {
		b.WriteString("/yes 允许，/no 拒绝。")
	} else {
		if strings.Contains(promptText, "Enter to") {
			b.WriteString("/yes 确认，")
		}
		b.WriteString("/cancel 返回。")
	}
	return b.String()
}

// FormatError renders an error for WeChat.
func (p *presenter) FormatError(err error) string {
	return fmt.Sprintf("[error] %v", err)
}

// FormatMediaAnnotation returns Chinese media annotations for agent messages.
func (p *presenter) FormatMediaAnnotation(mf types.MediaFile) string {
	switch mf.Type {
	case types.MediaTypeImage:
		return fmt.Sprintf("[图片: %s]", mf.Path)
	case types.MediaTypeVoice:
		return fmt.Sprintf("[语音: %s]", mf.Path)
	case types.MediaTypeVideo:
		return fmt.Sprintf("[视频: %s]", mf.Path)
	case types.MediaTypeFile:
		return fmt.Sprintf("[文件: %s] (%s)", mf.Path, mf.FileName)
	default:
		return ""
	}
}

// MediaInstructions returns WeChat-specific output rules for the Agent.
func (p *presenter) MediaInstructions() string {
	return fmt.Sprintf(`WeChat does not render Markdown — use plain text only.
Rich content (code >5 lines, tables, SVG/Mermaid) must be written to %s.
Include the file path in your reply; the system sends it as an attachment.
Short plain text can be returned directly.`, utils.TempFileRule())
}

// FormatText returns text as-is for WeChat.
func (p *presenter) FormatText(text string) string {
	return text
}

// ParseAction parses WeChat text commands into unified actions.
// /yes → confirm, /cancel → deny, /select N → select, /xxx → passthrough.
func (p *presenter) ParseAction(input string) *types.Action {
	trimmed := strings.TrimSpace(input)
	lower := strings.ToLower(trimmed)

	switch {
	case lower == "/yes" || lower == "/y" || lower == "/ok" || lower == "/allow" || lower == "/enter" ||
		trimmed == "是" || trimmed == "好" || trimmed == "好的" || trimmed == "可以" || trimmed == "确认":
		return &types.Action{Type: types.ActionConfirm}
	case lower == "/no" || lower == "/n" || lower == "/cancel" || lower == "/deny" ||
		trimmed == "不" || trimmed == "不要" || trimmed == "不行" || trimmed == "取消":
		return &types.Action{Type: types.ActionDeny}
	case strings.HasPrefix(lower, "/select "):
		val := strings.TrimSpace(trimmed[len("/select "):])
		if val != "" {
			return &types.Action{Type: types.ActionSelect, Value: val}
		}
		return nil
	default:
		if strings.HasPrefix(trimmed, "/") {
			return &types.Action{Type: types.ActionPassthrough, Value: trimmed}
		}
		return nil
	}
}

func optionHints(options []string) string {
	var hints []string
	for _, opt := range options {
		hints = append(hints, "/select "+opt)
	}
	return strings.Join(hints, " ")
}
