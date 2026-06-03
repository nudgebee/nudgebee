package cache

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"nudgebee/relay-server/pkg/config"
)

// Cache is a simple key-value store with TTL support.
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// NewCache returns a redisCache if provider is "redis", otherwise a memCache.
func NewCache(cfg *config.Config, logger *slog.Logger) (Cache, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Cache.Provider))

	switch provider {
	case "redis":
		addr := fmt.Sprintf("%s:%d", cfg.Cache.Redis.Host, cfg.Cache.Redis.Port)
		client := redis.NewClient(&redis.Options{
			Addr:     addr,
			Username: cfg.Cache.Redis.Username,
			Password: cfg.Cache.Redis.Password,
		})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Ping(ctx).Err(); err != nil {
			logger.Warn("redis unavailable at startup, will reconnect automatically",
				"addr", addr, "error", err)
		} else {
			logger.Info("cache provider: redis", "addr", addr)
		}
		return &redisCache{client: client, logger: logger}, nil

	case "", "in_memory":
		logger.Info("cache provider: in_memory")
		c := &memCache{}
		go c.sweepExpired(5 * time.Minute)
		return c, nil

	default:
		return nil, fmt.Errorf("unsupported cache provider %q (expected \"redis\" or \"in_memory\")", cfg.Cache.Provider)
	}
}

// --- Redis implementation ---

const keyPrefix = "relay:ws:"

type redisCache struct {
	client *redis.Client
	logger *slog.Logger
}

func (r *redisCache) Get(ctx context.Context, key string) ([]byte, bool) {
	val, err := r.client.Get(ctx, keyPrefix+key).Bytes()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			r.logger.Warn("redis cache get error", "key", key, "error", err)
		}
		return nil, false
	}
	return val, true
}

func (r *redisCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return r.client.Set(ctx, keyPrefix+key, value, ttl).Err()
}

func (r *redisCache) Delete(ctx context.Context, key string) error {
	return r.client.Del(ctx, keyPrefix+key).Err()
}

// --- In-memory implementation ---

type memEntry struct {
	data    []byte
	expires time.Time
}

type memCache struct {
	m sync.Map
}

func (c *memCache) Get(_ context.Context, key string) ([]byte, bool) {
	v, ok := c.m.Load(key)
	if !ok {
		return nil, false
	}
	e := v.(memEntry)
	if time.Now().After(e.expires) {
		c.m.Delete(key)
		return nil, false
	}
	return e.data, true
}

func (c *memCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	c.m.Store(key, memEntry{data: value, expires: time.Now().Add(ttl)})
	return nil
}

func (c *memCache) Delete(_ context.Context, key string) error {
	c.m.Delete(key)
	return nil
}

// sweepExpired periodically removes expired entries to prevent memory leaks.
func (c *memCache) sweepExpired(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		c.m.Range(func(key, value any) bool {
			if e, ok := value.(memEntry); ok && now.After(e.expires) {
				c.m.Delete(key)
			}
			return true
		})
	}
}
