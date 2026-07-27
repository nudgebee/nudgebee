package routing

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultTierRules_Valid(t *testing.T) {
	require.NoError(t, Validate(DefaultTierRules()), "shipped defaults must pass rule validation")
}

func TestTierCatalog(t *testing.T) {
	cat := TierCatalog()
	require.Len(t, cat, 3)
	for _, td := range cat {
		assert.True(t, IsTierAlias(td.Alias), "catalog alias must be a tier alias")
		assert.NotEmpty(t, td.Description)
	}
	assert.False(t, IsTierAlias("gpt-5"))
	assert.False(t, IsTierAlias(""))

	// DefaultTierRules is derived from the catalog — same aliases + targets, in order.
	rules := DefaultTierRules()
	require.Len(t, rules, len(cat))
	for i, td := range cat {
		assert.Equal(t, td.Alias, rules[i].Match.Model)
		assert.Equal(t, td.Provider, rules[i].Target.Provider)
		assert.Equal(t, td.Model, rules[i].Target.Model)
	}
}

func TestDefaultTierRules_Resolve(t *testing.T) {
	e := NewEngine(DefaultTierRules())

	// Each tier resolves to its concrete provider/model, on the generic lane (no
	// addressed provider) — match.provider is empty so it matches regardless.
	cases := map[string]struct{ provider, model string }{
		"nb-fast":  {"gemini", "gemini-3.6-flash"},
		"nb-cheap": {"gemini", "gemini-3.5-flash-lite"},
		"nb-smart": {"anthropic", "claude-opus-4-8"},
	}
	for alias, want := range cases {
		t.Run(alias, func(t *testing.T) {
			d := e.Resolve(Input{Model: alias}) // Provider "" = generic endpoint
			assert.Equal(t, want.provider, d.ResolvedProvider)
			assert.Equal(t, want.model, d.ResolvedModel)
			assert.Equal(t, alias, d.RequestedModel, "requested model stays the tier token (metering)")
		})
	}

	// A tier also resolves when addressed on a native lane (any provider matches).
	d := e.Resolve(Input{Provider: "anthropic", Model: "nb-fast"})
	assert.Equal(t, "gemini", d.ResolvedProvider)

	// A non-tier model is untouched (passthrough).
	d = e.Resolve(Input{Provider: "openai", Model: "gpt-5"})
	assert.Equal(t, ReasonPassthrough, d.Reason)
	assert.Equal(t, "gpt-5", d.ResolvedModel)
}

func TestTierRules_TenantOverrideWins(t *testing.T) {
	// A tenant rule remaps nb-fast; tenant rules are evaluated before globals, so it
	// must win over the shipped default for that tenant only.
	override := Rule{
		ID: "t1-fast", TenantID: "t1", Priority: 0, Enabled: true,
		Match:  Match{Model: "nb-fast"},
		Target: Target{Endpoint: Endpoint{Provider: "openai", Model: "gpt-5-mini"}},
	}
	e := NewEngine(append([]Rule{override}, DefaultTierRules()...))

	d := e.Resolve(Input{TenantID: "t1", Model: "nb-fast"})
	assert.Equal(t, "openai", d.ResolvedProvider, "tenant override wins")
	assert.Equal(t, "gpt-5-mini", d.ResolvedModel)

	// Another tenant still gets the global default.
	d = e.Resolve(Input{TenantID: "t2", Model: "nb-fast"})
	assert.Equal(t, "gemini", d.ResolvedProvider)
	assert.Equal(t, "gemini-3.6-flash", d.ResolvedModel)
}
