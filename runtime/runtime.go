package runtime

import "context"

// Runtime is a frontend process container abstraction (tmux, screen, etc.).
// It manages long-running interactive processes that need a persistent terminal.
type Runtime interface {
	StartProcess(ctx context.Context, name, workDir, command string) (*Process, error)
	CaptureOutput(ctx context.Context, proc *Process) (string, error)
	SendInput(ctx context.Context, proc *Process, keys ...string) error
	Kill(ctx context.Context, proc *Process) error
	Respawn(ctx context.Context, proc *Process, command string) error
	// Watch streams process output through a sliding window.
	// The handler receives the latest ~1 screen of content on each update.
	// Blocks until ctx is canceled.
	Watch(ctx context.Context, proc *Process, handler OutputHandler) error
	Close() error
}
