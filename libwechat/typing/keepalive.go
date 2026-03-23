package typing

import (
	"context"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/CMGS/gua/libwechat/client"
	"github.com/CMGS/gua/libwechat/types"
)

const typingInterval = 5 * time.Second

// KeepAlive manages periodic typing indicator sending for a user session.
type KeepAlive struct {
	client *client.Client
	cache  *ConfigCache
}

// NewKeepAlive creates a new KeepAlive with the given client and config cache.
func NewKeepAlive(c *client.Client, cache *ConfigCache) *KeepAlive {
	return &KeepAlive{
		client: c,
		cache:  cache,
	}
}

// Start launches a goroutine that sends typing indicators immediately and then
// every 5 seconds. The returned stop function cancels the goroutine and sends
// a best-effort cancel typing indicator.
func (k *KeepAlive) Start(ctx context.Context, userID, contextToken string) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)
		k.sendTyping(ctx, userID, contextToken, types.TypingStatusTyping)

		ticker := time.NewTicker(typingInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				k.sendTyping(ctx, userID, contextToken, types.TypingStatusTyping)
			}
		}
	}()

	return func() {
		cancel()
		<-done
		cancelCtx, cancelTimeout := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelTimeout()
		k.sendTyping(cancelCtx, userID, contextToken, types.TypingStatusCancel)
	}
}

func (k *KeepAlive) sendTyping(ctx context.Context, userID, contextToken string, status int) {
	logger := log.WithFunc("typing.sendTyping")

	ticket, err := k.cache.GetTicket(ctx, userID, contextToken)
	if err != nil {
		logger.Warnf(ctx, "failed to get ticket for user %s: %v", userID, err)
		return
	}

	if err := k.client.SendTyping(ctx, userID, ticket, status); err != nil {
		logger.Warnf(ctx, "failed to send status %d for user %s: %v", status, userID, err)
	}
}
