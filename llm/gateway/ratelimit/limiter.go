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
	limits LimitSource
	now    func() time.Time // injectable for tests
}

// LimitSource returns the limits applicable to a scope (tenant + optional user).
type LimitSource interface {
	LimitsFor(tenant, user string) []Limit
}

// New builds a Limiter. A nil store disables enforcement (allow-all).
func New(store CounterStore, limits LimitSource) *Limiter {
	return &Limiter{store: store, limits: limits, now: time.Now}
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
	for _, lim := range l.limits.LimitsFor(s.TenantID, s.UserID) {
		if !lim.Enabled {
			continue
		}
		k := key(lim.TenantID, lim.UserID, lim.Metric, lim.Period, now)
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
	for _, lim := range l.limits.LimitsFor(s.TenantID, s.UserID) {
		if !lim.Enabled {
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
		k := key(lim.TenantID, lim.UserID, lim.Metric, lim.Period, now)
		if err := l.store.Add(ctx, k, delta, ttl(lim.Period)); err != nil {
			slog.Error("ratelimit: reconcile failed", "error", err, "key", k)
		}
	}
}

func exceeded(l Limit) *Exceeded {
	scope := "tenant"
	if l.UserID != "" {
		scope = "user"
	}
	return &Exceeded{Metric: l.Metric, Period: l.Period, Limit: l.Value, Scope: scope}
}
