package backend

import "context"

// MediaType identifies the type of a media attachment.
type MediaType string

const (
	MediaTypeImage MediaType = "image"
	MediaTypeVoice MediaType = "voice"
	MediaTypeVideo MediaType = "video"
	MediaTypeFile  MediaType = "file"
)

// MediaFile is a downloaded media attachment with a local path.
type MediaFile struct {
	Path     string
	Type     MediaType
	FileName string // original filename (for file attachments)
}

// InboundMessage is a platform-agnostic inbound message.
type InboundMessage struct {
	SenderID   string      // platform user ID (e.g. xxx@im.wechat)
	SenderName string      // display name
	Text       string      // text content (includes quoted message formatting)
	MediaFiles []MediaFile // media downloaded to local paths
	ReplyToken string      // platform-specific reply routing token
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

// Backend is a messaging platform (WeChat, Discord, Telegram, etc.).
type Backend interface {
	Name() string
	Setup(ctx context.Context) error
	Start(ctx context.Context, handler InboundHandler) error
	Send(ctx context.Context, msg OutboundMessage) error
	StartTyping(ctx context.Context, userID, replyToken string) (stop func())
	Presenter() Presenter
}

// Presenter renders structured responses for a specific platform.
// The agent.Response is passed as individual fields to avoid import cycles.
type Presenter interface {
	// FormatPrompt renders a prompt for the user.
	// promptText is the raw prompt, options are available choices,
	// toolName/description are for permission prompts.
	FormatPrompt(promptText string, options []string, toolName, description string) string
	// FormatError renders an error for the user.
	FormatError(err error) string
	// FormatMediaAnnotation returns the text annotation for a media file.
	FormatMediaAnnotation(mf MediaFile) string
	// MediaInstructions returns platform-specific output rules for the Agent.
	MediaInstructions() string
	// FormatText processes agent reply text before sending to the user.
	FormatText(text string) string
}
