package memory

import (
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"sync"
	"time"
)

const (
	cacheNamespaceSoul  = "mem.soul"
	cacheNamespacePrefs = "mem.prefs"
)

var (
	cacheInitOnce sync.Once
)

// initCaches registers the memory cache namespaces. Idempotent; safe to call
// from Compose init or tests. Uses config.MemoryCacheTTLSeconds.
func initCaches() {
	cacheInitOnce.Do(func() {
		ttl := time.Duration(config.Config.MemoryCacheTTLSeconds) * time.Second
		if ttl <= 0 {
			ttl = 5 * time.Minute
		}
		common.CacheCreateNamespace(cacheNamespaceSoul,
			common.CacheNamespaceWithExpiration(ttl),
			common.CacheNamespaceWithMaxEntries(10_000),
		)
		common.CacheCreateNamespace(cacheNamespacePrefs,
			common.CacheNamespaceWithExpiration(ttl),
			common.CacheNamespaceWithMaxEntries(10_000),
		)
	})
}

// soulCacheKey returns the cache key for a user's soul.
func soulCacheKey(tenantID, userID string) string {
	return fmt.Sprintf("%s:%s", tenantID, userID)
}

// prefsCacheKey returns the cache key for a user's prefs + module filter.
// Module is included because the rendered block differs per requesting module.
func prefsCacheKey(tenantID, userID, agentModule string) string {
	return fmt.Sprintf("%s:%s:%s", tenantID, userID, agentModule)
}

// invalidateSoulCache removes any cached soul block for a user.
func invalidateSoulCache(tenantID, userID string) {
	if err := common.CacheDelete(cacheNamespaceSoul, soulCacheKey(tenantID, userID)); err != nil {
		slog.Warn("memory: invalidateSoulCache failed", "error", err, "tenant", tenantID, "user", userID)
	}
}

// invalidatePrefsCache removes any cached prefs block for a user.
// Because cache entries are per-module, we clear common module variants.
func invalidatePrefsCache(tenantID, userID string) {
	for _, mod := range []string{"", "generic", "observability", "k8s_ops", "cloud_ops", "finops", "automation"} {
		key := prefsCacheKey(tenantID, userID, mod)
		if err := common.CacheDelete(cacheNamespacePrefs, key); err != nil {
			slog.Debug("memory: invalidatePrefsCache miss", "error", err, "key", key)
		}
	}
}
