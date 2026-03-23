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
	// Name returns the backend identifier (e.g. "wechat", "discord").
	Name() string
	// Setup performs initial authentication (e.g. QR code login).
	Setup(ctx context.Context) error
	// Start begins receiving messages. Calls handler for each inbound message.
	// Blocks until ctx is cancelled.
	Start(ctx context.Context, handler InboundHandler) error
	// Send sends a message to a user on the platform.
	Send(ctx context.Context, msg OutboundMessage) error
}
