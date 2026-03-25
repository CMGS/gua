package tmux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/CMGS/gua/runtime"
)

const watchWindowSize = 80 * 40 // ~1 terminal screen worth of characters

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

// Respawn kills the running process in the pane and starts a new command.
func (t *Tmux) Respawn(ctx context.Context, proc *runtime.Process, command string) error {
	if proc == nil || proc.PaneID == "" {
		return fmt.Errorf("no pane to respawn in")
	}
	_ = t.SendInput(ctx, proc, "C-c")
	_, err := t.exec(ctx, "respawn-pane", "-k", "-t", proc.PaneID, command)
	return err
}

// Watch streams process output via tmux pipe-pane through a FIFO.
// The handler receives a sliding window of the latest ~3200 characters
// each time new output arrives. Blocks until ctx is canceled.
func (t *Tmux) Watch(ctx context.Context, proc *runtime.Process, handler runtime.OutputHandler) error {
	if proc == nil || proc.PaneID == "" {
		return nil
	}

	fifoPath := filepath.Join(os.TempDir(), fmt.Sprintf("gua-watch-%s.fifo", strings.ReplaceAll(proc.PaneID, "%", "")))
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil && !os.IsExist(err) {
		return fmt.Errorf("mkfifo: %w", err)
	}
	defer func() { _ = os.Remove(fifoPath) }()

	if _, err := t.exec(ctx, "pipe-pane", "-t", proc.PaneID, "cat > '"+fifoPath+"'"); err != nil {
		return fmt.Errorf("pipe-pane: %w", err)
	}
	defer t.exec(context.Background(), "pipe-pane", "-t", proc.PaneID, "") //nolint:errcheck

	// Open FIFO non-blocking to avoid goroutine leak if writer hasn't connected yet.
	f, err := os.OpenFile(fifoPath, os.O_RDONLY|syscall.O_NONBLOCK, 0) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open fifo: %w", err)
	}

	go func() {
		<-ctx.Done()
		_ = f.Close()
	}()

	window := make([]byte, 0, watchWindowSize*2)
	buf := make([]byte, 4096)
	hasData := false

	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			hasData = true
			window = append(window, buf[:n]...)
			if len(window) > watchWindowSize {
				window = window[len(window)-watchWindowSize:]
				for len(window) > 0 && !utf8.RuneStart(window[0]) {
					window = window[1:]
				}
			}
			handler(string(window))
		}
		if readErr != nil {
			// EAGAIN: no data yet (Linux). EOF before data: writer not connected (macOS).
			if errors.Is(readErr, syscall.EAGAIN) || (!hasData && errors.Is(readErr, io.EOF)) {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return readErr
		}
	}
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
