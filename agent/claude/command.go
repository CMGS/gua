package claude

import (
	"context"
	"fmt"
	"os"
	"strings"

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
		for _, tool := range []string{"Read", "Glob", "Grep", "LS", "Bash", "Write", "Edit", "mcp__gua__gua_reply"} {
			args = append(args, runtime.ShellQuote("--allowedTools"), runtime.ShellQuote(tool))
		}
	}
	args = append(args, runtime.ShellQuote("--dangerously-load-development-channels"), runtime.ShellQuote("server:gua"))

	claudeCmd := strings.Join(args, " ")
	inner := fmt.Sprintf("%s; code=$?; printf '\\n[gua] claude exited with code %%s\\n' \"$code\"; exec %s -l", claudeCmd, runtime.ShellQuote(shellPath))
	return fmt.Sprintf("%s -lc %s", runtime.ShellQuote(shellPath), runtime.ShellQuote(inner))
}

func (c *ClaudeCode) handleInteractiveControl(ctx context.Context, sess *userSession, action types.Action) (*agent.Response, error) {
	prompt := sess.prompt.Get()
	keys := actionToKeys(action, prompt)
	if keys == nil {
		return interactiveResponse(prompt), nil
	}
	if err := c.rt.SendInput(ctx, sess.proc, keys...); err != nil {
		return nil, err
	}
	sess.prompt.Clear()

	return c.waitForReplyWithTimeout(ctx, sess, controlWaitTimeout, false)
}

func (c *ClaudeCode) handlePermissionControl(ctx context.Context, sess *userSession, action types.Action, perm *protocol.Permission) (*agent.Response, error) {
	var behavior string
	switch action.Type {
	case types.ActionConfirm:
		behavior = behaviorAllow
	case types.ActionDeny:
		behavior = behaviorDeny
	default:
		return permissionResponse(perm), nil
	}

	reply := protocol.Permission{
		RequestID: perm.RequestID,
		Behavior:  behavior,
	}
	if err := sess.writeEnvelope(protocol.TypePermissionReply, reply); err != nil {
		return nil, fmt.Errorf("send permission reply: %w", err)
	}
	sess.permission.Clear()

	if behavior != behaviorAllow {
		return &agent.Response{Text: "已拒绝该操作。"}, nil
	}
	return c.waitForReply(ctx, sess)
}
