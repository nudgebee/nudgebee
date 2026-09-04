package common

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"nudgebee/services/config"
	"slices"
	"strings"
	"sync"
	"time"

	bigcache_store "github.com/allegro/bigcache/v3"
	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/lib/v4/store"
	"github.com/eko/gocache/store/bigcache/v4"
	redis_store "github.com/eko/gocache/store/redis/v4"
	"github.com/redis/go-redis/v9"
	"github.com/samber/lo"
)

var cacheManager *cache.Cache[any]
var cacheClient any
var cacheManagers = make(map[string]cacheNamespaceOptions)

var syncCacheManagers sync.Mutex

type cacheNamespaceOptions struct {
	Expiration time.Duration
	MaxSizeMb  int
	MaxEntries int
}
type CacheNamespaceOption func(o *cacheNamespaceOptions)

func CacheNamespaceWithExpiration(expiration time.Duration) CacheNamespaceOption {
	return func(o *cacheNamespaceOptions) {
		o.Expiration = expiration
	}
}

func CacheNamespaceWithMaxSizeMb(size int) CacheNamespaceOption {
	return func(o *cacheNamespaceOptions) {
		o.MaxSizeMb = size
	}
}

func CacheNamespaceWithMaxEntries(size int) CacheNamespaceOption {
	return func(o *cacheNamespaceOptions) {
		o.MaxEntries = size
	}
}

func CacheCreateNamespace(namespace string, options ...CacheNamespaceOption) {
	syncCacheManagers.Lock()
	defer syncCacheManagers.Unlock()

	if _, ok := cacheManagers[namespace]; ok {
		return
	}

	cacheNamespaceOptions := cacheNamespaceOptions{
		Expiration: time.Duration(config.Config.CacheExpirationMinutes) * time.Minute,
		MaxSizeMb:  config.Config.CacheInMemorySizeMb,
		MaxEntries: config.Config.CacheInMemoryMaxEntries,
	}
	for _, option := range options {
		option(&cacheNamespaceOptions)
	}

	slog.Info("creating cache namespace", "namespace", namespace, "provider", config.Config.CacheProvider)

	if cacheManager == nil {
		if config.Config.CacheProvider == "redis" {
			redisClient := redis.NewClient(&redis.Options{
				Addr:     fmt.Sprintf("%s:%d", config.Config.CacheRedisServerHost, config.Config.CacheRedisServerPort),
				Username: config.Config.CacheRedisUserName,
				Password: config.Config.CacheRedisUserPassword,
			})
			redisStore := redis_store.NewRedis(redisClient)
			cacheManager = cache.New[any](redisStore)
			cacheClient = redisClient
		} else {
			defaultConfig := bigcache_store.DefaultConfig(time.Duration(config.Config.CacheExpirationMinutes) * time.Minute)
			if cacheNamespaceOptions.Expiration > 0 {
				defaultConfig.LifeWindow = cacheNamespaceOptions.Expiration
			}
			if cacheNamespaceOptions.MaxSizeMb > 0 {
				defaultConfig.HardMaxCacheSize = cacheNamespaceOptions.MaxSizeMb
			}
			if cacheNamespaceOptions.MaxEntries > 0 {
				defaultConfig.MaxEntriesInWindow = cacheNamespaceOptions.MaxEntries
			}
			cacheClientLocal, _ := bigcache_store.New(context.Background(), defaultConfig)
			bigcacheStore := bigcache.NewBigcache(cacheClientLocal)
			cacheManager = cache.New[any](bigcacheStore)
			cacheClient = cacheClientLocal
		}
	}
	cacheManagers[namespace] = cacheNamespaceOptions
}

// cacheNamespaceExpiration returns the TTL a namespace was registered with, or
// 0 when the namespace is unknown.
func cacheNamespaceExpiration(namespace string) time.Duration {
	syncCacheManagers.Lock()
	defer syncCacheManagers.Unlock()
	if options, ok := cacheManagers[namespace]; ok {
		return options.Expiration
	}
	return 0
}

type CacheHealthInfo struct {
	Err      error
	Provider string
}

func CacheHealthCheck(ctx context.Context) CacheHealthInfo {
	if cacheClient == nil {
		return CacheHealthInfo{Err: fmt.Errorf("cache not initialized"), Provider: "none"}
	}
	if redisClient, ok := cacheClient.(*redis.Client); ok {
		return CacheHealthInfo{Err: redisClient.Ping(ctx).Err(), Provider: "redis"}
	}
	// In-memory cache (bigcache) — always healthy if initialized
	return CacheHealthInfo{Provider: "in_memory"}
}

func cacheGetManager(namespace string) (*cache.Cache[any], error) {
	if _, ok := cacheManagers[namespace]; ok {
		return cacheManager, nil
	}
	return nil, fmt.Errorf("cache: namespace %s not found", namespace)
}

type CacheGetOption interface {
}

