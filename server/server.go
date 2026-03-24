package server

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/projecteru2/core/log"

	"github.com/CMGS/gua/agent"
	"github.com/CMGS/gua/channel"
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

	// 1. Global commands.
	if handler, ok := s.commands[strings.ToLower(trimmed)]; ok {
		s.sendText(ctx, msg, "processing...")
		resp := handler(ctx, msg)
		s.sendResponseToUser(ctx, msg.SenderID, presenter, resp)
		return
	}

	// 2. Control actions.
	if len(msg.MediaFiles) == 0 {
		if action := presenter.ParseAction(trimmed); action != nil {
			if err := s.agent.Control(ctx, msg.SenderID, *action); err != nil {
				s.sendText(ctx, msg, presenter.FormatError(err))
			}
			return
		}
	}

	// 3. Send message first (creates session if needed), then ensure response loop.
	stopTyping := s.channel.StartTyping(ctx, msg.SenderID, msg.ReplyToken)

	agentMsg := FormatInbound(msg, presenter)
	if err := s.agent.Send(ctx, msg.SenderID, agentMsg); err != nil {
		stopTyping()
		logger := log.WithFunc("server.handleInbound")
		logger.Warnf(ctx, "send to agent for %s: %v", msg.SenderID, err)
		s.sendText(ctx, msg, presenter.FormatError(err))
		return
	}

	// Subscribe AFTER Send — session now exists.
	s.ensureResponseLoop(ctx, msg.SenderID, stopTyping)
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
	return v.(string)
}

func (s *Server) cmdYolo(ctx context.Context, msg channel.InboundMessage) *agent.Response {
	restarted, err := s.agent.Restart(ctx, msg.SenderID, map[string]string{"skip-permissions": "true"})
	if err != nil {
		return &agent.Response{Text: fmt.Sprintf("restart failed: %v", err)}
	}
	if !restarted {
		return &agent.Response{Text: "already in yolo mode"}
	}
	return &agent.Response{Text: "yolo mode activated"}
}

func (s *Server) cmdSafe(ctx context.Context, msg channel.InboundMessage) *agent.Response {
	restarted, err := s.agent.Restart(ctx, msg.SenderID, nil)
	if err != nil {
		return &agent.Response{Text: fmt.Sprintf("restart failed: %v", err)}
	}
	if !restarted {
		return &agent.Response{Text: "already in safe mode"}
	}
	return &agent.Response{Text: "safe mode restored"}
}

func (s *Server) sendText(ctx context.Context, msg channel.InboundMessage, text string) {
	_ = s.channel.Send(ctx, channel.OutboundMessage{
		RecipientID: msg.SenderID,
		Text:        text,
		ReplyToken:  msg.ReplyToken,
	})
}

func (s *Server) sendResponseToUser(ctx context.Context, userID string, p channel.Presenter, resp *agent.Response) {
	logger := log.WithFunc("server.sendResponse")

	if resp == nil {
		return
	}

	token := s.getReplyToken(userID)

	if resp.Prompt != agent.PromptNone {
		toolName, description := "", ""
		if resp.Permission != nil {
			toolName = resp.Permission.ToolName
			description = resp.Permission.Description
		}
		text := p.FormatPrompt(resp.PromptText, resp.Options, toolName, description)
		_ = s.channel.Send(ctx, channel.OutboundMessage{
			RecipientID: userID,
			Text:        text,
			ReplyToken:  token,
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
			logger.Warnf(ctx, "send text to %s: %v", userID, err)
		}
	}

	for _, f := range files {
		if err := s.channel.Send(ctx, channel.OutboundMessage{
			RecipientID: userID,
			FilePath:    f,
			ReplyToken:  token,
		}); err != nil {
			logger.Warnf(ctx, "send file %s to %s: %v", f, userID, err)
		}
		_ = os.Remove(f)
	}
}
