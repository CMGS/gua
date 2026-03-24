package runtime

import "strings"

// LineFilter decides whether a terminal line should be kept and whether
// it indicates an interactive prompt. Implementations are agent-specific
// (e.g. Claude Code filters "Claude Code v" lines, Codex filters differently).
type LineFilter func(line string) (keep bool, interactive bool)

// CompactInteractivePrompt extracts interactive prompt content from terminal output.
// The filter decides which lines to keep/discard per agent.
func CompactInteractivePrompt(pane string, filter LineFilter) string {
	pane = strings.ReplaceAll(pane, "\u00a0", " ")
	lines := strings.Split(pane, "\n")
	filtered := make([]string, 0, len(lines))
	interactive := false

	for _, raw := range lines {
		line, keep, isInteractive := normalizePromptLine(raw, filter)
		if isInteractive {
			interactive = true
		}
		if keep {
			filtered = append(filtered, line)
		}
	}

	if !interactive || len(filtered) == 0 {
		return ""
	}

	start := 0
	if len(filtered) > 8 {
		start = len(filtered) - 8
	}

	var compact []string
	for _, line := range filtered[start:] {
		if len(compact) > 0 && compact[len(compact)-1] == line {
			continue
		}
		compact = append(compact, line)
	}

	return strings.TrimSpace(strings.Join(compact, "\n"))
}

// OptionLineNumber extracts a numbered option prefix (e.g. "1" from "1. Yes").
func OptionLineNumber(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	dot := strings.IndexByte(line, '.')
	if dot <= 0 {
		return ""
	}
	for _, r := range line[:dot] {
		if r < '0' || r > '9' {
			return ""
		}
	}
	if dot+1 >= len(line) || line[dot+1] != ' ' {
		return ""
	}
	return line[:dot]
}

// IsSeparatorLine returns true if the line is entirely separator characters.
func IsSeparatorLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	return strings.Trim(line, "─-═━") == ""
}

// ExtractOptions parses numbered options or y/n indicators from a prompt.
func ExtractOptions(prompt string) []string {
	var opts []string

	for raw := range strings.SplitSeq(prompt, "\n") {
		if n := OptionLineNumber(strings.TrimSpace(raw)); n != "" {
			opts = append(opts, n)
		}
	}

	if len(opts) == 0 && hasYNIndicator(prompt) {
		opts = []string{"yes", "no"}
	}
	return opts
}

// ControlKeys maps user control input (/1, /yes, /no) to terminal key sequences.
func ControlKeys(prompt, input string) ([]string, bool) {
	cmd := NormalizeControl(input)
	yn := hasYNIndicator(prompt)

	switch cmd {
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return []string{cmd, "Enter"}, true
	case "yes", "y", "enter":
		if yn {
			return []string{"y", "Enter"}, true
		}
		return []string{"Enter"}, true
	case "no", "n", "cancel":
		if strings.Contains(prompt, "Esc to cancel") {
			return []string{"Escape"}, true
		}
		if yn {
			return []string{"n", "Enter"}, true
		}
		return []string{"C-c"}, true
	default:
		return nil, false
	}
}

// NormalizeControl strips "/" prefix and lowercases user input.
func NormalizeControl(input string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(input)), "/")
}

// ShellQuote returns a shell-safe single-quoted string.
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func normalizePromptLine(raw string, filter LineFilter) (line string, keep bool, interactive bool) {
	line = strings.TrimSpace(strings.TrimRight(raw, "\r"))
	if line == "" {
		return "", false, false
	}

	if IsSeparatorLine(line) {
		return "", false, false
	}

	// Universal interactive indicators.
	if strings.Contains(line, "Enter to confirm") ||
		strings.Contains(line, "Esc to cancel") ||
		hasYNIndicator(line) {
		return line, true, true
	}

	// Agent-specific filter.
	if filter != nil {
		k, i := filter(line)
		if !k {
			return "", false, i
		}
		if i {
			return line, true, true
		}
	}

	if idx := OptionLineNumber(line); idx != "" {
		return line, true, true
	}

	return line, true, false
}

func hasYNIndicator(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "y/n")
}
