package prompts

import (
	"fmt"
	"sync"
	"time"
)

// CacheEntry represents a cached prompt with expiration
type CacheEntry struct {
	Response  *PromptResponse
	ExpiresAt time.Time
}

// PromptCache is an in-memory cache for prompts
type PromptCache struct {
	entries map[string]*CacheEntry
	mu      sync.RWMutex
	ttl     time.Duration
}

// NewPromptCache creates a new cache with the specified TTL
func NewPromptCache(ttl time.Duration) *PromptCache {
	cache := &PromptCache{
		entries: make(map[string]*CacheEntry),
		ttl:     ttl,
	}

	// Start background cleanup goroutine
	go cache.cleanupExpired()

	return cache
}

// cacheKey generates a unique cache key for a prompt request
func cacheKey(req PromptRequest) string {
	return fmt.Sprintf("%s:%s:%s:%s",
		req.Name,
		req.Category,
		req.Provider,
		req.AccountID,
	)
}

// Get retrieves a prompt from cache
func (c *PromptCache) Get(req PromptRequest) (*PromptResponse, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := cacheKey(req)
	entry, exists := c.entries[key]

	if !exists {
		return nil, false
	}

	// Check if expired
	if time.Now().After(entry.ExpiresAt) {
		return nil, false
	}

	// Clone the response and update metadata
	response := &PromptResponse{
		Content: entry.Response.Content,
		Metadata: PromptMetadata{
			Version:      entry.Response.Metadata.Version,
			Provider:     entry.Response.Metadata.Provider,
			Category:     entry.Response.Metadata.Category,
			ConfigSource: entry.Response.Metadata.ConfigSource,
			ExperimentID: entry.Response.Metadata.ExperimentID,
			CacheHit:     true,
			LoadTimeMs:   0, // Will be set by loader
		},
	}

	return response, true
}

// Set stores a prompt in cache
func (c *PromptCache) Set(req PromptRequest, response *PromptResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := cacheKey(req)
	c.entries[key] = &CacheEntry{
		Response:  response,
		ExpiresAt: time.Now().Add(c.ttl),
	}
}

// Clear removes all entries from cache
func (c *PromptCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*CacheEntry)
}

// ClearKey removes a specific key from cache
func (c *PromptCache) ClearKey(req PromptRequest) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := cacheKey(req)
	delete(c.entries, key)
}

// ClearByPrompt removes all cache entries for a specific prompt
func (c *PromptCache) ClearByPrompt(promptName string, category PromptCategory) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Find all keys that match the prompt name and category
	keysToDelete := make([]string, 0)
	prefix := fmt.Sprintf("%s:%s:", promptName, category)

	for key := range c.entries {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			keysToDelete = append(keysToDelete, key)
		}
	}

	// Delete matching keys
	for _, key := range keysToDelete {
		delete(c.entries, key)
	}
}

// ClearByAccount removes all cache entries for a specific account
func (c *PromptCache) ClearByAccount(accountID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Find all keys that end with the account ID
	keysToDelete := make([]string, 0)
	suffix := ":" + accountID

	for key := range c.entries {
		if len(key) >= len(suffix) && key[len(key)-len(suffix):] == suffix {
			keysToDelete = append(keysToDelete, key)
		}
	}

	// Delete matching keys
	for _, key := range keysToDelete {
		delete(c.entries, key)
	}
}

// Size returns the number of entries in the cache
func (c *PromptCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.entries)
}

// cleanupExpired removes expired entries periodically
func (c *PromptCache) cleanupExpired() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		c.removeExpired()
	}
}

// removeExpired removes all expired entries
func (c *PromptCache) removeExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	keysToDelete := make([]string, 0)

	for key, entry := range c.entries {
		if now.After(entry.ExpiresAt) {
			keysToDelete = append(keysToDelete, key)
		}
	}

	for _, key := range keysToDelete {
		delete(c.entries, key)
	}
}
