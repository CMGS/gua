package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/projecteru2/core/log"

	"github.com/CMGS/gua/agent"
	"github.com/CMGS/gua/libwechat/auth"
	"github.com/CMGS/gua/protocol"
)

const (
	defaultClaudeCmd = "claude"
	defaultBridgeBin = "gua-bridge"
	defaultTmuxName  = "gua"

	bridgeConnTimeout = 30 * time.Second
	replyTimeout      = 300 * time.Second

	behaviorAllow = "allow"
)

// Option configures a ClaudeCode agent.
type Option func(*ClaudeCode)

// ClaudeCode implements agent.Agent by spawning Claude Code with an MCP bridge.
type ClaudeCode struct {
	claudeCmd   string
	bridgeBin   string
	model       string
	tmuxName    string
	baseWorkDir string
	claudeMD    string
	socketPath  string
	listener    net.Listener
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.RWMutex
	sessions    map[string]*userSession
}

// New creates a new ClaudeCode agent. The provided ctx controls the lifetime
// of all internal goroutines (accept loop, bridge readers, etc.).
func New(ctx context.Context, opts ...Option) (*ClaudeCode, error) {
	ctx, cancel := context.WithCancel(ctx)
	c := &ClaudeCode{
		claudeCmd: defaultClaudeCmd,
		bridgeBin: defaultBridgeBin,
		tmuxName:  defaultTmuxName,
		sessions:  make(map[string]*userSession),
		ctx:       ctx,
		cancel:    cancel,
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.baseWorkDir == "" {
		c.baseWorkDir = filepath.Join(os.TempDir(), "gua-claude")
	}

	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("gua-%s.sock", uuid.New().String()[:8]))
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

// WithTmuxName sets the tmux session name used to host Claude sessions.
func WithTmuxName(name string) Option {
	return func(c *ClaudeCode) {
		if name != "" {
			c.tmuxName = name
		}
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
func (c *ClaudeCode) Chat(ctx context.Context, userID string, msg agent.Message) (*agent.Response, error) {
	logger := log.WithFunc("claude.Chat")

	sess, err := c.getOrCreateSession(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	evt := protocol.ChannelEvent{
		Content: msg.Text,
		Meta:    map[string]string{"sender_id": userID},
	}
	if err := protocol.WriteEnvelope(sess.writer, protocol.TypeChannelEvent, evt); err != nil {
		return nil, fmt.Errorf("send channel event: %w", err)
	}

	timer := time.NewTimer(replyTimeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case env := <-sess.replyCh:
		if env == nil {
			return nil, fmt.Errorf("session closed")
		}
		tc, err := protocol.DecodePayload[protocol.ToolCall](env)
		if err != nil {
			logger.Warnf(ctx, "decode tool call: %v", err)
			return nil, fmt.Errorf("decode reply: %w", err)
		}
		resp := &agent.Response{Text: tc.Text}
		if tc.FilePath != "" {
			resp.Files = []string{tc.FilePath}
		}
		return resp, nil
	case <-timer.C:
		return nil, fmt.Errorf("reply timeout after %s", replyTimeout)
	}
}

// Close terminates a user's session.
func (c *ClaudeCode) Close(userID string) error {
	c.mu.Lock()
	sess, ok := c.sessions[userID]
	if ok {
		delete(c.sessions, userID)
	}
	c.mu.Unlock()

	if !ok {
		return nil
	}
	err := sess.close()
	if killErr := c.killTmuxWindow(context.Background(), sess.windowID); killErr != nil && err == nil {
		err = killErr
	}
	return err
}

// CloseAll terminates all sessions and cleans up the socket.
func (c *ClaudeCode) CloseAll() error {
	c.cancel()

	c.mu.Lock()
	sessions := c.sessions
	c.sessions = make(map[string]*userSession)
	c.mu.Unlock()

	for _, sess := range sessions {
		sess.close()                                          //nolint:errcheck
		c.killTmuxWindow(context.Background(), sess.windowID) //nolint:errcheck
	}

	c.listener.Close()
	os.Remove(c.socketPath) //nolint:errcheck
	return nil
}

type userSession struct {
	userID   string
	workDir  string
	windowID string
	paneID   string
	conn     net.Conn
	reader   *bufio.Reader
	writer   io.Writer
	replyCh  chan *protocol.Envelope
	cancel   context.CancelFunc

	connReady chan struct{} // closed when bridge connects
}

func (s *userSession) close() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.conn != nil {
		s.conn.Close()
	}
	return nil
}

func (c *ClaudeCode) getOrCreateSession(ctx context.Context, userID string) (*userSession, error) {
	c.mu.RLock()
	sess, ok := c.sessions[userID]
	c.mu.RUnlock()
	if ok {
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

	// Prepare workdir and files outside the lock.
	normalized := auth.NormalizeAccountID(userID)
	workDir := filepath.Join(c.baseWorkDir, "sessions", normalized)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, fmt.Errorf("create workdir: %w", err)
	}

	if c.claudeMD != "" {
		if err := os.WriteFile(filepath.Join(workDir, "CLAUDE.md"), []byte(c.claudeMD), 0o644); err != nil {
			return nil, fmt.Errorf("write CLAUDE.md: %w", err)
		}
	}

	mcpJSON, err := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"gua": map[string]any{
				"command": c.bridgeBin,
				"args":    []string{"--socket", c.socketPath, "--user", userID},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal mcp config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, ".mcp.json"), mcpJSON, 0o644); err != nil {
		return nil, fmt.Errorf("write .mcp.json: %w", err)
	}

	sessCtx, cancel := context.WithCancel(ctx)
	sess := &userSession{
		userID:    userID,
		workDir:   workDir,
		replyCh:   make(chan *protocol.Envelope, 64),
		cancel:    cancel,
		connReady: make(chan struct{}),
	}

	windowID, paneID, err := c.startTmuxSession(sessCtx, workDir, normalized)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start claude in tmux: %w", err)
	}
	sess.windowID = windowID
	sess.paneID = paneID

	// Register session under lock (brief).
	c.mu.Lock()
	if existing, ok := c.sessions[userID]; ok {
		// Another goroutine won the race — clean up ours.
		c.mu.Unlock()
		cancel()
		c.killTmuxWindow(context.Background(), windowID) //nolint:errcheck
		return existing, nil
	}
	c.sessions[userID] = sess
	c.mu.Unlock()

	logger.Infof(c.ctx, "spawned claude tmux_session=%s window=%s pane=%s user=%s workdir=%s", c.tmuxName, windowID, paneID, userID, workDir)

	select {
	case <-sess.connReady:
	case <-time.After(bridgeConnTimeout):
		pane, _ := c.capturePane(context.Background(), paneID)
		c.Close(userID) //nolint:errcheck
		if pane != "" {
			return nil, fmt.Errorf("bridge connection timeout for user %s\npane output:\n%s", userID, pane)
		}
		return nil, fmt.Errorf("bridge connection timeout for user %s", userID)
	case <-sessCtx.Done():
		c.Close(userID) //nolint:errcheck
		return nil, fmt.Errorf("session cancelled while waiting for bridge: %w", sessCtx.Err())
	}

	return sess, nil
}

func (c *ClaudeCode) startTmuxSession(ctx context.Context, workDir, windowName string) (string, string, error) {
	command := c.tmuxClaudeCommand()
	format := "#{window_id} #{pane_id}"

	if _, err := c.tmux(ctx, "has-session", "-t", c.tmuxName); err != nil {
		out, err := c.tmux(ctx, "new-session", "-d", "-s", c.tmuxName, "-n", windowName, "-c", workDir, "-P", "-F", format, command)
		if err != nil {
			return "", "", err
		}
		return parseTmuxIDs(out)
	}

	out, err := c.tmux(ctx, "new-window", "-d", "-t", c.tmuxName, "-n", windowName, "-c", workDir, "-P", "-F", format, command)
	if err != nil {
		return "", "", err
	}
	return parseTmuxIDs(out)
}

func (c *ClaudeCode) tmuxClaudeCommand() string {
	shellPath := os.Getenv("SHELL")
	if shellPath == "" {
		shellPath = "/bin/bash"
	}

	args := []string{shellQuote(c.claudeCmd)}
	if c.model != "" {
		args = append(args, shellQuote("--model"), shellQuote(c.model))
	}
	args = append(args, shellQuote("--dangerously-load-development-channels"), shellQuote("server:gua"))

	claudeCmd := strings.Join(args, " ")
	inner := fmt.Sprintf("%s; code=$?; printf '\\n[gua] claude exited with code %%s\\n' \"$code\"; exec %s -l", claudeCmd, shellQuote(shellPath))
	return fmt.Sprintf("%s -lc %s", shellQuote(shellPath), shellQuote(inner))
}

func (c *ClaudeCode) tmux(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *ClaudeCode) capturePane(ctx context.Context, paneID string) (string, error) {
	if paneID == "" {
		return "", nil
	}
	return c.tmux(ctx, "capture-pane", "-p", "-t", paneID, "-S", "-80")
}

func (c *ClaudeCode) killTmuxWindow(ctx context.Context, windowID string) error {
	if windowID == "" {
		return nil
	}
	_, err := c.tmux(ctx, "kill-window", "-t", windowID)
	return err
}

func parseTmuxIDs(out string) (string, string, error) {
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) < 2 {
		return "", "", fmt.Errorf("unexpected tmux output: %q", out)
	}
	return fields[0], fields[1], nil
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

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
		conn.Close()
		return
	}
	if env.Type != protocol.TypeRegister {
		logger.Warnf(c.ctx, "expected register, got: %s", env.Type)
		conn.Close()
		return
	}

	reg, err := protocol.DecodePayload[protocol.Register](env)
	if err != nil {
		logger.Warnf(c.ctx, "decode register: %v", err)
		conn.Close()
		return
	}

	c.mu.Lock()
	sess, ok := c.sessions[reg.UserID]
	if !ok {
		c.mu.Unlock()
		logger.Warnf(c.ctx, "no session for user: %s", reg.UserID)
		conn.Close()
		return
	}
	sess.conn = conn
	sess.reader = reader
	sess.writer = conn
	c.mu.Unlock()

	close(sess.connReady)
	logger.Infof(c.ctx, "bridge connected for user=%s", reg.UserID)

	c.readBridgeLoop(sess)

	// Bridge disconnected — clean up the zombie session.
	if pane, err := c.capturePane(context.Background(), sess.paneID); err == nil && pane != "" {
		logger.Warnf(c.ctx, "bridge disconnected for user=%s, cleaning up session\npane output:\n%s", sess.userID, pane)
	} else {
		logger.Warnf(c.ctx, "bridge disconnected for user=%s, cleaning up session", sess.userID)
	}
	c.Close(sess.userID) //nolint:errcheck
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
			reply := protocol.Permission{
				RequestID: perm.RequestID,
				Behavior:  behaviorAllow,
			}
			if err := protocol.WriteEnvelope(sess.writer, protocol.TypePermissionReply, reply); err != nil {
				logger.Warnf(c.ctx, "send permission reply: %v", err)
			}

		default:
			logger.Warnf(c.ctx, "unknown envelope from bridge: %s", env.Type)
		}
	}
}
