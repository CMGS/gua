package types

// ActionType identifies the kind of user control action.
type ActionType int

const (
	ActionNone        ActionType = iota
	ActionConfirm                // yes/allow/ok/enter
	ActionDeny                   // no/deny/cancel
	ActionSelect                 // /select N — numbered selection
	ActionPassthrough            // forward raw input to agent terminal
)

// Action is a platform-agnostic control action parsed from user input.
// Backend parses platform-specific input (text "/yes", button callback, etc.)
// into this unified representation.
type Action struct {
	Type  ActionType
	Value string // for ActionSelect: "1", "2", "3", etc.
}