func CacheGet(namespace string, key string) ([]byte, bool) {
	cache, err := cacheGetManager(namespace)
	if err != nil {
		slog.Error("cache: unable to get cache manager", "error", err)
		return nil, false
	}
	data, err := cache.Get(context.Background(), namespace+":"+key)
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

type cacheSetOptions struct {
	Expiration time.Duration
	Tags       []string
}
type CacheSetOption func(o *cacheSetOptions)

func CacheSetWithExpiration(expiration time.Duration) CacheSetOption {
	return func(o *cacheSetOptions) {
		o.Expiration = expiration
	}
}
func CacheSetWithTags(tags ...string) CacheSetOption {
	return func(o *cacheSetOptions) {
		o.Tags = tags
	}
}

func CacheSet(namespace string, key string, value []byte, options ...CacheSetOption) error {
	cache, err := cacheGetManager(namespace)
	if err != nil {
		return err
	}
	storeOptions := []store.Option{}
	cacheOption := cacheSetOptions{}
	for _, option := range options {
		option(&cacheOption)
	}
	if cacheOption.Expiration == 0 {
		// Fall back to the TTL the namespace was registered with. Without this
		// the redis provider stores entries with no expiry at all: gocache
		// passes the expiration straight through to SET, where 0 means "keep
		// forever", and CacheCreateNamespace's expiration only ever reaches the
		// in-memory store's LifeWindow. Every missed invalidation then became
		// permanent instead of bounded by the declared TTL.
		cacheOption.Expiration = cacheNamespaceExpiration(namespace)
	}
	if cacheOption.Expiration > 0 {
		storeOptions = append(storeOptions, store.WithExpiration(cacheOption.Expiration))
	}
	if cacheOption.Tags == nil {
		cacheOption.Tags = []string{}
	}
	cacheOption.Tags = append(cacheOption.Tags, "namespace:"+namespace)
	storeOptions = append(storeOptions, store.WithTags(cacheOption.Tags))
	return cache.Set(context.Background(), namespace+":"+key, string(value), storeOptions...)
}

func CacheDelete(namespace string, key string) error {
	cache, err := cacheGetManager(namespace)
	if err != nil {
		return err
	}
	err = cache.Delete(context.Background(), namespace+":"+key)
	// Deleting a key that was never cached is the desired end state, not a failure.
	// The bigcache backend reports it as an error (redis DEL just returns 0), which
	// made every invalidation of an uncached entry log a warning plus a stack trace
	// on the normal path. Callers treat Delete as best-effort; none distinguishes
	// "absent" from "removed".
	if errors.Is(err, bigcache_store.ErrEntryNotFound) {
		return nil
	}
	return err
}

func CacheDeleteWithTag(namespace string, tags ...string) error {
	cache, err := cacheGetManager(namespace)
	if err != nil {
		return err
	}
	if len(tags) == 0 {
		// No tags to invalidate — a no-op. Never fall through to Invalidate with
		// an empty tag set, whose behavior is store-dependent (could wipe all).
		return nil
	}
	// Do NOT append the namespace tag here. Every entry is tagged with
	// "namespace:<ns>" at set time, and gocache tag-invalidation is an OR over
	// the given tags — so including it would invalidate the whole namespace
	// (every account/tenant) on every call, not just the entries matching the
	// caller's tags. The namespace tag is reserved for CacheClear.
	return cache.Invalidate(context.Background(), store.WithInvalidateTags(tags))
}

func CacheClear(namespace string) error {
	cache, err := cacheGetManager(namespace)
	if err != nil {
		return err
	}
	return cache.Invalidate(context.Background(), store.WithInvalidateTags([]string{"namespace:" + namespace}))
}

// CacheRedisDeleteNamespace wipes every key in shared Redis whose key starts
// with "<namespace>:". Unlike CacheClear it does NOT require the namespace
// to be registered in this process — it issues raw SCAN MATCH + DEL via the
// Redis client. This is what lets api-server clear cache namespaces owned by
// other services (e.g. llm-server's llm_tool_config) given the platform
// uses a single shared Redis.
//
// Returns (0, nil) when the cache provider is not "redis": the in-memory
// (bigcache) provider is per-process, so namespaces from other services
// are unreachable by design.
//
// Uses SCAN (cursor-based, COUNT 500) — never KEYS — so it never blocks the
// Redis event loop on large keyspaces.
func CacheRedisDeleteNamespace(namespace string) (int, error) {
	if config.Config.CacheProvider != "redis" {
		return 0, nil
	}
	redisClient, ok := cacheClient.(*redis.Client)
	if !ok || redisClient == nil {
		return 0, nil
	}
	ctx := context.Background()
	pattern := namespace + ":*"
	var cursor uint64
	var cleared int
	for {
		batch, next, err := redisClient.Scan(ctx, cursor, pattern, 500).Result()
		if err != nil {
			return cleared, fmt.Errorf("cache: scan %q: %w", pattern, err)
		}
		cursor = next
		if len(batch) > 0 {
			if err := redisClient.Del(ctx, batch...).Err(); err != nil {
				return cleared, fmt.Errorf("cache: del: %w", err)
			}
			cleared += len(batch)
		}
		if cursor == 0 {
			break
		}
	}
	return cleared, nil
}

func CacheListNamesapces() []string {
	return slices.Collect(maps.Keys(cacheManagers))
}

func CacheListKeys(namespace string) ([]string, error) {
	_, err := cacheGetManager(namespace)
	if err != nil {
		return nil, errors.New("cache: namespace not found")
	}
	keys := make([]string, 0)

	if config.Config.CacheProvider == "redis" {
		redisClient := cacheClient.(*redis.Client)
		keys := redisClient.Keys(context.Background(), namespace+":*").Val()
		keys = lo.Map(keys, func(key string, i int) string {
			return strings.TrimPrefix(key, namespace+":")
		})
		return keys, nil
	} else {
		bigCacheClient := cacheClient.(*bigcache_store.BigCache)
		iterator := bigCacheClient.Iterator()

		for iterator.SetNext() {
			k, err := iterator.Value()
			if err != nil {
				return nil, err
			}
			if strings.HasPrefix(k.Key(), namespace+":") {
				key := strings.TrimPrefix(k.Key(), namespace+":")
				keys = append(keys, key)
			}
		}
	}

	return keys, nil
}
