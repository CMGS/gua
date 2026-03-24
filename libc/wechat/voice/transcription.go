package voice

import "github.com/CMGS/gua/libc/wechat/types"

// Transcription returns the WeChat built-in voice-to-text transcription
// from a VoiceItem. Returns an empty string if no transcription is available.
func Transcription(item *types.VoiceItem) string {
	if item != nil && item.Text != "" {
		return item.Text
	}
	return ""
}
