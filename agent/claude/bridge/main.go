package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/CMGS/gua/agent/claude/mcpserver"
	"github.com/CMGS/gua/agent/claude/protocol"
)

const defaultInstructions = `Messages arrive as <channel source="gua" sender="..." sender_id="...">.
Media files are downloaded locally; paths appear as [图片: /path] or [文件: /path] in the content.
Reply with the gua_reply tool, passing sender_id from the tag.
For file responses, set file_path to the absolute path of a real local file, never a directory.
Respond in the same language as the user.`

var guaReplySchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"sender_id": map[string]any{
			"type":        "string",
			"description": "sender_id from the channel event tag",
		},
		"text": map[string]any{
			"type":        "string",
			"description": "plain text reply to the user",
		},
		"file_path": map[string]any{
			"type":        "string",
			"description": "optional absolute path to a regular local file to send as attachment; must not be a directory",
		},
	},
	"required": []string{"sender_id", "text"},
}

func main() {
	os.Exit(func() int {
		logger := log.WithFunc("bridge.main")

		socketPath := flag.String("socket", "", "path to dispatcher Unix socket")
		userID := flag.String("user", "", "user ID for this bridge session")
		instrText := flag.String("instructions", "", "MCP channel instructions (overrides default)")
		hookMode := flag.String("hook", "", "hook mode: permission or elicitation (short-lived, reads stdin)")
		flag.Parse()

		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer cancel()

		if *socketPath == "" || *userID == "" {
			logger.Errorf(ctx, nil, "usage: bridge --socket /path/to/socket --user userID [--hook permission|elicitation]")
			return 1
		}

		// Hook mode: read CC hook JSON from stdin, forward to dispatcher, return decision.
		if *hookMode != "" {
			if err := runHook(ctx, *socketPath, *userID, *hookMode); err != nil {
				logger.Errorf(ctx, err, "hook exited")
				return 1
			}
			return 0
		}

		instr := defaultInstructions
		if *instrText != "" {
			instr = *instrText
		}

		if err := run(ctx, *socketPath, *userID, instr); err != nil {
			logger.Errorf(ctx, err, "bridge exited")
			return 1
		}
		return 0
	}())
}

func run(ctx context.Context, socketPath, userID, instructions string) error {
	logger := log.WithFunc("bridge.run")

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Connect to dispatcher Unix socket.
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("connect to dispatcher: %w", err)
	}
	defer conn.Close() //nolint:errcheck

	reader := bufio.NewReader(conn)

	// Register is delayed until CC sends notifications/initialized + a brief
	// settling time for CC to finish processing tools/list internally.
	var registerOnce sync.Once

	// Create MCP server.
	srv := mcpserver.New("gua", "1.0.0",
		mcpserver.WithInstructions(instructions),
		mcpserver.WithTools([]mcpserver.Tool{
			{
				Name:        "gua_reply",
				Description: "Send a reply to the user. Pass sender_id from the channel event.",
				InputSchema: guaReplySchema,
			},
		}),
		mcpserver.WithToolHandler(func(name string, args json.RawMessage) (any, error) {
			return handleToolCall(conn, name, args)
		}),
		mcpserver.WithNotificationHandler(func(method string, params json.RawMessage) {
			if method == "notifications/initialized" {
				registerOnce.Do(func() {
					go func() {
						time.Sleep(500 * time.Millisecond)
						if writeErr := protocol.WriteEnvelope(conn, protocol.TypeRegister, protocol.Register{UserID: userID}); writeErr != nil {
							logger.Warnf(ctx, "send register: %v", writeErr)
						}
					}()
				})
				return
			}
			handleNotification(ctx, conn, method, params)
		}),
	)

	errCh := make(chan error, 2)

	// MCP server goroutine: reads stdin, writes stdout.
	go func() {
		errCh <- srv.Run(ctx)
	}()

	// Socket reader goroutine: reads dispatcher envelopes, sends MCP notifications.
	go func() {
		errCh <- readDispatcher(ctx, reader, srv)
	}()

	// Wait for the first goroutine to finish.
	err = <-errCh
	cancel()
	if err != nil {
		logger.Infof(ctx, "bridge exiting: %v", err)
	}
	return err
}

func handleToolCall(conn net.Conn, name string, args json.RawMessage) (any, error) {
	if name != "gua_reply" {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}

	var tc protocol.ToolCall
	if err := json.Unmarshal(args, &tc); err != nil {
		return nil, fmt.Errorf("parse gua_reply args: %w", err)
	}

	if err := protocol.WriteEnvelope(conn, protocol.TypeToolCall, tc); err != nil {
		return nil, fmt.Errorf("send tool call: %w", err)
	}
	return "sent", nil
}

func handleNotification(ctx context.Context, conn net.Conn, method string, params json.RawMessage) {
	logger := log.WithFunc("bridge.handleNotification")

	if method != "notifications/claude/channel/permission_request" {
		logger.Debugf(ctx, "unhandled notification: %s", method)
		return
	}

	var perm protocol.Permission
	if err := json.Unmarshal(params, &perm); err != nil {
		logger.Warnf(ctx, "parse permission notification: %v", err)
		return
	}

	if err := protocol.WriteEnvelope(conn, protocol.TypePermissionRequest, perm); err != nil {
		logger.Warnf(ctx, "send permission request: %v", err)
	}
}

