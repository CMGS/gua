package channel

import (
	"context"

	"github.com/CMGS/gua/types"
)

// InboundMessage is a platform-agnostic inbound message.
type InboundMessage struct {
	SenderID   string            // platform user ID (e.g. xxx@im.wechat)
	SenderName string            // display name
	Text       string            // text content (includes quoted message formatting)
	MediaFiles []types.MediaFile // media downloaded to local paths
	ReplyToken string            // platform-specific reply routing token
}

// OutboundMessage is a platform-agnostic outbound message.
type OutboundMessage struct {
	RecipientID string
	Text        string
	FilePath    string // optional local file path for media
	ReplyToken  string
}

// InboundHandler is called for each inbound message from the platform.
type InboundHandler func(ctx context.Context, msg InboundMessage)

// Channel is a messaging platform (WeChat, Discord, Telegram, etc.).
type Channel interface {
	Name() string
	Setup(ctx context.Context) error
	Start(ctx context.Context, handler InboundHandler) error
	Send(ctx context.Context, msg OutboundMessage) error
	StartTyping(ctx context.Context, userID, replyToken string) (stop func())
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
