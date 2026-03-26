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

// MergeJSONFile reads an existing JSON file, deep-merges the provided keys
// into it, and writes back. If the file does not exist or is invalid JSON,
// starts fresh. Nested maps are recursively merged; other types are overwritten.
func MergeJSONFile(path string, keys map[string]any, perm os.FileMode) error {
	existing := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil { //nolint:gosec
		_ = json.Unmarshal(data, &existing) // invalid JSON → empty map
	}
	deepMerge(existing, keys)
	return WriteJSONFile(path, &existing, perm)
}

// UnmergeJSONFile removes the specified keys from a JSON file.
// Keys are paths like ["mcpServers", "gua"] meaning delete existing["mcpServers"]["gua"].
// If the parent map becomes empty after removal, it is also removed.
// If the file becomes empty or doesn't exist, the file is deleted.
func UnmergeJSONFile(path string, keyPaths ...[]string) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return // file doesn't exist, nothing to clean
	}

	existing := make(map[string]any)
	if json.Unmarshal(data, &existing) != nil {
		return
	}

	for _, keys := range keyPaths {
		removeNestedKey(existing, keys)
	}

	if len(existing) == 0 {
		_ = os.Remove(path)
		return
	}

	_ = WriteJSONFile(path, &existing, 0o644)
}

func removeNestedKey(m map[string]any, keys []string) {
	if len(keys) == 0 {
		return
	}
	if len(keys) == 1 {
		delete(m, keys[0])
		return
	}
	child, ok := m[keys[0]].(map[string]any)
	if !ok {
		return
	}
	removeNestedKey(child, keys[1:])
	if len(child) == 0 {
		delete(m, keys[0])
	}
}

func deepMerge(dst, src map[string]any) {
	for k, srcVal := range src {
		dstVal, exists := dst[k]
		if !exists {
			dst[k] = srcVal
			continue
		}
		dstMap, dstOk := dstVal.(map[string]any)
		srcMap, srcOk := srcVal.(map[string]any)
		if dstOk && srcOk {
			deepMerge(dstMap, srcMap)
		} else {
			dst[k] = srcVal
		}
	}
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
