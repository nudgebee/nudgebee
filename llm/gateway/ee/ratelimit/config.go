package ratelimit

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"nudgebee/llm-gateway/common"
	"nudgebee/llm-gateway/ratelimit"
)

// LoaderFunc loads the current limit set from the metastore.
type LoaderFunc func() ([]ratelimit.Limit, error)

// LimitStore caches limits from the metastore, refreshed on a ticker. Implements
// ratelimit.LimitSource. Limits are indexed by tenant (tenant-wide, user_id="") and
// by tenant|user so LimitsFor is a map lookup, not a scan.
type LimitStore struct {
	load LoaderFunc
	cur  atomic.Pointer[limitIndex]
	done chan struct{}
	wg   sync.WaitGroup
}

type limitIndex struct {
	byTenant map[string][]ratelimit.Limit // user_id == ""
	byUser   map[string][]ratelimit.Limit // key: tenant + "|" + user
}

func newIndex(limits []ratelimit.Limit) *limitIndex {
	idx := &limitIndex{byTenant: map[string][]ratelimit.Limit{}, byUser: map[string][]ratelimit.Limit{}}
	for _, l := range limits {
		if !l.Enabled {
			continue
		}
		if l.UserID == "" {
			idx.byTenant[l.TenantID] = append(idx.byTenant[l.TenantID], l)
		} else {
			idx.byUser[l.TenantID+"|"+l.UserID] = append(idx.byUser[l.TenantID+"|"+l.UserID], l)
		}
	}
	return idx
}

// NewLimitStore loads limits immediately and refreshes them on interval.
func NewLimitStore(load LoaderFunc, refresh time.Duration) *LimitStore {
	s := &LimitStore{load: load, done: make(chan struct{})}
	s.rebuild()
	if refresh <= 0 {
		refresh = 30 * time.Second
	}
	s.wg.Add(1)
	go s.refreshLoop(refresh)
	return s
}

func (s *LimitStore) LimitsFor(tenant, user string) []ratelimit.Limit {
	idx := s.cur.Load()
	if idx == nil {
		return nil
	}
	out := idx.byTenant[tenant]
	if user != "" {
		if u := idx.byUser[tenant+"|"+user]; len(u) > 0 {
			out = append(append([]ratelimit.Limit{}, out...), u...)
		}
	}
	return out
}

func (s *LimitStore) rebuild() {
	limits, err := s.load()
	if err != nil {
		slog.Error("ratelimit: limit load failed", "error", err)
		if s.cur.Load() != nil {
			return // keep last good
		}
		limits = nil
	}
	s.cur.Store(newIndex(limits))
}

func (s *LimitStore) refreshLoop(interval time.Duration) {
	defer s.wg.Done()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.rebuild()
		case <-s.done:
			return
		}
	}
}

func (s *LimitStore) Close() error {
	close(s.done)
	s.wg.Wait()
	return nil
}

// DBLoader reads enabled limits from the metastore.
func DBLoader() LoaderFunc {
	return func() ([]ratelimit.Limit, error) {
		db, err := common.GetDatabaseManager(common.Metastore)
		if err != nil {
			return nil, err
		}
		var limits []ratelimit.Limit
		if err := db.QueryAndScan(&limits,
			`SELECT tenant_id, user_id, metric, period, limit_value, enabled
			   FROM llm_gateway_rate_limits WHERE enabled = true`); err != nil {
			return nil, err
		}
		return limits, nil
	}
}
