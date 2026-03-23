package cdn

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // MD5 used for file integrity check, not security
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/CMGS/gua/libwechat/client"
	"github.com/CMGS/gua/libwechat/types"
)

const cdnBaseURL = "https://novac2c.cdn.weixin.qq.com/c2c"

// Downloader downloads and decrypts files from the WeChat CDN.
type Downloader struct {
	doer client.HTTPDoer
}

// NewDownloader creates a Downloader with the given HTTP doer.
func NewDownloader(doer client.HTTPDoer) *Downloader {
	return &Downloader{doer: doer}
}

// Download fetches an encrypted file from the CDN and decrypts it.
func (d *Downloader) Download(ctx context.Context, encryptQueryParam, aesKeyB64 string) ([]byte, error) {
	key, err := ParseAESKey(aesKeyB64)
	if err != nil {
		return nil, fmt.Errorf("cdn download: %w", err)
	}

	dlURL := cdnBaseURL + "/download?encrypted_query_param=" + url.QueryEscape(encryptQueryParam)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dlURL, nil)
	if err != nil {
		return nil, fmt.Errorf("cdn download: %w", err)
	}

	resp, err := d.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cdn download: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close on read path

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("cdn download: read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cdn download: HTTP %d: %s", resp.StatusCode, string(body))
	}

	plaintext, err := AESECBDecrypt(body, key)
	if err != nil {
		return nil, fmt.Errorf("cdn download: decrypt: %w", err)
	}

	return plaintext, nil
}

// Uploader uploads encrypted files to the WeChat CDN.
type Uploader struct {
	client     *client.Client
	maxRetries int
	cdnBaseURL string
}

// UploaderOption configures an Uploader.
type UploaderOption func(*Uploader)

// NewUploader creates an Uploader for the given client.
func NewUploader(c *client.Client, opts ...UploaderOption) *Uploader {
	u := &Uploader{
		client:     c,
		maxRetries: 3,
		cdnBaseURL: cdnBaseURL,
	}
	for _, opt := range opts {
		opt(u)
	}
	return u
}

// WithMaxRetries sets the maximum number of retries for 5xx errors.
func WithMaxRetries(n int) UploaderOption {
	return func(u *Uploader) {
		u.maxRetries = n
	}
}

// Upload encrypts and uploads data to the CDN.
func (u *Uploader) Upload(ctx context.Context, data []byte, toUserID string, mediaType int) (*types.UploadedFileInfo, error) {
	md5Sum := md5.Sum(data) //nolint:gosec // MD5 required by WeChat CDN protocol
	rawFileMD5 := hex.EncodeToString(md5Sum[:])

	filekeyBytes := make([]byte, 16)
	if _, err := rand.Read(filekeyBytes); err != nil {
		return nil, fmt.Errorf("cdn upload: generate filekey: %w", err)
	}
	filekey := hex.EncodeToString(filekeyBytes)

	aesKey := make([]byte, 16)
	if _, err := rand.Read(aesKey); err != nil {
		return nil, fmt.Errorf("cdn upload: generate AES key: %w", err)
	}

	ciphertextSize := AESECBPaddedSize(len(data))

	uploadResp, err := u.client.GetUploadURL(ctx, &types.GetUploadURLRequest{
		FileKey:     filekey,
		MediaType:   mediaType,
		ToUserID:    toUserID,
		RawSize:     len(data),
		RawFileMD5:  rawFileMD5,
		FileSize:    ciphertextSize,
		NoNeedThumb: true,
		AESKey:      hex.EncodeToString(aesKey),
		BaseInfo:    types.BaseInfo{},
	})
	if err != nil {
		return nil, fmt.Errorf("cdn upload: get upload url: %w", err)
	}
	if uploadResp.Ret != 0 {
		return nil, &client.APIError{Ret: uploadResp.Ret, ErrMsg: uploadResp.ErrMsg}
	}

	ciphertext, err := AESECBEncrypt(data, aesKey)
	if err != nil {
		return nil, fmt.Errorf("cdn upload: encrypt: %w", err)
	}

	uploadURL := u.cdnBaseURL + "/upload?encrypted_query_param=" + url.QueryEscape(uploadResp.UploadParam) + "&filekey=" + url.QueryEscape(filekey)

	encryptedParam, err := u.postWithRetry(ctx, uploadURL, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("cdn upload: %w", err)
	}

	return &types.UploadedFileInfo{
		FileKey:                     filekey,
		DownloadEncryptedQueryParam: encryptedParam,
		AESKey:                      aesKey,
		FileSize:                    len(data),
		FileSizeCiphertext:          ciphertextSize,
	}, nil
}

// UploadFile reads a file from disk and uploads it to the CDN.
func (u *Uploader) UploadFile(ctx context.Context, filePath, toUserID string, mediaType int) (*types.UploadedFileInfo, error) {
	data, err := os.ReadFile(filePath) //nolint:gosec // filePath is caller-provided, not user input
	if err != nil {
		return nil, fmt.Errorf("cdn upload file: read %s: %w", filePath, err)
	}
	return u.Upload(ctx, data, toUserID, mediaType)
}

func (u *Uploader) postWithRetry(ctx context.Context, rawURL string, ciphertext []byte) (string, error) {
	var lastErr error
	for attempt := range u.maxRetries {
		if attempt > 0 {
			backoff := time.Duration(1<<attempt) * time.Second // 2s, 4s, 8s...
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(backoff):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(ciphertext))
		if err != nil {
			return "", fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/octet-stream")

		resp, err := u.client.Doer().Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			lastErr = err
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			encryptedParam := resp.Header.Get("x-encrypted-param")
			if encryptedParam == "" {
				return "", fmt.Errorf("missing x-encrypted-param header in CDN response")
			}
			return encryptedParam, nil
		}

		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			msg := ""
			if readErr == nil {
				msg = string(body)
			}
			return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
		}

		if readErr == nil {
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
		} else {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
	}
	return "", fmt.Errorf("upload failed after %d attempts: %w", u.maxRetries, lastErr)
}
