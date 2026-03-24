package auth

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/CMGS/gua/libc/wechat/client"
	"github.com/CMGS/gua/libc/wechat/types"
)

const (
	qrCodeURL      = "https://ilinkai.weixin.qq.com/ilink/bot/get_bot_qrcode?bot_type=3"
	qrStatusURLFmt = "https://ilinkai.weixin.qq.com/ilink/bot/get_qrcode_status?qrcode="

	maxQRRefreshes = 3
	pollTimeout    = 480 * time.Second
	pollInterval   = 2 * time.Second
)

// FetchQRCode fetches a new QR code for bot login.
func FetchQRCode(ctx context.Context, opts ...client.ClientOption) (*types.QRCodeResponse, error) {
	c := client.NewUnauthenticatedClient(opts...)

	resp := &types.QRCodeResponse{}
	if err := c.DoGet(ctx, qrCodeURL, resp); err != nil {
		return nil, fmt.Errorf("fetch QR code: %w", err)
	}
	return resp, nil
}

// PollQRStatus long-polls for QR code scan confirmation.
// It calls onStatus on each status change and auto-refreshes the QR code up to
// 3 times when it expires. The total timeout is 480 seconds.
func PollQRStatus(ctx context.Context, qrcode string, onStatus func(status string), opts ...client.ClientOption) (*types.Credentials, error) {
	c := client.NewUnauthenticatedClient(opts...)

	ctx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()

	refreshes := 0
	lastStatus := ""

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("QR login timed out: %w", ctx.Err())
		default:
		}

		statusURL := qrStatusURLFmt + url.QueryEscape(qrcode)

		var resp types.QRStatusResponse
		if err := c.DoGet(ctx, statusURL, &resp); err != nil {
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("QR login timed out: %w", ctx.Err())
			case <-time.After(pollInterval):
			}
			continue
		}

		if resp.Status != lastStatus {
			lastStatus = resp.Status
			if onStatus != nil {
				onStatus(resp.Status)
			}
		}

		switch resp.Status {
		case types.QRStatusConfirmed:
			return &types.Credentials{
				BotToken:    resp.BotToken,
				ILinkBotID:  resp.ILinkBotID,
				BaseURL:     resp.BaseURL,
				ILinkUserID: resp.ILinkUserID,
			}, nil

		case types.QRStatusExpired:
			refreshes++
			if refreshes > maxQRRefreshes {
				return nil, fmt.Errorf("QR code expired after %d refreshes", maxQRRefreshes)
			}
			fresh, err := FetchQRCode(ctx, opts...)
			if err != nil {
				return nil, fmt.Errorf("refresh QR code: %w", err)
			}
			qrcode = fresh.QRCode
			lastStatus = ""

		default:
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("QR login timed out: %w", ctx.Err())
			case <-time.After(pollInterval):
			}
		}
	}
}
