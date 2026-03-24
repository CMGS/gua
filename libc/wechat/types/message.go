package types

// BaseInfo is included in all API request bodies.
type BaseInfo struct {
	ChannelVersion string `json:"channel_version,omitempty"`
}

// WeixinMessage represents a message from WeChat.
type WeixinMessage struct {
	FromUserID   string        `json:"from_user_id"`
	ToUserID     string        `json:"to_user_id"`
	MessageType  int           `json:"message_type"`  // MessageTypeUser or MessageTypeBot
	MessageState int           `json:"message_state"` // MessageStateNew/Generating/Finish
	ItemList     []MessageItem `json:"item_list"`
	ContextToken string        `json:"context_token"` // Required for reply association
}

// MessageItem is a single item in a message. A message can contain multiple items
// of different types (text + image, etc.).
type MessageItem struct {
	Type      int        `json:"type"`
	TextItem  *TextItem  `json:"text_item,omitempty"`
	ImageItem *ImageItem `json:"image_item,omitempty"`
	VideoItem *VideoItem `json:"video_item,omitempty"`
	FileItem  *FileItem  `json:"file_item,omitempty"`
	VoiceItem *VoiceItem `json:"voice_item,omitempty"`
	RefMsg    *RefMsg    `json:"ref_msg,omitempty"` // Quoted/referenced message
}

// RefMsg represents a quoted/referenced message attached to a text item.
type RefMsg struct {
	MessageItem *MessageItem `json:"message_item,omitempty"` // The quoted message content
	Title       string       `json:"title,omitempty"`        // Summary/preview text
}

// TextItem holds text content.
type TextItem struct {
	Text string `json:"text"`
}

// CDNMedia is a CDN media reference for encrypted uploads/downloads.
type CDNMedia struct {
	EncryptQueryParam string `json:"encrypt_query_param,omitempty"`
	AESKey            string `json:"aes_key,omitempty"`      // base64-encoded (dual format)
	EncryptType       int    `json:"encrypt_type,omitempty"` // 1 for AES-128-ECB
}

// GetUpdatesRequest is the body for getupdates API.
type GetUpdatesRequest struct {
	GetUpdatesBuf string   `json:"get_updates_buf"`
	BaseInfo      BaseInfo `json:"base_info"`
}

// GetUpdatesResponse is the response from getupdates API.
type GetUpdatesResponse struct {
	Ret                  int             `json:"ret"`
	ErrCode              int             `json:"errcode,omitempty"`
	ErrMsg               string          `json:"errmsg,omitempty"`
	Msgs                 []WeixinMessage `json:"msgs"`
	GetUpdatesBuf        string          `json:"get_updates_buf"`
	LongPollingTimeoutMs int             `json:"longpolling_timeout_ms,omitempty"`
}
