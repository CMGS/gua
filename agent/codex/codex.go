package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/CMGS/gua/agent"
	"github.com/CMGS/gua/runtime"
	"github.com/CMGS/gua/types"
	"github.com/CMGS/gua/utils"
)

const (
	defaultCodexCmd = "codex"
	defaultPolicy   = "on-request"
	defaultSandbox  = "workspace-write"
	yoloPolicy      = "never"
	yoloSandbox     = "danger-full-access"
)

// Option configures a Codex agent.
type Option func(*Codex)

// Codex implements agent.Agent by spawning codex mcp-server subprocesses.
type Codex struct {
	codexCmd    string
	model       string
	baseWorkDir string
	initPrompt  string // written as CODEX.md to session workdir
	rt          runtime.Runtime
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.RWMutex
	sessions    map[string]*codexSession
	userFlags   map[string]map[string]string
}

// New creates a new Codex agent.
func New(ctx context.Context, opts ...Option) (*Codex, error) {
	ctx, cancel := context.WithCancel(ctx)
	c := &Codex{
		codexCmd:  defaultCodexCmd,
		sessions:  make(map[string]*codexSession),
		userFlags: make(map[string]map[string]string),
		ctx:       ctx,
		cancel:    cancel,
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.baseWorkDir == "" {
		c.baseWorkDir = filepath.Join(os.TempDir(), "gua-codex")
	}
	if c.rt == nil {
		cancel()
		return nil, fmt.Errorf("runtime is required: use WithRuntime option")
	}
	return c, nil
}

func (c *Codex) Name() string          { return defaultCodexCmd }
func (c *Codex) CLICommands() []string { return nil }

func (c *Codex) ActiveSessions() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return slices.Collect(maps.Keys(c.sessions))
}

func (c *Codex) getSession(userID string) (*codexSession, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	sess, ok := c.sessions[userID]
	return sess, ok
}

// Send sends a message to the user's Codex session.
func (c *Codex) Send(ctx context.Context, userID string, msg agent.Message) error {
	sess, err := c.getOrCreateSession(ctx, userID)
	if err != nil {
		return err
	}

	// If there's a pending elicitation, re-push it instead of sending to codex.
	if e, ok := sess.elicitQueue.Peek(); ok {
		sess.pushResponse(elicitationResponse(&e))
		return nil
	}

	go c.callTool(sess, msg)
	return nil
}

// callTool serializes tool calls per session to prevent threadID races.
func (c *Codex) callTool(sess *codexSession, msg agent.Message) {
	sess.callMu.Lock()
	defer sess.callMu.Unlock()

	logger := log.WithFunc("codex.callTool")

	resp, err := c.doToolCall(sess, msg)
	if err != nil && sess.threadID != "" {
		// codex-reply failed (e.g. stale threadId after server restart) — fallback to new thread.
		logger.Warnf(c.ctx, "codex-reply failed for user=%s, retrying as new thread: %v", sess.userID, err)
		sess.threadID = ""
		resp, err = c.doToolCall(sess, msg)
	}
	if err != nil {
		logger.Warnf(c.ctx, "codex failed for user=%s: %v", sess.userID, err)
		sess.pushResponse(&agent.Response{Text: fmt.Sprintf("codex error: %v", err)})
		return
	}
	c.handleToolResult(sess, resp)
}

