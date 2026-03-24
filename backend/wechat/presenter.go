package wechat

import (
	"fmt"
	"strings"

	"github.com/CMGS/gua/backend"
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
func (p *presenter) FormatMediaAnnotation(mf backend.MediaFile) string {
	switch mf.Type {
	case backend.MediaTypeImage:
		return fmt.Sprintf("[图片: %s]", mf.Path)
	case backend.MediaTypeVoice:
		return fmt.Sprintf("[语音: %s]", mf.Path)
	case backend.MediaTypeVideo:
		return fmt.Sprintf("[视频: %s]", mf.Path)
	case backend.MediaTypeFile:
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
