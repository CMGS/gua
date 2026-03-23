package utils

import (
	"mime"
	"path/filepath"
	"strings"
)

// NormalizeID replaces @, ., and : with - for filesystem safety.
func NormalizeID(raw string) string {
	return idReplacer.Replace(raw)
}

var idReplacer = strings.NewReplacer("@", "-", ".", "-", ":", "-")

// DetectImageFormat checks magic bytes to determine image format.
// Returns "png", "gif", or "jpg" (default).
func DetectImageFormat(data []byte) string {
	if len(data) >= 4 && data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
		return "png"
	}
	if len(data) >= 3 && data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46 {
		return "gif"
	}
	return "jpg"
}

// DetectMIMEType returns the MIME type for a file path based on extension.
func DetectMIMEType(path string) string {
	ext := filepath.Ext(path)
	if ext == "" {
		return "application/octet-stream"
	}
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		return "application/octet-stream"
	}
	return mimeType
}

// IsRasterImage returns true for MIME types supported as images by most platforms.
func IsRasterImage(mimeType string) bool {
	return mimeType == "image/png" || mimeType == "image/jpeg" || mimeType == "image/gif" || mimeType == "image/bmp"
}

// CleanFileName strips the "gua-{uuid}." prefix from temp file names.
func CleanFileName(name string) string {
	if !strings.HasPrefix(name, "gua-") {
		return name
	}
	rest := name[4:]
	if len(rest) < 8 {
		return name
	}
	after := rest[8:]
	ext := filepath.Ext(name)
	switch {
	case strings.HasPrefix(after, "-") && len(after) > 1:
		return after[1:]
	case strings.HasPrefix(after, ".") && len(after) > 1:
		return "file" + ext
	default:
		return name
	}
}