func (c *Codex) doToolCall(sess *codexSession, msg agent.Message) (*jsonrpcMessage, error) {
	name := "codex"
	args := map[string]any{
		"prompt":          msg.Text,
		"cwd":             sess.workDir,
		"approval-policy": sess.approvalPolicy,
		"sandbox":         sess.sandbox,
	}
	if c.model != "" {
		args["model"] = c.model
	}
	if sess.threadID != "" {
		name = "codex-reply"
		args = map[string]any{
			"threadId": sess.threadID,
			"prompt":   msg.Text,
		}
	}
	return sess.rpc.call("tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
}

// Control handles user actions (confirm/deny for approvals).
func (c *Codex) Control(_ context.Context, userID string, action types.Action) (bool, error) {
	sess, ok := c.getSession(userID)
	if !ok || sess.elicitQueue.Len() == 0 {
		return false, nil
	}

	if action.Type != types.ActionConfirm && action.Type != types.ActionDeny {
		if e, peeked := sess.elicitQueue.Peek(); peeked {
			sess.pushResponse(elicitationResponse(&e))
		}
		return true, nil
	}

	front, ok := sess.elicitQueue.Pop()
	if !ok {
		return true, nil
	}

	decision := "approved"
	if action.Type == types.ActionDeny {
		decision = "denied"
	}
	front.replyCh <- decision

	if decision == "denied" {
		sess.pushResponse(&agent.Response{Text: "denied"})
	}

	if next, ok := sess.elicitQueue.Peek(); ok {
		sess.pushResponse(elicitationResponse(&next))
	}
	return true, nil
}

// Subscribe returns the response channel for a user.
func (c *Codex) Subscribe(userID string) <-chan *agent.Response {
	if sess, ok := c.getSession(userID); ok {
		return sess.outCh
	}
	ch := make(chan *agent.Response)
	close(ch)
	return ch
}

// RawInput is not supported by Codex (no terminal passthrough).
func (c *Codex) RawInput(_ context.Context, _ string, _ string) error {
	return fmt.Errorf("codex does not support raw terminal input")
}

// RespawnSession switches the user's session to a different working directory.
// resumeOpt is ignored — codex threadId is process-local and cannot survive respawn.
func (c *Codex) RespawnSession(_ context.Context, userID, workDir, _ string) (bool, error) {
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

	if sess, ok := c.getSession(userID); ok {
		if sess.workDir == absWorkDir {
			return false, nil
		}
		_ = c.Close(userID)
	}

	c.mu.Lock()
	if c.userFlags[userID] == nil {
		c.userFlags[userID] = make(map[string]string)
	}
	c.userFlags[userID]["workdir"] = absWorkDir
	c.mu.Unlock()

	return true, nil
}

// resolvePolicy returns the approval policy and sandbox mode for the given flags.
func resolvePolicy(flags map[string]string) (string, string) {
	if flags != nil && flags["skip-permissions"] == "true" {
		return yoloPolicy, yoloSandbox
	}
	return defaultPolicy, defaultSandbox
}

// Restart updates the session's approval policy without killing the process.
// approval-policy and sandbox are per-call parameters, so no process restart needed.
func (c *Codex) Restart(_ context.Context, userID string, flags map[string]string) (bool, error) {
	policy, sandbox := resolvePolicy(flags)

	// Update live session if it exists (no restart needed).
	if sess, ok := c.getSession(userID); ok {
		sess.callMu.Lock()
		changed := sess.approvalPolicy != policy
		if changed {
			sess.approvalPolicy = policy
			sess.sandbox = sandbox
		}
		sess.callMu.Unlock()
		return changed, nil
	}

	// No active session — store flags for next session creation.
	c.mu.Lock()
	if c.userFlags[userID] == nil {
		c.userFlags[userID] = make(map[string]string)
	}
	if policy == yoloPolicy {
		c.userFlags[userID]["skip-permissions"] = "true"
	} else {
		delete(c.userFlags[userID], "skip-permissions")
	}
	c.mu.Unlock()
	return true, nil
}

// Close terminates a user's session without clearing userFlags or threadId file.
func (c *Codex) Close(userID string) error {
	c.mu.Lock()
	sess, ok := c.sessions[userID]
	if ok {
		delete(c.sessions, userID)
	}
	c.mu.Unlock()

	if !ok {
		return agent.ErrNoSession
	}

	c.teardownSession(c.ctx, sess)
	return nil
}

// CloseAll terminates all sessions.
func (c *Codex) CloseAll() error {
	c.cancel()

	c.mu.Lock()
	sessions := c.sessions
	c.sessions = make(map[string]*codexSession)
	c.userFlags = make(map[string]map[string]string)
	c.mu.Unlock()

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cleanupCancel()

	for _, sess := range sessions {
		c.teardownSession(cleanupCtx, sess)
	}
	return nil
}

func (c *Codex) teardownSession(ctx context.Context, sess *codexSession) {
	if sess.rpc != nil {
		sess.rpc.cancelAll()
	}
	sess.close()
	if sess.proc != nil {
		_ = c.rt.Kill(ctx, sess.proc)
	}
}

func (c *Codex) getOrCreateSession(ctx context.Context, userID string) (*codexSession, error) {
	if sess, ok := c.getSession(userID); ok {
		return sess, nil
	}

	logger := log.WithFunc("codex.createSession")

	normalized := utils.NormalizeID(userID)
	workDir := c.resolveWorkDir(userID, normalized)
	if err := os.MkdirAll(workDir, 0o755); err != nil { //nolint:gosec
		return nil, fmt.Errorf("create workdir: %w", err)
	}

	if c.initPrompt != "" {
		if err := os.WriteFile(filepath.Join(workDir, "CODEX.md"), []byte(c.initPrompt), 0o644); err != nil { //nolint:gosec
			return nil, fmt.Errorf("write CODEX.md: %w", err)
		}
	}

	c.mu.RLock()
	policy, sandbox := resolvePolicy(c.userFlags[userID])
	c.mu.RUnlock()

	sessCtx, cancel := context.WithCancel(ctx)

	// Create FIFOs for JSON-RPC communication.
	fifoID := utils.ShortID()
	fifoInPath := filepath.Join(os.TempDir(), fmt.Sprintf("gua-codex-%s-in.fifo", fifoID))
	fifoOutPath := filepath.Join(os.TempDir(), fmt.Sprintf("gua-codex-%s-out.fifo", fifoID))
	for _, p := range []string{fifoInPath, fifoOutPath} {
		if err := syscall.Mkfifo(p, 0o600); err != nil {
			cancel()
			return nil, fmt.Errorf("mkfifo %s: %w", p, err)
		}
	}

	// Build command: codex mcp-server with stdin/stdout redirected through FIFOs.
	// tee /dev/stderr mirrors JSON-RPC traffic to the tmux pane for visual inspection.
	command := fmt.Sprintf("%s mcp-server < %s | tee /dev/stderr > %s",
		runtime.ShellQuote(c.codexCmd),
		runtime.ShellQuote(fifoInPath),
		runtime.ShellQuote(fifoOutPath))

	removeFIFOs := func() {
		_ = os.Remove(fifoInPath)
		_ = os.Remove(fifoOutPath)
	}

	proc, err := c.rt.StartProcess(sessCtx, "codex-"+normalized, workDir, command)
	if err != nil {
		cancel()
		removeFIFOs()
		return nil, fmt.Errorf("start codex pane: %w", err)
	}

	// Open FIFOs from gua side.
	// Order matters: open fifo_in (write) first to unblock shell's stdin redirect,
	// then fifo_out (read) to unblock shell's stdout redirect.
	fifoIn, err := os.OpenFile(fifoInPath, os.O_WRONLY, 0) //nolint:gosec
	if err != nil {
		cancel()
		_ = c.rt.Kill(c.ctx, proc)
		removeFIFOs()
		return nil, fmt.Errorf("open fifo_in: %w", err)
	}
	fifoOut, err := os.OpenFile(fifoOutPath, os.O_RDONLY, 0) //nolint:gosec
	if err != nil {
		cancel()
		_ = fifoIn.Close()
		_ = c.rt.Kill(c.ctx, proc)
		removeFIFOs()
		return nil, fmt.Errorf("open fifo_out: %w", err)
	}

	rpc := newRPCClient(fifoIn)
	sess := &codexSession{
		userID:         userID,
		workDir:        workDir,
		proc:           proc,
		rpc:            rpc,
		fifoIn:         fifoIn,
		fifoOut:        fifoOut,
		outCh:          make(chan *agent.Response, agent.ResponseBufSize),
		cancel:         cancel,
		approvalPolicy: policy,
		sandbox:        sandbox,
	}

	abort := func() {
		c.teardownSession(c.ctx, sess)
		removeFIFOs()
	}

	rpc.onRequest = func(msg *jsonrpcMessage) { c.handleServerRequest(sess, msg) }
	rpc.onNotification = func(msg *jsonrpcMessage) { c.handleNotification(sess, msg) }

	go rpc.readLoop(bufio.NewReader(fifoOut))

	// MCP initialize handshake.
	if err := c.initialize(sess); err != nil {
		abort()
		return nil, fmt.Errorf("MCP initialize: %w", err)
	}

	c.mu.Lock()
	if existing, ok := c.sessions[userID]; ok {
		c.mu.Unlock()
		abort()
		return existing, nil
	}
	c.sessions[userID] = sess
	c.mu.Unlock()

	logger.Infof(c.ctx, "spawned codex pane=%s for user=%s workdir=%s policy=%s", proc.PaneID, userID, workDir, policy)
	return sess, nil
}

func (c *Codex) resolveWorkDir(userID, normalized string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if flags, ok := c.userFlags[userID]; ok && flags["workdir"] != "" {
		return flags["workdir"]
	}
	return filepath.Join(c.baseWorkDir, "sessions", normalized)
}

func (c *Codex) initialize(sess *codexSession) error {
	resp, err := sess.rpc.call("initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "gua",
			"version": "1.0.0",
		},
	})
	if err != nil {
		return fmt.Errorf("initialize call: %w", err)
	}
	if resp.Error != nil {
		return resp.Error
	}
	return sess.rpc.notify("notifications/initialized", nil)
}

