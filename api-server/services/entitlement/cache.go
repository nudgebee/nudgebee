package entitlement

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	bigcacheLib "github.com/allegro/bigcache/v3"
	gocache "github.com/eko/gocache/lib/v4/cache"
	bigcacheStore "github.com/eko/gocache/store/bigcache/v4"
)

// Cache provides caching for entitlement data
type Cache struct {
	usageCache      *gocache.Cache[[]byte]
	mu              sync.RWMutex
	featureMappings map[string]*FeatureMapping // In-memory cache for feature mappings
}

// NewCache creates a new entitlement cache
func NewCache() *Cache {
	usageConfig := bigcacheLib.DefaultConfig(1 * time.Minute) // Short TTL for usage
	usageConfig.HardMaxCacheSize = 5                          // 5 MB — stores small usage counts only
	usageClient, err := bigcacheLib.New(context.Background(), usageConfig)
	if err != nil {
		slog.Error("Failed to create usage cache, using in-memory fallback", "error", err)
		// Create with minimal config as fallback
		usageConfig.Shards = 1
		usageConfig.MaxEntriesInWindow = 100
		usageConfig.MaxEntrySize = 500
		usageConfig.HardMaxCacheSize = 1
		usageClient, err = bigcacheLib.New(context.Background(), usageConfig)
		if err != nil {
			panic(fmt.Sprintf("entitlement: unable to create cache: %v", err))
		}
	}
	usageStore := bigcacheStore.NewBigcache(usageClient)

	// Feature mappings use simple in-memory map (no TTL needed, rarely changes)
	return &Cache{
		usageCache:      gocache.New[[]byte](usageStore),
		featureMappings: make(map[string]*FeatureMapping),
	}
}

// GetFeatureMapping retrieves a cached feature mapping
func (c *Cache) GetFeatureMapping(dimension string) *FeatureMapping {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.featureMappings[dimension]
}

// SetFeatureMapping caches a feature mapping
func (c *Cache) SetFeatureMapping(dimension string, mapping *FeatureMapping) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.featureMappings[dimension] = mapping
}

// GetUsage retrieves cached usage count, returns -1 if not found
func (c *Cache) GetUsage(tenantID, dimension string, billingPeriod time.Time) int {
	key := usageCacheKey(tenantID, dimension, billingPeriod)
	data, err := c.usageCache.Get(context.Background(), key)
	if err != nil || data == nil {
		return -1
	}

	var usage int
	if _, err := fmt.Sscanf(string(data), "%d", &usage); err != nil {
		return -1
	}
	return usage
}

// SetUsage caches a usage count
func (c *Cache) SetUsage(tenantID, dimension string, billingPeriod time.Time, usage int) {
	key := usageCacheKey(tenantID, dimension, billingPeriod)
	if err := c.usageCache.Set(context.Background(), key, []byte(fmt.Sprintf("%d", usage))); err != nil {
		slog.Warn("entitlement: failed to set usage cache", "key", key, "error", err)
	}
}

// InvalidateUsage removes a usage entry from cache
func (c *Cache) InvalidateUsage(tenantID, dimension string, billingPeriod time.Time) {
	key := usageCacheKey(tenantID, dimension, billingPeriod)
	if err := c.usageCache.Delete(context.Background(), key); err != nil {
		slog.Warn("entitlement: failed to delete usage cache", "key", key, "error", err)
	}
}

// InvalidateAllUsage removes all usage entries for a tenant
func (c *Cache) InvalidateAllUsage(tenantID string) {
	// BigCache doesn't support prefix deletion, so we'll rely on TTL expiration
	// For immediate invalidation, we'd need to track keys or use a different store
}

func usageCacheKey(tenantID, dimension string, billingPeriod time.Time) string {
	return fmt.Sprintf("usage:%s:%s:%s", tenantID, dimension, billingPeriod.Format("2006-01"))
}
