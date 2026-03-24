package wechat

import (
	"context"
	"fmt"
	"os"

	"github.com/projecteru2/core/log"

	libwechat "github.com/CMGS/gua/libc/wechat"
	wxtypes "github.com/CMGS/gua/libc/wechat/types"
	"github.com/CMGS/gua/types"
	"github.com/CMGS/gua/utils"
)

// DownloadMessageMedia downloads all media from a WeChat message to /tmp.
// Errors on individual items are logged and skipped.
func DownloadMessageMedia(ctx context.Context, bot *libwechat.Bot, msg *wxtypes.WeixinMessage) []types.MediaFile {
	logger := log.WithFunc("wechat.DownloadMessageMedia")
	var files []types.MediaFile

	for i := range msg.ItemList {
		item := &msg.ItemList[i]

		switch {
		case item.ImageItem != nil && item.ImageItem.Media != nil:
			data, err := bot.DownloadMedia(ctx, item.ImageItem.Media)
			if err != nil {
				logger.Warnf(ctx, "download image: %v", err)
				continue
			}
			if mf, err := saveMedia(data, utils.DetectImageFormat(data), types.MediaTypeImage, ""); err != nil {
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
			if mf, err := saveMedia(data, "mp4", types.MediaTypeVideo, ""); err != nil {
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
			if mf, err := saveMedia(data, item.FileItem.FileName, types.MediaTypeFile, item.FileItem.FileName); err != nil {
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
			if mf, err := saveMedia(data, "wav", types.MediaTypeVoice, ""); err != nil {
				logger.Warnf(ctx, "save voice: %v", err)
			} else {
				files = append(files, mf)
			}
		}
	}

	return files
}

// saveMedia writes data to a temp file and returns a MediaFile.
func saveMedia(data []byte, ext string, mediaType types.MediaType, fileName string) (types.MediaFile, error) {
	path := fmt.Sprintf("/tmp/gua-%s.%s", utils.ShortID(), ext)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return types.MediaFile{}, fmt.Errorf("write %s: %w", path, err)
	}
	return types.MediaFile{Path: path, Type: mediaType, FileName: fileName}, nil
}
