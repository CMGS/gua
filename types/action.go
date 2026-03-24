package types

// ActionType identifies the kind of user control action.
type ActionType int

const (
	ActionNone    ActionType = iota
	ActionConfirm            // yes/allow/ok
	ActionDeny               // no/deny/cancel
	ActionSelect             // numbered selection (1,2,3...)
)

// Action is a platform-agnostic control action parsed from user input.
// Backend parses platform-specific input (text "/yes", button callback, etc.)
// into this unified representation.
type Action struct {
	Type  ActionType
	Value string // for ActionSelect: "1", "2", "3", etc.
}
