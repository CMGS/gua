package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/CMGS/gua/agent"
	"github.com/CMGS/gua/agent/claude/protocol"
	"github.com/CMGS/gua/runtime"
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
}

// New creates a new ClaudeCode agent. The provided ctx controls the lifetime
// of all internal goroutines (accept loop, bridge readers, etc.).
func New(ctx context.Context, opts ...Option) (*ClaudeCode, error) {
	ctx, cancel := context.WithCancel(ctx)
	c := &ClaudeCode{
		claudeCmd: defaultClaudeCmd,
		bridgeBin: defaultBridgeBin,
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

	if perm := sess.getPendingPermission(); perm != nil {
		return permissionResponse(perm), nil
	}

	if err := c.sendChannelEvent(sess, userID, msg); err != nil {
		return nil, fmt.Errorf("send channel event: %w", err)
	}

	return c.waitForReply(ctx, sess)
}

// Control handles out-of-band user confirmations such as /yes or /no.
func (c *ClaudeCode) Control(ctx context.Context, userID, input string) (*agent.Response, bool, error) {
	c.mu.RLock()
	sess, ok := c.sessions[userID]
	c.mu.RUnlock()
	if !ok {
		return nil, false, nil
	}

	if perm := sess.getPendingPermission(); perm != nil {
		resp, err := c.handlePermissionControl(ctx, sess, input, perm)
		return resp, true, err
	}

	if sess.getWriter() == nil || sess.getPendingTmuxPrompt() != "" {
		resp, err := c.handleTmuxControl(ctx, sess, input)
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

type userSession struct {
	userID  string
	workDir string
	proc    *runtime.Process
	conn    net.Conn
	reader  *bufio.Reader
	replyCh chan *protocol.Envelope
	permCh  chan *protocol.Permission
	cancel  context.CancelFunc

	connReady chan struct{} // closed when bridge connects

	stateMu           sync.Mutex
	writer            io.Writer // guarded by stateMu for concurrent safety
	pendingPermission *protocol.Permission
	pendingTmuxPrompt string
}

func (s *userSession) close() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.conn != nil {
		_ = s.conn.Close()
	}
}

func (s *userSession) setPendingPermission(perm *protocol.Permission) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.pendingPermission = perm
}

func (s *userSession) getPendingPermission() *protocol.Permission {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.pendingPermission
}

func (s *userSession) clearPendingPermission() {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.pendingPermission = nil
}

func (s *userSession) getWriter() io.Writer {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.writer
}

func (s *userSession) writeEnvelope(typ string, payload any) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.writer == nil {
		return fmt.Errorf("session not connected")
	}
	return protocol.WriteEnvelope(s.writer, typ, payload)
}

func (s *userSession) setPendingTmuxPrompt(prompt string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.pendingTmuxPrompt = prompt
}

func (s *userSession) getPendingTmuxPrompt() string {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.pendingTmuxPrompt
}

