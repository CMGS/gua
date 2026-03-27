package telegram

import (
	"fmt"
	"strings"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/CMGS/gua/channel"
	"github.com/CMGS/gua/types"
	"github.com/CMGS/gua/utils"
)

type presenter struct{}

// FormatPrompt renders prompt text. Inline keyboards are built separately by buildKeyboard in Send().
func (p *presenter) FormatPrompt(kind channel.PromptKind, promptText string, options []string, toolName, description string) string {
	switch kind {
	case channel.PromptKindPermission, channel.PromptKindElicitation:
		if promptText != "" {
			return fmt.Sprintf("🔐 %s\n\n%s", toolName, promptText)
		}
		if toolName != "" {
			text := "🔐 " + toolName
			if description != "" {
				text += ": " + description
			}
			return text
		}
		return "Waiting for confirmation."
	default:
		return promptText
	}
}

func (p *presenter) FormatError(err error) string {
	return fmt.Sprintf("❌ %v", err)
}

func (p *presenter) FormatMediaAnnotation(mf types.MediaFile) string {
	switch mf.Type {
	case types.MediaTypeImage:
		return fmt.Sprintf("[Image: %s]", mf.Path)
	case types.MediaTypeVoice:
		return fmt.Sprintf("[Voice: %s]", mf.Path)
	case types.MediaTypeVideo:
		return fmt.Sprintf("[Video: %s]", mf.Path)
	case types.MediaTypeFile:
		return fmt.Sprintf("[File: %s] (%s)", mf.Path, mf.FileName)
	default:
		return ""
	}
}

func (p *presenter) MediaInstructions() string {
	return fmt.Sprintf(`Telegram supports Markdown formatting.
Short code and formatted text can be sent directly.
For long content (>4000 chars), tables, or diagrams, write to %s.
Include the file path in your reply; the system sends it as an attachment.`, utils.TempFileRule())
}

func (p *presenter) FormatText(text string) string {
	return text
}

func (p *presenter) ParseAction(input string) *types.Action {
	// Inline keyboard callback data.
	switch input {
	case callbackConfirm:
		return &types.Action{Type: types.ActionConfirm}
	case callbackDeny:
		return &types.Action{Type: types.ActionDeny}
	}
	if val, ok := strings.CutPrefix(input, callbackSelectPrefix); ok {
		return &types.Action{Type: types.ActionSelect, Value: val}
	}

	// Text command fallback.
	lower := strings.ToLower(strings.TrimSpace(input))
	switch lower {
	case "/yes", "/y":
		return &types.Action{Type: types.ActionConfirm}
	case "/no", "/n", "/cancel":
		return &types.Action{Type: types.ActionDeny}
	}
	if val, ok := strings.CutPrefix(lower, "/select "); ok {
		val = strings.TrimSpace(val)
		if val != "" {
			return &types.Action{Type: types.ActionSelect, Value: val}
		}
		return nil
	}
	if strings.HasPrefix(input, "/") {
		return &types.Action{Type: types.ActionPassthrough, Value: input}
	}
	return nil
}

func buildKeyboard(kind channel.PromptKind, options []string) *telego.InlineKeyboardMarkup {
	switch kind {
	case channel.PromptKindPermission, channel.PromptKindElicitation:
		return tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("✅ Allow").WithCallbackData(callbackConfirm),
				tu.InlineKeyboardButton("❌ Deny").WithCallbackData(callbackDeny),
			),
		)
	case channel.PromptKindTUIMenu, channel.PromptKindInteractive:
		var rows [][]telego.InlineKeyboardButton
		confirmable := false
		for _, opt := range options {
			if opt == types.OptionConfirm {
				confirmable = true
				continue
			}
			rows = append(rows, tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(opt).WithCallbackData(callbackSelectPrefix+opt),
			))
		}
		actionRow := []telego.InlineKeyboardButton{
			tu.InlineKeyboardButton("❌ Cancel").WithCallbackData(callbackDeny),
		}
		if confirmable {
			actionRow = append([]telego.InlineKeyboardButton{
				tu.InlineKeyboardButton("✅ Confirm").WithCallbackData(callbackConfirm),
			}, actionRow...)
		}
		rows = append(rows, actionRow)
		return tu.InlineKeyboard(rows...)
	default:
		return nil
	}
}
