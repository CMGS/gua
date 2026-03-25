package channel

import (
	"context"

	"github.com/CMGS/gua/types"
)

// Channel is a messaging platform (WeChat, Discord, Telegram, etc.).
type Channel interface {
	Name() string
	Setup(ctx context.Context) error
	Start(ctx context.Context, handler InboundHandler) error
	Send(ctx context.Context, msg OutboundMessage) error
	StartTyping(ctx context.Context, userID, replyToken string) (stop func())
	// ShareQR returns a local file path to a shareable QR/invite image.
	// Returns "" if not supported.
	ShareQR(ctx context.Context) (string, error)
	Presenter() Presenter
}

// Presenter renders structured responses for a specific platform.
type Presenter interface {
	FormatPrompt(promptText string, options []string, toolName, description string) string
	FormatError(err error) string
	FormatMediaAnnotation(mf types.MediaFile) string
	MediaInstructions() string
	FormatText(text string) string
	// ParseAction parses platform-specific input into a unified action.
	// Returns nil if the input is not a control action (treat as normal message).
	ParseAction(input string) *types.Action
}
