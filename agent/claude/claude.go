package claude

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
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
	replyTimeout       = 300 * time.Second
	controlWaitTimeout = 15 * time.Second
	promptPollInterval = 2 * time.Second

	behaviorAllow = "allow"
	behaviorDeny  = "deny"
)

// Option configures a ClaudeCode agent.
type Option func(*ClaudeCode)

// ClaudeCode implements agent.Agent by spawning Claude Code with an MCP bridge.
type ClaudeCode struct {
	claudeCmd   string
	bridgeBin   string
	model       string
	baseWorkDir string
	claudeMD    string
	socketPath  string
	listener    net.Listener
	rt          runtime.Runtime
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.RWMutex
	sessions    map[string]*userSession
	userFlags   map[string]map[string]string
}

// New creates a new ClaudeCode agent. The provided ctx controls the lifetime
// of all internal goroutines (accept loop, bridge readers, etc.).
func New(ctx context.Context, opts ...Option) (*ClaudeCode, error) {
	ctx, cancel := context.WithCancel(ctx)
	c := &ClaudeCode{
		claudeCmd: defaultClaudeCmd,
		bridgeBin: defaultBridgeBin,
		sessions:  make(map[string]*userSession),
		userFlags: make(map[string]map[string]string),
		ctx:       ctx,
		cancel:    cancel,
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

// WithClaudeCmd sets the path to the claude CLI binary.
func WithClaudeCmd(cmd string) Option {
	return func(c *ClaudeCode) { c.claudeCmd = cmd }
}

// WithBridgeBin sets the path to the bridge binary.
func WithBridgeBin(bin string) Option {
	return func(c *ClaudeCode) { c.bridgeBin = bin }
}

// WithModel sets the model for Claude Code.
func WithModel(model string) Option {
	return func(c *ClaudeCode) { c.model = model }
}

// WithRuntime sets the runtime container (tmux, screen, etc.) for hosting Claude sessions.
func WithRuntime(rt runtime.Runtime) Option {
	return func(c *ClaudeCode) {
		c.rt = rt
	}
}

// WithWorkDir sets the base working directory for sessions.
func WithWorkDir(dir string) Option {
	return func(c *ClaudeCode) { c.baseWorkDir = dir }
}

// WithClaudeMD sets the CLAUDE.md content written to each session workdir.
func WithClaudeMD(content string) Option {
	return func(c *ClaudeCode) { c.claudeMD = content }
}

// Name returns the agent identifier.
func (c *ClaudeCode) Name() string { return "claude" }

// Chat sends a message to the user's Claude Code session and waits for a reply.
// On first call for a user, the session is created and all startup prompts are
// auto-confirmed. The message is sent only after the bridge is fully connected.
func (c *ClaudeCode) Chat(ctx context.Context, userID string, msg agent.Message) (*agent.Response, error) {
	sess, err := c.getOrCreateSession(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	if perm := sess.permission.Get(); perm != nil {
		return permissionResponse(perm), nil
	}

	if err := c.sendChannelEvent(sess, userID, msg); err != nil {
		return nil, fmt.Errorf("send channel event: %w", err)
	}

	return c.waitForReply(ctx, sess)
}

// Control handles out-of-band user confirmations such as /yes or /no.
func (c *ClaudeCode) Control(ctx context.Context, userID string, action types.Action) (*agent.Response, bool, error) {
	c.mu.RLock()
	sess, ok := c.sessions[userID]
	c.mu.RUnlock()
	if !ok {
		return nil, false, nil
	}

	if perm := sess.permission.Get(); perm != nil {
		resp, err := c.handlePermissionControl(ctx, sess, action, perm)
		return resp, true, err
	}

	if sess.prompt.Get() != "" {
		resp, err := c.handleInteractiveControl(ctx, sess, action)
		return resp, true, err
	}

	return nil, false, nil
}

// Close terminates a user's session.
func (c *ClaudeCode) Close(userID string) error {
	c.mu.Lock()
	sess, ok := c.sessions[userID]
	if ok {
		delete(c.sessions, userID)
		delete(c.userFlags, userID)
	}
	c.mu.Unlock()

	if !ok {
		return nil
	}
	sess.close()
	return c.rt.Kill(c.ctx, sess.proc)
}

// CloseAll terminates all sessions and cleans up the socket.
func (c *ClaudeCode) CloseAll() error {
	c.cancel()

	c.mu.Lock()
	sessions := c.sessions
	c.sessions = make(map[string]*userSession)
	c.mu.Unlock()

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cleanupCancel()

	for _, sess := range sessions {
		sess.close()
		_ = c.rt.Kill(cleanupCtx, sess.proc)
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

	if flagsEqual(current, flags) {
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
	sess.close()
	sess.writer.Clear()
	sess.permission.Clear()
	sess.prompt.Clear()
	sess.connReady = make(chan struct{})

	command := c.buildCommand(userID, true)
	logger := log.WithFunc("claude.Restart")
	logger.Infof(c.ctx, "respawning claude for user=%s", userID)

	if err := c.rt.Respawn(ctx, sess.proc, command); err != nil {
		_ = c.Close(userID)
		_, err := c.createSession(ctx, userID)
		return err == nil, err
	}

	if err := runtime.AutoConfirmLoop(ctx, c.rt, sess.proc, sess.connReady, claudeLineFilter, []string{"Enter"}, promptPollInterval, bridgeConnTimeout); err != nil {
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
