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
	"github.com/CMGS/gua/utils"
	"github.com/CMGS/gua/protocol"
)

const (
	defaultClaudeCmd = "claude"
	defaultBridgeBin = "gua-bridge"
	defaultTmuxName  = "gua"

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
// On first call for a user, the session is created and all startup prompts are
// auto-confirmed. The message is sent only after the bridge is fully connected.
func (c *ClaudeCode) Chat(ctx context.Context, userID string, msg agent.Message) (*agent.Response, error) {
	sess, err := c.getOrCreateSession(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	if perm := sess.getPendingPermission(); perm != nil {
		return &agent.Response{
			Prompt:     agent.PromptPermission,
			PromptText: perm.TmuxPrompt,
			Options:    extractOptions(perm.TmuxPrompt),
			Permission: &agent.PermissionInfo{
				ToolName:     perm.ToolName,
				Description:  perm.Description,
				InputPreview: perm.InputPreview,
			},
		}, nil
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
				logger.Warnf(ctx, "decode tool call: %v", err)
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
			return &agent.Response{
				Prompt:     agent.PromptPermission,
				PromptText: perm.TmuxPrompt,
				Options:    extractOptions(perm.TmuxPrompt),
				Permission: &agent.PermissionInfo{
					ToolName:     perm.ToolName,
					Description:  perm.Description,
					InputPreview: perm.InputPreview,
				},
			}, nil
		case <-ticker.C:
			prompt, err := c.captureInteractivePrompt(ctx, sess.paneID)
			if err != nil {
				logger.Debugf(ctx, "capture interactive prompt for user=%s: %v", sess.userID, err)
				continue
			}
			if prompt != "" {
				sess.setPendingTmuxPrompt(prompt)
				return &agent.Response{
					Prompt:     agent.PromptInteractive,
					PromptText: prompt,
					Options:    extractOptions(prompt),
				}, nil
			}
		case <-timer.C:
			prompt, err := c.captureInteractivePrompt(ctx, sess.paneID)
			if err == nil && prompt != "" {
				sess.setPendingTmuxPrompt(prompt)
				return &agent.Response{
					Prompt:     agent.PromptInteractive,
					PromptText: prompt,
					Options:    extractOptions(prompt),
				}, nil
			}
			if timeoutIsError {
				return nil, fmt.Errorf("reply timeout after %s", timeout)
			}
			return &agent.Response{Text: "已发送选择，Claude 仍在处理中。"}, nil
		}
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
	replyCh  chan *protocol.Envelope
	permCh   chan *protocol.Permission
	cancel   context.CancelFunc

	connReady chan struct{} // closed when bridge connects

	stateMu           sync.Mutex
	writer            io.Writer // guarded by stateMu for concurrent safety
	pendingPermission *protocol.Permission
	pendingTmuxPrompt string
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
		permCh:    make(chan *protocol.Permission, 8),
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

	// Auto-confirm any interactive prompts that appear before the bridge connects
	// (e.g. --dangerously-load-development-channels confirmation, project trust).
	// The user should not see these — they are internal to the setup flow.
	ticker := time.NewTicker(promptPollInterval)
	defer ticker.Stop()
	timeout := time.NewTimer(bridgeConnTimeout)
	defer timeout.Stop()

	for {
		select {
		case <-sess.connReady:
			// Brief pause to let Claude finish initializing after bridge handshake.
			time.Sleep(500 * time.Millisecond)
			return sess, nil
		case <-ticker.C:
			c.autoConfirmPrompt(paneID)
		case <-timeout.C:
			logger.Warnf(c.ctx, "bridge connection timeout for user=%s, last pane capture follows", userID)
			if pane, err := c.capturePane(c.ctx, paneID); err == nil {
				logger.Warnf(c.ctx, "pane output:\n%s", pane)
			}
			c.Close(userID) //nolint:errcheck
			return nil, fmt.Errorf("bridge connection timeout for user %s", userID)
		case <-sessCtx.Done():
			c.Close(userID) //nolint:errcheck
			return nil, fmt.Errorf("session cancelled while waiting for bridge: %w", sessCtx.Err())
		}
	}
}

func (c *ClaudeCode) startTmuxSession(ctx context.Context, workDir, windowName string) (string, string, error) {
	command := c.tmuxClaudeCommand(workDir)
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

func (c *ClaudeCode) tmuxClaudeCommand(workDir string) string {
	shellPath := os.Getenv("SHELL")
	if shellPath == "" {
		shellPath = "/bin/bash"
	}

	args := []string{shellQuote(c.claudeCmd)}
	if c.model != "" {
		args = append(args, shellQuote("--model"), shellQuote(c.model))
	}
	// Resume previous conversation only if one exists.
	if hasExistingSession(workDir) {
		args = append(args, shellQuote("--continue"))
	}
	// Pre-approve common tools to reduce permission prompts.
	for _, tool := range []string{"Read", "Glob", "Grep", "LS", "Bash", "Write", "Edit", "mcp__gua__gua_reply"} {
		args = append(args, shellQuote("--allowedTools"), shellQuote(tool))
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

func (c *ClaudeCode) captureInteractivePrompt(ctx context.Context, paneID string) (string, error) {
	pane, err := c.capturePane(ctx, paneID)
	if err != nil {
		return "", err
	}
	return compactInteractivePrompt(pane), nil
}

// autoConfirmPrompt checks the tmux pane for interactive prompts and
// automatically confirms them by sending Enter. This is used during session
// startup to skip prompts like the development-channel warning or project trust.
func (c *ClaudeCode) autoConfirmPrompt(paneID string) {
	logger := log.WithFunc("claude.autoConfirmPrompt")
	prompt, err := c.captureInteractivePrompt(c.ctx, paneID)
	if err != nil || prompt == "" {
		return
	}
	logger.Debugf(c.ctx, "auto-confirming prompt: %s", prompt)
	c.sendTmuxKeys(c.ctx, paneID, "Enter") //nolint:errcheck
}

func (c *ClaudeCode) sendTmuxKeys(ctx context.Context, paneID string, keys ...string) error {
	args := append([]string{"send-keys", "-t", paneID}, keys...)
	_, err := c.tmux(ctx, args...)
	return err
}

func (c *ClaudeCode) handleTmuxControl(ctx context.Context, sess *userSession, input string) (*agent.Response, error) {
	prompt := sess.getPendingTmuxPrompt()
	keyset, ok := tmuxControlKeys(prompt, input)
	if !ok {
		return &agent.Response{
			Prompt:     agent.PromptInteractive,
			PromptText: prompt,
			Options:    extractOptions(prompt),
		}, nil
	}
	if err := c.sendTmuxKeys(ctx, sess.paneID, keyset...); err != nil {
		return nil, err
	}
	sess.clearPendingTmuxPrompt()

	return c.waitForReplyWithTimeout(ctx, sess, controlWaitTimeout, false)
}

func (c *ClaudeCode) handlePermissionControl(ctx context.Context, sess *userSession, input string, perm *protocol.Permission) (*agent.Response, error) {
	behavior, ok := permissionBehavior(input)
	if !ok {
		return &agent.Response{
			Prompt:     agent.PromptPermission,
			PromptText: perm.TmuxPrompt,
			Options:    extractOptions(perm.TmuxPrompt),
			Permission: &agent.PermissionInfo{
				ToolName:     perm.ToolName,
				Description:  perm.Description,
				InputPreview: perm.InputPreview,
			},
		}, nil
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

func tmuxControlKeys(prompt, input string) ([]string, bool) {
	cmd := normalizeControl(input)
	lower := strings.ToLower(prompt)
	hasYN := strings.Contains(lower, "y/n")

	switch cmd {
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return []string{cmd, "Enter"}, true
	case "yes", "y", "enter":
		if hasYN {
			return []string{"y", "Enter"}, true
		}
		return []string{"Enter"}, true
	case "no", "n", "cancel":
		if strings.Contains(prompt, "Esc to cancel") {
			return []string{"Escape"}, true
		}
		if hasYN {
			return []string{"n", "Enter"}, true
		}
		return []string{"C-c"}, true
	default:
		return nil, false
	}
}

func permissionBehavior(input string) (string, bool) {
	switch normalizeControl(input) {
	case "yes", "y", "allow", "ok":
		return behaviorAllow, true
	case "no", "n", "deny", "cancel":
		return behaviorDeny, true
	default:
		return "", false
	}
}

func normalizeControl(input string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(input)), "/")
}

// extractOptions parses numbered options ("1. ...", "2. ...") or y/n
// indicators from a prompt string and returns them as a slice.
func extractOptions(prompt string) []string {
	var opts []string
	lower := strings.ToLower(prompt)
	hasYN := strings.Contains(lower, "y/n") || strings.Contains(lower, "[y/n]")

	for _, raw := range strings.Split(prompt, "\n") {
		if n := optionLineNumber(strings.TrimSpace(raw)); n != "" {
			opts = append(opts, n)
		}
	}

	if len(opts) == 0 && hasYN {
		opts = []string{"yes", "no"}
	}
	return opts
}

func compactInteractivePrompt(pane string) string {
	pane = strings.ReplaceAll(pane, "\u00a0", " ")
	lines := strings.Split(pane, "\n")
	filtered := make([]string, 0, len(lines))
	interactive := false

	for _, raw := range lines {
		line, keep, isInteractive := normalizePromptLine(raw)
		if isInteractive {
			interactive = true
		}
		if keep {
			filtered = append(filtered, line)
		}
	}

	if !interactive || len(filtered) == 0 {
		return ""
	}

	start := 0
	if len(filtered) > 8 {
		start = len(filtered) - 8
	}

	var compact []string
	for _, line := range filtered[start:] {
		if len(compact) > 0 && compact[len(compact)-1] == line {
			continue
		}
		compact = append(compact, line)
	}

	return strings.TrimSpace(strings.Join(compact, "\n"))
}

func normalizePromptLine(raw string) (line string, keep bool, interactive bool) {
	line = strings.TrimSpace(strings.TrimRight(raw, "\r"))
	if line == "" {
		return "", false, false
	}

	switch {
	case isSeparatorLine(line):
		return "", false, false
	case strings.Contains(line, "Enter to confirm"),
		strings.Contains(line, "Esc to cancel"),
		strings.Contains(strings.ToLower(line), "[y/n]"),
		strings.Contains(strings.ToLower(line), "y/n"):
		return line, true, true
	case strings.Contains(line, "Claude Code v"),
		strings.Contains(line, "Listening for channel messages from:"),
		strings.Contains(line, "Experimental · inbound messages"),
		strings.HasPrefix(line, "/private/tmp/"),
		strings.HasPrefix(line, "/tmp/"),
		strings.HasPrefix(line, "← "),
		strings.HasPrefix(line, "● "):
		return "", false, false
	case strings.HasPrefix(line, "❯ "):
		trimmed := strings.TrimSpace(strings.TrimPrefix(line, "❯ "))
		if trimmed == "" {
			return "", false, false
		}
		line = trimmed
	}

	line = strings.TrimLeft(line, "⎿│└├┌─> ")
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "/") {
		return "", false, false
	}

	if idx := optionLineNumber(line); idx != "" {
		return line, true, true
	}

	return line, true, false
}

// hasExistingSession checks whether Claude Code has been run in this workdir before.
// Claude stores project state in ~/.claude/projects/ keyed by a hash of the path.
// We check for .claude/ in the workdir as a simpler heuristic — if it doesn't exist,
// this is a fresh session and --continue would fail.
func hasExistingSession(workDir string) bool {
	entries, err := os.ReadDir(filepath.Join(workDir, ".claude"))
	if err != nil {
		return false
	}
	return len(entries) > 0
}

func optionLineNumber(line string) string {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "❯"))
	if line == "" {
		return ""
	}
	dot := strings.IndexByte(line, '.')
	if dot <= 0 {
		return ""
	}
	for _, r := range line[:dot] {
		if r < '0' || r > '9' {
			return ""
		}
	}
	if dot+1 >= len(line) || line[dot+1] != ' ' {
		return ""
	}
	return line[:dot]
}

func isSeparatorLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	return strings.Trim(line, "─-═━") == ""
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
			// Capture the actual tmux terminal prompt which has richer content
			// (numbered options like "1. Yes  2. Yes, allow during session  3. No").
			if pane, captureErr := c.capturePane(c.ctx, sess.paneID); captureErr == nil && pane != "" {
				perm.TmuxPrompt = compactInteractivePrompt(pane)
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
