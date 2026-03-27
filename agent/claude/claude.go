package claude

import (
	"context"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/CMGS/gua/agent"
	"github.com/CMGS/gua/runtime"
	"github.com/CMGS/gua/types"
	"github.com/CMGS/gua/utils"
)

const (
	defaultClaudeCmd = "claude"
	defaultBridgeBin = "gua-bridge"

	bridgeConnTimeout  = 30 * time.Second
	promptPollInterval = 2 * time.Second
	hookTimeoutMS      = 300000 // 5 minutes, written to CC hook settings
	responseBufSize    = 64     // per-user response channel buffer

	behaviorAllow = "allow"
	behaviorDeny  = "deny"

	elicitAccept  = "accept"
	elicitDecline = "decline"

	flagTrue = "true"
)

// Option configures a ClaudeCode agent.
type Option func(*ClaudeCode)

// ClaudeCode implements agent.Agent by spawning Claude Code with an MCP bridge.
type ClaudeCode struct {
	claudeCmd        string
	bridgeBin        string
	model            string
	baseWorkDir      string
	claudeMD         string
	socketPath       string
	listener         net.Listener
	rt               runtime.Runtime
	ctx              context.Context
	cancel           context.CancelFunc
	mu               sync.RWMutex
	sessions         map[string]*userSession
	userFlags        map[string]map[string]string
	workdirOverrides map[string]string // per-user workdir override (set by /respawn)
}

// New creates a new ClaudeCode agent. The provided ctx controls the lifetime
// of all internal goroutines (accept loop, bridge readers, etc.).
func New(ctx context.Context, opts ...Option) (*ClaudeCode, error) {
	ctx, cancel := context.WithCancel(ctx)
	c := &ClaudeCode{
		claudeCmd:        defaultClaudeCmd,
		bridgeBin:        defaultBridgeBin,
		sessions:         make(map[string]*userSession),
		userFlags:        make(map[string]map[string]string),
		workdirOverrides: make(map[string]string),
		ctx:              ctx,
		cancel:           cancel,
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.baseWorkDir == "" {
		c.baseWorkDir = filepath.Join(os.TempDir(), "gua-claude")
	}
	if c.rt == nil {
		cancel()
		return nil, fmt.Errorf("runtime is required: use WithRuntime option")
	}

	// Resolve bridgeBin to absolute path so it works from any session workdir.
	if !filepath.IsAbs(c.bridgeBin) {
		abs, err := filepath.Abs(c.bridgeBin)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("resolve bridge binary path: %w", err)
		}
		c.bridgeBin = abs
	}

	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("gua-%s.sock", utils.ShortID()))
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("listen on unix socket: %w", err)
	}
	c.socketPath = socketPath
	c.listener = ln

	go c.acceptLoop()

	return c, nil
}

// Name returns the agent identifier.
func (c *ClaudeCode) Name() string { return "claude" }

func (c *ClaudeCode) CLICommands() []string { return ccCLICommands }

func (c *ClaudeCode) ActiveSessions() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return slices.Collect(maps.Keys(c.sessions))
}

func (c *ClaudeCode) getSession(userID string) (*userSession, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	sess, ok := c.sessions[userID]
	return sess, ok
}

func (c *ClaudeCode) getUserFlag(userID, key string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if flags, ok := c.userFlags[userID]; ok {
		return flags[key]
	}
	return ""
}

// Send sends a message to the user's Claude Code session. Non-blocking.
// Responses arrive asynchronously via the channel returned by Subscribe.
func (c *ClaudeCode) Send(ctx context.Context, userID string, msg agent.Message) error {
	sess, err := c.getOrCreateSession(ctx, userID)
	if err != nil {
		return err
	}

	if p, ok := sess.permQueue.Peek(); ok {
		sess.pushResponse(permissionResponse(p.perm))
		return nil
	}
	if e, ok := sess.elicitQueue.Peek(); ok {
		sess.pushResponse(elicitationResponse(e.elicit))
		return nil
	}
	// In TUI menu mode, resend menu instead of forwarding to MCP.
	if sess.tuiMenu.Get() {
		if prompt := sess.prompt.Get(); prompt != "" {
			sess.pushResponse(tuiMenuResponse(prompt))
			return nil
		}
	}

	// Wait for bridge to be connected before sending.
	select {
	case <-sess.getConnReady():
	case <-ctx.Done():
		return ctx.Err()
	}

	return c.sendChannelEvent(sess, userID, msg)
}

