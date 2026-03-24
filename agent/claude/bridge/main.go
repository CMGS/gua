package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

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
		flag.Parse()

		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer cancel()

		if *socketPath == "" || *userID == "" {
			logger.Errorf(ctx, nil, "usage: bridge --socket /path/to/socket --user userID")
			return 1
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

	// Send Register envelope.
	if writeErr := protocol.WriteEnvelope(conn, protocol.TypeRegister, protocol.Register{UserID: userID}); writeErr != nil {
		return fmt.Errorf("send register: %w", writeErr)
	}

	reader := bufio.NewReader(conn)

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
