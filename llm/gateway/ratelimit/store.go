package ratelimit

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// CounterStore tracks calendar-bucketed counters. Implementations are safe for
// concurrent use and must be atomic across replicas (Redis) or within the process
// (in-memory dev fallback).
type CounterStore interface {
	// CheckAndIncr atomically increments key by delta only if current+delta <= cap,
	// setting ttl when the key is first created. Returns allowed=false (without
	// incrementing) when it would exceed the cap. Used for the requests metric.
	CheckAndIncr(ctx context.Context, key string, delta, capVal int64, ttl time.Duration) (allowed bool, current int64, err error)
	// Get returns the current counter (0 if absent). Used to pre-check token/cost
	// limits, whose delta is only known after the response.
	Get(ctx context.Context, key string) (int64, error)
	// Add unconditionally increments key by delta, setting ttl on create. Used to
	// reconcile token/cost counters post-response.
	Add(ctx context.Context, key string, delta int64, ttl time.Duration) error
}

// --- Redis ---

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
func NewRedisStore(c *redis.Client) CounterStore { return &redisStore{c: c} }

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

// --- in-memory (single-pod dev fallback) ---

type memEntry struct {
	val     int64
	expires time.Time
}

type memStore struct {
	mu sync.Mutex
	m  map[string]memEntry
}

// NewMemStore returns an in-process CounterStore. Not shared across replicas — use
// only for single-pod/dev.
func NewMemStore() CounterStore { return &memStore{m: map[string]memEntry{}} }

func (s *memStore) cur(key string, now time.Time) int64 {
	e, ok := s.m[key]
	if !ok || now.After(e.expires) {
		delete(s.m, key)
		return 0
	}
	return e.val
}

func (s *memStore) CheckAndIncr(_ context.Context, key string, delta, capVal int64, ttl time.Duration) (bool, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	c := s.cur(key, now)
	if c+delta > capVal {
		return false, c, nil
	}
	s.m[key] = memEntry{val: c + delta, expires: now.Add(ttl)}
	return true, c + delta, nil
}

func (s *memStore) Get(_ context.Context, key string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur(key, time.Now()), nil
}

func (s *memStore) Add(_ context.Context, key string, delta int64, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	c := s.cur(key, now)
	exp := now.Add(ttl)
	if e, ok := s.m[key]; ok && now.Before(e.expires) {
		exp = e.expires // keep the original window expiry
	}
	s.m[key] = memEntry{val: c + delta, expires: exp}
	return nil
}