// Control handles out-of-band user actions such as confirm/deny/select.
func (c *ClaudeCode) Control(ctx context.Context, userID string, action types.Action) (bool, error) {
	sess, ok := c.getSession(userID)
	if !ok {
		return false, nil
	}

	if sess.permQueue.Len() > 0 {
		return true, c.handlePermissionControl(ctx, sess, action)
	}
	if sess.elicitQueue.Len() > 0 {
		c.handleElicitationControl(ctx, sess, action)
		return true, nil
	}
	// TUI menu mode — all actions handled here.
	if sess.tuiMenu.Get() {
		return true, c.handleTUIMenuControl(ctx, sess, action)
	}
	if sess.prompt.Get() != "" {
		return true, c.handleInteractiveControl(ctx, sess, action)
	}

	return false, nil
}

// RawInput sends text directly to the agent's terminal, bypassing MCP.
// Polls capture-pane for TUI menu response.
func (c *ClaudeCode) RawInput(ctx context.Context, userID string, input string) error {
	sess, ok := c.getSession(userID)
	if !ok {
		return fmt.Errorf("no active session for user %s", userID)
	}
	if err := c.rt.SendInput(ctx, sess.proc, input, "Enter"); err != nil {
		return err
	}
	c.startTUIMenuPoll(ctx, sess)
	return nil
}

// startTUIMenuPoll cancels any previous poll goroutine and starts a new one.
func (c *ClaudeCode) startTUIMenuPoll(ctx context.Context, sess *userSession) {
	if cancel := sess.pollCancel.Get(); cancel != nil {
		cancel()
	}
	pollCtx, cancel := context.WithCancel(ctx)
	sess.pollCancel.Set(cancel)
	go c.pollTUIMenu(pollCtx, sess)
}

