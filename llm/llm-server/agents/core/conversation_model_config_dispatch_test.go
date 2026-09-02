package core

import (
	"testing"

	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// modelConfigDaoStub satisfies IConversationDao by embedding the interface;
// only the two updaters we exercise are implemented. Any other method would
// panic with a nil pointer — which is fine because applyConversationModelConfig
// only calls these two.
type modelConfigDaoStub struct {
	IConversationDao
	blanketCalls    []blanketCall
	tierCalls       []tierCall
	configSrcCalls  []configSourceCall
	clearCalls      []string
	blanketErr      error
	tierErr         error
	configSourceErr error
	clearErr        error
}

type blanketCall struct {
	conversationID, provider, model string
}
type tierCall struct {
	conversationID string
	overrides      ConversationTierOverrides
}
type configSourceCall struct {
	conversationID, configSource string
}

func (s *modelConfigDaoStub) UpdateConversationModelBlanket(conversationId, provider, model string) error {
	s.blanketCalls = append(s.blanketCalls, blanketCall{conversationId, provider, model})
	return s.blanketErr
}
func (s *modelConfigDaoStub) UpdateConversationTierOverrides(conversationId string, overrides ConversationTierOverrides) error {
	s.tierCalls = append(s.tierCalls, tierCall{conversationId, overrides})
	return s.tierErr
}
func (s *modelConfigDaoStub) UpdateConversationConfigSource(conversationId, configSource string) error {
	s.configSrcCalls = append(s.configSrcCalls, configSourceCall{conversationId, configSource})
	return s.configSourceErr
}

func TestApplyConversationModelConfig_TierWinsWhenBothSet(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	stub := &modelConfigDaoStub{}
	tierOverrides := ConversationTierOverrides{Picks: map[string]TierModelPick{
		"reasoning": {Provider: "googleai", Model: "gemini-2.5-pro"},
	}}

	applyConversationModelConfig(ctx, stub, "conv-1", "openai", "gpt-4", tierOverrides, "", false)

	require.Len(t, stub.tierCalls, 1, "tier dispatch must run")
	assert.Equal(t, "conv-1", stub.tierCalls[0].conversationID)
	assert.Equal(t, tierOverrides, stub.tierCalls[0].overrides)
	assert.Empty(t, stub.blanketCalls, "blanket dispatch must NOT run when tier is set")
}

func TestApplyConversationModelConfig_TierOnly(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	stub := &modelConfigDaoStub{}
	tierOverrides := ConversationTierOverrides{Picks: map[string]TierModelPick{
		"retrieval": {Provider: "openai", Model: "gpt-4o-mini"},
	}}

	applyConversationModelConfig(ctx, stub, "conv-2", "", "", tierOverrides, "", false)

	require.Len(t, stub.tierCalls, 1)
	assert.Empty(t, stub.blanketCalls)
}

func TestApplyConversationModelConfig_BlanketOnly(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	stub := &modelConfigDaoStub{}

	applyConversationModelConfig(ctx, stub, "conv-3", "anthropic", "claude-opus-4-7", ConversationTierOverrides{}, "", false)

	require.Len(t, stub.blanketCalls, 1, "blanket dispatch must run")
	assert.Equal(t, "conv-3", stub.blanketCalls[0].conversationID)
	assert.Equal(t, "anthropic", stub.blanketCalls[0].provider)
	assert.Equal(t, "claude-opus-4-7", stub.blanketCalls[0].model)
	assert.Empty(t, stub.tierCalls, "tier dispatch must NOT run when only blanket is set")
}

func TestApplyConversationModelConfig_NoopWhenBothEmpty(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	stub := &modelConfigDaoStub{}

	applyConversationModelConfig(ctx, stub, "conv-4", "", "", ConversationTierOverrides{}, "", false)

	assert.Empty(t, stub.blanketCalls)
	assert.Empty(t, stub.tierCalls)
}

func TestApplyConversationModelConfig_PartialBlanketIgnored(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	stub := &modelConfigDaoStub{}

	// Provider without model — too incomplete to act on (caller's responsibility).
	applyConversationModelConfig(ctx, stub, "conv-5", "openai", "", ConversationTierOverrides{}, "", false)

	assert.Empty(t, stub.blanketCalls, "must not dispatch with half-set blanket pair")
	assert.Empty(t, stub.tierCalls)
}

// The pin is orthogonal to the blanket/tier choice — it must persist regardless
// of which of those branches runs, including when neither does.
func TestApplyConversationModelConfig_ConfigSourcePersistsAlongsideBlanket(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	stub := &modelConfigDaoStub{}

	applyConversationModelConfig(ctx, stub, "conv-6", "huggingface", "Qwen3.6-35B", ConversationTierOverrides{}, "env:tier:summary", false)

	require.Len(t, stub.configSrcCalls, 1, "pin must be written")
	assert.Equal(t, "conv-6", stub.configSrcCalls[0].conversationID)
	assert.Equal(t, "env:tier:summary", stub.configSrcCalls[0].configSource)
	require.Len(t, stub.blanketCalls, 1, "blanket pick must still be written")
}

func TestApplyConversationModelConfig_ConfigSourceWithoutModelPick(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	stub := &modelConfigDaoStub{}

	// Pinning a slot without naming a model is legal — the slot's primary serves.
	applyConversationModelConfig(ctx, stub, "conv-7", "", "", ConversationTierOverrides{}, "db:int-1", false)

	require.Len(t, stub.configSrcCalls, 1)
	assert.Equal(t, "db:int-1", stub.configSrcCalls[0].configSource)
	assert.Empty(t, stub.blanketCalls)
	assert.Empty(t, stub.tierCalls)
}

func TestApplyConversationModelConfig_EmptyConfigSourceIsNoop(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	stub := &modelConfigDaoStub{}

	// An unpinned turn must not clear an existing pin — the write only fires
	// when a source is actually supplied.
	applyConversationModelConfig(ctx, stub, "conv-8", "openai", "gpt-4o", ConversationTierOverrides{}, "", false)

	assert.Empty(t, stub.configSrcCalls, "no pin supplied → no write, so a prior pin survives")
	require.Len(t, stub.blanketCalls, 1)
}

func (s *modelConfigDaoStub) ClearConversationModelConfig(conversationId string) error {
	s.clearCalls = append(s.clearCalls, conversationId)
	return s.clearErr
}

func TestApplyConversationModelConfig_ResetClearsEverything(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	stub := &modelConfigDaoStub{}

	// A reset arrives with nothing else set — the picker only sends it when the
	// selection is empty — but assert it wins even if something did tag along,
	// since a partial clear leaves a state the picker can't render.
	applyConversationModelConfig(ctx, stub, "conv-9", "openai", "gpt-4o",
		ConversationTierOverrides{Picks: map[string]TierModelPick{"summary": {Provider: "googleai", Model: "x"}}},
		"db:int-1", true)

	require.Len(t, stub.clearCalls, 1, "reset must clear the stored config")
	assert.Equal(t, "conv-9", stub.clearCalls[0])
	assert.Empty(t, stub.blanketCalls, "no blanket write alongside a reset")
	assert.Empty(t, stub.tierCalls, "no tier write alongside a reset")
	assert.Empty(t, stub.configSrcCalls, "no pin write alongside a reset")
}

func TestApplyConversationModelConfig_NoResetLeavesConfigAlone(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	stub := &modelConfigDaoStub{}

	applyConversationModelConfig(ctx, stub, "conv-10", "openai", "gpt-4o", ConversationTierOverrides{}, "", false)

	assert.Empty(t, stub.clearCalls, "an ordinary turn must never clear")
	require.Len(t, stub.blanketCalls, 1)
}
