package runtime

import (
	"regexp"
	"strings"
)

// ansiRegex matches ANSI escape sequences (CSI, OSC, etc.).
var ansiRegex = regexp.MustCompile(`\x1b(?:\[[0-9;?]*[a-zA-Z]|\][^\x07]*\x07)`)

// LineFilter decides whether a terminal line should be kept and whether
// it indicates an interactive prompt. Implementations are agent-specific
// (e.g. Claude Code filters "Claude Code v" lines, Codex filters differently).
type LineFilter func(line string) (keep bool, interactive bool)

// CompactInteractivePrompt extracts interactive prompt content from terminal output.
// The filter decides which lines to keep/discard per agent.
func CompactInteractivePrompt(pane string, filter LineFilter) string {
	pane = ansiRegex.ReplaceAllString(pane, "")
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

// ExtractOptions parses numbered options from a prompt.
func ExtractOptions(prompt string) []string {
	var opts []string
	for raw := range strings.SplitSeq(prompt, "\n") {
		if n := OptionLineNumber(strings.TrimSpace(raw)); n != "" {
			opts = append(opts, n)
		}
	}
	return opts
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

	// Agent-specific filter handles ALL interactive detection.
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
