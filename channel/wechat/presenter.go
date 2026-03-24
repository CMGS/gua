package wechat

import (
	"fmt"
	"strings"

	"github.com/CMGS/gua/types"
)

type presenter struct{}

// FormatPrompt renders a prompt for WeChat using plain text with /yes /no /1 /2 hints.
func (p *presenter) FormatPrompt(promptText string, options []string, toolName, description string) string {
	if promptText != "" {
		hints := optionHints(options)
		var b strings.Builder
		fmt.Fprintf(&b, "Claude 需要确认:\n\n%s\n\n", promptText)
		if hints != "" {
			fmt.Fprintf(&b, "回复 %s 选择，", hints)
		}
		b.WriteString("/yes 允许，/no 拒绝。")
		return b.String()
	}

	if toolName != "" {
		text := "Claude 需要确认 " + toolName
		if description != "" {
			text += ": " + description
		}
		return text + "\n回复 /yes 允许，或 /no 拒绝。"
	}

	return "Claude 正在等待确认。回复 /yes 或 /no。"
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
	return `WeChat does not render Markdown — use plain text only.
Rich content (code >5 lines, tables, SVG/Mermaid) must be written to /tmp/gua-<description>.<ext>.
Include the file path in your reply; the system sends it as an attachment.
Short plain text can be returned directly.`
}

// FormatText returns text as-is for WeChat.
func (p *presenter) FormatText(text string) string {
	return text
}

// ParseAction parses WeChat text commands (/yes, /no, /1, etc.) into unified actions.
func (p *presenter) ParseAction(input string) *types.Action {
	cmd := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(input)), "/")
	switch cmd {
	case "yes", "y", "ok", "allow", "enter":
		return &types.Action{Type: types.ActionConfirm}
	case "no", "n", "cancel", "deny":
		return &types.Action{Type: types.ActionDeny}
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return &types.Action{Type: types.ActionSelect, Value: cmd}
	default:
		return nil
	}
}

func optionHints(options []string) string {
	var hints []string
	for _, opt := range options {
		if opt == "yes" || opt == "no" {
			continue
		}
		hints = append(hints, "/"+opt)
	}
	return strings.Join(hints, " ")
}
