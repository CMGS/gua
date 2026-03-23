package parse

import "github.com/CMGS/gua/libwechat/types"

// FormatQuotedMessage formats a RefMsg as a human-readable quoted message indicator.
// If the ref has a Title, it returns "[引用: title]".
// Otherwise, it inspects the referenced message item for media type.
func FormatQuotedMessage(refMsg *types.RefMsg) string {
	if refMsg == nil {
		return ""
	}

	if refMsg.Title != "" {
		return "[引用: " + refMsg.Title + "]"
	}

	if refMsg.MessageItem != nil {
		if refMsg.MessageItem.ImageItem != nil {
			return "[引用: 图片]"
		}
		if refMsg.MessageItem.VideoItem != nil {
			return "[引用: 视频]"
		}
		if refMsg.MessageItem.FileItem != nil {
			return "[引用: 文件]"
		}
		if refMsg.MessageItem.VoiceItem != nil {
			return "[引用: 语音]"
		}
	}

	return ""
}

// HasMedia returns true if the message item contains any media content
// (image, video, file, or voice with CDN media).
func HasMedia(item *types.MessageItem) bool {
	if item == nil {
		return false
	}
	if item.ImageItem != nil {
		return true
	}
	if item.VideoItem != nil {
		return true
	}
	if item.FileItem != nil {
		return true
	}
	if item.VoiceItem != nil && item.VoiceItem.Media != nil {
		return true
	}
	return false
}

// MediaCDNInfo extracts the CDNMedia from the first media-bearing field found
// in a message item. Returns nil if no media is present.
func MediaCDNInfo(item *types.MessageItem) *types.CDNMedia {
	if item == nil {
		return nil
	}
	if item.ImageItem != nil && item.ImageItem.Media != nil {
		return item.ImageItem.Media
	}
	if item.VideoItem != nil && item.VideoItem.Media != nil {
		return item.VideoItem.Media
	}
	if item.FileItem != nil && item.FileItem.Media != nil {
		return item.FileItem.Media
	}
	if item.VoiceItem != nil && item.VoiceItem.Media != nil {
		return item.VoiceItem.Media
	}
	return nil
}
