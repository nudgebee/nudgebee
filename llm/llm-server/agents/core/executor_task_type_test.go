package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/security"
)

// TestClassifyTaskType exercises the flag-free turn classifier that drives the
// token_usage.task_type attribution column (V831). It must label top-level
// retrieval turns as query, top-level RCA turns as investigation, and leave
// sub-agent / degenerate turns unclassified ("").
func TestClassifyTaskType(t *testing.T) {
	tests := []struct {
		name    string
		request NBAgentRequest
		want    string
	}{
		{
			name:    "top-level plain retrieval → query",
			request: NBAgentRequest{Query: "list all pods in the default namespace"},
			want:    taskTypeQuery,
		},
		{
			name:    "top-level causal question → investigation",
			request: NBAgentRequest{Query: "why is the checkout service returning 500 errors"},
			want:    taskTypeInvestigation,
		},
		{
			name:    "investigation conversation source → investigation regardless of query",
			request: NBAgentRequest{Query: "list pods", ConversationSource: ConversationSourceInvestigation},
			want:    taskTypeInvestigation,
		},
		{
			name:    "OriginalQuery wins over Query for classification",
			request: NBAgentRequest{OriginalQuery: "why did the deployment roll back", Query: "get pods"},
			want:    taskTypeInvestigation,
		},
		{
			name:    "sub-agent turn (ParentAgentId set, differs from AgentId) → unclassified",
			request: NBAgentRequest{AgentId: "agent-1", ParentAgentId: "orchestrator-9", Query: "list pods"},
			want:    "",
		},
		{
			name:    "top-level when ParentAgentId equals AgentId → classified",
			request: NBAgentRequest{AgentId: "agent-1", ParentAgentId: "agent-1", Query: "show me the services"},
			want:    taskTypeQuery,
		},
		{
			name:    "empty query → unclassified",
			request: NBAgentRequest{},
			want:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, classifyTaskType(tt.request))
		})
	}
}

// TestApplyTaskTypeAttribution_RoundTrip verifies the stamp/read pair: the value
// classifyTaskType produces is stored on ContextKeyTaskType and read back by
// taskTypeFromContext (which the token-usage writer uses).
func TestApplyTaskTypeAttribution_RoundTrip(t *testing.T) {
	base := security.NewRequestContext(context.Background(), nil, nil, nil, nil)

	// A top-level query stamps "query" and reads back.
	q := applyTaskTypeAttribution(base, NBAgentRequest{Query: "list pods"})
	assert.Equal(t, taskTypeQuery, taskTypeFromContext(q))

	// A sub-agent turn stamps "" — and re-stamping over a parent context that
	// carried "query" must RESET to "", not inherit the parent's label.
	sub := applyTaskTypeAttribution(q, NBAgentRequest{AgentId: "a", ParentAgentId: "p", Query: "list pods"})
	assert.Equal(t, "", taskTypeFromContext(sub))

	// nil context is tolerated (mirrors applyPromptVariant).
	assert.Nil(t, applyTaskTypeAttribution(nil, NBAgentRequest{}))
}

// TestTierAttributionForRecord confirms the writer helper returns nil pointers
// when nothing is stamped (legacy rows stay NULL) and populated pointers when
// tier / task_type are present on the context.
func TestTierAttributionForRecord(t *testing.T) {
	// nil context → both nil (no panic).
	nilTier, nilTask := tierAttributionForRecord(nil)
	assert.Nil(t, nilTier)
	assert.Nil(t, nilTask)

	// Unstamped context → both nil.
	bare := security.NewRequestContext(context.Background(), nil, nil, nil, nil)
	tier, task := tierAttributionForRecord(bare)
	assert.Nil(t, tier)
	assert.Nil(t, task)

	// Stamped tier + task_type → both non-nil with the stamped values.
	stamped := security.NewRequestContext(
		context.WithValue(
			context.WithValue(context.Background(), ContextKeyModelTier, ModelTierSummary),
			ContextKeyTaskType, taskTypeQuery,
		),
		nil, nil, nil, nil,
	)
	tier, task = tierAttributionForRecord(stamped)
	if assert.NotNil(t, tier) {
		assert.Equal(t, string(ModelTierSummary), *tier)
	}
	if assert.NotNil(t, task) {
		assert.Equal(t, taskTypeQuery, *task)
	}
}
