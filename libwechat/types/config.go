package types

// GetConfigRequest is the body for getconfig API.
type GetConfigRequest struct {
	ILinkUserID  string   `json:"ilink_user_id"`
	ContextToken string   `json:"context_token,omitempty"`
	BaseInfo     BaseInfo `json:"base_info"`
}

// GetConfigResponse is the response from getconfig API.
type GetConfigResponse struct {
	Ret          int    `json:"ret"`
	ErrMsg       string `json:"errmsg,omitempty"`
	TypingTicket string `json:"typing_ticket,omitempty"`
}

// SendTypingRequest is the body for sendtyping API.
type SendTypingRequest struct {
	ILinkUserID  string   `json:"ilink_user_id"`
	TypingTicket string   `json:"typing_ticket"`
	Status       int      `json:"status"` // TypingStatusTyping or TypingStatusCancel
	BaseInfo     BaseInfo `json:"base_info"`
}

// SendTypingResponse is the response from sendtyping API.
type SendTypingResponse struct {
	Ret    int    `json:"ret"`
	ErrMsg string `json:"errmsg,omitempty"`
}
