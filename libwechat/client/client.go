package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/CMGS/gua/libwechat/types"
)

const defaultBaseURL = "https://ilinkai.weixin.qq.com"

// Client is the low-level iLink HTTP API client.
type Client struct {
	baseURL  string
	botToken string
	botID    string
	httpDoer HTTPDoer
}

// NewClient creates an authenticated client from credentials.
func NewClient(creds *types.Credentials, opts ...ClientOption) *Client {
	if creds == nil {
		creds = &types.Credentials{}
	}

	baseURL := defaultBaseURL
	if creds.BaseURL != "" {
		baseURL = creds.BaseURL
	}

	c := &Client{
		baseURL:  baseURL,
		botToken: creds.BotToken,
		botID:    creds.ILinkBotID,
		httpDoer: &http.Client{Timeout: 60 * time.Second},
	}

	for _, opt := range opts {
		opt(c)
	}
	return c
}

// NewUnauthenticatedClient creates a client for the login flow (no auth token).
func NewUnauthenticatedClient(opts ...ClientOption) *Client {
	c := &Client{
		baseURL:  defaultBaseURL,
		httpDoer: &http.Client{Timeout: 60 * time.Second},
	}

	for _, opt := range opts {
		opt(c)
	}
	return c
}

// BotID returns the bot's user ID.
func (c *Client) BotID() string { return c.botID }

// BaseURL returns the API base URL.
func (c *Client) BaseURL() string { return c.baseURL }

// Doer returns the underlying HTTP doer.
func (c *Client) Doer() HTTPDoer { return c.httpDoer }

// GetUpdates long-polls for new messages.
func (c *Client) GetUpdates(ctx context.Context, buf string) (*types.GetUpdatesResponse, error) {
	var resp types.GetUpdatesResponse
	err := c.doRequest(ctx, http.MethodPost, "/ilink/bot/getupdates", &types.GetUpdatesRequest{
		GetUpdatesBuf: buf,
		BaseInfo:      types.BaseInfo{},
	}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// SendMessage sends a message to a user.
func (c *Client) SendMessage(ctx context.Context, req *types.SendMessageRequest) (*types.SendMessageResponse, error) {
	var resp types.SendMessageResponse
	if err := c.doRequest(ctx, http.MethodPost, "/ilink/bot/sendmessage", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetConfig fetches bot config (typing_ticket) for a user.
func (c *Client) GetConfig(ctx context.Context, userID, contextToken string) (*types.GetConfigResponse, error) {
	var resp types.GetConfigResponse
	err := c.doRequest(ctx, http.MethodPost, "/ilink/bot/getconfig", &types.GetConfigRequest{
		ILinkUserID:  userID,
		ContextToken: contextToken,
		BaseInfo:     types.BaseInfo{},
	}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// SendTyping sends a typing indicator to a user.
func (c *Client) SendTyping(ctx context.Context, userID, ticket string, status int) error {
	var resp types.SendTypingResponse
	err := c.doRequest(ctx, http.MethodPost, "/ilink/bot/sendtyping", &types.SendTypingRequest{
		ILinkUserID:  userID,
		TypingTicket: ticket,
		Status:       status,
		BaseInfo:     types.BaseInfo{},
	}, &resp)
	if err != nil {
		return err
	}
	if resp.Ret != 0 {
		return &APIError{Ret: resp.Ret, ErrMsg: resp.ErrMsg}
	}
	return nil
}

// GetUploadURL gets a signed upload URL from the iLink API.
func (c *Client) GetUploadURL(ctx context.Context, req *types.GetUploadURLRequest) (*types.GetUploadURLResponse, error) {
	var resp types.GetUploadURLResponse
	if err := c.doRequest(ctx, http.MethodPost, "/ilink/bot/getuploadurl", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DoGet performs a GET request with auth headers.
func (c *Client) DoGet(ctx context.Context, rawURL string, result any) error {
	return c.doRequest(ctx, http.MethodGet, rawURL, nil, result)
}

// doRequest performs an HTTP request with auth headers and JSON response handling.
func (c *Client) doRequest(ctx context.Context, method, path string, body, result any) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	reqURL := path
	if method == http.MethodPost {
		reqURL = c.baseURL + path
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	c.setHeaders(req, method)

	resp, err := c.httpDoer.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP %s %s: %w", method, path, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	if err := json.Unmarshal(respBody, result); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}

	return nil
}

// setHeaders sets the required iLink auth headers on a request.
func (c *Client) setHeaders(req *http.Request, method string) {
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	if c.botToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.botToken)
	}
	req.Header.Set("X-WECHAT-UIN", generateWechatUIN())
}

// generateWechatUIN generates a random X-WECHAT-UIN header value.
func generateWechatUIN() string {
	var n uint32
	if err := binary.Read(rand.Reader, binary.LittleEndian, &n); err != nil {
		return base64.StdEncoding.EncodeToString([]byte("0"))
	}
	return base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "%d", n))
}
