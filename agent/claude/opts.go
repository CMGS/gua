package claude

import "github.com/CMGS/gua/runtime"

// WithClaudeCmd sets the path to the claude CLI binary.
func WithClaudeCmd(cmd string) Option {
	return func(c *ClaudeCode) { c.claudeCmd = cmd }
}

// WithBridgeBin sets the path to the bridge binary.
func WithBridgeBin(bin string) Option {
	return func(c *ClaudeCode) { c.bridgeBin = bin }
}

// WithModel sets the model for Claude Code.
func WithModel(model string) Option {
	return func(c *ClaudeCode) { c.model = model }
}

// WithRuntime sets the runtime container (tmux, screen, etc.) for hosting Claude sessions.
func WithRuntime(rt runtime.Runtime) Option {
	return func(c *ClaudeCode) { c.rt = rt }
}

// WithWorkDir sets the base working directory for sessions.
func WithWorkDir(dir string) Option {
	return func(c *ClaudeCode) { c.baseWorkDir = dir }
}

// WithClaudeMD sets the CLAUDE.md content written to each session workdir.
func WithClaudeMD(content string) Option {
	return func(c *ClaudeCode) { c.claudeMD = content }
}
