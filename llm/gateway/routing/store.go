package routing

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Resolver resolves a request to a routing decision. Both *Engine (fixed rules)
// and *Store (config + DB, hot-reloaded) satisfy it.
type Resolver interface {
	Resolve(Input) Decision
}

// LoaderFunc loads the current DB rule set. nil means "no DB source".
type LoaderFunc func() ([]Rule, error)

// Store holds the live routing engine, combining static config-file rules with
// DB rules that are refreshed periodically and hot-swapped atomically. Precedence:
// within equal priority, tenant rules beat global (Engine), and DB rules beat
// config-file rules (DB is merged first, stable sort keeps it ahead).
type Store struct {
	static []Rule
	load   LoaderFunc // nil = static only
	cur    atomic.Pointer[Engine]
	done   chan struct{}
	wg     sync.WaitGroup
}

// NewStore compiles the static rules immediately (so routing works with no DB),
// then, if a loader is given, merges DB rules and refreshes them on interval.
func NewStore(static []Rule, load LoaderFunc, refresh time.Duration) *Store {
	s := &Store{static: static, load: load, done: make(chan struct{})}
	s.rebuild()
	if load != nil {
		if refresh <= 0 {
			refresh = 30 * time.Second
		}
		s.wg.Add(1)
		go s.refreshLoop(refresh)
	}
	return s
}

func (s *Store) Resolve(in Input) Decision {
	if e := s.cur.Load(); e != nil {
		return e.Resolve(in)
	}
	return (*Engine)(nil).Resolve(in) // passthrough safety
}

// rebuild reloads DB rules (if any), merges with static, and swaps the engine.
// On DB error it keeps the current engine (or falls back to static on first load).
func (s *Store) rebuild() {
	merged := make([]Rule, 0, len(s.static))
	if s.load != nil {
		dbRules, err := s.load()
		if err != nil {
			slog.Error("routing: DB rule load failed", "error", err)
			if s.cur.Load() != nil {
				return // keep serving the last good engine
			}
		} else {
			merged = append(merged, dbRules...) // DB first → wins ties over config file
		}
	}
	merged = append(merged, s.static...)
	s.cur.Store(NewEngine(merged))
}

func (s *Store) refreshLoop(interval time.Duration) {
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

// Close stops the background refresher.
func (s *Store) Close() error {
	close(s.done)
	s.wg.Wait()
	return nil
}
