package typing

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/CMGS/gua/libc/wechat/client"
)

const (
	minTTL         = 12 * time.Hour
	maxTTL         = 24 * time.Hour
	initialBackoff = 2 * time.Second
	maxBackoff     = 1 * time.Hour
)

type cacheEntry struct {
	ticket          string
	expiresAt       time.Time
	backoffDuration time.Duration
}

// ConfigCache caches per-user typing tickets obtained from GetConfig.
type ConfigCache struct {
	client  *client.Client
	mu      sync.RWMutex
	entries map[string]*cacheEntry
}

// NewConfigCache creates a new ConfigCache.
func NewConfigCache(c *client.Client) *ConfigCache {
	return &ConfigCache{
		client:  c,
		entries: make(map[string]*cacheEntry),
	}
}

// GetTicket returns a cached typing ticket for the user, or fetches a new one.
func (cc *ConfigCache) GetTicket(ctx context.Context, userID, contextToken string) (string, error) {
	cc.mu.RLock()
	entry, ok := cc.entries[userID]
	cc.mu.RUnlock()

	if ok && time.Now().Before(entry.expiresAt) && entry.ticket != "" {
		return entry.ticket, nil
	}

	cc.mu.Lock()
	defer cc.mu.Unlock()

	// Double-check after acquiring write lock.
	entry, ok = cc.entries[userID]
	if ok && time.Now().Before(entry.expiresAt) && entry.ticket != "" {
		return entry.ticket, nil
	}

	// In backoff: don't retry yet.
	if ok && entry.ticket == "" && time.Now().Before(entry.expiresAt) {
		return "", fmt.Errorf("getconfig for user %s: in backoff (retry after %s)", userID, entry.expiresAt.Format(time.RFC3339))
	}

	// Evict expired entries periodically during writes.
	cc.evictExpired()

	resp, err := cc.client.GetConfig(ctx, userID, contextToken)
	if err != nil {
		cc.applyBackoff(userID, entry)
		return "", fmt.Errorf("getconfig for user %s: %w", userID, err)
	}
	if resp.Ret != 0 {
		cc.applyBackoff(userID, entry)
		return "", &client.APIError{Ret: resp.Ret, ErrMsg: resp.ErrMsg}
	}

	ttl := minTTL + time.Duration(rand.Int64N(int64(maxTTL-minTTL))) //nolint:gosec // non-cryptographic use for cache TTL jitter
	cc.entries[userID] = &cacheEntry{
		ticket:    resp.TypingTicket,
		expiresAt: time.Now().Add(ttl),
	}
	return resp.TypingTicket, nil
}

// Invalidate removes the cached ticket for a user.
func (cc *ConfigCache) Invalidate(userID string) {
	cc.mu.Lock()
	delete(cc.entries, userID)
	cc.mu.Unlock()
}

func (cc *ConfigCache) applyBackoff(userID string, existing *cacheEntry) {
	backoff := initialBackoff
	if existing != nil && existing.backoffDuration > 0 {
		backoff = min(existing.backoffDuration*2, maxBackoff)
	}
	cc.entries[userID] = &cacheEntry{
		expiresAt:       time.Now().Add(backoff),
		backoffDuration: backoff,
	}
}

// evictExpired removes expired entries. Must be called with cc.mu held for write.
func (cc *ConfigCache) evictExpired() {
	now := time.Now()
	for k, v := range cc.entries {
		if now.After(v.expiresAt) {
			delete(cc.entries, k)
		}
	}
}
