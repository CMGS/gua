package claude

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/projecteru2/core/log"

	"github.com/CMGS/gua/agent"
	"github.com/CMGS/gua/agent/claude/protocol"
	"github.com/CMGS/gua/runtime"
	"github.com/CMGS/gua/types"
)

func (c *ClaudeCode) buildCommand(userID string, continueSession bool) string {
	shellPath := os.Getenv("SHELL")
	if shellPath == "" {
		shellPath = "/bin/bash"
	}

	args := []string{runtime.ShellQuote(c.claudeCmd)}
	if c.model != "" {
		args = append(args, runtime.ShellQuote("--model"), runtime.ShellQuote(c.model))
	}
	// Resume previous conversation only if one exists.
	if continueSession {
		args = append(args, runtime.ShellQuote("--continue"))
	}
	if c.getUserFlag(userID, "skip-permissions") == "true" {
		args = append(args, runtime.ShellQuote("--dangerously-skip-permissions"))
	} else {
		// Pre-approve common tools to reduce permission prompts.
		for _, tool := range []string{"Read", "Glob", "Grep", "LS", "mcp__gua__gua_reply"} {
			args = append(args, runtime.ShellQuote("--allowedTools"), runtime.ShellQuote(tool))
		}
	}
	args = append(args, runtime.ShellQuote("--dangerously-load-development-channels"), runtime.ShellQuote("server:gua"))

	claudeCmd := strings.Join(args, " ")
	inner := fmt.Sprintf("%s; code=$?; printf '\\n[gua] claude exited with code %%s\\n' \"$code\"; exec %s -l", claudeCmd, runtime.ShellQuote(shellPath))
	return fmt.Sprintf("%s -lc %s", runtime.ShellQuote(shellPath), runtime.ShellQuote(inner))
}

func (c *ClaudeCode) handleInteractiveControl(ctx context.Context, sess *userSession, action types.Action) error {
	prompt := sess.prompt.Get()
	keys := actionToKeys(action, prompt)
	if keys == nil {
		sess.pushResponse(interactiveResponse(prompt))
		return nil
	}
	if err := c.rt.SendInput(ctx, sess.proc, keys...); err != nil {
		return err
	}
	sess.prompt.Clear()
	return nil
}

func (c *ClaudeCode) handlePermissionControl(_ context.Context, sess *userSession, action types.Action, perm *protocol.Permission) error {
	var behavior string
	switch action.Type {
	case types.ActionConfirm:
		behavior = behaviorAllow
	case types.ActionDeny:
		behavior = behaviorDeny
	default:
		// Re-push the prompt
		sess.pushResponse(permissionResponse(perm))
		return nil
	}

	reply := protocol.Permission{
		RequestID: perm.RequestID,
		Behavior:  behavior,
	}

	// Route reply: hook channel (preferred) → bridge conn (fallback).
	if ch := sess.hookPermReply.Get(); ch != nil {
		ch <- reply
	} else if err := sess.writeEnvelope(protocol.TypePermissionReply, reply); err != nil {
		sess.permission.Clear()
		return fmt.Errorf("send permission reply: %w", err)
	}
	sess.permission.Clear()

	if behavior == behaviorDeny {
		sess.pushResponse(&agent.Response{Text: "denied"})
	}
	return nil
}

func (c *ClaudeCode) handleElicitationControl(_ context.Context, sess *userSession, action types.Action, elicit *protocol.Elicitation) {
	var elicitAction string
	switch action.Type {
	case types.ActionConfirm:
		elicitAction = elicitAccept
	case types.ActionDeny:
		elicitAction = elicitDecline
	default:
		sess.pushResponse(elicitationResponse(elicit))
		return
	}

	reply := protocol.ElicitationReply{
		ElicitationID: elicit.ElicitationID,
		Action:        elicitAction,
	}

	if ch := sess.hookElicitReply.Get(); ch != nil {
		ch <- reply
	} else {
		log.WithFunc("claude.handleElicitationControl").Warnf(c.ctx, "no hook channel for elicitation reply, user=%s", sess.userID)
	}
	sess.elicitation.Clear()

	if elicitAction == elicitDecline {
		sess.pushResponse(&agent.Response{Text: "declined"})
	}
}
