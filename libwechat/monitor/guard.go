package monitor

import (
	"fmt"
	"sync"
	"time"
)

// SessionGuard prevents API calls for a configurable duration after a session
// expiration is detected.
type SessionGuard struct {
	mu            sync.RWMutex
	pauseUntil    time.Time
	pauseDuration time.Duration
}

// GuardOption configures a SessionGuard.
type GuardOption func(*SessionGuard)

// NewSessionGuard creates a SessionGuard with a default pause duration of 1 hour.
func NewSessionGuard(opts ...GuardOption) *SessionGuard {
	g := &SessionGuard{
		pauseDuration: time.Hour,
	}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// WithPauseDuration sets the pause duration after a session expiration.
func WithPauseDuration(d time.Duration) GuardOption {
	return func(g *SessionGuard) {
		g.pauseDuration = d
	}
}

// Check returns an error if the guard is currently paused, or nil if API calls are allowed.
func (g *SessionGuard) Check() error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if time.Now().Before(g.pauseUntil) {
		remaining := time.Until(g.pauseUntil)
		return fmt.Errorf("session paused for %s", remaining.Truncate(time.Second))
	}
	return nil
}

// Trigger activates the pause, setting pauseUntil to now + pauseDuration.
func (g *SessionGuard) Trigger() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pauseUntil = time.Now().Add(g.pauseDuration)
}

// IsPaused reports whether the guard is currently paused.
func (g *SessionGuard) IsPaused() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return time.Now().Before(g.pauseUntil)
}

// RemainingPause returns the remaining pause duration. Returns 0 if not paused.
func (g *SessionGuard) RemainingPause() time.Duration {
	g.mu.RLock()
	defer g.mu.RUnlock()

	remaining := time.Until(g.pauseUntil)
	if remaining < 0 {
		return 0
	}
	return remaining
}
