package claude

import (
	"bufio"
	"context"
	"net"

	"github.com/projecteru2/core/log"

	"github.com/CMGS/gua/agent"
	"github.com/CMGS/gua/agent/claude/protocol"
	"github.com/CMGS/gua/runtime"
)

func (c *ClaudeCode) acceptLoop() {
	for {
		conn, err := c.listener.Accept()
		if err != nil {
			if c.ctx.Err() != nil {
				return // clean shutdown
			}
			log.WithFunc("claude.acceptLoop").Warnf(c.ctx, "accept error: %v", err)
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
		logger.Warnf(c.ctx, "read initial envelope: %v", err)
		_ = conn.Close()
		return
	}

	// Dispatch based on the first envelope type.
	switch env.Type {
	case protocol.TypeRegister:
		c.handleBridgeSession(conn, reader, env)
	case protocol.TypeHookPermission:
		c.handleHookPermission(conn, env)
	case protocol.TypeHookElicitation:
		c.handleHookElicitation(conn, env)
	default:
		logger.Warnf(c.ctx, "unexpected initial envelope: %s", env.Type)
		_ = conn.Close()
	}
}

func (c *ClaudeCode) handleBridgeSession(conn net.Conn, reader *bufio.Reader, env *protocol.Envelope) {
	logger := log.WithFunc("claude.handleBridgeSession")

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

	sess.signalReady()
	logger.Infof(c.ctx, "bridge connected for user=%s", reg.UserID)

	// Stream-watch output and read bridge events in parallel.
	sessCtx, sessCancel := context.WithCancel(c.ctx)
	go c.watchOutput(sessCtx, sess)
	c.readBridgeLoop(sess)
	sessCancel()

	// Shutdown or respawn — skip cleanup, CloseAll handles it.
	if c.ctx.Err() != nil || sess.respawning.Get() {
		return
	}

	logger.Infof(c.ctx, "bridge disconnected for user=%s, cleaning up session", sess.userID)
	_ = c.Close(sess.userID)
}

// monitorHookConn detects when a hook connection closes (CC kills hook on timeout).
// Returns a context that is canceled when conn is closed or the parent ctx is done.
func monitorHookConn(parent context.Context, conn net.Conn) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	go func() {
		buf := make([]byte, 1)
		_, _ = conn.Read(buf) // blocks until close
		cancel()
	}()
	return ctx, cancel
}

func (c *ClaudeCode) handleHookPermission(conn net.Conn, env *protocol.Envelope) {
	logger := log.WithFunc("claude.handleHookPermission")
	defer conn.Close() //nolint:errcheck

	hp, err := protocol.DecodePayload[protocol.HookPermission](env)
	if err != nil {
		logger.Warnf(c.ctx, "decode hook permission: %v", err)
		return
	}

	sess, ok := c.getSession(hp.UserID)
	if !ok {
		logger.Warnf(c.ctx, "no session for hook user: %s", hp.UserID)
		return
	}

	hookCtx, hookCancel := monitorHookConn(c.ctx, conn)
	defer hookCancel()

	replyCh := make(chan protocol.Permission, 1)
	perm := &hp.Permission
	sess.permQueue.Push(pendingPerm{perm: perm, replyCh: replyCh})
	// Only push the first prompt — subsequent ones are pushed after the user handles each one.
	if sess.permQueue.Len() == 1 {
		sess.pushResponse(permissionResponse(perm))
	}

	logger.Debugf(c.ctx, "hook permission request for user=%s tool=%s", hp.UserID, perm.ToolName)

	select {
	case reply := <-replyCh:
		if writeErr := protocol.WriteEnvelope(conn, protocol.TypePermissionReply, reply); writeErr != nil {
			logger.Warnf(c.ctx, "write hook permission reply: %v", writeErr)
		}
	case <-hookCtx.Done():
		// Remove stale entry so it doesn't block the queue.
		sess.permQueue.Remove(func(p pendingPerm) bool { return p.replyCh == replyCh })
		logger.Debugf(c.ctx, "hook permission canceled for user=%s", hp.UserID)
	}
}

