package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSource struct{ limits []Limit }

// fakeSource ignores the scope arguments and returns a fixed set; the Limiter's own
// model guard is what these tests exercise.
func (f fakeSource) LimitsFor(tenant, user, apiKey, model string) []Limit { return f.limits }

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

func TestLimiter_KeyScopedCap(t *testing.T) {
	l := New(NewMemStore(), fakeSource{[]Limit{
		{TenantID: "t", UserID: "u", APIKeyID: "k1", Metric: MetricRequests, Period: PeriodMinute, Value: 1, Enabled: true},
	}})
	fixedNow(l, time.Date(2026, 7, 10, 14, 30, 0, 0, time.UTC))
	ctx := context.Background()

	assert.Nil(t, l.Check(ctx, Scope{TenantID: "t", UserID: "u", APIKeyID: "k1"}))
	ex := l.Check(ctx, Scope{TenantID: "t", UserID: "u", APIKeyID: "k1"})
	require.NotNil(t, ex)
	assert.Equal(t, "key", ex.Scope)
}

func TestLimiter_ModelScopedCapOnlyAppliesToThatModel(t *testing.T) {
	l := New(NewMemStore(), fakeSource{[]Limit{
		{TenantID: "t", UserID: "u", APIKeyID: "k1", Model: "gpt-5", Metric: MetricRequests, Period: PeriodMinute, Value: 1, Enabled: true},
	}})
	fixedNow(l, time.Date(2026, 7, 10, 14, 30, 0, 0, time.UTC))
	ctx := context.Background()
	gpt5 := Scope{TenantID: "t", UserID: "u", APIKeyID: "k1", Model: "gpt-5"}
	haiku := Scope{TenantID: "t", UserID: "u", APIKeyID: "k1", Model: "claude-haiku-4-5-20251001"}

	assert.Nil(t, l.Check(ctx, gpt5))
	ex := l.Check(ctx, gpt5)
	require.NotNil(t, ex)
	assert.Equal(t, "key+model", ex.Scope)
	// The other model shares the key but not the cap.
	assert.Nil(t, l.Check(ctx, haiku))
	assert.Nil(t, l.Check(ctx, haiku))
}

func TestLimiter_KeyCountersAreSeparate(t *testing.T) {
	l := New(NewMemStore(), fakeSource{[]Limit{
		{TenantID: "t", UserID: "u", Metric: MetricRequests, Period: PeriodMinute, Value: 10, Enabled: true},
	}})
	fixedNow(l, time.Date(2026, 7, 10, 14, 30, 0, 0, time.UTC))
	ctx := context.Background()
	// A user-scoped limit (APIKeyID == "") counts every key against one bucket.
	assert.Nil(t, l.Check(ctx, Scope{TenantID: "t", UserID: "u", APIKeyID: "k1"}))
	assert.Nil(t, l.Check(ctx, Scope{TenantID: "t", UserID: "u", APIKeyID: "k2"}))
}

func TestLimiter_ReconcileRespectsModelGuard(t *testing.T) {
	l := New(NewMemStore(), fakeSource{[]Limit{
		{TenantID: "t", UserID: "u", APIKeyID: "k1", Model: "gpt-5", Metric: MetricCost, Period: PeriodDay, Value: 1.0, Enabled: true},
	}})
	fixedNow(l, time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC))
	ctx := context.Background()
	other := Scope{TenantID: "t", UserID: "u", APIKeyID: "k1", Model: "claude-haiku-4-5-20251001"}

	// Spend on another model must not fill the gpt-5 bucket.
	l.Reconcile(ctx, other, 100, 5.00)
	assert.Nil(t, l.Check(ctx, Scope{TenantID: "t", UserID: "u", APIKeyID: "k1", Model: "gpt-5"}))
}

// #40339 / #40340: a model row matches the served target or the requested name, bare or
// "provider/model", and a provider prefix stays pinned to that provider.
func TestLimit_AppliesTo(t *testing.T) {
	served := Scope{Provider: "anthropic", Model: "claude-opus-4-8", RequestedProvider: "anthropic", RequestedModel: "nb-smart"}
	cases := []struct {
		model string
		want  bool
	}{
		{"", true},
		{"claude-opus-4-8", true},
		{"anthropic/claude-opus-4-8", true},
		{"Anthropic/Claude-Opus-4-8", true},
		{"nb-smart", true},
		{"anthropic/nb-smart", true},
		{"bedrock/claude-opus-4-8", false},
		{"claude-haiku-4-5", false},
		{"anthropic/claude-haiku-4-5", false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, Limit{Model: c.model}.appliesTo(served), c.model)
	}
	// An OpenRouter model id that itself contains "/" still matches on OpenRouter only.
	or := Scope{Provider: "openrouter", Model: "anthropic/claude-3.5-sonnet"}
	assert.True(t, Limit{Model: "openrouter/anthropic/claude-3.5-sonnet"}.appliesTo(or))
	assert.False(t, Limit{Model: "anthropic/claude-3.5-sonnet"}.appliesTo(Scope{Provider: "anthropic", Model: "claude-3.5-sonnet-x"}))
	// A model id whose "/" is not a provider (Hugging Face org) must still match literally.
	hf := Scope{Provider: "huggingface", Model: "Qwen/Qwen3.6-35B-A3B-FP8"}
	assert.True(t, Limit{Model: "Qwen/Qwen3.6-35B-A3B-FP8"}.appliesTo(hf))
}

func TestLimiter_AliasAndDirectShareModelCap(t *testing.T) {
	l := New(NewMemStore(), fakeSource{[]Limit{
		{TenantID: "t", APIKeyID: "k1", Model: "anthropic/claude-opus-4-8", Metric: MetricTokens, Period: PeriodMinute, Value: 10, Enabled: true},
	}})
	fixedNow(l, time.Date(2026, 7, 10, 14, 30, 0, 0, time.UTC))
	ctx := context.Background()
	alias := Scope{TenantID: "t", APIKeyID: "k1", Provider: "anthropic", Model: "claude-opus-4-8", RequestedModel: "nb-smart"}
	direct := Scope{TenantID: "t", APIKeyID: "k1", Provider: "anthropic", Model: "claude-opus-4-8", RequestedModel: "claude-opus-4-8"}

	l.Reconcile(ctx, alias, 20, 0)
	assert.NotNil(t, l.Check(ctx, direct), "tokens spent via the alias fill the opus bucket")
}

func TestLimiter_DisabledAllowsAll(t *testing.T) {
	var l *Limiter
	assert.Nil(t, l.Check(context.Background(), Scope{TenantID: "t"}))
	assert.False(t, l.Enabled())

	l2 := New(nil, nil) // no store
	assert.Nil(t, l2.Check(context.Background(), Scope{TenantID: "t"}))
}
