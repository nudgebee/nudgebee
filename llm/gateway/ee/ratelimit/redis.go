// Package ratelimit is the EE distributed rate-limit backend: the Redis-backed
// counter store (atomic across replicas), the config constructor that selects Redis
// vs the OSS in-memory store, and the metastore-backed limit loader. It wires these
// into an OSS *ratelimit.Limiter and registers a builder via ratelimit.RegisterBuilder
// in init(); the OSS build enforces no limits (Build returns nil, which is allow-all).
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"nudgebee/llm-gateway/config"
	"nudgebee/llm-gateway/ratelimit"

	"github.com/redis/go-redis/v9"
)

// checkAndIncrLua: if cur+delta <= cap, INCRBY and set EXPIRE on first write.
// KEYS[1]=key ARGV[1]=delta ARGV[2]=cap ARGV[3]=ttlSeconds. Returns {allowed, cur}.
var checkAndIncrLua = redis.NewScript(`
local cur = tonumber(redis.call('GET', KEYS[1]) or '0')
local delta = tonumber(ARGV[1])
if cur + delta > tonumber(ARGV[2]) then return {0, cur} end
local new = redis.call('INCRBY', KEYS[1], delta)
if new == delta then redis.call('EXPIRE', KEYS[1], ARGV[3]) end
return {1, new}
`)

// addLua: INCRBY key by delta and set EXPIRE only when the key was just created
// (new value == delta), atomically — so a crash can't leave a counter with no TTL.
// KEYS[1]=key ARGV[1]=delta ARGV[2]=ttlSeconds. Returns the new value.
var addLua = redis.NewScript(`
local new = redis.call('INCRBY', KEYS[1], tonumber(ARGV[1]))
if new == tonumber(ARGV[1]) then redis.call('EXPIRE', KEYS[1], ARGV[2]) end
return new
`)

type redisStore struct{ c *redis.Client }

// NewRedisStore wraps a redis client as a CounterStore.
func NewRedisStore(c *redis.Client) ratelimit.CounterStore { return &redisStore{c: c} }

func (s *redisStore) CheckAndIncr(ctx context.Context, key string, delta, capVal int64, ttl time.Duration) (bool, int64, error) {
	res, err := checkAndIncrLua.Run(ctx, s.c, []string{key}, delta, capVal, int(ttl.Seconds())).Int64Slice()
	if err != nil {
		return false, 0, err
	}
	return res[0] == 1, res[1], nil
}

func (s *redisStore) Get(ctx context.Context, key string) (int64, error) {
	v, err := s.c.Get(ctx, key).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return v, err
}

func (s *redisStore) Add(ctx context.Context, key string, delta int64, ttl time.Duration) error {
	// Atomic INCRBY + first-write EXPIRE via Lua, so a crash can't leave a key
	// without a TTL (which would strand a counter forever).
	return addLua.Run(ctx, s.c, []string{key}, delta, int(ttl.Seconds())).Err()
}

// NewStoreFromConfig builds the CounterStore for the configured cache provider:
// Redis (distributed, multi-replica) when cache_provider=redis and a host is set,
// otherwise the OSS in-memory store (single-pod/dev). Returns nil when no limiting
// backend is desired — but here we always return at least the in-memory store.
func NewStoreFromConfig() (ratelimit.CounterStore, error) {
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
	return ratelimit.NewMemStore(), nil
}
