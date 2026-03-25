package runtime

// Process represents a running process inside the runtime container.
type Process struct {
	ID     string // container process identifier (tmux: window_id)
	PaneID string // interactive pane identifier (tmux: pane_id)
}

// OutputHandler is called with the current sliding window content
// each time new output is detected from the process.
type OutputHandler func(content string)
