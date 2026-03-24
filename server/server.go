package server

import (
	"context"
	"os"
	"strings"

	"github.com/projecteru2/core/log"

	"github.com/CMGS/gua/agent"
	"github.com/CMGS/gua/backend"
)

const maxConcurrent = 32

// Server orchestrates a Backend and an Agent.
type Server struct {
	backend backend.Backend
	agent   agent.Agent
	sem     chan struct{}
}

// New creates a Server that bridges the given backend and agent.
func New(b backend.Backend, a agent.Agent) *Server {
	return &Server{
		backend: b,
		agent:   a,
		sem:     make(chan struct{}, maxConcurrent),
	}
}

// Run starts the server. Blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	logger := log.WithFunc("server.Run")
	logger.Infof(ctx, "starting server: backend=%s agent=%s", s.backend.Name(), s.agent.Name())

	err := s.backend.Start(ctx, func(ctx context.Context, msg backend.InboundMessage) {
		s.sem <- struct{}{}
		go func() {
			defer func() { <-s.sem }()
			s.handleInbound(ctx, msg)
		}()
	})

	s.agent.CloseAll() //nolint:errcheck
	return err
}

func (s *Server) handleInbound(ctx context.Context, msg backend.InboundMessage) {
	if msg.Text == "" && len(msg.MediaFiles) == 0 {
		return
	}

	presenter := s.backend.Presenter()

	if len(msg.MediaFiles) == 0 && strings.HasPrefix(strings.TrimSpace(msg.Text), "/") {
		if resp, handled, err := s.agent.Control(ctx, msg.SenderID, strings.TrimSpace(msg.Text)); handled {
			if err != nil {
				s.sendError(ctx, msg, presenter, err)
				return
			}
			s.sendResponse(ctx, msg, presenter, resp)
			return
		}
	}

	stopTyping := s.backend.StartTyping(ctx, msg.SenderID, msg.ReplyToken)

	agentMsg := FormatInbound(msg, presenter)
	resp, err := s.agent.Chat(ctx, msg.SenderID, agentMsg)
	stopTyping()
	if err != nil {
		s.sendError(ctx, msg, presenter, err)
		return
	}

	s.sendResponse(ctx, msg, presenter, resp)
}

func (s *Server) sendError(ctx context.Context, msg backend.InboundMessage, p backend.Presenter, err error) {
	logger := log.WithFunc("server.sendError")
	logger.Warnf(ctx, "error for %s: %v", msg.SenderID, err)
	s.backend.Send(ctx, backend.OutboundMessage{ //nolint:errcheck
		RecipientID: msg.SenderID,
		Text:        p.FormatError(err),
		ReplyToken:  msg.ReplyToken,
	})
}

func (s *Server) sendResponse(ctx context.Context, msg backend.InboundMessage, p backend.Presenter, resp *agent.Response) {
	logger := log.WithFunc("server.sendResponse")

	if resp == nil {
		return
	}

	// Prompt responses — delegate rendering to presenter.
	if resp.Prompt != agent.PromptNone {
		toolName, description := "", ""
		if resp.Permission != nil {
			toolName = resp.Permission.ToolName
			description = resp.Permission.Description
		}
		text := p.FormatPrompt(resp.PromptText, resp.Options, toolName, description)
		s.backend.Send(ctx, backend.OutboundMessage{ //nolint:errcheck
			RecipientID: msg.SenderID,
			Text:        text,
			ReplyToken:  msg.ReplyToken,
		})
		return
	}

	// Normal text response — let presenter process text.
	cleanText, textFiles := ExtractFiles(p.FormatText(resp.Text))
	files := MergeFiles(textFiles, resp.Files)

	if cleanText != "" {
		if err := s.backend.Send(ctx, backend.OutboundMessage{
			RecipientID: msg.SenderID,
			Text:        cleanText,
			ReplyToken:  msg.ReplyToken,
		}); err != nil {
			logger.Warnf(ctx, "send text to %s: %v", msg.SenderID, err)
		}
	}

	for _, f := range files {
		if err := s.backend.Send(ctx, backend.OutboundMessage{
			RecipientID: msg.SenderID,
			FilePath:    f,
			ReplyToken:  msg.ReplyToken,
		}); err != nil {
			logger.Warnf(ctx, "send file %s to %s: %v", f, msg.SenderID, err)
		}
		os.Remove(f) //nolint:errcheck
	}
}
