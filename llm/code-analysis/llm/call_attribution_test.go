package llm

import (
	"context"
	"testing"

	"nudgebee/code-analysis-agent/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func attributionClient(t *testing.T) *Client {
	t.Helper()
	cfg := &config.Config{}
	cfg.LLM.Provider = "googleai"
	cfg.LLM.Model = "test-model"
	return &Client{config: cfg}
}

// Every call must carry the tier of the client that made it, so spend can be
// split by role. Without this, code_analyzer rows were indistinguishable.
func TestRecordCallUsage_StampsClientTier(t *testing.T) {
	c := attributionClient(t)
	c.SetModelTier(ModelTierRetrieval)

	c.RecordCallUsage(TokenUsageCall{PromptTokens: 100, CompletionTokens: 10})

	calls, _ := c.SnapshotCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, ModelTierRetrieval, calls[0].ModelTier)
}

// An unstamped client must leave the field empty rather than guessing a tier —
// empty persists as NULL, which stays distinguishable from a real value.
func TestRecordCallUsage_UnstampedClientLeavesTierEmpty(t *testing.T) {
	c := attributionClient(t)

	c.RecordCallUsage(TokenUsageCall{PromptTokens: 100, CompletionTokens: 10})

	calls, _ := c.SnapshotCalls()
	require.Len(t, calls, 1)
	assert.Empty(t, calls[0].ModelTier)
}

// An explicit per-call tier wins over the client's, so a call made on a
// borrowed client can still report the tier it actually ran on.
func TestRecordCallUsage_ExplicitTierWins(t *testing.T) {
	c := attributionClient(t)
	c.SetModelTier(ModelTierReasoning)

	c.RecordCallUsage(TokenUsageCall{PromptTokens: 100, ModelTier: ModelTierSummary})

	calls, _ := c.SnapshotCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, ModelTierSummary, calls[0].ModelTier)
}

// The phase label rides on the context, so it reaches all four recording paths
// without changing any call signature.
func TestCallPhaseFromContext(t *testing.T) {
	assert.Empty(t, callPhaseFromContext(context.Background()), "main-loop calls carry no phase")
	//nolint:staticcheck // SA1012: passing nil is the case under test — the
	// helper must tolerate it rather than panic on an unset context.
	assert.Empty(t, callPhaseFromContext(nil))

	ctx := WithCallPhase(context.Background(), CallPhaseReflection)
	assert.Equal(t, CallPhaseReflection, callPhaseFromContext(ctx))

	// Innermost label wins, so a compaction inside a reflection is attributed
	// to the compaction.
	nested := WithCallPhase(ctx, CallPhaseCompaction)
	assert.Equal(t, CallPhaseCompaction, callPhaseFromContext(nested))
}

// A derived tier client's records must keep their own tier when they propagate
// up to the run client — the parent must not restamp them with its own.
func TestAppendCall_ParentPreservesChildTier(t *testing.T) {
	run := attributionClient(t)
	run.SetModelTier(ModelTierReasoning)
	derived := attributionClient(t)
	derived.SetModelTier(ModelTierRetrieval)
	derived.ShareUsageWith(run)

	derived.RecordCallUsage(TokenUsageCall{PromptTokens: 100, CompletionTokens: 10, TaskType: CallPhaseReflection})
	run.RecordCallUsage(TokenUsageCall{PromptTokens: 200, CompletionTokens: 20})

	parentCalls, _ := run.SnapshotCalls()
	require.Len(t, parentCalls, 2, "the run client must see both its own and the derived client's calls")
	assert.Equal(t, ModelTierRetrieval, parentCalls[0].ModelTier)
	assert.Equal(t, CallPhaseReflection, parentCalls[0].TaskType)
	assert.Equal(t, ModelTierReasoning, parentCalls[1].ModelTier)
	assert.Empty(t, parentCalls[1].TaskType)
}
