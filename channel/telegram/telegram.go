package telegram

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/projecteru2/core/log"

	"github.com/CMGS/gua/channel"
	"github.com/CMGS/gua/types"
	"github.com/CMGS/gua/utils"
)

// Callback data values — shared between buildKeyboard and ParseAction.
const (
	callbackConfirm      = "confirm"
	callbackDeny         = "deny"
	callbackSelectPrefix = "select_"
)

var (
	telegramPresenter channel.Presenter = &presenter{}

	// HTTP client with timeout for file downloads (Telegram files up to 20MB).
	fileClient = &http.Client{Timeout: 60 * time.Second}
)

type Telegram struct {
	bot   *telego.Bot
	token string
}

func New(token string) *Telegram {
	return &Telegram{token: token}
}

func (t *Telegram) Name() string { return "telegram" }

func (t *Telegram) Setup(ctx context.Context) error {
	// Use net/http instead of default fasthttp — fasthttp ignores
	// context cancellation, causing shutdown to block until poll timeout.
	bot, err := telego.NewBot(t.token, telego.WithHTTPClient(http.DefaultClient))
	if err != nil {
		return fmt.Errorf("create bot: %w", err)
	}
	me, err := bot.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("verify token: %w", err)
	}
	t.bot = bot

	// Clear any previously registered commands — all gua commands (/close, /clean,
	// /respawn) require context (an active topic) and should be typed manually,
	// not triggered from the menu (which creates unwanted new topics in forum mode).
	_ = bot.DeleteMyCommands(ctx, nil)

	log.WithFunc("telegram.Setup").Infof(ctx, "bot verified: @%s", me.Username)
	return nil
}

func (t *Telegram) Start(ctx context.Context, handler channel.InboundHandler) error {
	if t.bot == nil {
		if err := t.Setup(ctx); err != nil {
			return err
		}
	}

	updates, err := t.bot.UpdatesViaLongPolling(ctx, &telego.GetUpdatesParams{
		Timeout:        10,
		AllowedUpdates: []string{"message", "callback_query"},
	})
	if err != nil {
		return fmt.Errorf("start long polling: %w", err)
	}

	for update := range updates {
		switch {
		case update.Message != nil:
			t.handleMessage(ctx, update.Message, handler)
		case update.CallbackQuery != nil:
			t.handleCallback(ctx, update.CallbackQuery, handler)
		}
	}

	return nil
}

func (t *Telegram) handleMessage(ctx context.Context, msg *telego.Message, handler channel.InboundHandler) {
	logger := log.WithFunc("telegram.handleMessage")

	if msg.ForumTopicClosed != nil && msg.MessageThreadID > 0 {
		if msg.From == nil {
			logger.Warnf(ctx, "topic %d closed but msg.From is nil, cannot identify session", msg.MessageThreadID)
			return
		}
		senderID, replyToken := buildIDs(msg)
		logger.Infof(ctx, "topic closed, sending /close for sender=%s", senderID)
		handler(ctx, channel.InboundMessage{SenderID: senderID, Text: "/close", ReplyToken: replyToken})
		return
	}
	if msg.ForumTopicReopened != nil {
		return
	}
	if msg.From == nil {
		return
	}

	senderID, replyToken := buildIDs(msg)
	text := msg.Text
	if text == "" {
		text = msg.Caption
	}
	mediaFiles := downloadMedia(ctx, t.bot, msg)

	if text == "" && len(mediaFiles) == 0 {
		return
	}

	handler(ctx, channel.InboundMessage{
		SenderID:   senderID,
		Text:       text,
		MediaFiles: mediaFiles,
		ReplyToken: replyToken,
	})
}

func (t *Telegram) handleCallback(ctx context.Context, query *telego.CallbackQuery, handler channel.InboundHandler) {
	// Acknowledge the callback to remove the loading indicator.
	_ = t.bot.AnswerCallbackQuery(ctx, &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
	})

	if query.Data == "" {
		return
	}
	senderID, replyToken := buildIDsFromCallback(query)
	handler(ctx, channel.InboundMessage{
		SenderID:   senderID,
		Text:       query.Data,
		ReplyToken: replyToken,
		ActionOnly: true,
	})
}

func (t *Telegram) Send(ctx context.Context, msg channel.OutboundMessage) error {
	chatID, topicID := parseReplyToken(msg.ReplyToken)
	if chatID == 0 {
		return fmt.Errorf("invalid reply token: %s", msg.ReplyToken)
	}

	if msg.FilePath != "" {
		return wrapSendError(t.sendFile(ctx, chatID, topicID, msg))
	}

	var keyboard *telego.InlineKeyboardMarkup
	if msg.PromptKind != channel.PromptKindNone {
		keyboard = buildKeyboard(msg.PromptKind, msg.Options)
	}

	for _, chunk := range splitMessage(msg.Text, 4096) {
		params := tu.Message(tu.ID(chatID), chunk)
		if topicID > 0 {
			params = params.WithMessageThreadID(topicID)
		}
		if keyboard != nil {
			params = params.WithReplyMarkup(keyboard)
			keyboard = nil // only attach keyboard to first chunk
		}
		if _, err := t.bot.SendMessage(ctx, params); err != nil {
			return wrapSendError(err)
		}
	}
	return nil
}

