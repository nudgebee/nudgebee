package routing

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve_PassthroughWhenNoRules(t *testing.T) {
	e := NewEngine(nil)
	d := e.Resolve(Input{Provider: "anthropic", Model: "claude-opus-4-8"})
	assert.Equal(t, ReasonPassthrough, d.Reason)
	assert.Equal(t, "claude-opus-4-8", d.ResolvedModel)
	assert.Equal(t, "anthropic", d.ResolvedProvider)
	assert.Empty(t, d.RuleID)
}

func TestResolve_AliasRewrite(t *testing.T) {
	e := NewEngine([]Rule{{
		ID: "reasoning", Enabled: true, Priority: 10,
		Match:  Match{Provider: "anthropic", Model: "nb-reasoning"},
		Target: Target{Endpoint: Endpoint{Model: "claude-opus-4-8"}, Affinity: AffinityPrefixHash},
	}})
	d := e.Resolve(Input{Provider: "anthropic", Model: "nb-reasoning"})
	assert.Equal(t, ReasonAlias, d.Reason)
	assert.Equal(t, "reasoning", d.RuleID)
	assert.Equal(t, "nb-reasoning", d.RequestedModel)
	assert.Equal(t, "claude-opus-4-8", d.ResolvedModel)
	assert.Equal(t, "anthropic", d.ResolvedProvider)
	assert.Equal(t, AffinityPrefixHash, d.Strategy)
}

func TestResolve_NoMatchDifferentProvider(t *testing.T) {
	e := NewEngine([]Rule{{
		ID: "reasoning", Enabled: true,
		Match:  Match{Provider: "anthropic", Model: "nb-reasoning"},
		Target: Target{Endpoint: Endpoint{Model: "claude-opus-4-8"}},
	}})
	// Same alias but on the openai lane → no match → passthrough.
	d := e.Resolve(Input{Provider: "openai", Model: "nb-reasoning"})
	assert.Equal(t, ReasonPassthrough, d.Reason)
	assert.Equal(t, "nb-reasoning", d.ResolvedModel)
}

func TestResolve_TenantBeforeGlobalThenPriority(t *testing.T) {
	e := NewEngine([]Rule{
		{ID: "global-lo", Enabled: true, Priority: 100, Match: Match{Model: "m"}, Target: Target{Endpoint: Endpoint{Model: "global"}}},
		{ID: "tenant", Enabled: true, Priority: 100, TenantID: "t1", Match: Match{Model: "m"}, Target: Target{Endpoint: Endpoint{Model: "tenant"}}},
		{ID: "global-hi", Enabled: true, Priority: 1, Match: Match{Model: "m"}, Target: Target{Endpoint: Endpoint{Model: "global-hi"}}},
	})
	// Tenant rule wins even at equal/worse priority than global.
	assert.Equal(t, "tenant", e.Resolve(Input{Model: "m", TenantID: "t1"}).ResolvedModel)
	// Other tenant → global, lowest priority number first.
	assert.Equal(t, "global-hi", e.Resolve(Input{Model: "m", TenantID: "t2"}).ResolvedModel)
}

func TestResolve_DisabledRuleSkipped(t *testing.T) {
	e := NewEngine([]Rule{{
		ID: "off", Enabled: false,
		Match:  Match{Model: "m"},
		Target: Target{Endpoint: Endpoint{Model: "x"}},
	}})
	assert.Equal(t, ReasonPassthrough, e.Resolve(Input{Model: "m"}).Reason)
}

func TestValidate_AllowsCrossProvider(t *testing.T) {
	// P2: cross-provider substitution is valid (the proxy translates).
	assert.NoError(t, Validate([]Rule{{
		ID:     "cross",
		Match:  Match{Provider: "anthropic"},
		Target: Target{Endpoint: Endpoint{Provider: "openai", Model: "gpt-4o"}},
	}}))
}

func TestValidate_RejectsCrossProviderWithoutModel(t *testing.T) {
	// A cross-provider target with no model would forward the client's native model
	// name to the wrong provider — require an explicit target model.
	err := Validate([]Rule{{
		ID:     "nomodel",
		Match:  Match{Provider: "anthropic"},
		Target: Target{Endpoint: Endpoint{Provider: "gemini"}},
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires target.model")
}

func TestValidate_RejectsUnknownTargetProvider(t *testing.T) {
	err := Validate([]Rule{{
		ID:     "bogus",
		Match:  Match{Provider: "anthropic"},
		Target: Target{Endpoint: Endpoint{Provider: "not-a-provider", Model: "x"}},
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown target.provider")
}

func TestResolve_CrossProviderSubstitution(t *testing.T) {
	e := NewEngine([]Rule{{
		ID: "sub", Enabled: true,
		Match:  Match{Provider: "anthropic", Model: "claude-opus-4-8"},
		Target: Target{Endpoint: Endpoint{Provider: "gemini", Model: "gemini-3.1-flash"}},
	}})
	d := e.Resolve(Input{Provider: "anthropic", Model: "claude-opus-4-8"})
	assert.Equal(t, ReasonSubstitute, d.Reason)
	assert.Equal(t, "gemini", d.ResolvedProvider)
	assert.Equal(t, "gemini-3.1-flash", d.ResolvedModel)
	assert.Equal(t, "anthropic", d.RequestedProvider)
}

func TestValidate_AllowsSameFamilyAndEmptyTarget(t *testing.T) {
	assert.NoError(t, Validate([]Rule{
		{ID: "same", Match: Match{Provider: "anthropic"}, Target: Target{Endpoint: Endpoint{Provider: "anthropic", Model: "claude-opus-4-8"}}},
		{ID: "keep", Match: Match{Provider: "anthropic", Model: "nb-fast"}, Target: Target{Endpoint: Endpoint{Model: "claude-haiku-4-5-20251001"}}},
	}))
}

func TestValidate_DuplicateID(t *testing.T) {
	err := Validate([]Rule{
		{ID: "dup", Match: Match{Provider: "openai"}, Target: Target{Endpoint: Endpoint{Model: "gpt-4o"}}},
		{ID: "dup", Match: Match{Provider: "openai"}, Target: Target{Endpoint: Endpoint{Model: "gpt-4o-mini"}}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate id")
}
