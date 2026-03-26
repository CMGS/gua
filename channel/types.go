package channel

import (
	"context"

	"github.com/CMGS/gua/types"
)

// PromptKind identifies the type of interactive prompt for channel-specific rendering.
// Defined independently from agent.PromptType — server maps between them.
type PromptKind int

const (
	PromptKindNone        PromptKind = iota
	PromptKindPermission             // tool permission approval
	PromptKindInteractive            // terminal interactive prompt
	PromptKindElicitation            // MCP elicitation request
	PromptKindTUIMenu                // TUI cursor menu (/model, /fast)

	OptionConfirm = "confirm"
)

// InboundHandler is called for each inbound message from the platform.
type InboundHandler func(ctx context.Context, msg InboundMessage)

// InboundMessage is a platform-agnostic inbound message.
type InboundMessage struct {
	SenderID   string            // platform user ID (e.g. xxx@im.wechat)
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
	PromptKind  PromptKind // interactive prompt type (0=normal message)
	Options     []string   // option values for structured interaction (e.g. ["1","2","3"])
}
