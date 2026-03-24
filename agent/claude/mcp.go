package claude

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/CMGS/gua/agent"
	"github.com/CMGS/gua/agent/claude/protocol"
	"github.com/CMGS/gua/runtime"
)

func (c *ClaudeCode) acceptLoop() {
	logger := log.WithFunc("claude.acceptLoop")

	for {
		conn, err := c.listener.Accept()
		if err != nil {
			logger.Warnf(c.ctx, "accept error: %v", err)
			return
		}
		go c.handleBridgeConn(conn)
	}
}

func (c *ClaudeCode) handleBridgeConn(conn net.Conn) {
	logger := log.WithFunc("claude.handleBridgeConn")

	reader := bufio.NewReader(conn)

	env, err := protocol.ReadEnvelope(reader)
	if err != nil {
		logger.Warnf(c.ctx, "read register envelope: %v", err)
		_ = conn.Close()
		return
	}
	if env.Type != protocol.TypeRegister {
		logger.Warnf(c.ctx, "expected register, got: %s", env.Type)
		_ = conn.Close()
		return
	}

	reg, err := protocol.DecodePayload[protocol.Register](env)
	if err != nil {
		logger.Warnf(c.ctx, "decode register: %v", err)
		_ = conn.Close()
		return
	}

	c.mu.Lock()
	sess, ok := c.sessions[reg.UserID]
	if !ok {
		c.mu.Unlock()
		logger.Warnf(c.ctx, "no session for user: %s", reg.UserID)
		_ = conn.Close()
		return
	}
	sess.conn = conn
	sess.reader = reader
	sess.writer.Set(conn)
	sess.prompt.Clear()
	c.mu.Unlock()

	select {
	case <-sess.connReady:
	default:
		close(sess.connReady)
	}
	logger.Infof(c.ctx, "bridge connected for user=%s", reg.UserID)

	c.readBridgeLoop(sess)

	// If a Respawn is in progress, the bridge disconnect is expected — don't clean up.
	if sess.respawning.Get() {
		logger.Debugf(c.ctx, "bridge disconnected for user=%s (respawning, skipping cleanup)", sess.userID)
		return
	}

	if pane, err := c.rt.CaptureOutput(c.ctx, sess.proc); err == nil && pane != "" {
		logger.Infof(c.ctx, "bridge disconnected for user=%s, cleaning up session\npane output:\n%s", sess.userID, pane)
	} else {
		logger.Infof(c.ctx, "bridge disconnected for user=%s, cleaning up session", sess.userID)
	}
	_ = c.Close(sess.userID)
}

func (c *ClaudeCode) readBridgeLoop(sess *userSession) {
	logger := log.WithFunc("claude.readBridgeLoop")

	for {
		env, err := protocol.ReadEnvelope(sess.reader)
		if err != nil {
			logger.Warnf(c.ctx, "bridge read error for user=%s: %v", sess.userID, err)
			return
		}

		switch env.Type {
		case protocol.TypeToolCall:
			select {
			case sess.replyCh <- env:
			default:
				logger.Warnf(c.ctx, "reply channel full for user=%s, dropping", sess.userID)
			}

		case protocol.TypePermissionRequest:
			perm, err := protocol.DecodePayload[protocol.Permission](env)
			if err != nil {
				logger.Warnf(c.ctx, "decode permission request: %v", err)
				continue
			}
			// Capture the actual runtime terminal prompt which has richer content
			// (numbered options like "1. Yes  2. Yes, allow during session  3. No").
			if pane, captureErr := c.rt.CaptureOutput(c.ctx, sess.proc); captureErr == nil && pane != "" {
				perm.Prompt = runtime.CompactInteractivePrompt(pane, claudeLineFilter)
			}
			sess.permission.Set(perm)
			select {
			case sess.permCh <- perm:
			default:
				logger.Warnf(c.ctx, "permission channel full for user=%s, dropping notification", sess.userID)
			}

		default:
			logger.Debugf(c.ctx, "unknown envelope from bridge: %s", env.Type)
		}
	}
}

func (c *ClaudeCode) sendChannelEvent(sess *userSession, userID string, msg agent.Message) error {
	logger := log.WithFunc("claude.sendChannelEvent")
	evt := protocol.ChannelEvent{
		Content: msg.Text,
		Meta:    map[string]string{"sender_id": userID},
	}
	if err := sess.writeEnvelope(protocol.TypeChannelEvent, evt); err != nil {
		return err
	}
	logger.Debugf(c.ctx, "sent channel event to user=%s, text=%d bytes", userID, len(msg.Text))
	return nil
}

func (c *ClaudeCode) waitForReply(ctx context.Context, sess *userSession) (*agent.Response, error) {
	return c.waitForReplyWithTimeout(ctx, sess, replyTimeout, true)
}

func (c *ClaudeCode) waitForReplyWithTimeout(ctx context.Context, sess *userSession, timeout time.Duration, timeoutIsError bool) (*agent.Response, error) {
	logger := log.WithFunc("claude.waitForReply")
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(promptPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case env := <-sess.replyCh:
			if env == nil {
				return nil, fmt.Errorf("session closed")
			}
			tc, err := protocol.DecodePayload[protocol.ToolCall](env)
			if err != nil {
				return nil, fmt.Errorf("decode reply: %w", err)
			}
			resp := &agent.Response{Text: tc.Text}
			if tc.FilePath != "" {
				resp.Files = []string{tc.FilePath}
			}
			return resp, nil
		case perm := <-sess.permCh:
			if perm == nil {
				return nil, fmt.Errorf("session closed")
			}
			return permissionResponse(perm), nil
		case <-ticker.C:
			prompt, err := runtime.CaptureInteractivePrompt(ctx, c.rt, sess.proc, claudeLineFilter)
			if err != nil {
				logger.Debugf(ctx, "capture interactive prompt for user=%s: %v", sess.userID, err)
				continue
			}
			if prompt != "" {
				sess.prompt.Set(prompt)
				return interactiveResponse(prompt), nil
			}
		case <-timer.C:
			prompt, err := runtime.CaptureInteractivePrompt(ctx, c.rt, sess.proc, claudeLineFilter)
			if err == nil && prompt != "" {
				sess.prompt.Set(prompt)
				return interactiveResponse(prompt), nil
			}
			if timeoutIsError {
				return nil, fmt.Errorf("reply timeout after %s", timeout)
			}
			return &agent.Response{Text: "已发送选择，Claude 仍在处理中。"}, nil
		}
	}
}
