package config

import "embed"

//go:embed *.md
var mdFS embed.FS

// MergedMD returns the combined CLAUDE.md content for the given backend,
// with platform-specific media instructions from the presenter.
func MergedMD(backendName string, mediaInstructions string) string {
	md := readMD("base.md")
	if overlay := readMD(backendName + ".md"); overlay != "" {
		md += "\n\n" + overlay
	}
	if mediaInstructions != "" {
		md += "\n\n" + mediaInstructions
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
