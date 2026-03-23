package monitor

import (
	"context"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/CMGS/gua/libwechat/client"
	"github.com/CMGS/gua/libwechat/types"
)

const (
	initialBackoff = 3 * time.Second
	maxBackoff     = 60 * time.Second
	errCodeExpired = -14
)

// Handler is called for each incoming message.
type Handler func(ctx context.Context, msg types.WeixinMessage)

// MonitorOption configures a Monitor.
type MonitorOption func(*Monitor)

// Monitor long-polls for new messages and dispatches them to a handler.
type Monitor struct {
	client    *client.Client
	handler   Handler
	syncState SyncState
	guard     *SessionGuard
}

// NewMonitor creates a Monitor for the given client and handler.
func NewMonitor(c *client.Client, handler Handler, opts ...MonitorOption) *Monitor {
	m := &Monitor{
		client:  c,
		handler: handler,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// WithSyncState sets the sync state persistence backend.
func WithSyncState(s SyncState) MonitorOption {
	return func(m *Monitor) { m.syncState = s }
}

// WithSessionGuard sets the session guard for pause-on-expiry behavior.
func WithSessionGuard(g *SessionGuard) MonitorOption {
	return func(m *Monitor) { m.guard = g }
}

// Run starts the long-poll loop. Blocks until ctx is canceled.
func (m *Monitor) Run(ctx context.Context) error {
	logger := log.WithFunc("monitor.Run")

	var buf string
	backoff := initialBackoff

	if m.syncState != nil {
		if loaded, err := m.syncState.Load(); err != nil {
			logger.Warnf(ctx, "failed to load sync state: %v", err)
		} else {
			buf = loaded
		}
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		if m.guard != nil {
			if err := m.guard.Check(); err != nil {
				logger.Warnf(ctx, "session paused, remaining %s", m.guard.RemainingPause())
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(5 * time.Second):
				}
				continue
			}
		}

		resp, err := m.client.GetUpdates(ctx, buf)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			logger.Warnf(ctx, "GetUpdates error, retrying in %s: %v", backoff, err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		backoff = initialBackoff

		if resp.ErrCode == errCodeExpired {
			logger.Warnf(ctx, "%s", "session expired (errcode -14), triggering guard")
			if m.guard != nil {
				m.guard.Trigger()
			}
			buf = ""
			if m.syncState != nil {
				_ = m.syncState.Save("")
			}
			continue
		}

		if resp.Ret != 0 || resp.ErrCode != 0 {
			logger.Warnf(ctx, "server error: ret=%d errcode=%d errmsg=%s", resp.Ret, resp.ErrCode, resp.ErrMsg)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		if resp.GetUpdatesBuf != "" {
			buf = resp.GetUpdatesBuf
		}

		if m.syncState != nil && buf != "" {
			if err := m.syncState.Save(buf); err != nil {
				logger.Warnf(ctx, "failed to save sync state: %v", err)
			}
		}

		for _, msg := range resp.Msgs {
			m.handler(ctx, msg)
		}
	}
}
