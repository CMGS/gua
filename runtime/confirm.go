package runtime

import (
	"context"
	"time"

	"github.com/projecteru2/core/log"
)

// ConfirmFunc decides which keys to send for a given interactive prompt.
// Returns nil to skip (do not confirm this prompt).
type ConfirmFunc func(prompt string) []string

// AutoConfirmLoop polls a process for interactive prompts and auto-confirms them.
// confirm is an agent-specific function that decides how to respond to each prompt.
// filter is the agent-specific line filter for prompt detection.
// Stops when ready is closed, timeout fires, or ctx is canceled.
func AutoConfirmLoop(ctx context.Context, rt Runtime, proc *Process, ready <-chan struct{}, filter LineFilter, confirm ConfirmFunc, interval, timeout time.Duration) error {
	logger := log.WithFunc("runtime.AutoConfirmLoop")

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-ready:
			return nil
		case <-ticker.C:
			prompt, err := CaptureInteractivePrompt(ctx, rt, proc, filter)
			if err != nil || prompt == "" {
				continue
			}
			keys := confirm(prompt)
			if keys == nil {
				continue
			}
			logger.Debugf(ctx, "auto-confirming prompt: %s", prompt)
			if err := rt.SendInput(ctx, proc, keys...); err != nil {
				logger.Warnf(ctx, "auto-confirm SendInput failed: %v", err)
			}
		case <-timer.C:
			return context.DeadlineExceeded
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// CaptureInteractivePrompt captures output and extracts any interactive prompt
// using the given line filter.
func CaptureInteractivePrompt(ctx context.Context, rt Runtime, proc *Process, filter LineFilter) (string, error) {
	pane, err := rt.CaptureOutput(ctx, proc)
	if err != nil {
		return "", err
	}
	return CompactInteractivePrompt(pane, filter), nil
}
