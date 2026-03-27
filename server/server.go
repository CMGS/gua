package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/projecteru2/core/log"

	"github.com/CMGS/gua/agent"
	"github.com/CMGS/gua/channel"
	"github.com/CMGS/gua/types"
)

const maxConcurrent = 32

type commandHandler func(ctx context.Context, msg channel.InboundMessage) *agent.Response

// Server orchestrates a Channel and an Agent.
type Server struct {
	channel     channel.Channel
	agent       agent.Agent
	sem         chan struct{}
	commands    map[string]commandHandler
	replyTokens sync.Map // userID → latest replyToken
	subscribers sync.Map // userID → bool (whether responseLoop is running)
}

// New creates a Server that bridges the given channel and agent.
func New(ch channel.Channel, a agent.Agent) *Server {
	s := &Server{
		channel: ch,
		agent:   a,
		sem:     make(chan struct{}, maxConcurrent),
	}
	s.commands = map[string]commandHandler{
		"/whosyourdaddy": s.cmdYolo,
		"/imyourdaddy":   s.cmdSafe,
		"/share":         s.cmdShare,
		"/close":         s.cmdClose,
		"/clean":         s.cmdClean,
	}
	return s
}

// Run starts the server. Blocks until ctx is canceled.
func (s *Server) Run(ctx context.Context) error {
	logger := log.WithFunc("server.Run")
	logger.Infof(ctx, "starting server: channel=%s agent=%s", s.channel.Name(), s.agent.Name())

	err := s.channel.Start(ctx, func(ctx context.Context, msg channel.InboundMessage) {
		s.sem <- struct{}{}
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.WithFunc("server.handleInbound").Warnf(ctx, "panic recovered: %v", r)
				}
				<-s.sem
			}()
			s.handleInbound(ctx, msg)
		}()
	})

	_ = s.agent.CloseAll()
	return err
}

func (s *Server) handleInbound(ctx context.Context, msg channel.InboundMessage) {
	if msg.Text == "" && len(msg.MediaFiles) == 0 {
		return
	}

	// Track latest reply token for this user.
	s.replyTokens.Store(msg.SenderID, msg.ReplyToken)

	presenter := s.channel.Presenter()
	trimmed := strings.TrimSpace(msg.Text)

	// 1. Global commands — use msg.ReplyToken directly (commands like /close
	// may destroy the session, racing with async replyToken cleanup).
	if resp := s.dispatchCommand(ctx, msg, trimmed); resp != nil {
		s.sendCommandResponse(ctx, msg, presenter, resp)
		return
	}

	// 2. Control actions and agent CLI commands.
	if len(msg.MediaFiles) == 0 {
		if handled := s.handleAction(ctx, msg, presenter, trimmed); handled {
			return
		}
		// Action-only messages (e.g. inline keyboard callback) should not
		// fall through to the agent as text when the action is stale.
		if msg.ActionOnly {
			return
		}
	}

	// 3. Send message first (creates session if needed), then ensure response loop.
	stopTyping := s.channel.StartTyping(ctx, msg.SenderID, msg.ReplyToken)

	agentMsg := FormatInbound(msg, presenter)
	if err := s.agent.Send(ctx, msg.SenderID, agentMsg); err != nil {
		stopTyping()
		log.WithFunc("server.handleInbound").Warnf(ctx, "send to agent for %s: %v", msg.SenderID, err)
		s.sendText(ctx, msg, presenter.FormatError(err))
		return
	}

	// Subscribe AFTER Send — session now exists.
	s.ensureResponseLoop(ctx, msg.SenderID, stopTyping)
}

// handleAction processes control actions and agent CLI commands.
// Returns true if the message was handled.
func (s *Server) handleAction(ctx context.Context, msg channel.InboundMessage, presenter channel.Presenter, trimmed string) bool {
	action := presenter.ParseAction(trimmed)
	if action == nil {
		return false
	}

	// Passthrough: check agent CLI whitelist.
	if action.Type == types.ActionPassthrough {
		fields := strings.Fields(action.Value)
		if len(fields) == 0 {
			return false
		}
		cmd := fields[0]
		if !s.isAgentCLICommand(cmd) {
			return false // not whitelisted → treat as normal message
		}
		stopTyping := s.channel.StartTyping(ctx, msg.SenderID, msg.ReplyToken)
		if err := s.agent.RawInput(ctx, msg.SenderID, action.Value); err != nil {
			stopTyping()
			s.sendText(ctx, msg, presenter.FormatError(err))
		} else {
			s.ensureResponseLoop(ctx, msg.SenderID, stopTyping)
		}
		return true
	}

	// Control: only consumed when agent has an active prompt.
	handled, err := s.agent.Control(ctx, msg.SenderID, *action)
	if err != nil {
		s.sendText(ctx, msg, presenter.FormatError(err))
		return true
	}
	return handled
}

