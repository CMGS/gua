package wechat

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/projecteru2/core/log"

	"github.com/CMGS/gua/backend"
	"github.com/CMGS/gua/libwechat"
	"github.com/CMGS/gua/libwechat/types"
)

// DownloadMessageMedia downloads all media from a WeChat message to /tmp.
// Errors on individual items are logged and skipped.
func DownloadMessageMedia(ctx context.Context, bot *libwechat.Bot, msg *types.WeixinMessage) []backend.MediaFile {
	logger := log.WithFunc("wechat.DownloadMessageMedia")
	var files []backend.MediaFile

	for i := range msg.ItemList {
		item := &msg.ItemList[i]

		switch {
		case item.ImageItem != nil && item.ImageItem.Media != nil:
			data, err := bot.DownloadMedia(ctx, item.ImageItem.Media)
			if err != nil {
				logger.Warnf(ctx, "download image: %v", err)
				continue
			}
			if mf, err := saveMedia(data, detectImageFormat(data), backend.MediaTypeImage, ""); err != nil {
				logger.Warnf(ctx, "save image: %v", err)
			} else {
				files = append(files, mf)
			}

		case item.VideoItem != nil && item.VideoItem.Media != nil:
			data, err := bot.DownloadMedia(ctx, item.VideoItem.Media)
			if err != nil {
				logger.Warnf(ctx, "download video: %v", err)
				continue
			}
			if mf, err := saveMedia(data, "mp4", backend.MediaTypeVideo, ""); err != nil {
				logger.Warnf(ctx, "save video: %v", err)
			} else {
				files = append(files, mf)
			}

		case item.FileItem != nil && item.FileItem.Media != nil:
			data, err := bot.DownloadMedia(ctx, item.FileItem.Media)
			if err != nil {
				logger.Warnf(ctx, "download file: %v", err)
				continue
			}
			if mf, err := saveMedia(data, item.FileItem.FileName, backend.MediaTypeFile, item.FileItem.FileName); err != nil {
				logger.Warnf(ctx, "save file: %v", err)
			} else {
				files = append(files, mf)
			}

		case item.VoiceItem != nil && item.VoiceItem.Text == "" && item.VoiceItem.Media != nil:
			data, err := bot.DownloadVoice(ctx, item.VoiceItem)
			if err != nil {
				logger.Warnf(ctx, "download voice: %v", err)
				continue
			}
			if mf, err := saveMedia(data, "wav", backend.MediaTypeVoice, ""); err != nil {
				logger.Warnf(ctx, "save voice: %v", err)
			} else {
				files = append(files, mf)
			}
		}
	}

	return files
}

// saveMedia writes data to a temp file and returns a MediaFile.
func saveMedia(data []byte, ext string, mediaType backend.MediaType, fileName string) (backend.MediaFile, error) {
	path := fmt.Sprintf("/tmp/gua-%s.%s", uuid.New().String()[:8], ext)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return backend.MediaFile{}, fmt.Errorf("write %s: %w", path, err)
	}
	return backend.MediaFile{Path: path, Type: mediaType, FileName: fileName}, nil
}

// detectImageFormat checks magic bytes to determine image format.
func detectImageFormat(data []byte) string {
	if len(data) >= 4 && data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
		return "png"
	}
	if len(data) >= 3 && data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46 {
		return "gif"
	}
	return "jpg"
}
