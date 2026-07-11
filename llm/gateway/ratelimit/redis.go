package ratelimit

import (
	"context"
	"fmt"
	"time"

	"nudgebee/llm-gateway/config"

	"github.com/redis/go-redis/v9"
)

// NewStoreFromConfig builds the CounterStore for the configured cache provider:
// Redis (distributed, multi-replica) when cache_provider=redis and a host is set,
// otherwise an in-memory store (single-pod/dev). Returns nil when no limiting
// backend is desired — but here we always return at least the in-memory store.
func NewStoreFromConfig() (CounterStore, error) {
	if config.Config.CacheProvider == "redis" && config.Config.CacheRedisServerHost != "" {
		c := redis.NewClient(&redis.Options{
			Addr:     fmt.Sprintf("%s:%d", config.Config.CacheRedisServerHost, config.Config.CacheRedisServerPort),
			Username: config.Config.CacheRedisUserName,
			Password: config.Config.CacheRedisUserPassword,
		})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.Ping(ctx).Err(); err != nil {
			return nil, fmt.Errorf("ratelimit: redis ping: %w", err)
		}
		return NewRedisStore(c), nil
	}
	return NewMemStore(), nil
}
