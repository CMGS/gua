package client

import "net/http"

// HTTPDoer is the interface for making HTTP requests. Enables mocking in tests.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ClientOption configures the Client.
type ClientOption func(*Client)

// WithBaseURL sets a custom base URL for the iLink API.
func WithBaseURL(url string) ClientOption {
	return func(c *Client) {
		c.baseURL = url
	}
}

// WithHTTPDoer sets a custom HTTP client (for testing or custom transport).
func WithHTTPDoer(doer HTTPDoer) ClientOption {
	return func(c *Client) {
		c.httpDoer = doer
	}
}
