package utils

import "sync"

// SyncValue is a thread-safe wrapper for a value of type T.
type SyncValue[T any] struct {
	mu  sync.RWMutex
	val T
}

// Get returns the current value.
func (v *SyncValue[T]) Get() T {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.val
}

// Set updates the value.
func (v *SyncValue[T]) Set(val T) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.val = val
}

// Clear resets the value to its zero value.
func (v *SyncValue[T]) Clear() {
	v.mu.Lock()
	defer v.mu.Unlock()
	var zero T
	v.val = zero
}
