// Package cache is a thin, graceful-degradation wrapper over a Redis-or-in-memory
// cache for ticket-server. It mirrors the api-server common/cache.go pattern but is
// trimmed to what the create-meta path needs (Get/Set/Delete + tag invalidation by
// integration). The two services are separate Go modules, so the code can't be shared.
//
// Degradation contract: any read error returns a miss, and write/delete errors are
// logged and swallowed — a cache problem must never fail a create-meta request. When
// cache_provider is unset/"in_memory" the wrapper uses bigcache, so dev/test and any
// deployment without Redis work unchanged.
package cache

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"nudgebee/tickets-server/common"

	bigcache_store "github.com/allegro/bigcache/v3"
	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/lib/v4/store"
	"github.com/eko/gocache/store/bigcache/v4"
	redis_store "github.com/eko/gocache/store/redis/v4"
	"github.com/redis/go-redis/v9"
)

var (
	cacheManager  *cache.Cache[any]
	namespaces    = make(map[string]struct{})
	initOnce      sync.Once
	syncManagers  sync.Mutex
	integrationNS = "integration"
)

// initManager lazily builds the single backing store (redis or bigcache) the first
// time any namespace is created. Safe to call repeatedly.
func initManager() {
	initOnce.Do(func() {
		if common.Config.CacheProvider == "redis" {
			redisClient := redis.NewClient(&redis.Options{
				Addr:     fmt.Sprintf("%s:%d", common.Config.CacheRedisServerHost, common.Config.CacheRedisServerPort),
				Username: common.Config.CacheRedisUserName,
				Password: common.Config.CacheRedisUserPassword,
			})
			cacheManager = cache.New[any](redis_store.NewRedis(redisClient))
			slog.Info("ticket-server cache initialized", "provider", "redis", "addr", redisClient.Options().Addr)
			return
		}

		expiration := time.Duration(common.Config.CacheExpirationMinutes) * time.Minute
		if expiration <= 0 {
			expiration = 30 * time.Minute
		}
		cfg := bigcache_store.DefaultConfig(expiration)
		if common.Config.CacheInMemorySizeMb > 0 {
			cfg.HardMaxCacheSize = common.Config.CacheInMemorySizeMb
		}
		if common.Config.CacheInMemoryMaxEntries > 0 {
			cfg.MaxEntriesInWindow = common.Config.CacheInMemoryMaxEntries
		}
		client, err := bigcache_store.New(context.Background(), cfg)
		if err != nil {
			slog.Error("ticket-server cache: bigcache init failed; cache disabled", "error", err)
			return
		}
		cacheManager = cache.New[any](bigcache.NewBigcache(client))
		slog.Info("ticket-server cache initialized", "provider", "in_memory")
	})
}

// CreateNamespace registers a logical namespace. Idempotent.
func CreateNamespace(namespace string) {
	syncManagers.Lock()
	defer syncManagers.Unlock()
	initManager()
	namespaces[namespace] = struct{}{}
}

func ready(namespace string) bool {
	if cacheManager == nil {
		return false
	}
	_, ok := namespaces[namespace]
	return ok
}

// Get returns the cached bytes for (namespace, key). A miss — including any backing
// error (Redis down, deserialize failure, namespace not registered) — returns (nil, false).
// ctx is the request context so a Redis op is cancelled if the client disconnects.
func Get(ctx context.Context, namespace, key string) ([]byte, bool) {
	if !ready(namespace) {
		return nil, false
	}
	data, err := cacheManager.Get(ctx, namespace+":"+key)
	if err != nil || data == nil {
		return nil, false
	}
	switch v := data.(type) {
	case string:
		return []byte(v), true
	case []byte:
		return v, true
	}
	return nil, false
}

// Set stores value under (namespace, key) with ttl, tagged by integrationID so it can
// be invalidated wholesale on integration change. Errors are returned but callers are
// expected to treat caching as best-effort (log and continue).
func Set(ctx context.Context, namespace, key string, value []byte, ttl time.Duration, integrationID string) error {
	if !ready(namespace) {
		return fmt.Errorf("cache: namespace %q not ready", namespace)
	}
	opts := []store.Option{
		store.WithTags([]string{"namespace:" + namespace, integrationNS + ":" + integrationID}),
	}
	if ttl > 0 {
		opts = append(opts, store.WithExpiration(ttl))
	}
	return cacheManager.Set(ctx, namespace+":"+key, string(value), opts...)
}

// Delete removes a single (namespace, key) entry.
func Delete(ctx context.Context, namespace, key string) error {
	if !ready(namespace) {
		return fmt.Errorf("cache: namespace %q not ready", namespace)
	}
	return cacheManager.Delete(ctx, namespace+":"+key)
}

// DeleteByIntegration invalidates every entry tagged with the given integration,
// regardless of project — used on integration save/test and the bi-hourly sync so an
// edit is reflected immediately rather than waiting out the TTL.
func DeleteByIntegration(ctx context.Context, namespace, integrationID string) error {
	if !ready(namespace) {
		return fmt.Errorf("cache: namespace %q not ready", namespace)
	}
	return cacheManager.Invalidate(ctx, store.WithInvalidateTags([]string{integrationNS + ":" + integrationID}))
}
