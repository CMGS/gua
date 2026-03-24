package client

import (
	"errors"
	"fmt"
)

const errCodeSessionExpired = -14

// APIError represents an error response from the iLink API.
type APIError struct {
	Ret     int
	ErrCode int
	ErrMsg  string
}

func (e *APIError) Error() string {
	if e.ErrCode != 0 {
		return fmt.Sprintf("ilink API error: ret=%d errcode=%d errmsg=%s", e.Ret, e.ErrCode, e.ErrMsg)
	}
	return fmt.Sprintf("ilink API error: ret=%d errmsg=%s", e.Ret, e.ErrMsg)
}

// IsSessionExpired returns true if the error indicates a session expiration (errcode -14).
func IsSessionExpired(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrCode == errCodeSessionExpired
	}
	return false
}