func (s *Server) dispatchCommand(ctx context.Context, msg channel.InboundMessage, trimmed string) *agent.Response {
	// Exact match (commands without arguments).
	if handler, ok := s.commands[strings.ToLower(trimmed)]; ok {
		return handler(ctx, msg)
	}
	// Prefix match (commands with arguments).
	fields := strings.Fields(trimmed)
	if len(fields) > 0 {
		switch strings.ToLower(fields[0]) {
		case "/respawn":
			return s.cmdRespawn(ctx, msg, fields)
		case "/rename":
			return s.cmdRename(ctx, msg, fields)
		}
	}
	return nil
}

func (s *Server) cmdClose(_ context.Context, msg channel.InboundMessage) *agent.Response {
	if err := s.agent.Close(msg.SenderID); err != nil {
		if errors.Is(err, agent.ErrNoSession) {
			return &agent.Response{Text: "no active session"}
		}
		return &agent.Response{Text: fmt.Sprintf("close failed: %v", err)}
	}
	return &agent.Response{Text: "session closed"}
}

func (s *Server) cmdClean(ctx context.Context, _ channel.InboundMessage) *agent.Response {
	var cleaned int
	for _, sid := range s.agent.ActiveSessions() {
		if err := s.channel.ProbeThread(ctx, sid); errors.Is(err, channel.ErrThreadGone) {
			_ = s.agent.Close(sid)
			cleaned++
		}
	}
	if cleaned == 0 {
		return &agent.Response{Text: "no stale sessions"}
	}
	return &agent.Response{Text: fmt.Sprintf("cleaned %d stale session(s)", cleaned)}
}

func (s *Server) cmdRename(ctx context.Context, msg channel.InboundMessage, fields []string) *agent.Response {
	if len(fields) < 2 {
		return &agent.Response{Text: "usage: /rename <name>"}
	}
	name := strings.Join(fields[1:], " ")

	// Rename agent session (CC's /rename command).
	if err := s.agent.RawInput(ctx, msg.SenderID, "/rename "+name); err != nil {
		return &agent.Response{Text: fmt.Sprintf("rename failed: %v", err)}
	}

	// Rename channel thread/topic (Telegram only, no-op for WeChat).
	s.channel.RenameThread(ctx, msg.ReplyToken, name)

	return &agent.Response{Text: fmt.Sprintf("renamed to %s", name)}
}

func (s *Server) cmdRespawn(ctx context.Context, msg channel.InboundMessage, fields []string) *agent.Response {
	if len(fields) < 2 {
		return &agent.Response{Text: "usage: /respawn <workdir> [--continue | --resume <id>]"}
	}
	s.sendText(ctx, msg, "processing...")
	workDir := fields[1]

	// Parse resume option.
	var resumeOpt string
	args := fields[2:]
	if slices.Contains(args, "--continue") {
		resumeOpt = "continue"
	} else if idx := slices.Index(args, "--resume"); idx >= 0 {
		if idx+1 >= len(args) {
			return &agent.Response{Text: "usage: /respawn <workdir> --resume <session-id>"}
		}
		resumeOpt = args[idx+1]
	}

	changed, err := s.agent.RespawnSession(ctx, msg.SenderID, workDir, resumeOpt)
	if err != nil {
		return &agent.Response{Text: fmt.Sprintf("respawn failed: %v", err)}
	}
	if !changed {
		return &agent.Response{Text: "already in " + workDir}
	}
	return &agent.Response{Text: "session switched to " + workDir}
}

func mapPromptKind(p agent.PromptType) channel.PromptKind {
	switch p {
	case agent.PromptPermission:
		return channel.PromptKindPermission
	case agent.PromptInteractive:
		return channel.PromptKindInteractive
	case agent.PromptElicitation:
		return channel.PromptKindElicitation
	case agent.PromptTUIMenu:
		return channel.PromptKindTUIMenu
	default:
		return channel.PromptKindNone
	}
}

func (s *Server) isAgentCLICommand(cmd string) bool {
	return slices.ContainsFunc(s.agent.CLICommands(), func(c string) bool {
		return strings.EqualFold(cmd, c)
	})
}

func (s *Server) ensureResponseLoop(ctx context.Context, userID string, stopTyping func()) {
	if _, loaded := s.subscribers.LoadOrStore(userID, true); loaded {
		if stopTyping != nil {
			stopTyping()
		}
		return // already running
	}

	ch := s.agent.Subscribe(userID)
	presenter := s.channel.Presenter()

	go func() {
		logger := log.WithFunc("server.responseLoop")
		defer s.subscribers.Delete(userID)
		defer s.replyTokens.Delete(userID)

		typingStopped := false
		for resp := range ch {
			if !typingStopped && stopTyping != nil {
				stopTyping()
				typingStopped = true
			}
			token := s.getReplyToken(userID)
			if token == "" {
				logger.Warnf(ctx, "no reply token for user %s, dropping response", userID)
				continue
			}
			s.sendResponseToUser(ctx, userID, presenter, resp)
		}
		if !typingStopped && stopTyping != nil {
			stopTyping()
		}
	}()
}

