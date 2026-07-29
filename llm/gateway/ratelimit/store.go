package ratelimit

import (
	"context"
	"sync"
	"time"
)

// CounterStore tracks calendar-bucketed counters. Implementations are safe for
// concurrent use and must be atomic across replicas or within the process
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
