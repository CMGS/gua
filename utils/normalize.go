package utils

import (
	"strings"

	"github.com/google/uuid"
)

var idReplacer = strings.NewReplacer("@", "-", ".", "-", ":", "-")

// NormalizeID replaces @, ., and : with - for filesystem safety.
func NormalizeID(raw string) string {
	return idReplacer.Replace(raw)
}

// ShortID returns a short random hex identifier (8 chars from UUID).
func ShortID() string {
	return uuid.New().String()[:8]
}
