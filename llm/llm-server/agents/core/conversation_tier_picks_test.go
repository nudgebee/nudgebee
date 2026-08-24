package core

import (
	"testing"

	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The benchmark server encodes "use this config's own model" as a source with no
// provider/model. Requiring both discarded those picks, so run 549be3006f6c
// recorded gemma for summary+retrieval and executed the account defaults.
func TestTierOverrides_SourceOnlyPickIsKept(t *testing.T) {
	got, dropped := tierOverridesFromPicks(map[string]toolcore.TierModelPick{
		"summary":   {ConfigSource: "db:126f7502-0514-4c26-97c2-3843c49e126f"},
		"retrieval": {ConfigSource: "db:126f7502-0514-4c26-97c2-3843c49e126f"},
	})

	require.Empty(t, dropped)
	require.Len(t, got.Picks, 2)
	assert.Equal(t, "db:126f7502-0514-4c26-97c2-3843c49e126f", got.Picks["summary"].ConfigSource)
	// Model stays empty on purpose — the slot's own primary applies downstream.
	assert.Empty(t, got.Picks["summary"].Model)
	assert.Empty(t, got.Picks["summary"].Provider)
}

// The Nubi UI always sends a complete triple; that path must be untouched.
func TestTierOverrides_CompletePickIsKept(t *testing.T) {
	got, dropped := tierOverridesFromPicks(map[string]toolcore.TierModelPick{
		"reasoning": {Provider: "googleai", Model: "gemini-3.1-pro-preview", ConfigSource: "env:tier:reasoning"},
	})

	require.Empty(t, dropped)
	require.Len(t, got.Picks, 1)
	assert.Equal(t, "gemini-3.1-pro-preview", got.Picks["reasoning"].Model)
	assert.Equal(t, "googleai", got.Picks["reasoning"].Provider)
	assert.Equal(t, "env:tier:reasoning", got.Picks["reasoning"].ConfigSource)
}

// provider+model with no source was valid before per-tier credentials existed.
func TestTierOverrides_ProviderAndModelWithoutSourceIsKept(t *testing.T) {
	got, dropped := tierOverridesFromPicks(map[string]toolcore.TierModelPick{
		"summary": {Provider: "googleai", Model: "gemini-3.5-flash-lite"},
	})

	require.Empty(t, dropped)
	require.Len(t, got.Picks, 1)
	assert.Empty(t, got.Picks["summary"].ConfigSource)
}

// Half-set entries carry no usable instruction and must still be rejected —
// but reported, not silently swallowed.
func TestTierOverrides_HalfSetPicksAreDroppedAndReported(t *testing.T) {
	got, dropped := tierOverridesFromPicks(map[string]toolcore.TierModelPick{
		"summary":   {Provider: "googleai"},               // no model, no source
		"retrieval": {Model: "gemini-3.6-flash"},          // no provider, no source
		"reasoning": {ConfigSource: "env:tier:reasoning"}, // usable
	})

	assert.Len(t, got.Picks, 1, "only the source-bearing pick survives")
	assert.Contains(t, got.Picks, "reasoning")

	require.Len(t, dropped, 2)
	tiers := []string{dropped[0].Tier, dropped[1].Tier}
	assert.ElementsMatch(t, []string{"summary", "retrieval"}, tiers)
}

// One unusable pick must not take the others down with it — the failure mode in
// 549be3006f6c was per-tier, and reasoning applied while the other two did not.
func TestTierOverrides_DroppingOnePickLeavesOthersIntact(t *testing.T) {
	got, dropped := tierOverridesFromPicks(map[string]toolcore.TierModelPick{
		"summary":   {ConfigSource: "db:126f7502-0514-4c26-97c2-3843c49e126f"},
		"retrieval": {ConfigSource: "db:126f7502-0514-4c26-97c2-3843c49e126f"},
		"reasoning": {Provider: "googleai"}, // half-set
	})

	require.Len(t, dropped, 1)
	assert.Equal(t, "reasoning", dropped[0].Tier)
	assert.Len(t, got.Picks, 2)
	assert.Contains(t, got.Picks, "summary")
	assert.Contains(t, got.Picks, "retrieval")
}

func TestTierOverrides_EmptyInputYieldsNoPicks(t *testing.T) {
	got, dropped := tierOverridesFromPicks(nil)
	assert.False(t, got.HasAny())
	assert.Empty(t, dropped)
}

// HasAny and Get enforced the same provider+model rule as the intake filter, so
// a source-only pick was neither persisted nor returned to the resolver. Both
// had to change for run 549be3006f6c's filters to take effect.
func TestTierOverrides_HasAnyAcceptsSourceOnly(t *testing.T) {
	o := ConversationTierOverrides{Picks: map[string]TierModelPick{
		"summary": {ConfigSource: "db:126f7502-0514-4c26-97c2-3843c49e126f"},
	}}
	assert.True(t, o.HasAny(), "a source-only pick must count as present, or it is never saved")
}

func TestTierOverrides_GetReturnsSourceOnly(t *testing.T) {
	o := ConversationTierOverrides{Picks: map[string]TierModelPick{
		"summary": {ConfigSource: "db:126f7502-0514-4c26-97c2-3843c49e126f"},
	}}
	got, ok := o.Get("summary")
	require.True(t, ok, "tierPinFor calls Get; returning false here is why the tier fell back")
	assert.Equal(t, "db:126f7502-0514-4c26-97c2-3843c49e126f", got.ConfigSource)
	assert.Empty(t, got.Model, "the slot's own model applies when the pick names none")
}

func TestTierOverrides_HalfSetStillRejectedByHasAnyAndGet(t *testing.T) {
	o := ConversationTierOverrides{Picks: map[string]TierModelPick{
		"summary":   {Provider: "googleai"},
		"retrieval": {Model: "gemini-3.6-flash"},
	}}
	assert.False(t, o.HasAny())
	_, ok := o.Get("summary")
	assert.False(t, ok)
	_, ok = o.Get("retrieval")
	assert.False(t, ok)
}
