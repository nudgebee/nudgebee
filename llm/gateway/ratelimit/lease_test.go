package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func concurrentLimiter(limits ...Limit) *Limiter {
	return New(NewMemStore(), fakeSource{limits}).WithLeases(NewMemLeaseStore())
}

func TestAcquire_CapsInFlightAndFreesOnRelease(t *testing.T) {
	l := concurrentLimiter(Limit{TenantID: "t", APIKeyID: "k", Metric: MetricConcurrent, Value: 2, Enabled: true})
	ctx := context.Background()
	s := Scope{TenantID: "t", APIKeyID: "k"}

	r1, ex := l.Acquire(ctx, s, "req-1")
	require.Nil(t, ex)
	_, ex = l.Acquire(ctx, s, "req-2")
	require.Nil(t, ex)
	_, ex = l.Acquire(ctx, s, "req-3")
	require.NotNil(t, ex, "a third request at once is over the cap of 2")
	assert.Equal(t, MetricConcurrent, ex.Metric)
	assert.Equal(t, "key", ex.Scope)

	r1()
	_, ex = l.Acquire(ctx, s, "req-4")
	assert.Nil(t, ex, "a finished request frees its slot")
}

func TestAcquire_ReleaseWorksAfterRequestContextIsCancelled(t *testing.T) {
	l := concurrentLimiter(Limit{TenantID: "t", Metric: MetricConcurrent, Value: 1, Enabled: true})
	ctx, cancel := context.WithCancel(context.Background())
	s := Scope{TenantID: "t"}

	release, ex := l.Acquire(ctx, s, "req-1")
	require.Nil(t, ex)
	cancel() // the client hung up mid-stream
	release()

	_, ex = l.Acquire(context.Background(), s, "req-2")
	assert.Nil(t, ex, "the slot is freed even though the request's context was cancelled")
}

// scopedSource returns tenant rows for every scope and key rows only for their key.
type scopedSource struct{ limits []Limit }

func (f scopedSource) LimitsFor(tenant, user, apiKey, model string) []Limit {
	var out []Limit
	for _, l := range f.limits {
		if l.APIKeyID == "" || l.APIKeyID == apiKey {
			out = append(out, l)
		}
	}
	return out
}

func TestAcquire_AllOrNothingAcrossScopes(t *testing.T) {
	l := New(NewMemStore(), scopedSource{[]Limit{
		{TenantID: "t", Metric: MetricConcurrent, Value: 5, Enabled: true},
		{TenantID: "t", APIKeyID: "k", Metric: MetricConcurrent, Value: 1, Enabled: true},
	}}).WithLeases(NewMemLeaseStore())
	ctx := context.Background()
	s := Scope{TenantID: "t", APIKeyID: "k"}

	_, ex := l.Acquire(ctx, s, "req-1")
	require.Nil(t, ex)
	_, ex = l.Acquire(ctx, s, "req-2") // tenant has room, key is full
	require.NotNil(t, ex)

	// The tenant slot taken for req-2 before the key refused it was given back: four
	// other keys can still use the tenant's remaining 4 slots.
	for i, key := range []string{"a", "b", "c", "d"} {
		_, ex = l.Acquire(ctx, Scope{TenantID: "t", APIKeyID: key}, "other-"+string(rune('0'+i)))
		assert.Nil(t, ex, "tenant slot %d should be free", i)
	}
}

func TestAcquire_IgnoresOtherMetricsAndCheckIgnoresConcurrent(t *testing.T) {
	l := concurrentLimiter(
		Limit{TenantID: "t", Metric: MetricRequests, Period: PeriodMinute, Value: 1, Enabled: true},
		Limit{TenantID: "t", Metric: MetricConcurrent, Value: 1, Enabled: true},
	)
	fixedNow(l, time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC))
	ctx := context.Background()
	s := Scope{TenantID: "t"}

	release, ex := l.Acquire(ctx, s, "req-1")
	require.Nil(t, ex)
	assert.Nil(t, l.Check(ctx, s), "Check counts requests, not slots")
	release()
	l.Reconcile(ctx, s, 100, 1) // must not touch the concurrent row
}

func TestAcquire_NoLeaseStoreOrNoLimitsIsAllowAll(t *testing.T) {
	l := New(NewMemStore(), fakeSource{[]Limit{{TenantID: "t", Metric: MetricConcurrent, Value: 1, Enabled: true}}})
	release, ex := l.Acquire(context.Background(), Scope{TenantID: "t"}, "a")
	assert.Nil(t, ex)
	assert.NotNil(t, release)
	release()

	var nilLimiter *Limiter
	release, ex = nilLimiter.Acquire(context.Background(), Scope{TenantID: "t"}, "a")
	assert.Nil(t, ex)
	release()
}

func TestMemLeaseStore_ExpiredSlotsAreDropped(t *testing.T) {
	s := NewMemLeaseStore()
	ctx := context.Background()
	ok, _ := s.Acquire(ctx, "k", "a", 1, time.Millisecond)
	require.True(t, ok)
	time.Sleep(5 * time.Millisecond) // a pod died holding "a"; its slot expires
	ok, _ = s.Acquire(ctx, "k", "b", 1, time.Minute)
	assert.True(t, ok)
}

func TestAcquire_ModelRowMatchesPinnedNameAndAlias(t *testing.T) {
	l := concurrentLimiter(Limit{TenantID: "t", Model: "anthropic/claude-haiku-4-5", Metric: MetricConcurrent, Value: 1, Enabled: true})
	ctx := context.Background()

	served := Scope{TenantID: "t", Provider: "anthropic", Model: "claude-haiku-4-5", RequestedModel: "fast"}
	_, ex := l.Acquire(ctx, served, "req-1")
	require.Nil(t, ex)
	_, ex = l.Acquire(ctx, Scope{TenantID: "t", Provider: "openai", Model: "gpt-4o", RequestedProvider: "anthropic", RequestedModel: "claude-haiku-4-5"}, "req-2")
	assert.NotNil(t, ex, "a call that asked for the capped model shares its slot")
	_, ex = l.Acquire(ctx, Scope{TenantID: "t", Provider: "openai", Model: "gpt-4o"}, "req-3")
	assert.Nil(t, ex, "another model is not capped")
}