func (c *ClaudeCode) handleHookElicitation(conn net.Conn, env *protocol.Envelope) {
	logger := log.WithFunc("claude.handleHookElicitation")
	defer conn.Close() //nolint:errcheck

	he, err := protocol.DecodePayload[protocol.HookElicitation](env)
	if err != nil {
		logger.Warnf(c.ctx, "decode hook elicitation: %v", err)
		return
	}

	sess, ok := c.getSession(he.UserID)
	if !ok {
		logger.Warnf(c.ctx, "no session for hook user: %s", he.UserID)
		return
	}

	hookCtx, hookCancel := monitorHookConn(c.ctx, conn)
	defer hookCancel()

	replyCh := make(chan protocol.ElicitationReply, 1)
	elicit := &he.Elicitation
	sess.elicitQueue.Push(pendingElicit{elicit: elicit, replyCh: replyCh})
	if sess.elicitQueue.Len() == 1 {
		sess.pushResponse(elicitationResponse(elicit))
	}

	logger.Debugf(c.ctx, "hook elicitation for user=%s server=%s", he.UserID, elicit.ServerName)

	select {
	case reply := <-replyCh:
		if writeErr := protocol.WriteEnvelope(conn, protocol.TypeElicitationReply, reply); writeErr != nil {
			logger.Warnf(c.ctx, "write hook elicitation reply: %v", writeErr)
		}
	case <-hookCtx.Done():
		sess.elicitQueue.Remove(func(e pendingElicit) bool { return e.replyCh == replyCh })
		logger.Debugf(c.ctx, "hook elicitation canceled for user=%s", he.UserID)
	}
}

func (c *ClaudeCode) readBridgeLoop(sess *userSession) {
	logger := log.WithFunc("claude.readBridgeLoop")

	for {
		env, err := protocol.ReadEnvelope(sess.reader)
		if err != nil {
			if c.ctx.Err() == nil {
				logger.Warnf(c.ctx, "bridge read error for user=%s: %v", sess.userID, err)
			}
			return
		}

		switch env.Type {
		case protocol.TypeToolCall:
			tc, err := protocol.DecodePayload[protocol.ToolCall](env)
			if err != nil {
				logger.Warnf(c.ctx, "decode tool call: %v", err)
				continue
			}
			resp := &agent.Response{Text: tc.Text}
			if tc.FilePath != "" {
				resp.Files = []string{tc.FilePath}
			}
			sess.pushResponse(resp)

		case protocol.TypePermissionRequest:
			perm, err := protocol.DecodePayload[protocol.Permission](env)
			if err != nil {
				logger.Warnf(c.ctx, "decode permission request: %v", err)
				continue
			}
			if pane, captureErr := c.rt.CaptureOutput(c.ctx, sess.proc); captureErr == nil && pane != "" {
				perm.Prompt = runtime.CompactInteractivePrompt(pane, claudeLineFilter)
			}
			sess.permQueue.Push(pendingPerm{perm: perm}) // nil replyCh = reply via bridge writeEnvelope
			sess.pushResponse(permissionResponse(perm))

		default:
			logger.Debugf(c.ctx, "unknown envelope from bridge: %s", env.Type)
		}
	}
}

// watchOutput keeps the pipe-pane FIFO alive for the session.
// Interactive prompt detection is NOT done here — pipe-pane output from
// TUI redraws is garbled (cursor positioning, no real whitespace).
// All interactive detection goes through capture-pane poll (pollTUIMenu).
func (c *ClaudeCode) watchOutput(ctx context.Context, sess *userSession) {
	if err := c.rt.Watch(ctx, sess.proc, func(_ string) {
		// Intentionally empty — Watch keeps the FIFO alive.
		// Future: could be used for non-interactive streaming output.
	}); err != nil && ctx.Err() == nil {
		log.WithFunc("claude.watchOutput").Warnf(ctx, "watch ended for user=%s: %v", sess.userID, err)
	}
}

func (c *ClaudeCode) sendChannelEvent(sess *userSession, userID string, msg agent.Message) error {
	evt := protocol.ChannelEvent{
		Content: msg.Text,
		Meta:    map[string]string{"sender_id": userID},
	}
	return sess.writeEnvelope(protocol.TypeChannelEvent, evt)
}