func (t *Telegram) StartTyping(ctx context.Context, _ string, replyToken string) (stop func()) {
	chatID, topicID := parseReplyToken(replyToken)
	if chatID == 0 {
		return func() {}
	}

	typingCtx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()

		sendAction := func() {
			// Use a detached context so canceling the typing indicator
			// doesn't abort an in-flight HTTP request (telego logs ERROR).
			actionCtx, actionCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer actionCancel()
			params := &telego.SendChatActionParams{
				ChatID: tu.ID(chatID),
				Action: telego.ChatActionTyping,
			}
			if topicID > 0 {
				params.MessageThreadID = topicID
			}
			_ = t.bot.SendChatAction(actionCtx, params)
		}

		sendAction()
		for {
			select {
			case <-typingCtx.Done():
				return
			case <-ticker.C:
				sendAction()
			}
		}
	}()
	return cancel
}

func (t *Telegram) ShareQR(_ context.Context) (string, error) { return "", nil }
func (t *Telegram) Presenter() channel.Presenter              { return telegramPresenter }

func (t *Telegram) RenameThread(ctx context.Context, replyToken, name string) {
	chatID, topicID := parseReplyToken(replyToken)
	if chatID == 0 || topicID == 0 {
		return
	}
	if err := t.bot.EditForumTopic(ctx, &telego.EditForumTopicParams{
		ChatID:          tu.ID(chatID),
		MessageThreadID: topicID,
		Name:            name,
	}); err != nil {
		log.WithFunc("telegram.RenameThread").Warnf(ctx, "rename topic %d in chat %d: %v", topicID, chatID, err)
	}
}

// ProbeThread checks if the topic for senderID still exists by sending
// a probe message and deleting it. sendChatAction is unreliable for
// private chat topics (Telegram ignores invalid thread_id silently).
func (t *Telegram) ProbeThread(ctx context.Context, senderID string) error {
	parts := strings.SplitN(senderID, ":", 3)
	if len(parts) < 3 {
		return nil
	}
	chatID, _ := strconv.ParseInt(parts[0], 10, 64)
	topicID, _ := strconv.Atoi(parts[2])
	if chatID == 0 || topicID == 0 {
		return nil
	}
	params := tu.Message(tu.ID(chatID), "🔄")
	params = params.WithMessageThreadID(topicID)
	msg, err := t.bot.SendMessage(ctx, params)
	if err != nil {
		return fmt.Errorf("%w: %v", channel.ErrThreadGone, err)
	}
	_ = t.bot.DeleteMessage(ctx, &telego.DeleteMessageParams{
		ChatID:    tu.ID(chatID),
		MessageID: msg.MessageID,
	})
	return nil
}

// composeSenderReply builds senderID and replyToken from chat/user/topic IDs.
// senderID includes chatID to prevent cross-chat session collisions.
func composeSenderReply(chatID, userID string, topicID int) (senderID, replyToken string) {
	if topicID > 0 {
		tid := strconv.Itoa(topicID)
		return chatID + ":" + userID + ":" + tid, chatID + ":" + tid
	}
	return chatID + ":" + userID, chatID
}

func buildIDs(msg *telego.Message) (senderID, replyToken string) {
	return composeSenderReply(
		strconv.FormatInt(msg.Chat.ID, 10),
		strconv.FormatInt(msg.From.ID, 10),
		msg.MessageThreadID,
	)
}

func buildIDsFromCallback(query *telego.CallbackQuery) (senderID, replyToken string) {
	userID := strconv.FormatInt(query.From.ID, 10)
	if query.Message == nil {
		return userID, ""
	}
	msg := query.Message.Message()
	if msg == nil {
		return userID, ""
	}
	return composeSenderReply(
		strconv.FormatInt(msg.Chat.ID, 10),
		userID,
		msg.MessageThreadID,
	)
}

// wrapSendError wraps Telegram API errors to detect deleted threads/chats.
func wrapSendError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "thread not found") || strings.Contains(msg, "chat not found") {
		return fmt.Errorf("%w: %v", channel.ErrThreadGone, err)
	}
	return err
}

func parseReplyToken(token string) (chatID int64, topicID int) {
	parts := strings.SplitN(token, ":", 2)
	chatID, _ = strconv.ParseInt(parts[0], 10, 64)
	if len(parts) > 1 {
		topicID, _ = strconv.Atoi(parts[1])
	}
	return
}

