package utils

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ReadJSONFile reads a JSON file and unmarshals it into a value of type T.
func ReadJSONFile[T any](path string) (*T, error) {
	data, err := os.ReadFile(path) //nolint:gosec // intentional: generic file utility
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	v := new(T)
	if err := json.Unmarshal(data, v); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", path, err)
	}
	return v, nil
}

// WriteJSONFile marshals a value to JSON and writes it to path.
// Parent directories are created as needed.
func WriteJSONFile[T any](path string, v *T, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create dir for %s: %w", path, err)
	}

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}

	if err := os.WriteFile(path, data, perm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
