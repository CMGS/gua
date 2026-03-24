package types

// ImageItem holds image content.
type ImageItem struct {
	URL     string    `json:"url,omitempty"`      // Direct URL (unencrypted)
	Media   *CDNMedia `json:"media,omitempty"`    // Encrypted CDN reference
	MidSize int       `json:"mid_size,omitempty"` // Ciphertext size
}

// VideoItem holds video content.
type VideoItem struct {
	Media     *CDNMedia `json:"media,omitempty"`
	VideoSize int       `json:"video_size,omitempty"` // Ciphertext size
}

// FileItem holds file attachment content.
type FileItem struct {
	Media    *CDNMedia `json:"media,omitempty"`
	FileName string    `json:"file_name,omitempty"`
	Len      string    `json:"len,omitempty"` // Plaintext size as string
}

// VoiceItem holds voice content with full metadata.
type VoiceItem struct {
	Media         *CDNMedia `json:"media,omitempty"`
	EncodeType    int       `json:"encode_type,omitempty"` // 1=pcm,2=adpcm,3=feature,4=speex,5=amr,6=silk,7=mp3,8=ogg-speex
	BitsPerSample int       `json:"bits_per_sample,omitempty"`
	SampleRate    int       `json:"sample_rate,omitempty"` // Hz
	Playtime      int       `json:"playtime,omitempty"`    // Duration in milliseconds
	Text          string    `json:"text,omitempty"`        // WeChat built-in voice-to-text transcription
}
