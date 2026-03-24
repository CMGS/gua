package types

// SendMessageRequest is the body for sendmessage API.
type SendMessageRequest struct {
	Msg      SendMsg  `json:"msg"`
	BaseInfo BaseInfo `json:"base_info"`
}

// SendMsg is the message payload for sending.
type SendMsg struct {
	FromUserID   string        `json:"from_user_id"` // Always "" (empty) per official implementation
	ToUserID     string        `json:"to_user_id"`
	ClientID     string        `json:"client_id"`     // UUID for message correlation
	MessageType  int           `json:"message_type"`  // Always MessageTypeBot (2)
	MessageState int           `json:"message_state"` // Always MessageStateFinish (2)
	ItemList     []MessageItem `json:"item_list"`
	ContextToken string        `json:"context_token"` // From incoming message, required
}

// SendMessageResponse is the response from sendmessage API.
type SendMessageResponse struct {
	Ret    int    `json:"ret"`
	ErrMsg string `json:"errmsg,omitempty"`
}
