package messaging

import (
	"context"

	"github.com/CMGS/gua/libwechat/client"
)

// SendErrorNotice sends a text message to the user on a best-effort basis.
// It generates its own clientID and ignores all errors. This is suitable
// for notifying the user about errors when failure to notify is acceptable.
func SendErrorNotice(ctx context.Context, c *client.Client, toUserID, text, contextToken string) {
	clientID := NewClientID()
	_ = SendText(ctx, c, toUserID, text, contextToken, clientID)
}
