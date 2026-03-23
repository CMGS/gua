package parse

import (
	"strings"

	"github.com/CMGS/gua/libwechat/types"
)

// Media priority levels (higher = preferred).
const (
	priNone  = 0
	priVoice = 1
	priFile  = 2
	priVideo = 3
)

// ExtractText collects all text from a message's item list, joined with newlines.
// If a text item has a RefMsg, the quoted message summary is prepended.
func ExtractText(msg *types.WeixinMessage) string {
	var parts []string

	for i := range msg.ItemList {
		item := &msg.ItemList[i]
		if item.TextItem == nil {
			continue
		}

		text := item.TextItem.Text
		if item.RefMsg != nil {
			if quoted := FormatQuotedMessage(item.RefMsg); quoted != "" {
				text = quoted + "\n" + text
			}
		}

		parts = append(parts, text)
	}

	return strings.Join(parts, "\n")
}

// ExtractMedia finds the first media item by priority: IMAGE > VIDEO > FILE > VOICE.
// Voice items with a Text field (transcription) are excluded.
// Falls back to ref_msg media if no direct media is found.
func ExtractMedia(msg *types.WeixinMessage) *types.MessageItem {
	var best *types.MessageItem
	bestPri := priNone

	for i := range msg.ItemList {
		item := &msg.ItemList[i]

		var pri int
		switch {
		case item.ImageItem != nil:
			return item // highest priority, return immediately
		case item.VideoItem != nil:
			pri = priVideo
		case item.FileItem != nil:
			pri = priFile
		case item.VoiceItem != nil && item.VoiceItem.Text == "":
			pri = priVoice
		}

		if pri > bestPri {
			best = item
			bestPri = pri
		}
	}

	if best != nil {
		return best
	}

	// Fallback: check ref_msg in text items for media.
	for i := range msg.ItemList {
		item := &msg.ItemList[i]
		if item.RefMsg != nil && item.RefMsg.MessageItem != nil && HasMedia(item.RefMsg.MessageItem) {
			return item.RefMsg.MessageItem
		}
	}

	return nil
}
