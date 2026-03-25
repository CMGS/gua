package server

import (
	"os"
	"regexp"
	"strings"

	"github.com/CMGS/gua/agent"
	"github.com/CMGS/gua/channel"
	"github.com/CMGS/gua/utils"
)

var (
	filePathRegex  = utils.TempFileRegex()
	blankLineRegex = regexp.MustCompile(`\n{3,}`)
)

// FormatInbound converts an InboundMessage into an agent Message,
// using the presenter to annotate media files in a platform-specific way.
func FormatInbound(msg channel.InboundMessage, p channel.Presenter) agent.Message {
	var b strings.Builder
	b.WriteString(msg.Text)

	for _, mf := range msg.MediaFiles {
		annotation := p.FormatMediaAnnotation(mf)
		if annotation != "" {
			b.WriteString("\n")
			b.WriteString(annotation)
		}
	}

	return agent.Message{
		Text:       b.String(),
		MediaFiles: msg.MediaFiles,
	}
}

// ExtractFiles extracts /tmp/gua-* file paths from agent response text.
func ExtractFiles(text string) (string, []string) {
	matches := filePathRegex.FindAllString(text, -1)
	if len(matches) == 0 {
		return text, nil
	}

	seen := make(map[string]struct{}, len(matches))
	valid := make(map[string]struct{}, len(matches))
	var paths []string

	for _, p := range matches {
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		if isSendableFile(p) {
			valid[p] = struct{}{}
			paths = append(paths, p)
		}
	}

	cleaned := filePathRegex.ReplaceAllStringFunc(text, func(match string) string {
		if _, ok := valid[match]; ok {
			return ""
		}
		return match
	})
	cleaned = blankLineRegex.ReplaceAllString(cleaned, "\n\n")
	cleaned = strings.TrimSpace(cleaned)

	return cleaned, paths
}

// MergeFiles deduplicates file paths. Paths are assumed to be pre-validated
// (ExtractFiles already checks isSendableFile; agent Files are trusted).
func MergeFiles(paths ...[]string) []string {
	seen := map[string]struct{}{}
	var merged []string

	for _, group := range paths {
		for _, p := range group {
			if p == "" {
				continue
			}
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			merged = append(merged, p)
		}
	}

	return merged
}

func isSendableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular()
}
