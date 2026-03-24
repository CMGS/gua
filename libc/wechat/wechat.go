package wechat

import (
	"context"

	"github.com/CMGS/gua/libc/wechat/cdn"
	"github.com/CMGS/gua/libc/wechat/client"
	"github.com/CMGS/gua/libc/wechat/messaging"
	"github.com/CMGS/gua/libc/wechat/monitor"
	"github.com/CMGS/gua/libc/wechat/parse"
	"github.com/CMGS/gua/libc/wechat/types"
	"github.com/CMGS/gua/libc/wechat/typing"
	"github.com/CMGS/gua/libc/wechat/voice"
)

// Bot is a high-level convenience wrapper that composes all subsystems.
type Bot struct {
	Client     *client.Client
	Downloader *cdn.Downloader
	Uploader   *cdn.Uploader
	Typing     *typing.ConfigCache
	KeepAlive  *typing.KeepAlive
	Guard      *monitor.SessionGuard
	VoiceDec   voice.Decoder

	syncState  monitor.SyncState
	clientOpts []client.ClientOption
}

// BotOption configures the Bot.
type BotOption func(*Bot)

// NewBot creates a Bot with all subsystems initialized from credentials.
func NewBot(creds *types.Credentials, opts ...BotOption) *Bot {
	b := &Bot{
		Guard:     monitor.NewSessionGuard(),
		VoiceDec:  &voice.NoOpDecoder{},
		syncState: monitor.NewMemorySyncState(),
	}

	// Apply options first so clientOpts are collected.
	for _, opt := range opts {
		opt(b)
	}

	b.Client = client.NewClient(creds, b.clientOpts...)
	b.Downloader = cdn.NewDownloader(b.Client.Doer())
	b.Uploader = cdn.NewUploader(b.Client)
	b.Typing = typing.NewConfigCache(b.Client)
	b.KeepAlive = typing.NewKeepAlive(b.Client, b.Typing)

	return b
}

// WithVoiceDecoder sets a custom voice decoder.
func WithVoiceDecoder(d voice.Decoder) BotOption {
	return func(b *Bot) {
		b.VoiceDec = d
	}
}

// WithHTTPDoer sets a custom HTTP doer for the client.
func WithHTTPDoer(doer client.HTTPDoer) BotOption {
	return func(b *Bot) {
		b.clientOpts = append(b.clientOpts, client.WithHTTPDoer(doer))
	}
}

// WithSyncState sets a custom sync state for message polling persistence.
func WithSyncState(s monitor.SyncState) BotOption {
	return func(b *Bot) {
		b.syncState = s
	}
}

// WithSessionGuard sets a custom session guard.
func WithSessionGuard(g *monitor.SessionGuard) BotOption {
	return func(b *Bot) {
		b.Guard = g
	}
}

// Run starts the long-poll monitor loop. Blocks until ctx is canceled.
func (b *Bot) Run(ctx context.Context, handler monitor.Handler) error {
	m := monitor.NewMonitor(b.Client, handler,
		monitor.WithSyncState(b.syncState),
		monitor.WithSessionGuard(b.Guard),
	)
	return m.Run(ctx)
}

// SendText sends a text message.
func (b *Bot) SendText(ctx context.Context, toUserID, text, contextToken string) error {
	return messaging.SendText(ctx, b.Client, toUserID, text, contextToken, messaging.NewClientID())
}

// SendImageFile uploads and sends an image from a file path.
func (b *Bot) SendImageFile(ctx context.Context, toUserID, filePath, contextToken string) error {
	info, err := b.Uploader.UploadFile(ctx, filePath, toUserID, types.UploadMediaTypeImage)
	if err != nil {
		return err
	}
	return messaging.SendImage(ctx, b.Client, toUserID, info, contextToken, messaging.NewClientID())
}

// SendVideoFile uploads and sends a video from a file path.
func (b *Bot) SendVideoFile(ctx context.Context, toUserID, filePath, contextToken string) error {
	info, err := b.Uploader.UploadFile(ctx, filePath, toUserID, types.UploadMediaTypeVideo)
	if err != nil {
		return err
	}
	return messaging.SendVideo(ctx, b.Client, toUserID, info, contextToken, messaging.NewClientID())
}

// SendFile uploads and sends a file attachment.
func (b *Bot) SendFile(ctx context.Context, toUserID, filePath, fileName, contextToken string) error {
	info, err := b.Uploader.UploadFile(ctx, filePath, toUserID, types.UploadMediaTypeFile)
	if err != nil {
		return err
	}
	return messaging.SendFile(ctx, b.Client, toUserID, info, fileName, contextToken, messaging.NewClientID())
}

// StartTyping begins sending typing indicators every 5 seconds. Returns a stop function.
func (b *Bot) StartTyping(ctx context.Context, userID, contextToken string) (stop func()) {
	return b.KeepAlive.Start(ctx, userID, contextToken)
}

// DownloadMedia downloads and decrypts media from a CDNMedia reference.
// Returns (nil, nil) if media is nil.
func (b *Bot) DownloadMedia(ctx context.Context, media *types.CDNMedia) ([]byte, error) {
	if media == nil {
		return nil, nil
	}
	return b.Downloader.Download(ctx, media.EncryptQueryParam, media.AESKey)
}

// DownloadVoice downloads, decrypts, and optionally decodes a voice message to WAV.
// Returns (nil, nil) if item is nil or has no media.
// Use voice.Transcription() to get WeChat's built-in transcription text.
func (b *Bot) DownloadVoice(ctx context.Context, item *types.VoiceItem) ([]byte, error) {
	if item == nil || item.Media == nil {
		return nil, nil
	}
	data, err := b.Downloader.Download(ctx, item.Media.EncryptQueryParam, item.Media.AESKey)
	if err != nil {
		return nil, err
	}
	return b.VoiceDec.DecodeToWAV(data)
}

// ParseText extracts text from a message, including quoted message formatting.
func (b *Bot) ParseText(msg *types.WeixinMessage) string {
	return parse.ExtractText(msg)
}

// ParseMedia extracts the best media item from a message with ref_msg fallback.
func (b *Bot) ParseMedia(msg *types.WeixinMessage) *types.MessageItem {
	return parse.ExtractMedia(msg)
}

// SendErrorNotice sends an error message to a user. Fire-and-forget, never returns error.
func (b *Bot) SendErrorNotice(ctx context.Context, toUserID, text, contextToken string) {
	messaging.SendErrorNotice(ctx, b.Client, toUserID, text, contextToken)
}