func (t *Telegram) sendFile(ctx context.Context, chatID int64, topicID int, msg channel.OutboundMessage) error {
	f, err := os.Open(msg.FilePath) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer f.Close() //nolint:errcheck

	fileName := filepath.Base(msg.FilePath)
	mimeType := utils.DetectMIMEType(msg.FilePath)

	switch {
	case utils.IsRasterImage(mimeType):
		params := tu.Photo(tu.ID(chatID), tu.File(f))
		if topicID > 0 {
			params = params.WithMessageThreadID(topicID)
		}
		if msg.Text != "" {
			params.Caption = msg.Text
		}
		_, err = t.bot.SendPhoto(ctx, params)
	default:
		namedFile := &namedReader{Reader: f, name: fileName}
		params := tu.Document(tu.ID(chatID), tu.File(namedFile))
		if topicID > 0 {
			params = params.WithMessageThreadID(topicID)
		}
		if msg.Text != "" {
			params.Caption = msg.Text
		}
		_, err = t.bot.SendDocument(ctx, params)
	}
	return err
}

type namedReader struct {
	io.Reader
	name string
}

func (n *namedReader) Name() string { return n.name }

// splitMessage splits text into chunks of at most maxRunes characters.
// Telegram's 4096 limit is character-based (not bytes), so we count runes.
func splitMessage(text string, maxRunes int) []string {
	if utf8.RuneCountInString(text) <= maxRunes {
		return []string{text}
	}

	var chunks []string
	inCodeBlock := false

	for len(text) > 0 {
		if utf8.RuneCountInString(text) <= maxRunes {
			chunks = append(chunks, text)
			break
		}

		// Convert rune limit to a byte offset for slicing.
		byteLimit := runeOffset(text, maxRunes)

		// Find a split point: prefer paragraph > line > hard cut.
		cutAt := byteLimit
		if idx := strings.LastIndex(text[:byteLimit], "\n\n"); idx > byteLimit/2 {
			cutAt = idx + 2
		} else if idx := strings.LastIndex(text[:byteLimit], "\n"); idx > byteLimit/2 {
			cutAt = idx + 1
		}

		chunk := text[:cutAt]
		text = text[cutAt:]

		fenceCount := strings.Count(chunk, "```")
		if fenceCount%2 != 0 {
			inCodeBlock = !inCodeBlock
		}

		if inCodeBlock {
			chunk += "\n```"
			text = "```\n" + text
		}

		chunks = append(chunks, chunk)
	}
	return chunks
}

// runeOffset returns the byte offset of the n-th rune in s,
// or len(s) if s has fewer than n runes.
func runeOffset(s string, n int) int {
	var i, count int
	for i < len(s) && count < n {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		count++
	}
	return i
}

func downloadMedia(ctx context.Context, bot *telego.Bot, msg *telego.Message) []types.MediaFile {
	logger := log.WithFunc("telegram.downloadMedia")
	var files []types.MediaFile

	if len(msg.Photo) > 0 {
		largest := msg.Photo[len(msg.Photo)-1]
		if f, err := downloadFile(ctx, bot, largest.FileID, "jpg", types.MediaTypeImage); err == nil {
			files = append(files, f)
		} else {
			logger.Warnf(ctx, "download photo: %v", err)
		}
	}
	if msg.Document != nil {
		ext := filepath.Ext(msg.Document.FileName)
		if ext == "" {
			ext = "bin"
		} else {
			ext = ext[1:] // strip leading dot
		}
		if f, err := downloadFile(ctx, bot, msg.Document.FileID, ext, types.MediaTypeFile); err == nil {
			f.FileName = msg.Document.FileName
			files = append(files, f)
		} else {
			logger.Warnf(ctx, "download document: %v", err)
		}
	}
	if msg.Voice != nil {
		if f, err := downloadFile(ctx, bot, msg.Voice.FileID, "ogg", types.MediaTypeVoice); err == nil {
			files = append(files, f)
		} else {
			logger.Warnf(ctx, "download voice: %v", err)
		}
	}
	if msg.Video != nil {
		if f, err := downloadFile(ctx, bot, msg.Video.FileID, "mp4", types.MediaTypeVideo); err == nil {
			files = append(files, f)
		} else {
			logger.Warnf(ctx, "download video: %v", err)
		}
	}
	return files
}

func downloadFile(ctx context.Context, bot *telego.Bot, fileID, ext string, mediaType types.MediaType) (types.MediaFile, error) {
	file, err := bot.GetFile(ctx, &telego.GetFileParams{FileID: fileID})
	if err != nil {
		return types.MediaFile{}, err
	}

	data, err := httpGet(ctx, bot.FileDownloadURL(file.FilePath))
	if err != nil {
		return types.MediaFile{}, err
	}

	path := utils.TempPathRandom(ext)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return types.MediaFile{}, err
	}
	return types.MediaFile{Path: path, Type: mediaType}, nil
}

func httpGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := fileClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
