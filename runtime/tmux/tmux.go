package tmux

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/CMGS/gua/runtime"
)

// Tmux implements runtime.Runtime using tmux sessions.
type Tmux struct {
	sessionName string
}

// New creates a tmux-based Runtime with the given session name.
func New(sessionName string) *Tmux {
	return &Tmux{sessionName: sessionName}
}

// StartProcess creates a new tmux window running the given command.
func (t *Tmux) StartProcess(ctx context.Context, name, workDir, command string) (*runtime.Process, error) {
	format := "#{window_id} #{pane_id}"

	if _, err := t.exec(ctx, "has-session", "-t", t.sessionName); err != nil {
		out, err := t.exec(ctx, "new-session", "-d", "-s", t.sessionName, "-n", name, "-c", workDir, "-P", "-F", format, command)
		if err != nil {
			return nil, err
		}
		return parseIDs(out)
	}

	out, err := t.exec(ctx, "new-window", "-d", "-t", t.sessionName, "-n", name, "-c", workDir, "-P", "-F", format, command)
	if err != nil {
		return nil, err
	}
	return parseIDs(out)
}

// CaptureOutput captures the visible pane output (last 80 lines).
func (t *Tmux) CaptureOutput(ctx context.Context, proc *runtime.Process) (string, error) {
	if proc == nil || proc.PaneID == "" {
		return "", nil
	}
	return t.exec(ctx, "capture-pane", "-p", "-t", proc.PaneID, "-S", "-80")
}

// SendInput sends keystrokes to the process pane.
func (t *Tmux) SendInput(ctx context.Context, proc *runtime.Process, keys ...string) error {
	if proc == nil || proc.PaneID == "" {
		return nil
	}
	args := append([]string{"send-keys", "-t", proc.PaneID}, keys...)
	_, err := t.exec(ctx, args...)
	return err
}

// Kill terminates the tmux window hosting the process.
func (t *Tmux) Kill(ctx context.Context, proc *runtime.Process) error {
	if proc == nil || proc.ID == "" {
		return nil
	}
	_, err := t.exec(ctx, "kill-window", "-t", proc.ID)
	return err
}

// Close is a no-op for tmux — the session persists for reuse.
func (t *Tmux) Close() error {
	return nil
}

func (t *Tmux) exec(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func parseIDs(out string) (*runtime.Process, error) {
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) < 2 {
		return nil, fmt.Errorf("unexpected tmux output: %q", out)
	}
	return &runtime.Process{ID: fields[0], PaneID: fields[1]}, nil
}
