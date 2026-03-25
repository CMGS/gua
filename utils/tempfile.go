package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// TempPrefix is the filename prefix for all gua temp files.
const TempPrefix = "gua-"

// TempDir returns the OS temp directory used for gua temp files.
func TempDir() string { return os.TempDir() }

// TempPath returns a unique temp file path: <os.TempDir()>/gua-<name>.<ext>
func TempPath(name, ext string) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("%s%s.%s", TempPrefix, name, ext))
}

// TempPathRandom returns a unique temp file path with a random ID.
func TempPathRandom(ext string) string {
	return TempPath(ShortID(), ext)
}

// TempFileRegex returns a compiled regex matching gua temp file paths.
// Used by server/formatter to extract file paths from agent responses.
func TempFileRegex() *regexp.Regexp {
	prefix := regexp.QuoteMeta(filepath.Join(os.TempDir(), TempPrefix))
	return regexp.MustCompile(prefix + `[^\s"'\x60\]\)]+`)
}

// TempFileRule returns the file naming rule for agent prompts.
func TempFileRule() string {
	return filepath.Join(os.TempDir(), TempPrefix+"<description>.<ext>")
}
