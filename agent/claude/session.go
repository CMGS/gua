package claude

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/CMGS/gua/agent"
	"github.com/CMGS/gua/agent/claude/protocol"
	"github.com/CMGS/gua/runtime"
	"github.com/CMGS/gua/utils"
)

type userSession struct {
	userID  string
	workDir string
	proc    *runtime.Process
	conn    net.Conn
	reader  *bufio.Reader
	outCh   chan *agent.Response
	cancel  context.CancelFunc

	connReady chan struct{} // closed when bridge connects

	writeMu     sync.Mutex // guards writeEnvelope (net.Conn.Write is not atomic for large messages)
	writer      utils.SyncValue[io.Writer]
	permission  utils.SyncValue[*protocol.Permission]
	elicitation utils.SyncValue[*protocol.Elicitation]
	prompt      utils.SyncValue[string]             // pending interactive/TUI menu prompt
	tuiMenu     utils.SyncValue[bool]               // true when in TUI menu mode (e.g. /model)
	pollCancel  utils.SyncValue[context.CancelFunc] // cancels previous TUI poll goroutine
	respawning  utils.SyncValue[bool]               // true during Respawn, prevents bridge disconnect cleanup

	// Hook reply channels — set when a CC hook process is waiting for user decision.
	hookPermReply   utils.SyncValue[chan protocol.Permission]
	hookElicitReply utils.SyncValue[chan protocol.ElicitationReply]
}

func (s *userSession) close() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.conn != nil {
		_ = s.conn.Close()
	}
	if s.outCh != nil {
		close(s.outCh)
	}
}

func (s *userSession) pushResponse(resp *agent.Response) {
	defer func() {
		recover() //nolint:errcheck // send on closed channel during shutdown
	}()
	select {
	case s.outCh <- resp:
	default:
		// channel full, drop this response
	}
}

func (s *userSession) writeEnvelope(typ string, payload any) error {
	w := s.writer.Get()
	if w == nil {
		return fmt.Errorf("session not connected")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return protocol.WriteEnvelope(w, typ, payload)
}

func (c *ClaudeCode) getOrCreateSession(ctx context.Context, userID string) (*userSession, error) {
	if sess, ok := c.getSession(userID); ok {
		return sess, nil
	}
	return c.createSession(ctx, userID)
}

func (c *ClaudeCode) createSession(ctx context.Context, userID string) (*userSession, error) {
	logger := log.WithFunc("claude.createSession")

	// Double-check under write lock.
	c.mu.Lock()
	if sess, ok := c.sessions[userID]; ok {
		c.mu.Unlock()
		return sess, nil
	}
	c.mu.Unlock()

	normalized := utils.NormalizeID(userID)
	workDir := filepath.Join(c.baseWorkDir, "sessions", normalized)
	continuing := hasExistingSession(workDir)

	if err := os.MkdirAll(workDir, 0o755); err != nil { //nolint:gosec // non-sensitive per-user session directory
		return nil, fmt.Errorf("create workdir: %w", err)
	}

	if c.claudeMD != "" {
		if err := os.WriteFile(filepath.Join(workDir, "CLAUDE.md"), []byte(c.claudeMD), 0o644); err != nil { //nolint:gosec // non-sensitive config file
			return nil, fmt.Errorf("write CLAUDE.md: %w", err)
		}
	}

	mcpConfig := map[string]any{
		"mcpServers": map[string]any{
			"gua": map[string]any{
				"command": c.bridgeBin,
				"args":    []string{"--socket", c.socketPath, "--user", userID},
			},
		},
	}
	if err := utils.WriteJSONFile(filepath.Join(workDir, ".mcp.json"), &mcpConfig, 0o644); err != nil {
		return nil, err
	}

	// Write hook settings so CC routes permission/elicitation through gua-bridge.
	hookCmd := func(hookType string) string {
		return fmt.Sprintf("%s --socket %s --user %s --hook %s", c.bridgeBin, c.socketPath, userID, hookType)
	}
	hookSettings := map[string]any{
		"hooks": map[string]any{
			"PermissionRequest": []map[string]any{{
				"matcher": "",
				"hooks": []map[string]any{{
					"type":    "command",
					"command": hookCmd("permission"),
					"timeout": 300000,
				}},
			}},
			"Elicitation": []map[string]any{{
				"matcher": "",
				"hooks": []map[string]any{{
					"type":    "command",
					"command": hookCmd("elicitation"),
					"timeout": 300000,
				}},
			}},
		},
	}
	if err := utils.WriteJSONFile(filepath.Join(workDir, ".claude", "settings.local.json"), &hookSettings, 0o644); err != nil {
		return nil, err
	}

	sessCtx, cancel := context.WithCancel(ctx)
	sess := &userSession{
		userID:    userID,
		workDir:   workDir,
		outCh:     make(chan *agent.Response, 64),
		cancel:    cancel,
		connReady: make(chan struct{}),
	}

	command := c.buildCommand(userID, continuing)
	logger.Debugf(ctx, "command for user=%s: %s", userID, command)
	proc, err := c.rt.StartProcess(sessCtx, normalized, workDir, command)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start claude: %w", err)
	}
	sess.proc = proc

	// Register session under lock (brief).
	c.mu.Lock()
	if existing, ok := c.sessions[userID]; ok {
		// Another goroutine won the race — clean up ours.
		c.mu.Unlock()
		cancel()
		_ = c.rt.Kill(ctx, proc)
		return existing, nil
	}
	c.sessions[userID] = sess
	c.mu.Unlock()

	logger.Infof(c.ctx, "spawned claude window=%s pane=%s user=%s workdir=%s", proc.ID, proc.PaneID, userID, workDir)

	// Auto-confirm startup prompts (MCP trust, dev channels) before bridge connects.
	timeout := bridgeConnTimeout
	if continuing {
		timeout = 5 * time.Second // short timeout: --continue fails fast if no conversation
	}
	err = runtime.AutoConfirmLoop(sessCtx, c.rt, proc, sess.connReady, claudeLineFilter, []string{"Enter"}, promptPollInterval, timeout)
	if err != nil && continuing {
		// --continue failed (no previous conversation). Retry without it.
		logger.Infof(c.ctx, "no previous conversation, restarting without --continue for user=%s", userID)
		sess.connReady = make(chan struct{})
		command = c.buildCommand(userID, false)
		if respawnErr := c.rt.Respawn(ctx, proc, command); respawnErr == nil {
			err = runtime.AutoConfirmLoop(sessCtx, c.rt, proc, sess.connReady, claudeLineFilter, []string{"Enter"}, promptPollInterval, bridgeConnTimeout)
		}
	}
	if err != nil {
		if pane, captureErr := c.rt.CaptureOutput(c.ctx, proc); captureErr == nil {
			logger.Warnf(c.ctx, "pane output:\n%s", pane)
		}
		_ = c.Close(userID)
		return nil, fmt.Errorf("bridge connection timeout for user %s: %w", userID, err)
	}

	return sess, nil
}
