package core

import (
	"context"
	"testing"

	llmruntime "nudgebee/llm/llms/runtime"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
)

func disableCachingValue(t *testing.T, ctx *security.RequestContext) bool {
	t.Helper()
	v, _ := ctx.GetContext().Value(llmruntime.ContextKeyDisableCaching).(bool)
	return v
}

func optedOutContext() *security.RequestContext {
	return security.NewRequestContext(
		context.WithValue(context.Background(), llmruntime.ContextKeyDisableCaching, true),
		nil, nil, nil, nil)
}

func TestSubAgentCacheContext_ClearsDelegatorOptOut(t *testing.T) {
	assert.False(t, disableCachingValue(t, subAgentCacheContext(optedOutContext())),
		"a delegator's opt-out must not reach the sub-agent it calls")
}

// The copy must not disturb the caller's own context: a delegator that makes its
// own LLM calls after delegating must still be opted out.
func TestSubAgentCacheContext_DoesNotMutateParent(t *testing.T) {
	parent := optedOutContext()
	_ = subAgentCacheContext(parent)
	assert.True(t, disableCachingValue(t, parent), "parent must keep its own opt-out")
}

// Fields other than the context must survive the copy.
func TestSubAgentCacheContext_PreservesOtherFields(t *testing.T) {
	sec := security.NewSecurityContextForSuperAdmin()
	parent := security.NewRequestContext(
		context.WithValue(context.Background(), llmruntime.ContextKeyDisableCaching, true),
		sec, nil, nil, nil)

	child := subAgentCacheContext(parent)
	assert.Same(t, sec, child.GetSecurityContext())
	assert.Equal(t, parent.GetTracer(), child.GetTracer())
	// GetLogger re-annotates with the caller's file and line on every call, so
	// only its presence is assertable here.
	assert.NotNil(t, child.GetLogger())
}

// The helper must not fabricate a context for callers that never opted out, and
// must tolerate the nil ctx that some tool paths still pass.
func TestSubAgentCacheContext_PassesThroughUntouched(t *testing.T) {
	plain := security.NewRequestContext(context.Background(), nil, nil, nil, nil)
	assert.Same(t, plain, subAgentCacheContext(plain))
	assert.Nil(t, subAgentCacheContext(nil))
}
