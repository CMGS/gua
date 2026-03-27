package wechat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	qrterminal "github.com/mdp/qrterminal/v3"
	"github.com/projecteru2/core/log"
	goqrcode "github.com/skip2/go-qrcode"

	"github.com/CMGS/gua/channel"
	libwechat "github.com/CMGS/gua/libc/wechat"
	"github.com/CMGS/gua/libc/wechat/auth"
	"github.com/CMGS/gua/libc/wechat/parse"
	"github.com/CMGS/gua/libc/wechat/types"
	"github.com/CMGS/gua/libc/wechat/voice"
	"github.com/CMGS/gua/utils"
)

var wechatPresenter channel.Presenter = &presenter{}

// WeChat implements channel.Channel for the WeChat iLink platform.
type WeChat struct {
	bot         *libwechat.Bot
	creds       *types.Credentials
	accountsDir string // where to save new account credentials from /share
}

// New creates a WeChat backend with the given credentials.
func New(creds *types.Credentials, accountsDir string) *WeChat {
	return &WeChat{creds: creds, accountsDir: accountsDir}
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

// Start begins receiving messages. Blocks until ctx is canceled.
func (w *WeChat) Start(ctx context.Context, handler channel.InboundHandler) error {
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

		logger.Debugf(ctx, "inbound from %s: text=%d bytes, media=%d files", msg.FromUserID, len(text), len(mediaFiles))

		handler(ctx, channel.InboundMessage{
			SenderID:   msg.FromUserID,
			Text:       text,
			MediaFiles: mediaFiles,
			ReplyToken: msg.ContextToken,
		})
	})
}

// Send sends a message to a user on the platform.
func (w *WeChat) Send(ctx context.Context, msg channel.OutboundMessage) error {
	if msg.FilePath != "" {
		mimeType := utils.DetectMIMEType(msg.FilePath)
		// Only raster images are supported by WeChat CDN; SVG, WebP etc. go as file attachments.
		isRaster := utils.IsRasterImage(mimeType)
		switch {
		case isRaster:
			if err := w.bot.SendImageFile(ctx, msg.RecipientID, msg.FilePath, msg.ReplyToken); err != nil {
				return err
			}
		case strings.HasPrefix(mimeType, "video/"):
			if err := w.bot.SendVideoFile(ctx, msg.RecipientID, msg.FilePath, msg.ReplyToken); err != nil {
				return err
			}
		default:
			fileName := utils.CleanFileName(filepath.Base(msg.FilePath))
			if err := w.bot.SendFile(ctx, msg.RecipientID, msg.FilePath, fileName, msg.ReplyToken); err != nil {
				return err
			}
		}
	}

	if msg.Text != "" {
		if err := w.bot.SendText(ctx, msg.RecipientID, msg.Text, msg.ReplyToken); err != nil {
			return err
		}
	}

	return nil
}

func (w *WeChat) ProbeThread(_ context.Context, _ string) error { return nil }
func (w *WeChat) RenameThread(_ context.Context, _, _ string)   {}

func (w *WeChat) StartTyping(ctx context.Context, userID, replyToken string) (stop func()) {
	if w.bot == nil {
		return func() {}
	}
	return w.bot.StartTyping(ctx, userID, replyToken)
}

// ShareQR fetches a bot login QR code, saves it as a PNG image, and polls
// for scan confirmation in the background. On success, saves credentials
// to the accounts directory (picked up by the file watcher).
func (w *WeChat) ShareQR(ctx context.Context) (string, error) {
	qr, err := auth.FetchQRCode(ctx)
	if err != nil {
		return "", fmt.Errorf("fetch QR: %w", err)
	}
	path := utils.TempPath("share-"+utils.ShortID(), "png")
	if err := goqrcode.WriteFile(qr.QRCodeImgContent, goqrcode.Medium, 512, path); err != nil {
		return "", fmt.Errorf("generate QR image: %w", err)
	}

	if w.accountsDir != "" {
		go func() {
			logger := log.WithFunc("wechat.ShareQR")
			creds, pollErr := auth.PollQRStatus(ctx, qr.QRCode, func(status string) {
				logger.Debugf(ctx, "share QR status: %s", status)
			})
			if pollErr != nil {
				logger.Warnf(ctx, "share QR poll failed: %v", pollErr)
				return
			}
			credPath := filepath.Join(w.accountsDir, utils.NormalizeID(creds.ILinkBotID)+".json")
			if saveErr := auth.SaveCredentials(credPath, creds); saveErr != nil {
				logger.Warnf(ctx, "save shared credentials: %v", saveErr)
				return
			}
			logger.Infof(ctx, "new account registered via /share: %s", creds.ILinkBotID)
		}()
	}

	return path, nil
}

// Presenter returns the WeChat presenter for rendering responses.
func (w *WeChat) Presenter() channel.Presenter {
	return wechatPresenter
}

// Creds returns the stored credentials (for persistence by caller).
func (w *WeChat) Creds() *types.Credentials {
	return w.creds
}