func (s *Server) getReplyToken(userID string) string {
	v, ok := s.replyTokens.Load(userID)
	if !ok {
		return ""
	}
	token, _ := v.(string)
	return token
}

func (s *Server) cmdYolo(ctx context.Context, msg channel.InboundMessage) *agent.Response {
	s.sendText(ctx, msg, "processing...")
	restarted, err := s.agent.Restart(ctx, msg.SenderID, map[string]string{"skip-permissions": "true"})
	if err != nil {
		return &agent.Response{Text: fmt.Sprintf("restart failed: %v", err)}
	}
	if !restarted {
		return &agent.Response{Text: "already in yolo mode"}
	}
	return &agent.Response{Text: "yolo mode activated"}
}

func (s *Server) cmdShare(ctx context.Context, msg channel.InboundMessage) *agent.Response {
	path, err := s.channel.ShareQR(ctx)
	if err != nil {
		return &agent.Response{Text: fmt.Sprintf("share failed: %v", err)}
	}
	if path == "" {
		return &agent.Response{Text: "share not supported by this channel"}
	}
	return &agent.Response{Files: []string{path}}
}

func (s *Server) cmdSafe(ctx context.Context, msg channel.InboundMessage) *agent.Response {
	s.sendText(ctx, msg, "processing...")
	restarted, err := s.agent.Restart(ctx, msg.SenderID, nil)
	if err != nil {
		return &agent.Response{Text: fmt.Sprintf("restart failed: %v", err)}
	}
	if !restarted {
		return &agent.Response{Text: "already in safe mode"}
	}
	return &agent.Response{Text: "safe mode restored"}
}

// handleSendError checks if a send error means the thread is gone (deleted topic).
// If so, closes the session. Returns true if the caller should stop sending.
func (s *Server) handleSendError(ctx context.Context, userID string, err error) bool {
	if errors.Is(err, channel.ErrThreadGone) {
		log.WithFunc("server.handleSendError").Infof(ctx, "thread gone for %s, closing session", userID)
		_ = s.agent.Close(userID)
		return true
	}
	log.WithFunc("server.handleSendError").Warnf(ctx, "send to %s: %v", userID, err)
	return false
}

func (s *Server) sendText(ctx context.Context, msg channel.InboundMessage, text string) {
	_ = s.channel.Send(ctx, channel.OutboundMessage{
		RecipientID: msg.SenderID,
		Text:        text,
		ReplyToken:  msg.ReplyToken,
	})
}

// sendCommandResponse sends a command response using the original msg.ReplyToken.
// Unlike sendResponseToUser (which looks up the token from a map), this avoids
// a race where /close deletes the session before the response is sent.
func (s *Server) sendCommandResponse(ctx context.Context, msg channel.InboundMessage, p channel.Presenter, resp *agent.Response) {
	if resp == nil {
		return
	}
	if resp.Text != "" {
		s.sendText(ctx, msg, p.FormatText(resp.Text))
	}
	for _, f := range resp.Files {
		_ = s.channel.Send(ctx, channel.OutboundMessage{
			RecipientID: msg.SenderID,
			FilePath:    f,
			ReplyToken:  msg.ReplyToken,
		})
	}
}

func (s *Server) sendResponseToUser(ctx context.Context, userID string, p channel.Presenter, resp *agent.Response) {
	if resp == nil {
		return
	}

	token := s.getReplyToken(userID)

	if resp.Prompt != agent.PromptNone {
		toolName, description := "", ""
		if resp.Permission != nil {
			toolName = resp.Permission.ToolName
			description = resp.Permission.Description
			if description == "" {
				description = resp.Permission.InputPreview
			}
		}
		kind := mapPromptKind(resp.Prompt)
		text := p.FormatPrompt(kind, resp.PromptText, resp.Options, toolName, description)
		_ = s.channel.Send(ctx, channel.OutboundMessage{
			RecipientID: userID,
			Text:        text,
			ReplyToken:  token,
			PromptKind:  kind,
			Options:     resp.Options,
		})
		return
	}

	cleanText, textFiles := ExtractFiles(p.FormatText(resp.Text))
	files := MergeFiles(textFiles, resp.Files)

	if cleanText != "" {
		if err := s.channel.Send(ctx, channel.OutboundMessage{
			RecipientID: userID,
			Text:        cleanText,
			ReplyToken:  token,
		}); err != nil {
			if s.handleSendError(ctx, userID, err) {
				return
			}
		}
	}

	for _, f := range files {
		if err := s.channel.Send(ctx, channel.OutboundMessage{
			RecipientID: userID,
			FilePath:    f,
			ReplyToken:  token,
		}); err != nil {
			if s.handleSendError(ctx, userID, err) {
				_ = os.Remove(f)
				return
			}
		}
		_ = os.Remove(f)
	}
}