func (s *userSession) clearPendingTmuxPrompt() {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.pendingTmuxPrompt = ""
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
	normalized := utils.NormalizeID(userID)
	workDir := filepath.Join(c.baseWorkDir, "sessions", normalized)
	if err := os.MkdirAll(workDir, 0o755); err != nil { //nolint:gosec // non-sensitive per-user session directory
		return nil, fmt.Errorf("create workdir: %w", err)
	}

	if c.claudeMD != "" {
		if err := os.WriteFile(filepath.Join(workDir, "CLAUDE.md"), []byte(c.claudeMD), 0o644); err != nil { //nolint:gosec // non-sensitive config file
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
	if writeErr := os.WriteFile(filepath.Join(workDir, ".mcp.json"), mcpJSON, 0o644); writeErr != nil { //nolint:gosec // non-sensitive config file
		return nil, fmt.Errorf("write .mcp.json: %w", writeErr)
	}

	sessCtx, cancel := context.WithCancel(ctx)
	sess := &userSession{
		userID:    userID,
		workDir:   workDir,
		replyCh:   make(chan *protocol.Envelope, 64),
		permCh:    make(chan *protocol.Permission, 8),
		cancel:    cancel,
		connReady: make(chan struct{}),
	}

	command := c.tmuxClaudeCommand(workDir)
	proc, err := c.rt.StartProcess(sessCtx, normalized, workDir, command)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start claude in tmux: %w", err)
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

	// Auto-confirm any interactive prompts that appear before the bridge connects
	// (e.g. --dangerously-load-development-channels confirmation, project trust).
	// The user should not see these — they are internal to the setup flow.
	err = runtime.AutoConfirmLoop(sessCtx, c.rt, proc, sess.connReady, claudeLineFilter, []string{"Enter"}, promptPollInterval, bridgeConnTimeout)
	if err != nil {
		// timeout or canceled - log pane output for debugging
		if pane, captureErr := c.rt.CaptureOutput(c.ctx, proc); captureErr == nil {
			logger.Warnf(c.ctx, "pane output:\n%s", pane)
		}
		_ = c.Close(userID)
		return nil, fmt.Errorf("bridge connection timeout for user %s: %w", userID, err)
	}
	time.Sleep(500 * time.Millisecond) // let Claude finish initializing

	return sess, nil
}

func (c *ClaudeCode) tmuxClaudeCommand(workDir string) string {
	shellPath := os.Getenv("SHELL")
	if shellPath == "" {
		shellPath = "/bin/bash"
	}

	args := []string{runtime.ShellQuote(c.claudeCmd)}
	if c.model != "" {
		args = append(args, runtime.ShellQuote("--model"), runtime.ShellQuote(c.model))
	}
	// Resume previous conversation only if one exists.
	if hasExistingSession(workDir) {
		args = append(args, runtime.ShellQuote("--continue"))
	}
	// Pre-approve common tools to reduce permission prompts.
	for _, tool := range []string{"Read", "Glob", "Grep", "LS", "Bash", "Write", "Edit", "mcp__gua__gua_reply"} {
		args = append(args, runtime.ShellQuote("--allowedTools"), runtime.ShellQuote(tool))
	}
	args = append(args, runtime.ShellQuote("--dangerously-load-development-channels"), runtime.ShellQuote("server:gua"))

	claudeCmd := strings.Join(args, " ")
	inner := fmt.Sprintf("%s; code=$?; printf '\\n[gua] claude exited with code %%s\\n' \"$code\"; exec %s -l", claudeCmd, runtime.ShellQuote(shellPath))
	return fmt.Sprintf("%s -lc %s", runtime.ShellQuote(shellPath), runtime.ShellQuote(inner))
}

func (c *ClaudeCode) handleTmuxControl(ctx context.Context, sess *userSession, input string) (*agent.Response, error) {
	prompt := sess.getPendingTmuxPrompt()
	keyset, ok := runtime.ControlKeys(prompt, input)
	if !ok {
		return interactiveResponse(prompt), nil
	}
	if err := c.rt.SendInput(ctx, sess.proc, keyset...); err != nil {
		return nil, err
	}
	sess.clearPendingTmuxPrompt()

	return c.waitForReplyWithTimeout(ctx, sess, controlWaitTimeout, false)
}

func (c *ClaudeCode) handlePermissionControl(ctx context.Context, sess *userSession, input string, perm *protocol.Permission) (*agent.Response, error) {
	behavior, ok := permissionBehavior(input)
	if !ok {
		return permissionResponse(perm), nil
	}
	reply := protocol.Permission{
		RequestID: perm.RequestID,
		Behavior:  behavior,
	}
	if err := sess.writeEnvelope(protocol.TypePermissionReply, reply); err != nil {
		return nil, fmt.Errorf("send permission reply: %w", err)
	}
	sess.clearPendingPermission()

	if behavior != behaviorAllow {
		return &agent.Response{Text: "已拒绝该操作。"}, nil
	}
	return c.waitForReply(ctx, sess)
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
	sess.stateMu.Lock()
	sess.writer = conn
	sess.pendingTmuxPrompt = ""
	sess.stateMu.Unlock()
	c.mu.Unlock()

	select {
	case <-sess.connReady:
	default:
		close(sess.connReady)
	}
	logger.Infof(c.ctx, "bridge connected for user=%s", reg.UserID)

	c.readBridgeLoop(sess)

	// Bridge disconnected — clean up the zombie session.
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
			// Capture the actual tmux terminal prompt which has richer content
			// (numbered options like "1. Yes  2. Yes, allow during session  3. No").
			if pane, captureErr := c.rt.CaptureOutput(c.ctx, sess.proc); captureErr == nil && pane != "" {
				perm.TmuxPrompt = runtime.CompactInteractivePrompt(pane, claudeLineFilter)
			}
			sess.setPendingPermission(perm)
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
				sess.setPendingTmuxPrompt(prompt)
				return interactiveResponse(prompt), nil
			}
		case <-timer.C:
			prompt, err := runtime.CaptureInteractivePrompt(ctx, c.rt, sess.proc, claudeLineFilter)
			if err == nil && prompt != "" {
				sess.setPendingTmuxPrompt(prompt)
				return interactiveResponse(prompt), nil
			}
			if timeoutIsError {
				return nil, fmt.Errorf("reply timeout after %s", timeout)
			}
			return &agent.Response{Text: "已发送选择，Claude 仍在处理中。"}, nil
		}
	}
}

func permissionResponse(perm *protocol.Permission) *agent.Response {
	return &agent.Response{
		Prompt:     agent.PromptPermission,
		PromptText: perm.TmuxPrompt,
		Options:    runtime.ExtractOptions(perm.TmuxPrompt),
		Permission: &agent.PermissionInfo{
			ToolName:     perm.ToolName,
			Description:  perm.Description,
			InputPreview: perm.InputPreview,
		},
	}
}

func interactiveResponse(prompt string) *agent.Response {
	return &agent.Response{
		Prompt:     agent.PromptInteractive,
		PromptText: prompt,
		Options:    runtime.ExtractOptions(prompt),
	}
}

func permissionBehavior(input string) (string, bool) {
	switch runtime.NormalizeControl(input) {
	case "yes", "y", "allow", "ok":
		return behaviorAllow, true
	case "no", "n", "deny", "cancel":
		return behaviorDeny, true
	default:
		return "", false
	}
}

// claudeLineFilter filters terminal lines specific to Claude Code's TUI.
func claudeLineFilter(line string) (keep bool, interactive bool) {
	switch {
	case strings.Contains(line, "Claude Code v"),
		strings.Contains(line, "Listening for channel messages from:"),
		strings.Contains(line, "Experimental · inbound messages"),
		strings.HasPrefix(line, "/private/tmp/"),
		strings.HasPrefix(line, "/tmp/"),
		strings.HasPrefix(line, "← "),
		strings.HasPrefix(line, "● "):
		return false, false
	case strings.HasPrefix(line, "❯ "):
		trimmed := strings.TrimSpace(strings.TrimPrefix(line, "❯ "))
		return trimmed != "", false
	}

	// Strip Claude box-drawing decorations.
	stripped := strings.TrimLeft(line, "⎿│└├┌─> ")
	stripped = strings.TrimSpace(stripped)
	if stripped == "" || strings.HasPrefix(stripped, "/") {
		return false, false
	}

	return true, false
}

func hasExistingSession(workDir string) bool {
	entries, err := os.ReadDir(filepath.Join(workDir, ".claude"))
	if err != nil {
		return false
	}
	return len(entries) > 0
}
