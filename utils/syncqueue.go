package utils

import "sync"

// SyncQueue is a thread-safe FIFO queue.
type SyncQueue[T any] struct {
	mu    sync.Mutex
	items []T
}

// Push appends an item to the back of the queue.
func (q *SyncQueue[T]) Push(v T) {
	q.mu.Lock()
	q.items = append(q.items, v)
	q.mu.Unlock()
}

// Peek returns the front item without removing it.
// Returns zero value and false if the queue is empty.
func (q *SyncQueue[T]) Peek() (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		var zero T
		return zero, false
	}
	return q.items[0], true
}

// Pop removes and returns the front item.
// Returns zero value and false if the queue is empty.
func (q *SyncQueue[T]) Pop() (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		var zero T
		return zero, false
	}
	front := q.items[0]
	var zero T
	q.items[0] = zero // zero slot for GC
	q.items = q.items[1:]
	return front, true
}

// Remove removes the first item matching the predicate.
// Returns true if an item was removed.
func (q *SyncQueue[T]) Remove(match func(T) bool) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, item := range q.items {
		if match(item) {
			var zero T
			q.items[i] = zero
			q.items = append(q.items[:i], q.items[i+1:]...)
			return true
		}
	}
	return false
}

// Drain removes all items and returns them.
func (q *SyncQueue[T]) Drain() []T {
	q.mu.Lock()
	defer q.mu.Unlock()
	items := q.items
	q.items = nil
	return items
}

// Len returns the number of items in the queue.
func (q *SyncQueue[T]) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}
