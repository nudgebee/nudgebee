package ratelimit

import (
	"context"
	"sync"
	"time"
)

// LeaseTTL bounds how long a slot can outlive its request: a pod that dies mid-request
// never releases, so its slots expire on their own after this long.
const LeaseTTL = 10 * time.Minute

// LeaseStore holds the slots of in-flight requests for the concurrent metric. Unlike a
// CounterStore it has no time window: a slot lives from Acquire to Release (or LeaseTTL).
type LeaseStore interface {
	// Acquire adds member to key if fewer than capVal unexpired members are present,
	// atomically, and returns false without adding when the key is full.
	Acquire(ctx context.Context, key, member string, capVal int64, ttl time.Duration) (bool, error)
	// Release removes member from key. Removing an absent member is not an error.
	Release(ctx context.Context, key, member string) error
}

// leaseKey is the slot set for a concurrent limit's scope. No period or bucket: the
// same set is used for the whole life of a request.
// rl:{tenant}:{user|-}:{key|-}:{model|-}:concurrent
func leaseKey(tenant, user, apiKey, model string) string {
	if user == "" {
		user = "-"
	}
	if apiKey == "" {
		apiKey = "-"
	}
	if model == "" {
		model = "-"
	}
	return "rl:" + tenant + ":" + user + ":" + apiKey + ":" + model + ":" + string(MetricConcurrent)
}

// --- in-memory (single-pod dev fallback) ---

type memLeaseStore struct {
	mu sync.Mutex
	m  map[string]map[string]time.Time // key → member → expiry
}

// NewMemLeaseStore returns an in-process LeaseStore. Not shared across replicas, so a
// cap holds per pod only.
func NewMemLeaseStore() LeaseStore { return &memLeaseStore{m: map[string]map[string]time.Time{}} }

func (s *memLeaseStore) Acquire(_ context.Context, key, member string, capVal int64, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	set := s.m[key]
	if set == nil {
		set = map[string]time.Time{}
		s.m[key] = set
	}
	for mem, exp := range set {
		if now.After(exp) {
			delete(set, mem)
		}
	}
	if int64(len(set)) >= capVal {
		return false, nil
	}
	set[member] = now.Add(ttl)
	return true, nil
}

func (s *memLeaseStore) Release(_ context.Context, key, member string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if set := s.m[key]; set != nil {
		delete(set, member)
		if len(set) == 0 {
			delete(s.m, key)
		}
	}
	return nil
}
