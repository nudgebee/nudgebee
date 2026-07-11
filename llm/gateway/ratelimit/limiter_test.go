package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSource struct{ limits []Limit }

func (f fakeSource) LimitsFor(tenant, user string) []Limit { return f.limits }

func fixedNow(l *Limiter, t time.Time) { l.now = func() time.Time { return t } }

func TestLimiter_RequestsCap(t *testing.T) {
	l := New(NewMemStore(), fakeSource{[]Limit{
		{TenantID: "t", Metric: MetricRequests, Period: PeriodMinute, Value: 2, Enabled: true},
	}})
	fixedNow(l, time.Date(2026, 7, 10, 14, 30, 0, 0, time.UTC))
	ctx := context.Background()
	s := Scope{TenantID: "t"}

	assert.Nil(t, l.Check(ctx, s)) // 1
	assert.Nil(t, l.Check(ctx, s)) // 2
	ex := l.Check(ctx, s)          // 3 → over
	require.NotNil(t, ex)
	assert.Equal(t, MetricRequests, ex.Metric)
	assert.Equal(t, "tenant", ex.Scope)
}

func TestLimiter_CostReconcile(t *testing.T) {
	l := New(NewMemStore(), fakeSource{[]Limit{
		{TenantID: "t", Metric: MetricCost, Period: PeriodDay, Value: 1.0, Enabled: true}, // $1/day
	}})
	fixedNow(l, time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC))
	ctx := context.Background()
	s := Scope{TenantID: "t"}

	assert.Nil(t, l.Check(ctx, s))    // nothing spent yet
	l.Reconcile(ctx, s, 100, 0.60)    // spend $0.60
	assert.Nil(t, l.Check(ctx, s))    // still under $1
	l.Reconcile(ctx, s, 100, 0.60)    // total $1.20
	assert.NotNil(t, l.Check(ctx, s)) // over → blocked
}

func TestLimiter_CalendarReset(t *testing.T) {
	l := New(NewMemStore(), fakeSource{[]Limit{
		{TenantID: "t", Metric: MetricRequests, Period: PeriodMinute, Value: 1, Enabled: true},
	}})
	ctx := context.Background()
	s := Scope{TenantID: "t"}

	fixedNow(l, time.Date(2026, 7, 10, 14, 30, 0, 0, time.UTC))
	assert.Nil(t, l.Check(ctx, s))
	assert.NotNil(t, l.Check(ctx, s)) // over within the same minute

	fixedNow(l, time.Date(2026, 7, 10, 14, 31, 0, 0, time.UTC)) // next minute bucket
	assert.Nil(t, l.Check(ctx, s), "new calendar window resets the counter")
}

func TestLimiter_TokensPreCheckThenReconcile(t *testing.T) {
	l := New(NewMemStore(), fakeSource{[]Limit{
		{TenantID: "t", Metric: MetricTokens, Period: PeriodHour, Value: 50, Enabled: true},
	}})
	fixedNow(l, time.Date(2026, 7, 10, 14, 0, 0, 0, time.UTC))
	ctx := context.Background()
	s := Scope{TenantID: "t"}

	assert.Nil(t, l.Check(ctx, s))
	l.Reconcile(ctx, s, 40, 0) // 40 tokens
	assert.Nil(t, l.Check(ctx, s), "40 < 50, still allowed")
	l.Reconcile(ctx, s, 20, 0) // 60 total, over
	assert.NotNil(t, l.Check(ctx, s))
}

func TestLimiter_UserScopeLabel(t *testing.T) {
	l := New(NewMemStore(), fakeSource{[]Limit{
		{TenantID: "t", UserID: "u", Metric: MetricRequests, Period: PeriodMinute, Value: 0, Enabled: true},
	}})
	fixedNow(l, time.Date(2026, 7, 10, 14, 30, 0, 0, time.UTC))
	ex := l.Check(context.Background(), Scope{TenantID: "t", UserID: "u"})
	require.NotNil(t, ex)
	assert.Equal(t, "user", ex.Scope)
}

func TestLimiter_DisabledAllowsAll(t *testing.T) {
	var l *Limiter
	assert.Nil(t, l.Check(context.Background(), Scope{TenantID: "t"}))
	assert.False(t, l.Enabled())

	l2 := New(nil, nil) // no store
	assert.Nil(t, l2.Check(context.Background(), Scope{TenantID: "t"}))
}
