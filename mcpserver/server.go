package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/projecteru2/core/log"
)

const (
	protocolVersion            = "2024-11-05"
	headerPrefix               = "Content-Length: "
	maxContentLength           = 10 * 1024 * 1024 // 10MB
	frameModeUnknown frameMode = iota
	frameModeLine
	frameModeContentLength
)

type frameMode int

// Tool describes an MCP tool exposed to Claude Code.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

// ToolHandler is called when Claude invokes a tool.
type ToolHandler func(name string, args json.RawMessage) (any, error)

// NotificationHandler is called for incoming notifications (like permission_request).
type NotificationHandler func(method string, params json.RawMessage)

// Option configures a Server.
type Option func(*Server)

// Server is a lightweight MCP JSON-RPC 2.0 server over stdio.
type Server struct {
	name          string
	version       string
	instructions  string
	tools         []Tool
	toolHandler   ToolHandler
	notifyHandler NotificationHandler

	mu        sync.Mutex
	writer    *bufio.Writer
	frameMode frameMode
}

// New creates a new MCP server with the given name and version.
func New(name, version string, opts ...Option) *Server {
	s := &Server{
		name:    name,
		version: version,
		writer:  bufio.NewWriter(os.Stdout),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// WithInstructions sets the instructions returned during initialize.
func WithInstructions(instructions string) Option {
	return func(s *Server) { s.instructions = instructions }
}

// WithTools registers the tools the server exposes.
func WithTools(tools []Tool) Option {
	return func(s *Server) { s.tools = tools }
}

// WithToolHandler sets the handler called when Claude invokes a tool.
func WithToolHandler(h ToolHandler) Option {
	return func(s *Server) { s.toolHandler = h }
}

// WithNotificationHandler sets the handler called for incoming notifications.
func WithNotificationHandler(h NotificationHandler) Option {
	return func(s *Server) { s.notifyHandler = h }
}

// Run starts the stdio read loop. Blocks until ctx is cancelled or stdin closes.
func (s *Server) Run(ctx context.Context) error {
	logger := log.WithFunc("mcpserver.Run")
	reader := bufio.NewReader(os.Stdin)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		data, mode, err := readFrame(reader)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("read frame: %w", err)
		}
		s.setFrameMode(mode)

		var req jsonrpcRequest
		if err := json.Unmarshal(data, &req); err != nil {
			logger.Warnf(ctx, "invalid JSON-RPC message: %v", err)
			continue
		}

		// Notifications have no ID — dispatch and skip.
		if req.ID == nil {
			s.handleNotification(ctx, req)
			continue
		}

		// Request — must send a response.
		s.handleRequest(ctx, req)
	}
}

// SendNotification sends an MCP notification to Claude Code via stdout.
func (s *Server) SendNotification(method string, params any) error {
	msg := jsonrpcMessage{
		JSONRPC: "2.0",
		Method:  method,
	}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal notification params: %w", err)
		}
		msg.Params = raw
	}
	return s.writeMessage(msg)
}

type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

type jsonrpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) handleRequest(ctx context.Context, req jsonrpcRequest) {
	logger := log.WithFunc("mcpserver.handleRequest")

	var result any
	var rpcErr any

	switch req.Method {
	case "initialize":
		result = s.handleInitialize()
	case "ping":
		result = map[string]any{}
	case "tools/list":
		result = s.handleToolsList()
	case "resources/list":
		result = map[string]any{"resources": []any{}}
	case "resources/templates/list":
		result = map[string]any{"resourceTemplates": []any{}}
	case "prompts/list":
		result = map[string]any{"prompts": []any{}}
	case "tools/call":
		r, err := s.handleToolsCall(ctx, req.Params)
		if err != nil {
			logger.Warnf(ctx, "tools/call error: %v", err)
			rpcErr = jsonrpcError{Code: -32603, Message: err.Error()}
		} else {
			result = r
		}
	default:
		logger.Debugf(ctx, "unknown method: %s", req.Method)
		rpcErr = jsonrpcError{Code: -32601, Message: "method not found: " + req.Method}
	}

	resp := jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
		Error:   rpcErr,
	}
	if err := s.writeMessage(resp); err != nil {
		logger.Warnf(ctx, "write response: %v", err)
	}
}

func (s *Server) handleInitialize() map[string]any {
	resp := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			"experimental": map[string]any{
				"claude/channel":            map[string]any{},
				"claude/channel/permission": map[string]any{},
			},
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    s.name,
			"version": s.version,
		},
	}
	if s.instructions != "" {
		resp["instructions"] = s.instructions
	}
	return resp
}

func (s *Server) handleToolsList() map[string]any {
	return map[string]any{"tools": s.tools}
}

func (s *Server) handleToolsCall(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse tools/call params: %w", err)
	}

	if s.toolHandler == nil {
		return nil, fmt.Errorf("no tool handler registered")
	}

	result, err := s.toolHandler(p.Name, p.Arguments)
	if err != nil {
		return nil, err
	}

	text, ok := result.(string)
	if !ok {
		raw, _ := json.Marshal(result)
		text = string(raw)
	}

	return map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
	}, nil
}

func (s *Server) handleNotification(ctx context.Context, req jsonrpcRequest) {
	logger := log.WithFunc("mcpserver.handleNotification")

	switch req.Method {
	case "notifications/initialized":
	case "notifications/cancelled":
		// no-op
	default:
		if s.notifyHandler != nil {
			s.notifyHandler(req.Method, req.Params)
		} else {
			logger.Debugf(ctx, "unhandled notification: %s", req.Method)
		}
	}
}

func readFrame(r *bufio.Reader) ([]byte, frameMode, error) {
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, frameModeUnknown, err
		}

		line = strings.TrimRight(line, "\r\n")
		if strings.TrimSpace(line) == "" {
			continue
		}

		if !strings.HasPrefix(line, headerPrefix) {
			return []byte(line), frameModeLine, nil
		}

		length, err := strconv.Atoi(strings.TrimPrefix(line, headerPrefix))
		if err != nil {
			return nil, frameModeUnknown, fmt.Errorf("invalid Content-Length: %w", err)
		}
		if length <= 0 || length > maxContentLength {
			return nil, frameModeUnknown, fmt.Errorf("invalid Content-Length: %d", length)
		}

		for {
			headerLine, err := r.ReadString('\n')
			if err != nil {
				return nil, frameModeUnknown, fmt.Errorf("read headers: %w", err)
			}
			if strings.TrimSpace(headerLine) == "" {
				break
			}
		}

		body := make([]byte, length)
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, frameModeUnknown, fmt.Errorf("read body (%d bytes): %w", length, err)
		}
		return body, frameModeContentLength, nil
	}
}

// writeMessage writes a Content-Length framed JSON-RPC message to stdout.
func (s *Server) writeMessage(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch s.frameMode {
	case frameModeContentLength:
		header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
		if _, err := s.writer.WriteString(header); err != nil {
			return err
		}
		if _, err := s.writer.Write(data); err != nil {
			return err
		}
	default:
		if _, err := s.writer.Write(data); err != nil {
			return err
		}
		if err := s.writer.WriteByte('\n'); err != nil {
			return err
		}
	}
	return s.writer.Flush()
}

func (s *Server) setFrameMode(mode frameMode) {
	if mode == frameModeUnknown {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.frameMode == frameModeUnknown {
		s.frameMode = mode
	}
}
