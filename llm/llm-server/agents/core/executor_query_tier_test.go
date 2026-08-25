package core

import (
	"testing"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
)

// setDownshift toggles the query model-downshift flag and returns a restore func.
func setDownshift(v bool) func() {
	prev := config.Config.LlmServerReact3QueryModelDownshiftEnabled
	config.Config.LlmServerReact3QueryModelDownshiftEnabled = v
	return func() { config.Config.LlmServerReact3QueryModelDownshiftEnabled = prev }
}

// TestIsTopLevelPlainRetrievalTurn covers the single classification that drives both
// the lean prompt variant and the query model downshift.
func TestIsTopLevelPlainRetrievalTurn(t *testing.T) {
	cases := []struct {
		name string
		req  NBAgentRequest
		want bool
	}{
		{"top-level query", NBAgentRequest{OriginalQuery: "list pods in the default namespace"}, true},
		{"top-level investigation", NBAgentRequest{OriginalQuery: "why is the api pod crashlooping"}, false},
		{"sub-agent brief (parent set)", NBAgentRequest{OriginalQuery: "list pods", AgentId: "a2", ParentAgentId: "a1"}, false},
		{"empty query → false (keep full/pro)", NBAgentRequest{}, false},
		{"investigation source overrides", NBAgentRequest{OriginalQuery: "list pods", ConversationSource: ConversationSourceInvestigation}, false},
		{"falls back to Query when OriginalQuery empty", NBAgentRequest{Query: "list pods"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, isTopLevelPlainRetrievalTurn(c.req))
		})
	}
}

// TestResolveModelTier is the core of #12: a top-level query on a Reasoning
// orchestrator downshifts to Summary ONLY when the flag is on; everything else
// keeps the agent's declared tier.
func TestResolveModelTier(t *testing.T) {
	reasoning := catTestCategorisedAgent{category: ModelTierReasoning}
	query := NBAgentRequest{OriginalQuery: "list pods in the default namespace"}
	investigation := NBAgentRequest{OriginalQuery: "why is the api pod crashlooping"}
	subAgentQuery := NBAgentRequest{OriginalQuery: "list pods", AgentId: "a2", ParentAgentId: "a1"}

	t.Run("flag off → base tier (no-op, byte-identical to today)", func(t *testing.T) {
		defer setDownshift(false)()
		assert.Equal(t, ModelTierReasoning, resolveModelTier(reasoning, query))
	})
	t.Run("flag on + top-level query + Reasoning → Summary", func(t *testing.T) {
		defer setDownshift(true)()
		assert.Equal(t, ModelTierSummary, resolveModelTier(reasoning, query))
	})
	t.Run("flag on + investigation → Reasoning (no downshift)", func(t *testing.T) {
		defer setDownshift(true)()
		assert.Equal(t, ModelTierReasoning, resolveModelTier(reasoning, investigation))
	})
	t.Run("flag on + sub-agent query → no downshift (not top-level)", func(t *testing.T) {
		defer setDownshift(true)()
		assert.Equal(t, ModelTierReasoning, resolveModelTier(reasoning, subAgentQuery))
	})
	t.Run("flag on + non-Reasoning agent → unchanged (only Reasoning downshifts)", func(t *testing.T) {
		defer setDownshift(true)()
		retrieval := catTestCategorisedAgent{category: ModelTierRetrieval}
		assert.Equal(t, ModelTierRetrieval, resolveModelTier(retrieval, query))
	})
}
