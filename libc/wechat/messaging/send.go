package messaging

import (
	"context"
	"strconv"

	"github.com/google/uuid"

	"github.com/CMGS/gua/libc/wechat/client"
	"github.com/CMGS/gua/libc/wechat/types"
)

// NewClientID generates a new UUID for message correlation.
func NewClientID() string { return uuid.New().String() }

// SendText sends a text message to the specified user.
func SendText(ctx context.Context, c *client.Client, toUserID, text, contextToken, clientID string) error {
	item := types.MessageItem{
		Type: types.ItemTypeText,
		TextItem: &types.TextItem{
			Text: text,
		},
	}
	return sendMessage(ctx, c, toUserID, contextToken, clientID, item)
}

// SendImage sends an image message using uploaded file info.
func SendImage(ctx context.Context, c *client.Client, toUserID string, info *types.UploadedFileInfo, contextToken, clientID string) error {
	item := types.MessageItem{
		Type: types.ItemTypeImage,
		ImageItem: &types.ImageItem{
			Media:   cdnMediaFromInfo(info),
			MidSize: info.FileSizeCiphertext,
		},
	}
	return sendMessage(ctx, c, toUserID, contextToken, clientID, item)
}

// SendVideo sends a video message using uploaded file info.
func SendVideo(ctx context.Context, c *client.Client, toUserID string, info *types.UploadedFileInfo, contextToken, clientID string) error {
	item := types.MessageItem{
		Type: types.ItemTypeVideo,
		VideoItem: &types.VideoItem{
			Media:     cdnMediaFromInfo(info),
			VideoSize: info.FileSizeCiphertext,
		},
	}
	return sendMessage(ctx, c, toUserID, contextToken, clientID, item)
}

// SendFile sends a file message using uploaded file info.
func SendFile(ctx context.Context, c *client.Client, toUserID string, info *types.UploadedFileInfo, fileName, contextToken, clientID string) error {
	item := types.MessageItem{
		Type: types.ItemTypeFile,
		FileItem: &types.FileItem{
			Media:    cdnMediaFromInfo(info),
			FileName: fileName,
			Len:      strconv.Itoa(info.FileSize),
		},
	}
	return sendMessage(ctx, c, toUserID, contextToken, clientID, item)
}

// SendVoice sends a voice message using uploaded file info.
func SendVoice(ctx context.Context, c *client.Client, toUserID string, info *types.UploadedFileInfo, contextToken, clientID string) error {
	item := types.MessageItem{
		Type: types.ItemTypeVoice,
		VoiceItem: &types.VoiceItem{
			Media: cdnMediaFromInfo(info),
		},
	}
	return sendMessage(ctx, c, toUserID, contextToken, clientID, item)
}

// cdnMediaFromInfo builds a CDNMedia from uploaded file info.
func cdnMediaFromInfo(info *types.UploadedFileInfo) *types.CDNMedia {
	return &types.CDNMedia{
		EncryptQueryParam: info.DownloadEncryptedQueryParam,
		AESKey:            info.AESKeyBase64(),
		EncryptType:       types.EncryptTypeAES128ECB,
	}
}

// sendMessage is the shared helper that builds the request and calls SendMessage.
func sendMessage(ctx context.Context, c *client.Client, toUserID, contextToken, clientID string, item types.MessageItem) error {
	req := &types.SendMessageRequest{
		Msg: types.SendMsg{
			FromUserID:   "",
			ToUserID:     toUserID,
			ClientID:     clientID,
			MessageType:  types.MessageTypeBot,
			MessageState: types.MessageStateFinish,
			ItemList:     []types.MessageItem{item},
			ContextToken: contextToken,
		},
		BaseInfo: types.BaseInfo{},
	}

	resp, err := c.SendMessage(ctx, req)
	if err != nil {
		return err
	}
	if resp.Ret != 0 {
		return &client.APIError{Ret: resp.Ret, ErrMsg: resp.ErrMsg}
	}
	return nil
}
