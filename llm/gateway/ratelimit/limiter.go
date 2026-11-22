package ratelimit

import (
	"context"
	"log/slog"
	"time"
)

// Limiter enforces quotas against a CounterStore. Limits come from a source
// (metastore config), the store holds live counters.
type Limiter struct {
	store  CounterStore
	leases LeaseStore // slots for the concurrent metric; nil leaves it unenforced
	limits LimitSource
	now    func() time.Time // injectable for tests
}

// LimitSource returns the limits applicable to a scope (tenant + optional user, key, model).
type LimitSource interface {
	LimitsFor(tenant, user, apiKey, model string) []Limit
}

// New builds a Limiter. A nil store disables enforcement (allow-all).
func New(store CounterStore, limits LimitSource) *Limiter {
	return &Limiter{store: store, limits: limits, now: time.Now}
}

// WithLeases sets the store that holds concurrent-request slots and returns l.
func (l *Limiter) WithLeases(ls LeaseStore) *Limiter {
	l.leases = ls
	return l
}

// releaseTimeout bounds one release attempt; it runs after the client may be gone.
const releaseTimeout = 2 * time.Second

// Acquire takes a slot for every concurrent limit that applies to s, all or nothing.
// On success it returns a release func (never nil) that frees them; it must run when
// the request ends, and uses its own context because the request's may be cancelled by
// then. When a limit is full it frees what it took and returns that limit.
func (l *Limiter) Acquire(ctx context.Context, s Scope, member string) (func(), *Exceeded) {
	noop := func() {}
	if !l.Enabled() || l.leases == nil {
		return noop, nil
	}
	var held []string
	release := func() {
		for _, k := range held {
			l.releaseOne(k, member)
		}
	}
	for _, lim := range l.limits.LimitsFor(s.TenantID, s.UserID, s.APIKeyID, s.Model) {
		if !lim.Enabled || lim.Metric != MetricConcurrent || !lim.appliesTo(s) {
			continue
		}
		k := leaseKey(lim.TenantID, lim.UserID, lim.APIKeyID, lim.Model)
		ok, err := l.leases.Acquire(ctx, k, member, lim.intCap(), LeaseTTL)
		if err != nil {
			slog.Error("ratelimit: concurrency acquire failed — allowing", "error", err, "key", k)
			continue // fail-open, as Check does
		}
		if !ok {
			release()
			return noop, exceeded(lim)
		}
		held = append(held, k)
	}
	if len(held) == 0 {
		return noop, nil
	}
	return release, nil
}

// releaseOne frees one slot, retrying once; a slot that still fails expires after LeaseTTL.
func (l *Limiter) releaseOne(k, member string) {
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
		err := l.leases.Release(ctx, k, member)
		cancel()
		if err == nil {
			return
		}
		slog.Error("ratelimit: concurrency release failed", "error", err, "key", k, "attempt", attempt+1)
	}
}

// builderHook is an optionally-registered limiter builder. When none is registered
// it is nil, Build returns nil, and Limiter.Enabled() is nil-safe (allow-all), so
// enforcement is disabled.
var builderHook func() *Limiter

// RegisterBuilder registers a limiter builder. When none is registered, Build
// returns nil (enforcement disabled).
func RegisterBuilder(fn func() *Limiter) { builderHook = fn }

// Build returns a fully-wired Limiter from the registered builder, or nil when none
// is registered (enforcement disabled — allow-all). A nil *Limiter is allow-all
// (Enabled() is nil-safe), so callers can use the result directly.
func Build() *Limiter {
	if builderHook == nil {
		return nil
	}
	return builderHook()
}

// Close releases resources held by the limiter's limit source — e.g. a background
// refresh ticker. Safe to call on a nil Limiter (allow-all, nothing to release).
func (l *Limiter) Close() error {
	if l == nil {
		return nil
	}
	if c, ok := l.limits.(interface{ Close() error }); ok {
		return c.Close()
	}
	return nil
}

// Enabled reports whether enforcement is active (store + limits configured).
func (l *Limiter) Enabled() bool { return l != nil && l.store != nil && l.limits != nil }

// Check evaluates all applicable limits before the provider call. For the requests
// metric it atomically reserves one (so concurrent replicas can't both slip past);
// for tokens/cost it checks the already-accumulated value (their delta is only
// known post-response). Returns the first tripped limit, or nil to allow.
func (l *Limiter) Check(ctx context.Context, s Scope) *Exceeded {
	if !l.Enabled() {
		return nil
	}
	now := l.now()
	for _, lim := range l.limits.LimitsFor(s.TenantID, s.UserID, s.APIKeyID, s.Model) {
		// A model-scoped row only applies to that model. Guarding here (not only in the
		// store) keeps the rule in one place and testable without the EE index.
		if !lim.Enabled || !lim.appliesTo(s) {
			continue
		}
		k := key(lim.TenantID, lim.UserID, lim.APIKeyID, lim.Model, lim.Metric, lim.Period, now)
		switch lim.Metric {
		case MetricRequests:
			allowed, _, err := l.store.CheckAndIncr(ctx, k, 1, lim.intCap(), ttl(lim.Period))
			if err != nil {
				slog.Error("ratelimit: counter check failed — allowing", "error", err, "key", k)
				continue // fail-open: never block traffic on a limiter outage
			}
			if !allowed {
				return exceeded(lim)
			}
		case MetricTokens, MetricCost:
			cur, err := l.store.Get(ctx, k)
			if err != nil {
				slog.Error("ratelimit: counter read failed — allowing", "error", err, "key", k)
				continue
			}
			if cur >= lim.intCap() {
				return exceeded(lim)
			}
		}
	}
	return nil
}

// Reconcile adds the actual usage after a response: token counts and cost (USD).
// Only the tokens/cost limits configured for the scope are updated (requests were
// already counted in Check).
func (l *Limiter) Reconcile(ctx context.Context, s Scope, tokens int, costUSD float64) {
	if !l.Enabled() {
		return
	}
	now := l.now()
	for _, lim := range l.limits.LimitsFor(s.TenantID, s.UserID, s.APIKeyID, s.Model) {
		if !lim.Enabled || !lim.appliesTo(s) {
			continue
		}
		var delta int64
		switch lim.Metric {
		case MetricTokens:
			delta = int64(tokens)
		case MetricCost:
			delta = int64(costUSD * costScale)
		default:
			continue
		}
		if delta <= 0 {
			continue
		}
		k := key(lim.TenantID, lim.UserID, lim.APIKeyID, lim.Model, lim.Metric, lim.Period, now)
		if err := l.store.Add(ctx, k, delta, ttl(lim.Period)); err != nil {
			slog.Error("ratelimit: reconcile failed", "error", err, "key", k)
		}
	}
}

func exceeded(l Limit) *Exceeded {
	scope := "tenant"
	switch {
	case l.APIKeyID != "" && l.Model != "":
		scope = "key+model"
	case l.APIKeyID != "":
		scope = "key"
	case l.UserID != "":
		scope = "user"
	}
	return &Exceeded{Metric: l.Metric, Period: l.Period, Limit: l.Value, Scope: scope}
}
