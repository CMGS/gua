package server

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/projecteru2/core/log"

	"github.com/CMGS/gua/agent"
	"github.com/CMGS/gua/channel"
)

const (
	maxConcurrent  = 32
	workingTimeout = 30 * time.Second // notify user if agent hasn't replied
)

type commandHandler func(ctx context.Context, msg channel.InboundMessage) *agent.Response

// Server orchestrates a Channel and an Agent.
type Server struct {
	channel  channel.Channel
	agent    agent.Agent
	sem      chan struct{}
	commands map[string]commandHandler
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

	presenter := s.channel.Presenter()
	trimmed := strings.TrimSpace(msg.Text)

	// 1. Global commands.
	if handler, ok := s.commands[strings.ToLower(trimmed)]; ok {
		s.sendText(ctx, msg, "processing...")
		resp := handler(ctx, msg)
		s.sendResponse(ctx, msg, presenter, resp)
		return
	}

	// 2. Control actions.
	if len(msg.MediaFiles) == 0 { //nolint:nestif
		if action := presenter.ParseAction(trimmed); action != nil {
			resp, handled, err := s.agent.Control(ctx, msg.SenderID, *action)
			if handled {
				if err != nil {
					s.sendError(ctx, msg, presenter, err)
					return
				}
				s.sendResponse(ctx, msg, presenter, resp)
				return
			}
		}
	}

	// 3. Normal chat — with "working" notification if agent is slow.
	stopTyping := s.channel.StartTyping(ctx, msg.SenderID, msg.ReplyToken)

	agentMsg := FormatInbound(msg, presenter)

	type chatResult struct {
		resp *agent.Response
		err  error
	}
	resultCh := make(chan chatResult, 1)
	go func() {
		resp, err := s.agent.Chat(ctx, msg.SenderID, agentMsg)
		resultCh <- chatResult{resp, err}
	}()

	notified := false
	timer := time.NewTimer(workingTimeout)
	defer timer.Stop()

	select {
	case r := <-resultCh:
		stopTyping()
		if r.err != nil {
			s.sendError(ctx, msg, presenter, r.err)
			return
		}
		s.sendResponse(ctx, msg, presenter, r.resp)
	case <-timer.C:
		s.sendText(ctx, msg, "still working...")
		notified = true
	case <-ctx.Done():
		stopTyping()
		return
	}

	if notified {
		select {
		case r := <-resultCh:
			stopTyping()
			if r.err != nil {
				s.sendError(ctx, msg, presenter, r.err)
				return
			}
			s.sendResponse(ctx, msg, presenter, r.resp)
		case <-ctx.Done():
			stopTyping()
		}
	}
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

func (s *Server) sendError(ctx context.Context, msg channel.InboundMessage, p channel.Presenter, err error) {
	logger := log.WithFunc("server.sendError")
	logger.Warnf(ctx, "error for %s: %v", msg.SenderID, err)
	_ = s.channel.Send(ctx, channel.OutboundMessage{
		RecipientID: msg.SenderID,
		Text:        p.FormatError(err),
		ReplyToken:  msg.ReplyToken,
	})
}

func (s *Server) sendResponse(ctx context.Context, msg channel.InboundMessage, p channel.Presenter, resp *agent.Response) {
	logger := log.WithFunc("server.sendResponse")

	if resp == nil {
		return
	}

	if resp.Prompt != agent.PromptNone {
		toolName, description := "", ""
		if resp.Permission != nil {
			toolName = resp.Permission.ToolName
			description = resp.Permission.Description
		}
		text := p.FormatPrompt(resp.PromptText, resp.Options, toolName, description)
		_ = s.channel.Send(ctx, channel.OutboundMessage{
			RecipientID: msg.SenderID,
			Text:        text,
			ReplyToken:  msg.ReplyToken,
		})
		return
	}

	cleanText, textFiles := ExtractFiles(p.FormatText(resp.Text))
	files := MergeFiles(textFiles, resp.Files)

	if cleanText != "" {
		if err := s.channel.Send(ctx, channel.OutboundMessage{
			RecipientID: msg.SenderID,
			Text:        cleanText,
			ReplyToken:  msg.ReplyToken,
		}); err != nil {
			logger.Warnf(ctx, "send text to %s: %v", msg.SenderID, err)
		}
	}

	for _, f := range files {
		if err := s.channel.Send(ctx, channel.OutboundMessage{
			RecipientID: msg.SenderID,
			FilePath:    f,
			ReplyToken:  msg.ReplyToken,
		}); err != nil {
			logger.Warnf(ctx, "send file %s to %s: %v", f, msg.SenderID, err)
		}
		_ = os.Remove(f)
	}
}
