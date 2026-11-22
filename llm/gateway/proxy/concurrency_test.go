package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm-gateway/auth"
	"nudgebee/llm-gateway/metering"
	"nudgebee/llm-gateway/ratelimit"
)

// refuseStage stands in for a stage after ratelimit that rejects (no credentials, a
// detected secret): the request already holds its slot when it is refused.
type refuseStage struct{}

func (refuseStage) Name() string { return "refuse" }
func (refuseStage) Handle(rc *RequestContext) (bool, error) {
	rc.Gin.AbortWithStatus(http.StatusForbidden)
	return true, nil
}

func postChat(t *testing.T, h *handler) int {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", chatCompletionsPath, strings.NewReader(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`))
	c.Set("nb_identity", auth.Identity{TenantID: "t", UserID: "u", APIKeyID: "k"})
	h.handleChat(c)
	return rec.Code
}

func TestConcurrency_SlotFreedWhenALaterStageRefuses(t *testing.T) {
	limiter := ratelimit.New(ratelimit.NewMemStore(), fakeLimitSource{[]ratelimit.Limit{
		{TenantID: "t", APIKeyID: "k", Metric: ratelimit.MetricConcurrent, Value: 1, Enabled: true},
	}}).WithLeases(ratelimit.NewMemLeaseStore())
	h := &handler{sink: &metering.CapturingSink{}, pipeline: NewPipeline(ratelimitStage{limiter: limiter}, refuseStage{})}

	assert.Equal(t, http.StatusForbidden, postChat(t, h))
	// With a cap of 1, a leaked slot would turn the second request into a 429.
	assert.Equal(t, http.StatusForbidden, postChat(t, h), "the refused request gave its slot back")
}

func TestConcurrency_RejectDoesNotUseUpRequestsQuota(t *testing.T) {
	limiter := ratelimit.New(ratelimit.NewMemStore(), fakeLimitSource{[]ratelimit.Limit{
		{TenantID: "t", APIKeyID: "k", Metric: ratelimit.MetricRequests, Period: ratelimit.PeriodMinute, Value: 1, Enabled: true},
		{TenantID: "t", APIKeyID: "k", Metric: ratelimit.MetricConcurrent, Value: 1, Enabled: true},
	}}).WithLeases(ratelimit.NewMemLeaseStore())
	h := &handler{sink: &metering.CapturingSink{}, pipeline: NewPipeline(ratelimitStage{limiter: limiter}, refuseStage{})}

	// Another request from the same key is in flight.
	release, ex := limiter.Acquire(context.Background(), ratelimit.Scope{TenantID: "t", UserID: "u", APIKeyID: "k", Model: "gpt-4o-mini"}, "in-flight")
	require.Nil(t, ex)
	assert.Equal(t, http.StatusTooManyRequests, postChat(t, h), "one too many in flight")
	release()

	// The refused request did not count toward the 1-per-minute requests limit.
	assert.Equal(t, http.StatusForbidden, postChat(t, h))
}
