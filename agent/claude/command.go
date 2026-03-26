package claude

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

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
	// Resume previous conversation.
	if resumeID := c.getUserFlag(userID, "resume"); resumeID != "" {
		args = append(args, runtime.ShellQuote("--resume"), runtime.ShellQuote(resumeID))
	} else if continueSession {
		args = append(args, runtime.ShellQuote("--continue"))
	}
	if c.getUserFlag(userID, "skip-permissions") == flagTrue {
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

func (c *ClaudeCode) handleTUIMenuControl(ctx context.Context, sess *userSession, action types.Action) error {
	prompt := sess.prompt.Get()

	switch action.Type {
	case types.ActionDeny:
		// /cancel → Esc → exit menu
		if err := c.rt.SendInput(ctx, sess.proc, "Escape"); err != nil {
			return err
		}
		sess.tuiMenu.Clear()
		sess.prompt.Clear()
		go c.pollTUIMenuExit(ctx, sess)
		return nil

	case types.ActionConfirm:
		// /yes → Enter (only when menu has "Enter to")
		if !strings.Contains(prompt, "Enter to") {
			sess.pushResponse(tuiMenuResponse(prompt))
			return nil
		}
		if err := c.rt.SendInput(ctx, sess.proc, "Enter"); err != nil {
			return err
		}
		sess.tuiMenu.Clear()
		sess.prompt.Clear()
		go c.pollTUIMenuExit(ctx, sess)
		return nil

	case types.ActionSelect:
		keys := tuiMenuSelectKeys(action, prompt)
		if keys == nil {
			sess.pushResponse(tuiMenuResponse(prompt))
			return nil
		}
		if err := c.rt.SendInput(ctx, sess.proc, keys...); err != nil {
			return err
		}
		sess.prompt.Clear()
		c.startTUIMenuPoll(ctx, sess)
		return nil

	default:
		// Unrecognized action → resend menu
		sess.pushResponse(tuiMenuResponse(prompt))
		return nil
	}
}

// pollTUIMenuExit captures status text after exiting a TUI menu.
func (c *ClaudeCode) pollTUIMenuExit(ctx context.Context, sess *userSession) {
	time.Sleep(300 * time.Millisecond)
	pane, err := c.rt.CaptureOutput(ctx, sess.proc)
	if err != nil {
		if ctx.Err() == nil {
			log.WithFunc("claude.pollTUIMenuExit").Debugf(ctx, "capture output: %v", err)
		}
		return
	}
	if status := captureStatusText(pane); status != "" {
		sess.pushResponse(&agent.Response{Text: status})
	}
}

func (c *ClaudeCode) handlePermissionControl(_ context.Context, sess *userSession, action types.Action) error {
	// For unrecognized actions, peek to re-push the prompt without consuming it.
	if action.Type != types.ActionConfirm && action.Type != types.ActionDeny {
		if p, ok := sess.permQueue.Peek(); ok {
			sess.pushResponse(permissionResponse(p.perm))
		}
		return nil
	}

	// Atomic pop — no peek-then-pop race.
	front, ok := sess.permQueue.Pop()
	if !ok {
		return nil
	}

	behavior := behaviorAllow
	if action.Type == types.ActionDeny {
		behavior = behaviorDeny
	}

	reply := protocol.Permission{
		RequestID: front.perm.RequestID,
		Behavior:  behavior,
	}

	if front.replyCh != nil {
		front.replyCh <- reply
	} else if err := sess.writeEnvelope(protocol.TypePermissionReply, reply); err != nil {
		return fmt.Errorf("send permission reply: %w", err)
	}

	if behavior == behaviorDeny {
		sess.pushResponse(&agent.Response{Text: "denied"})
	}

	// Push the next pending permission (if any) so the user sees it.
	if next, ok := sess.permQueue.Peek(); ok {
		sess.pushResponse(permissionResponse(next.perm))
	}
	return nil
}

func (c *ClaudeCode) handleElicitationControl(_ context.Context, sess *userSession, action types.Action) {
	if action.Type != types.ActionConfirm && action.Type != types.ActionDeny {
		if e, ok := sess.elicitQueue.Peek(); ok {
			sess.pushResponse(elicitationResponse(e.elicit))
		}
		return
	}

	front, ok := sess.elicitQueue.Pop()
	if !ok {
		return
	}

	elicitAction := elicitAccept
	if action.Type == types.ActionDeny {
		elicitAction = elicitDecline
	}

	reply := protocol.ElicitationReply{
		ElicitationID: front.elicit.ElicitationID,
		Action:        elicitAction,
	}

	if front.replyCh != nil {
		front.replyCh <- reply
	} else {
		log.WithFunc("claude.handleElicitationControl").Warnf(c.ctx, "no hook channel for elicitation reply, user=%s", sess.userID)
	}

	if elicitAction == elicitDecline {
		sess.pushResponse(&agent.Response{Text: "declined"})
	}

	if next, ok := sess.elicitQueue.Peek(); ok {
		sess.pushResponse(elicitationResponse(next.elicit))
	}
}