// runHook handles short-lived hook mode: reads CC hook JSON from stdin,
// forwards to dispatcher, waits for user decision, outputs CC hook JSON.
func runHook(ctx context.Context, socketPath, userID, hookType string) error {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("connect to dispatcher: %w", err)
	}
	defer conn.Close() //nolint:errcheck

	reader := bufio.NewReader(conn)

	switch hookType {
	case "permission":
		return runPermissionHook(conn, reader, userID, input)
	case "elicitation":
		return runElicitationHook(conn, reader, userID, input)
	default:
		return fmt.Errorf("unknown hook type: %s", hookType)
	}
}

func runPermissionHook(conn net.Conn, reader *bufio.Reader, userID string, input []byte) error {
	var hookInput struct {
		ToolName  string          `json:"tool_name"`
		ToolInput json.RawMessage `json:"tool_input"`
	}
	if err := json.Unmarshal(input, &hookInput); err != nil {
		return fmt.Errorf("parse hook input: %w", err)
	}

	inputPreview := string(hookInput.ToolInput)
	if len(inputPreview) > 200 {
		inputPreview = inputPreview[:200] + "..."
	}

	hp := protocol.HookPermission{
		UserID: userID,
		Permission: protocol.Permission{
			ToolName:     hookInput.ToolName,
			InputPreview: inputPreview,
		},
	}
	if err := protocol.WriteEnvelope(conn, protocol.TypeHookPermission, hp); err != nil {
		return fmt.Errorf("send hook permission: %w", err)
	}

	env, err := protocol.ReadEnvelope(reader)
	if err != nil {
		return fmt.Errorf("read permission reply: %w", err)
	}
	reply, err := protocol.DecodePayload[protocol.Permission](env)
	if err != nil {
		return fmt.Errorf("decode permission reply: %w", err)
	}

	output := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName": "PermissionRequest",
			"decision": map[string]any{
				"behavior": reply.Behavior,
			},
		},
	}
	return json.NewEncoder(os.Stdout).Encode(output)
}

func runElicitationHook(conn net.Conn, reader *bufio.Reader, userID string, input []byte) error {
	var hookInput struct {
		MCPServerName string          `json:"mcp_server_name"`
		Message       string          `json:"message"`
		ElicitationID string          `json:"elicitation_id"`
		Schema        json.RawMessage `json:"requested_schema"`
	}
	if err := json.Unmarshal(input, &hookInput); err != nil {
		return fmt.Errorf("parse hook input: %w", err)
	}

	he := protocol.HookElicitation{
		UserID: userID,
		Elicitation: protocol.Elicitation{
			ElicitationID: hookInput.ElicitationID,
			ServerName:    hookInput.MCPServerName,
			Message:       hookInput.Message,
			Schema:        hookInput.Schema,
		},
	}
	if err := protocol.WriteEnvelope(conn, protocol.TypeHookElicitation, he); err != nil {
		return fmt.Errorf("send hook elicitation: %w", err)
	}

	env, err := protocol.ReadEnvelope(reader)
	if err != nil {
		return fmt.Errorf("read elicitation reply: %w", err)
	}
	reply, err := protocol.DecodePayload[protocol.ElicitationReply](env)
	if err != nil {
		return fmt.Errorf("decode elicitation reply: %w", err)
	}

	output := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName": "Elicitation",
			"action":        reply.Action,
		},
	}
	if reply.Content != nil {
		output["hookSpecificOutput"].(map[string]any)["content"] = reply.Content
	}
	return json.NewEncoder(os.Stdout).Encode(output)
}

func readDispatcher(ctx context.Context, r *bufio.Reader, srv *mcpserver.Server) error {
	logger := log.WithFunc("bridge.readDispatcher")

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		env, err := protocol.ReadEnvelope(r)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("read envelope: %w", err)
		}

		switch env.Type {
		case protocol.TypeChannelEvent:
			evt, err := protocol.DecodePayload[protocol.ChannelEvent](env)
			if err != nil {
				logger.Warnf(ctx, "decode channel event: %v", err)
				continue
			}
			if err := srv.SendNotification("notifications/claude/channel", evt); err != nil {
				logger.Warnf(ctx, "send channel notification: %v", err)
			}

		case protocol.TypePermissionReply:
			perm, err := protocol.DecodePayload[protocol.Permission](env)
			if err != nil {
				logger.Warnf(ctx, "decode permission reply: %v", err)
				continue
			}
			if err := srv.SendNotification("notifications/claude/channel/permission", perm); err != nil {
				logger.Warnf(ctx, "send permission notification: %v", err)
			}

		default:
			logger.Debugf(ctx, "unknown envelope type: %s", env.Type)
		}
	}
}
