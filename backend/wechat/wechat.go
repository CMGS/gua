package wechat

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	qrterminal "github.com/mdp/qrterminal/v3"
	"github.com/projecteru2/core/log"

	"github.com/CMGS/gua/backend"
	"github.com/CMGS/gua/libwechat"
	"github.com/CMGS/gua/libwechat/auth"
	"github.com/CMGS/gua/libwechat/parse"
	"github.com/CMGS/gua/libwechat/types"
	"github.com/CMGS/gua/libwechat/voice"
)

// WeChat implements backend.Backend for the WeChat iLink platform.
type WeChat struct {
	bot   *libwechat.Bot
	creds *types.Credentials
}

// New creates a WeChat backend with the given credentials.
func New(creds *types.Credentials) *WeChat {
	return &WeChat{creds: creds}
}

// Name returns the backend identifier.
func (w *WeChat) Name() string { return "wechat" }

// Setup performs QR code login and stores credentials internally.
func (w *WeChat) Setup(ctx context.Context) error {
	logger := log.WithFunc("wechat.Setup")

	qr, err := auth.FetchQRCode(ctx)
	if err != nil {
		return fmt.Errorf("fetch QR code: %w", err)
	}

	// QR code must render to stderr (stdout may be used by MCP protocol).
	logger.Infof(ctx, "%s", "QR code ready, scan with WeChat")
	qrterminal.GenerateWithConfig(qr.QRCodeImgContent, qrterminal.Config{
		Level:          qrterminal.L,
		Writer:         os.Stderr,
		HalfBlocks:     true,
		BlackChar:      qrterminal.BLACK_BLACK,
		WhiteBlackChar: qrterminal.WHITE_BLACK,
		WhiteChar:      qrterminal.WHITE_WHITE,
		BlackWhiteChar: qrterminal.BLACK_WHITE,
		QuietZone:      1,
	})
	logger.Infof(ctx, "QR URL: %s", qr.QRCodeImgContent)

	creds, err := auth.PollQRStatus(ctx, qr.QRCode, func(status string) {
		logger.Infof(ctx, "QR status: %s", status)
	})
	if err != nil {
		return fmt.Errorf("QR login: %w", err)
	}

	w.creds = creds
	return nil
}

// Start begins receiving messages. Blocks until ctx is cancelled.
func (w *WeChat) Start(ctx context.Context, handler backend.InboundHandler) error {
	logger := log.WithFunc("wechat.Start")

	w.bot = libwechat.NewBot(w.creds)

	return w.bot.Run(ctx, func(ctx context.Context, msg types.WeixinMessage) {
		// Only process finished user messages.
		if msg.MessageType != types.MessageTypeUser || msg.MessageState != types.MessageStateFinish {
			return
		}

		text := parse.ExtractText(&msg)
		mediaFiles := DownloadMessageMedia(ctx, w.bot, &msg)

		// Append voice transcriptions to text.
		for i := range msg.ItemList {
			item := &msg.ItemList[i]
			if item.VoiceItem == nil {
				continue
			}
			if t := voice.Transcription(item.VoiceItem); t != "" {
				text += fmt.Sprintf("\n[语音转文字] %s", t)
			}
		}

		if text == "" && len(mediaFiles) == 0 {
			return
		}

		logger.Warnf(ctx, "inbound from %s: text=%d bytes, media=%d files", msg.FromUserID, len(text), len(mediaFiles))

		handler(ctx, backend.InboundMessage{
			SenderID:   msg.FromUserID,
			Text:       text,
			MediaFiles: mediaFiles,
			ReplyToken: msg.ContextToken,
		})
	})
}

// Send sends a message to a user on the platform.
func (w *WeChat) Send(ctx context.Context, msg backend.OutboundMessage) error {
	logger := log.WithFunc("wechat.Send")

	if msg.FilePath != "" {
		mimeType := detectMIMEType(msg.FilePath)
		switch {
		case strings.HasPrefix(mimeType, "image/"):
			if err := w.bot.SendImageFile(ctx, msg.RecipientID, msg.FilePath, msg.ReplyToken); err != nil {
				logger.Warnf(ctx, "send image to %s: %v", msg.RecipientID, err)
				return err
			}
		case strings.HasPrefix(mimeType, "video/"):
			if err := w.bot.SendVideoFile(ctx, msg.RecipientID, msg.FilePath, msg.ReplyToken); err != nil {
				logger.Warnf(ctx, "send video to %s: %v", msg.RecipientID, err)
				return err
			}
		default:
			fileName := filepath.Base(msg.FilePath)
			if err := w.bot.SendFile(ctx, msg.RecipientID, msg.FilePath, fileName, msg.ReplyToken); err != nil {
				logger.Warnf(ctx, "send file to %s: %v", msg.RecipientID, err)
				return err
			}
		}
	}

	if msg.Text != "" {
		if err := w.bot.SendText(ctx, msg.RecipientID, msg.Text, msg.ReplyToken); err != nil {
			logger.Warnf(ctx, "send text to %s: %v", msg.RecipientID, err)
			return err
		}
	}

	return nil
}

// Creds returns the stored credentials (for persistence by caller).
func (w *WeChat) Creds() *types.Credentials {
	return w.creds
}

// detectMIMEType detects the MIME type from the file extension.
// Falls back to "application/octet-stream" if unknown.
func detectMIMEType(path string) string {
	ext := filepath.Ext(path)
	if ext == "" {
		return "application/octet-stream"
	}
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		return "application/octet-stream"
	}
	return mimeType
}
