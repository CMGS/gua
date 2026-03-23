package config

import "embed"

//go:embed *.md
var mdFS embed.FS

// MergedMD returns the combined CLAUDE.md content for the given backend.
// Loads base.md + {backendName}.md if it exists.
func MergedMD(backendName string) string {
	md := readMD("base.md")
	if overlay := readMD(backendName + ".md"); overlay != "" {
		md += "\n\n" + overlay
	}
	return md
}

func readMD(name string) string {
	data, err := mdFS.ReadFile(name)
	if err != nil {
		return ""
	}
	return string(data)
}
