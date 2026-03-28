package codex

import "github.com/CMGS/gua/runtime"

// WithCodexCmd sets the path to the codex CLI binary.
func WithCodexCmd(cmd string) Option {
	return func(c *Codex) { c.codexCmd = cmd }
}

// WithModel sets the model for Codex.
func WithModel(model string) Option {
	return func(c *Codex) { c.model = model }
}

// WithRuntime sets the runtime container (tmux, screen, etc.).
func WithRuntime(rt runtime.Runtime) Option {
	return func(c *Codex) { c.rt = rt }
}

// WithWorkDir sets the base working directory for sessions.
func WithWorkDir(dir string) Option {
	return func(c *Codex) { c.baseWorkDir = dir }
}

// WithInitPrompt sets the init prompt content written to each session workdir.
func WithInitPrompt(content string) Option {
	return func(c *Codex) { c.initPrompt = content }
}
