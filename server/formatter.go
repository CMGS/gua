package server

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/CMGS/gua/agent"
	"github.com/CMGS/gua/backend"
)

var (
	filePathRegex  = regexp.MustCompile(`/tmp/gua-[^\s"'\x60\]\)]+`)
	blankLineRegex = regexp.MustCompile(`\n{3,}`)
)

// FormatInbound converts an InboundMessage into an agent Message.
func FormatInbound(msg backend.InboundMessage) agent.Message {
	var b strings.Builder
	b.WriteString(msg.Text)

	for _, mf := range msg.MediaFiles {
		switch mf.Type {
		case backend.MediaTypeImage:
			fmt.Fprintf(&b, "\n[图片: %s]", mf.Path)
		case backend.MediaTypeVoice:
			fmt.Fprintf(&b, "\n[语音: %s]", mf.Path)
		case backend.MediaTypeVideo:
			fmt.Fprintf(&b, "\n[视频: %s]", mf.Path)
		case backend.MediaTypeFile:
			fmt.Fprintf(&b, "\n[文件: %s] (%s)", mf.Path, mf.FileName)
		}
	}

	return agent.Message{
		Text:       b.String(),
		MediaFiles: msg.MediaFiles,
	}
}

// ExtractFiles extracts /tmp/gua-* file paths from agent response text.
// Returns the cleaned text and a list of valid existing file paths.
func ExtractFiles(text string) (string, []string) {
	matches := filePathRegex.FindAllString(text, -1)
	if len(matches) == 0 {
		return text, nil
	}

	seen := make(map[string]struct{}, len(matches))
	var paths []string

	for _, p := range matches {
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		if _, err := os.Stat(p); err == nil {
			paths = append(paths, p)
		}
	}

	cleaned := filePathRegex.ReplaceAllString(text, "")
	cleaned = blankLineRegex.ReplaceAllString(cleaned, "\n\n")
	cleaned = strings.TrimSpace(cleaned)

	return cleaned, paths
}
