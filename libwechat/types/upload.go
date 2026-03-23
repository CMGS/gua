package types

import (
	"encoding/base64"
	"encoding/hex"
)

// GetUploadURLRequest is the body for getuploadurl API.
type GetUploadURLRequest struct {
	FileKey     string   `json:"filekey"`    // 32 hex chars (random 16 bytes)
	MediaType   int      `json:"media_type"` // UploadMediaType*
	ToUserID    string   `json:"to_user_id"`
	RawSize     int      `json:"rawsize"`    // Plaintext file size
	RawFileMD5  string   `json:"rawfilemd5"` // Hex-encoded MD5 of plaintext
	FileSize    int      `json:"filesize"`   // Ciphertext size (after AES+PKCS7)
	NoNeedThumb bool     `json:"no_need_thumb"`
	AESKey      string   `json:"aeskey"` // Hex-encoded AES key
	BaseInfo    BaseInfo `json:"base_info"`
}

// GetUploadURLResponse is the response from getuploadurl API.
type GetUploadURLResponse struct {
	Ret         int    `json:"ret"`
	ErrMsg      string `json:"errmsg,omitempty"`
	UploadParam string `json:"upload_param,omitempty"` // Signed query string for CDN upload
}

// UploadedFileInfo contains the result of a successful CDN upload.
type UploadedFileInfo struct {
	FileKey                     string // 32 hex chars
	DownloadEncryptedQueryParam string // From CDN x-encrypted-param header
	AESKey                      []byte // Raw 16 bytes
	FileSize                    int    // Plaintext size
	FileSizeCiphertext          int    // Ciphertext size after AES-ECB + PKCS7
}

// AESKeyHex returns the AES key as a hex-encoded string (for getuploadurl request).
func (u *UploadedFileInfo) AESKeyHex() string {
	return hex.EncodeToString(u.AESKey)
}

// AESKeyBase64 returns the AES key encoded as base64(hex_string_bytes).
// This matches the WeChat protocol: Buffer.from(hexStr).toString("base64").
func (u *UploadedFileInfo) AESKeyBase64() string {
	hexStr := hex.EncodeToString(u.AESKey)
	return base64.StdEncoding.EncodeToString([]byte(hexStr))
}
