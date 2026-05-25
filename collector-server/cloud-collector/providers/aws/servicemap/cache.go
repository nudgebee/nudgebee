package servicemap

import (
	"container/list"
	"nudgebee/collector/cloud/providers"
	"sync"
	"time"

	"log/slog"
)

// Cache provides caching for service map queries
type Cache interface {
	Get(key string) (*providers.ServiceMapApplication, bool)
	Set(key string, app *providers.ServiceMapApplication, ttl time.Duration)
	Delete(key string)
	Clear()
	Stats() CacheStats
}

// CacheStats provides metrics about cache performance
type CacheStats struct {
	Hits      int64
	Misses    int64
	Evictions int64
	Size      int
	MaxSize   int
	HitRate   float64
}

// InMemoryCache implements an LRU cache with TTL support
type InMemoryCache struct {
	mu        sync.RWMutex
	data      map[string]*cacheEntry
	lru       *list.List
	maxSize   int
	ttl       time.Duration
	hits      int64
	misses    int64
	evictions int64
}

type cacheEntry struct {
	key       string
	value     *providers.ServiceMapApplication
	expiresAt time.Time
	element   *list.Element
}

// NewInMemoryCache creates a new in-memory LRU cache
func NewInMemoryCache(maxSize int, defaultTTL time.Duration) *InMemoryCache {
	return &InMemoryCache{
		data:    make(map[string]*cacheEntry, maxSize),
		lru:     list.New(),
		maxSize: maxSize,
		ttl:     defaultTTL,
	}
}

// Get retrieves a value from cache
func (c *InMemoryCache) Get(key string) (*providers.ServiceMapApplication, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, exists := c.data[key]
	if !exists {
		c.misses++
		return nil, false
	}

	// Check if expired
	if time.Now().After(entry.expiresAt) {
		c.deleteEntry(entry)
		c.misses++
		return nil, false
	}

	// Move to front of LRU list
	c.lru.MoveToFront(entry.element)
	c.hits++

	return entry.value, true
}

// Set stores a value in cache with optional TTL
func (c *InMemoryCache) Set(key string, app *providers.ServiceMapApplication, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Use default TTL if not specified
	if ttl == 0 {
		ttl = c.ttl
	}

	// Check if key already exists
	if entry, exists := c.data[key]; exists {
		// Update existing entry
		entry.value = app
		entry.expiresAt = time.Now().Add(ttl)
		c.lru.MoveToFront(entry.element)
		return
	}

	// Evict if at capacity
	if c.lru.Len() >= c.maxSize {
		c.evictOldest()
	}

	// Create new entry
	entry := &cacheEntry{
		key:       key,
		value:     app,
		expiresAt: time.Now().Add(ttl),
	}

	entry.element = c.lru.PushFront(entry)
	c.data[key] = entry
}

// Delete removes a value from cache
func (c *InMemoryCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, exists := c.data[key]; exists {
		c.deleteEntry(entry)
	}
}

// Clear removes all entries from cache
func (c *InMemoryCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.data = make(map[string]*cacheEntry, c.maxSize)
	c.lru.Init()
}

// Stats returns cache statistics
func (c *InMemoryCache) Stats() CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	total := c.hits + c.misses
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(c.hits) / float64(total)
	}

	return CacheStats{
		Hits:      c.hits,
		Misses:    c.misses,
		Evictions: c.evictions,
		Size:      len(c.data),
		MaxSize:   c.maxSize,
		HitRate:   hitRate,
	}
}

// deleteEntry removes an entry from cache (must hold lock)
func (c *InMemoryCache) deleteEntry(entry *cacheEntry) {
	c.lru.Remove(entry.element)
	delete(c.data, entry.key)
}

// evictOldest removes the least recently used entry (must hold lock)
func (c *InMemoryCache) evictOldest() {
	if elem := c.lru.Back(); elem != nil {
		entry := elem.Value.(*cacheEntry)
		c.deleteEntry(entry)
		c.evictions++
	}
}

// CleanExpired removes expired entries (should be called periodically)
func (c *InMemoryCache) CleanExpired() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	count := 0

	// Iterate through all entries and remove expired ones
	for _, entry := range c.data {
		if now.After(entry.expiresAt) {
			c.deleteEntry(entry)
			count++
		}
	}

	return count
}

// StartCleanupWorker starts a background goroutine to periodically clean expired entries
func (c *InMemoryCache) StartCleanupWorker(interval time.Duration, stopChan <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			removed := c.CleanExpired()
			if removed > 0 {
				// Could log this if logger was available
				_ = removed
			}
		case <-stopChan:
			return
		}
	}
}

// TieredCache implements a two-tier cache (L1 in-memory, L2 optional distributed)
type TieredCache struct {
	l1     Cache
	l2     Cache // Optional, can be nil
	logger *slog.Logger
}

// NewTieredCache creates a new tiered cache
func NewTieredCache(l1 Cache, l2 Cache, logger *slog.Logger) *TieredCache {
	return &TieredCache{
		l1:     l1,
		l2:     l2,
		logger: logger,
	}
}

// Get retrieves from L1, falling back to L2
func (t *TieredCache) Get(key string) (*providers.ServiceMapApplication, bool) {
	// Try L1 first
	if app, found := t.l1.Get(key); found {
		return app, true
	}

	// Try L2 if available
	if t.l2 != nil {
		if app, found := t.l2.Get(key); found {
			// Promote to L1
			t.l1.Set(key, app, 5*time.Minute)
			return app, true
		}
	}

	return nil, false
}

// Set stores in both L1 and L2
func (t *TieredCache) Set(key string, app *providers.ServiceMapApplication, ttl time.Duration) {
	// Store in L1
	t.l1.Set(key, app, ttl)

	// Store in L2 if available (with longer TTL)
	if t.l2 != nil {
		l2TTL := ttl * 12 // L2 keeps data longer
		if l2TTL < 1*time.Hour {
			l2TTL = 1 * time.Hour
		}
		t.l2.Set(key, app, l2TTL)
	}
}

// Delete removes from both tiers
func (t *TieredCache) Delete(key string) {
	t.l1.Delete(key)
	if t.l2 != nil {
		t.l2.Delete(key)
	}
}

// Clear removes all entries from both tiers
func (t *TieredCache) Clear() {
	t.l1.Clear()
	if t.l2 != nil {
		t.l2.Clear()
	}
}

// Stats returns combined statistics
func (t *TieredCache) Stats() CacheStats {
	stats := t.l1.Stats()

	if t.l2 != nil {
		l2Stats := t.l2.Stats()
		stats.Hits += l2Stats.Hits
		stats.Misses += l2Stats.Misses
		stats.Evictions += l2Stats.Evictions

		// Recalculate hit rate
		total := stats.Hits + stats.Misses
		if total > 0 {
			stats.HitRate = float64(stats.Hits) / float64(total)
		}
	}

	return stats
}

// CacheKeyBuilder builds consistent cache keys from service map queries
type CacheKeyBuilder struct{}

// BuildKey creates a cache key for a service map application
func (b *CacheKeyBuilder) BuildKey(id providers.ServiceApplicationId) string {
	return id.Key()
}

// BuildQueryKey creates a cache key for an entire query
func (b *CacheKeyBuilder) BuildQueryKey(resources []ResourceRequest, region string) string {
	// Simple concatenation - could use hash for very long queries
	result := region + ":"
	for i, res := range resources {
		if i > 0 {
			result += ","
		}
		result += res.ResourceType + "/" + res.ResourceID
	}
	return result
}
