package proxy

import (
	"context"
	"net/http/httptest"
	"testing"

	"nudgebee/llm-gateway/auth"
	"nudgebee/llm-gateway/metering"
	"nudgebee/llm-gateway/ratelimit"
	"nudgebee/llm-gateway/routing"

	"github.com/gin-gonic/gin"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLimitSource mimics the EE store's per-key indexing (task 4): it only returns a
// limit when the scope passed in matches the limit's own tenant/user/key. Like the EE
// store it ignores the model — Check's own guard decides which model a row covers — so
// this proves ratelimitStage.Handle threads rc.Identity.APIKeyID into the Check call.
type fakeLimitSource struct{ limits []ratelimit.Limit }

func (f fakeLimitSource) LimitsFor(tenant, user, apiKey, model string) []ratelimit.Limit {
	var out []ratelimit.Limit
	for _, lim := range f.limits {
		if lim.TenantID != "" && lim.TenantID != tenant {
			continue
		}
		if lim.UserID != "" && lim.UserID != user {
			continue
		}
		if lim.APIKeyID != "" && lim.APIKeyID != apiKey {
			continue
		}
		out = append(out, lim)
	}
	return out
}

func newRateLimitTestContext(identity auth.Identity, model string) *RequestContext {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/anthropic/v1/messages", nil)
	return &RequestContext{
		Gin:      c,
		Ctx:      context.Background(),
		Identity: identity,
		Model:    model,
	}
}

// TestRatelimitStage_ScopesToAPIKeyAndRequestedModel locks the wiring for task 5:
// a limit configured for one (tenant, user, api key, model) tuple must trip only
// for requests carrying that exact key and the model the CLIENT asked for — not
// merely the tenant/user, and not a different key or a different requested model.
func TestRatelimitStage_ScopesToAPIKeyAndRequestedModel(t *testing.T) {
	limiter := ratelimit.New(ratelimit.NewMemStore(), fakeLimitSource{[]ratelimit.Limit{
		{TenantID: "t", UserID: "u", APIKeyID: "key-1", Model: "gpt-5",
			Metric: ratelimit.MetricRequests, Period: ratelimit.PeriodMinute, Value: 1, Enabled: true},
	}})
	s := ratelimitStage{limiter: limiter}

	// First request against key-1/gpt-5 consumes the one allowed slot.
	rc := newRateLimitTestContext(auth.Identity{TenantID: "t", UserID: "u", APIKeyID: "key-1"}, "gpt-5")
	stop, err := s.Handle(rc)
	require.NoError(t, err)
	assert.False(t, stop, "first request under the cap should pass")

	// Second request, same key + model, must be rejected (429).
	rc2 := newRateLimitTestContext(auth.Identity{TenantID: "t", UserID: "u", APIKeyID: "key-1"}, "gpt-5")
	stop2, err := s.Handle(rc2)
	require.NoError(t, err)
	assert.True(t, stop2, "second request over the cap should be rejected")
	assert.Equal(t, "rate_limited", rc2.RejectReason)

	// A different api key must not share key-1's bucket.
	rcOtherKey := newRateLimitTestContext(auth.Identity{TenantID: "t", UserID: "u", APIKeyID: "key-2"}, "gpt-5")
	stopOtherKey, err := s.Handle(rcOtherKey)
	require.NoError(t, err)
	assert.False(t, stopOtherKey, "a different api key must not be limited by key-1's cap")

	// The same key against a different REQUESTED model must not share the bucket —
	// quotas match what the client asked for (rc.Model), not a served/mapped model.
	rcOtherModel := newRateLimitTestContext(auth.Identity{TenantID: "t", UserID: "u", APIKeyID: "key-1"}, "claude-haiku-4-5")
	stopOtherModel, err := s.Handle(rcOtherModel)
	require.NoError(t, err)
	assert.False(t, stopOtherModel, "a different requested model must not be limited by the gpt-5 cap")
}

// A routing rule rewrites rc.Model to the resolved model before this stage runs; the
// cap on the model the client asked for must still apply, matching Reconcile.
func TestRatelimitStage_UsesRequestedModelAfterRouting(t *testing.T) {
	limiter := ratelimit.New(ratelimit.NewMemStore(), fakeLimitSource{[]ratelimit.Limit{
		{TenantID: "t", UserID: "u", APIKeyID: "key-1", Model: "gpt-5",
			Metric: ratelimit.MetricRequests, Period: ratelimit.PeriodMinute, Value: 1, Enabled: true},
	}})
	s := ratelimitStage{limiter: limiter}
	routed := func() *RequestContext {
		rc := newRateLimitTestContext(auth.Identity{TenantID: "t", UserID: "u", APIKeyID: "key-1"}, "gpt-5-mini")
		rc.Decision = routing.Decision{RequestedModel: "gpt-5", ResolvedModel: "gpt-5-mini"}
		return rc
	}

	stop, err := s.Handle(routed())
	require.NoError(t, err)
	assert.False(t, stop, "first routed request under the gpt-5 cap should pass")

	rc2 := routed()
	stop2, err := s.Handle(rc2)
	require.NoError(t, err)
	assert.True(t, stop2, "second routed request must hit the cap on the requested model")
	assert.Equal(t, "rate_limited", rc2.RejectReason)
}

// #40339: a quota saved as "provider/model" must cap the bare model the request names.
func TestRatelimitStage_ProviderQualifiedQuotaMatchesBareModel(t *testing.T) {
	limiter := ratelimit.New(ratelimit.NewMemStore(), fakeLimitSource{[]ratelimit.Limit{
		{TenantID: "t", UserID: "u", APIKeyID: "key-1", Model: "anthropic/claude-haiku-4-5",
			Metric: ratelimit.MetricRequests, Period: ratelimit.PeriodMinute, Value: 1, Enabled: true},
	}})
	s := ratelimitStage{limiter: limiter}
	call := func() *RequestContext {
		rc := newRateLimitTestContext(auth.Identity{TenantID: "t", UserID: "u", APIKeyID: "key-1"}, "claude-haiku-4-5")
		rc.Provider = schemas.Anthropic
		rc.Decision = routing.Decision{RequestedProvider: "anthropic", RequestedModel: "claude-haiku-4-5",
			ResolvedProvider: "anthropic", ResolvedModel: "claude-haiku-4-5"}
		return rc
	}

	stop, err := s.Handle(call())
	require.NoError(t, err)
	assert.False(t, stop, "first call under the cap should pass")
	rc2 := call()
	stop2, err := s.Handle(rc2)
	require.NoError(t, err)
	assert.True(t, stop2, "second call must hit the anthropic/claude-haiku-4-5 cap")
	assert.Equal(t, "rate_limited", rc2.RejectReason)
}

// A quota pinned to one provider must not cap the same model name on another provider.
func TestRatelimitStage_ProviderQualifiedQuotaKeepsProviderPinned(t *testing.T) {
	limiter := ratelimit.New(ratelimit.NewMemStore(), fakeLimitSource{[]ratelimit.Limit{
		{TenantID: "t", UserID: "u", APIKeyID: "key-1", Model: "bedrock/claude-haiku-4-5",
			Metric: ratelimit.MetricRequests, Period: ratelimit.PeriodMinute, Value: 1, Enabled: true},
	}})
	s := ratelimitStage{limiter: limiter}
	for i := 0; i < 2; i++ {
		rc := newRateLimitTestContext(auth.Identity{TenantID: "t", UserID: "u", APIKeyID: "key-1"}, "claude-haiku-4-5")
		rc.Provider = schemas.Anthropic
		stop, err := s.Handle(rc)
		require.NoError(t, err)
		assert.False(t, stop, "a bedrock-pinned quota must not cap anthropic traffic")
	}
}

// #40340: a tier alias that routes to a capped model must count against that model's quota.
func TestRatelimitStage_AliasCountsAgainstRoutedModel(t *testing.T) {
	limiter := ratelimit.New(ratelimit.NewMemStore(), fakeLimitSource{[]ratelimit.Limit{
		{TenantID: "t", UserID: "u", APIKeyID: "key-1", Model: "claude-opus-4-8",
			Metric: ratelimit.MetricRequests, Period: ratelimit.PeriodMinute, Value: 1, Enabled: true},
	}})
	s := ratelimitStage{limiter: limiter}
	id := auth.Identity{TenantID: "t", UserID: "u", APIKeyID: "key-1"}

	direct := newRateLimitTestContext(id, "claude-opus-4-8")
	direct.Provider = schemas.Anthropic
	direct.Decision = routing.Decision{RequestedProvider: "anthropic", RequestedModel: "claude-opus-4-8"}
	stop, err := s.Handle(direct)
	require.NoError(t, err)
	assert.False(t, stop, "first direct call under the cap should pass")

	alias := newRateLimitTestContext(id, "claude-opus-4-8")
	alias.Provider = schemas.Anthropic
	alias.Decision = routing.Decision{RequestedProvider: "anthropic", RequestedModel: "nb-smart",
		ResolvedProvider: "anthropic", ResolvedModel: "claude-opus-4-8"}
	stop2, err := s.Handle(alias)
	require.NoError(t, err)
	assert.True(t, stop2, "nb-smart routes to claude-opus-4-8, so it must hit the opus cap")
	assert.Equal(t, "rate_limited", alias.RejectReason)
}

// #40340: post-call token usage is metered against the model actually served, not the alias.
func TestMeter_ReconcilesAgainstServedModel(t *testing.T) {
	limiter := ratelimit.New(ratelimit.NewMemStore(), fakeLimitSource{[]ratelimit.Limit{
		{TenantID: "t", UserID: "u", APIKeyID: "key-1", Model: "claude-opus-4-8",
			Metric: ratelimit.MetricTokens, Period: ratelimit.PeriodMinute, Value: 10, Enabled: true},
	}})
	h := &handler{sink: &metering.CapturingSink{}, limiter: limiter}
	id := auth.Identity{TenantID: "t", UserID: "u", APIKeyID: "key-1"}
	rm := &reqMeta{provider: schemas.Anthropic, model: "claude-opus-4-8", path: "/v1/messages", identity: id,
		decision: routing.Decision{RequestedProvider: "anthropic", RequestedModel: "nb-smart",
			ResolvedProvider: "anthropic", ResolvedModel: "claude-opus-4-8"}}
	h.meter(context.Background(), rm, 200, nil,
		&schemas.BifrostPassthroughUsage{LLMUsage: &schemas.BifrostLLMUsage{PromptTokens: 20, TotalTokens: 20}}, nil)

	rc := newRateLimitTestContext(id, "claude-opus-4-8")
	rc.Provider = schemas.Anthropic
	stop, err := ratelimitStage{limiter: limiter}.Handle(rc)
	require.NoError(t, err)
	assert.True(t, stop, "20 tokens served by opus via nb-smart must count against the 10-token opus cap")
}
