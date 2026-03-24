package runtime

import "context"

// Process represents a running process inside the runtime container.
type Process struct {
	ID     string // container process identifier (tmux: window_id)
	PaneID string // interactive pane identifier (tmux: pane_id)
}

// Runtime is a frontend process container abstraction (tmux, screen, etc.).
// It manages long-running interactive processes that need a persistent terminal.
type Runtime interface {
	// StartProcess launches a command in the container, returning process handles.
	StartProcess(ctx context.Context, name, workDir, command string) (*Process, error)
	// CaptureOutput captures the current visible output of a process.
	CaptureOutput(ctx context.Context, proc *Process) (string, error)
	// SendInput sends keystrokes to a process.
	SendInput(ctx context.Context, proc *Process, keys ...string) error
	// Kill terminates a process.
	Kill(ctx context.Context, proc *Process) error
	// Respawn kills the running process in the same pane and starts a new command.
	// The Process handles (window/pane IDs) remain the same.
	Respawn(ctx context.Context, proc *Process, command string) error
	// Close cleans up all runtime resources.
	Close() error
}