// handleToolResult extracts threadId and content from a tools/call response.
func (c *Codex) handleToolResult(sess *codexSession, resp *jsonrpcMessage) {
	if resp == nil || resp.Result == nil {
		return
	}

	var toolResult struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent struct {
			ThreadID string `json:"threadId"`
			Content  string `json:"content"`
		} `json:"structuredContent"`
	}
	if json.Unmarshal(resp.Result, &toolResult) != nil {
		return
	}

	// Extract threadId from structuredContent.
	if toolResult.StructuredContent.ThreadID != "" {
		sess.threadID = toolResult.StructuredContent.ThreadID
	}

	// Push response text (prefer structuredContent, fall back to content[0]).
	if text := toolResult.StructuredContent.Content; text != "" {
		sess.pushResponse(&agent.Response{Text: text})
		return
	}
	for _, item := range toolResult.Content {
		if item.Type == "text" && item.Text != "" {
			sess.pushResponse(&agent.Response{Text: item.Text})
			return
		}
	}
}

// handleServerRequest processes elicitation/create requests from codex.
func (c *Codex) handleServerRequest(sess *codexSession, msg *jsonrpcMessage) {
	if msg.Method != "elicitation/create" || msg.ID == nil {
		return
	}

	var params struct {
		Message          string   `json:"message"`
		CodexElicitation string   `json:"codex_elicitation"`
		CodexCommand     []string `json:"codex_command"`
		CodexCwd         string   `json:"codex_cwd"`
	}
	if json.Unmarshal(msg.Params, &params) != nil {
		_ = sess.rpc.respond(*msg.ID, map[string]string{"decision": "denied"})
		return
	}

	displayMsg := params.Message
	if params.CodexElicitation == "exec-approval" && len(params.CodexCommand) > 0 {
		displayMsg = fmt.Sprintf("Execute: %s\nCwd: %s", strings.Join(params.CodexCommand, " "), params.CodexCwd)
	}

	replyCh := make(chan string, 1)
	pending := pendingElicitation{
		requestID: *msg.ID,
		kind:      params.CodexElicitation,
		message:   displayMsg,
		replyCh:   replyCh,
	}

	sess.elicitQueue.Push(pending)
	if sess.elicitQueue.Len() == 1 {
		sess.pushResponse(elicitationResponse(&pending))
	}

	go func() {
		decision := <-replyCh
		_ = sess.rpc.respond(*msg.ID, map[string]string{"decision": decision})
	}()
}

// handleNotification processes codex/event notifications.
// Events are nested under params.msg (e.g. {"params":{"msg":{"type":"exec_output",...}}}).
func (c *Codex) handleNotification(sess *codexSession, msg *jsonrpcMessage) {
	if msg.Method != "codex/event" {
		return
	}

	var params struct {
		Msg struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Output  string `json:"output"`
		} `json:"msg"`
	}
	if json.Unmarshal(msg.Params, &params) != nil {
		return
	}

	switch params.Msg.Type {
	case "exec_output":
		if params.Msg.Output != "" {
			sess.pushResponse(&agent.Response{Text: params.Msg.Output})
		}
	case "patch_applied":
		if params.Msg.Message != "" {
			sess.pushResponse(&agent.Response{Text: params.Msg.Message})
		}
	}
}

func elicitationResponse(e *pendingElicitation) *agent.Response {
	var toolName string
	switch e.kind {
	case "exec-approval":
		toolName = "exec"
	case "patch-approval":
		toolName = "patch"
	default:
		toolName = "codex"
	}
	return &agent.Response{
		Prompt: agent.PromptPermission,
		Permission: &agent.PermissionInfo{
			ToolName:    toolName,
			Description: e.message,
		},
	}
}