// pollTUIMenu polls capture-pane for a CC TUI menu.
// If no menu found after polling, captures status text as feedback.
func (c *ClaudeCode) pollTUIMenu(ctx context.Context, sess *userSession) {
	time.Sleep(500 * time.Millisecond)
	for range 10 {
		if ctx.Err() != nil {
			return
		}
		pane, err := c.rt.CaptureOutput(ctx, sess.proc)
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		menu := extractTUIMenu(pane)
		if menu == "" {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		sess.tuiMenu.Set(true)
		sess.prompt.Set(menu)
		sess.pushResponse(tuiMenuResponse(menu))
		return
	}
	// No menu found — command completed without TUI. Send status text.
	sess.tuiMenu.Clear()
	sess.prompt.Clear()
	if pane, err := c.rt.CaptureOutput(ctx, sess.proc); err == nil {
		if status := captureStatusText(pane); status != "" {
			sess.pushResponse(&agent.Response{Text: status})
		}
	}
}

// RespawnSession switches the user's session to a different working directory.
func (c *ClaudeCode) RespawnSession(ctx context.Context, userID, workDir, resumeOpt string) (bool, error) {
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		return false, fmt.Errorf("resolve workdir: %w", err)
	}
	info, err := os.Stat(absWorkDir)
	if err != nil {
		return false, fmt.Errorf("workdir %s: %w", absWorkDir, err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("workdir %s is not a directory", absWorkDir)
	}

	sess, hasSession := c.getSession(userID)
	if hasSession && sess.workDir == absWorkDir {
		return false, nil // idempotent
	}
	if hasSession {
		_ = c.Close(userID)
	}

	c.mu.Lock()
	c.workdirOverrides[userID] = absWorkDir
	if resumeOpt != "" {
		if c.userFlags[userID] == nil {
			c.userFlags[userID] = make(map[string]string)
		}
		if resumeOpt == "continue" {
			c.userFlags[userID]["continue"] = flagTrue
		} else {
			c.userFlags[userID]["resume"] = resumeOpt
		}
	}
	c.mu.Unlock()

	_, err = c.createSession(ctx, userID)
	return err == nil, err
}

func (c *ClaudeCode) getWorkdirOverride(userID string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.workdirOverrides[userID]
}

// Subscribe returns a channel that receives all responses for a user.
func (c *ClaudeCode) Subscribe(userID string) <-chan *agent.Response {
	sess, ok := c.getSession(userID)
	if !ok {
		// Return a closed channel if no session
		ch := make(chan *agent.Response)
		close(ch)
		return ch
	}
	return sess.outCh
}

// Close terminates a user's session.
func (c *ClaudeCode) Close(userID string) error {
	c.mu.Lock()
	sess, ok := c.sessions[userID]
	isOverride := c.workdirOverrides[userID] != ""
	if ok {
		delete(c.sessions, userID)
		delete(c.userFlags, userID)
		delete(c.workdirOverrides, userID)
	}
	c.mu.Unlock()

	if !ok {
		return agent.ErrNoSession
	}
	if isOverride {
		defer cleanupWorkdir(sess.workDir)
	}
	sess.close()
	return c.rt.Kill(c.ctx, sess.proc)
}

// CloseAll terminates all sessions and cleans up the socket.
func (c *ClaudeCode) CloseAll() error {
	c.cancel()

	c.mu.Lock()
	sessions := c.sessions
	overrides := c.workdirOverrides
	c.sessions = make(map[string]*userSession)
	c.workdirOverrides = make(map[string]string)
	c.mu.Unlock()

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cleanupCancel()

	for userID, sess := range sessions {
		sess.close()
		_ = c.rt.Kill(cleanupCtx, sess.proc)
		if overrides[userID] != "" {
			cleanupWorkdir(sess.workDir)
		}
	}

	_ = c.listener.Close()
	_ = os.Remove(c.socketPath)
	return nil
}

// Restart terminates the user's current session and starts a new one with the given flags.
func (c *ClaudeCode) Restart(ctx context.Context, userID string, flags map[string]string) (bool, error) {
	c.mu.RLock()
	current := c.userFlags[userID]
	sess, hasSession := c.sessions[userID]
	c.mu.RUnlock()

	if maps.Equal(current, flags) {
		return false, nil
	}

	c.mu.Lock()
	c.userFlags[userID] = flags
	c.mu.Unlock()

	if !hasSession || sess == nil || sess.proc == nil {
		// No existing session — just close and create fresh.
		_ = c.Close(userID)
		_, err := c.createSession(ctx, userID)
		return err == nil, err
	}

	// Respawn in the same pane — keeps workdir, session data intact.
	sess.respawning.Set(true)
	sess.reset()

	sess.resetConnReady()

	for len(sess.outCh) > 0 {
		<-sess.outCh
	}

	command := c.buildCommand(userID, true)
	logger := log.WithFunc("claude.Restart")
	logger.Infof(c.ctx, "respawning claude for user=%s", userID)

	if err := c.rt.Respawn(ctx, sess.proc, command); err != nil {
		// Preserve workdir override across Close (which deletes it).
		savedOverride := c.getWorkdirOverride(userID)
		_ = c.Close(userID)
		if savedOverride != "" {
			c.mu.Lock()
			c.workdirOverrides[userID] = savedOverride
			c.mu.Unlock()
		}
		_, err := c.createSession(ctx, userID)
		return err == nil, err
	}

	if err := runtime.AutoConfirmLoop(ctx, c.rt, sess.proc, sess.getConnReady(), claudeLineFilter, claudeAutoConfirm, promptPollInterval, bridgeConnTimeout); err != nil {
		sess.respawning.Clear()
		logger.Warnf(c.ctx, "bridge timeout after respawn for user=%s: %v", userID, err)
		if pane, captureErr := c.rt.CaptureOutput(c.ctx, sess.proc); captureErr == nil {
			logger.Warnf(c.ctx, "pane output:\n%s", pane)
		}
		return false, fmt.Errorf("bridge timeout after respawn: %w", err)
	}

	sess.respawning.Clear()
	return true, nil
}
