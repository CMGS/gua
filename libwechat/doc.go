// Package libwechat provides a Go library for the WeChat iLink Bot API.
//
// It implements the full iLink protocol used by WeChat AI bot integrations,
// including QR code login, message sending/receiving, CDN media handling,
// voice message processing, and quoted message parsing.
//
// Quick start:
//
//	creds, _ := auth.PollQRStatus(ctx, qr.QRCode, nil)
//	bot := libwechat.NewBot(creds)
//	bot.Run(ctx, func(ctx context.Context, msg types.WeixinMessage) {
//	    text := parse.ExtractText(&msg)
//	    bot.SendText(ctx, msg.FromUserID, "Echo: "+text, msg.ContextToken)
//	})
package libwechat
