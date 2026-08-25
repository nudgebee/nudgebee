package metering

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizedCandidates(t *testing.T) {
	// Dated + dash-versioned real ID → date-stripped, dotted, and both.
	assert.Equal(t,
		[]string{"claude-haiku-4-5", "claude-haiku-4.5-20251001", "claude-haiku-4.5"},
		normalizedCandidates("claude-haiku-4-5-20251001"))

	// A dashed real ID whose dotted+dated form is what the catalog stores.
	assert.Contains(t, normalizedCandidates("claude-sonnet-4-5-20250929"), "claude-sonnet-4.5-20250929")

	// Single-digit version + date must NOT be mangled (the date is not a version).
	assert.NotContains(t, normalizedCandidates("claude-opus-4-20250514"), "claude-opus-4.20250514")

	// OpenAI's dashed date suffix (-YYYY-MM-DD) is stripped to the undated name, so a
	// client that pins a dated OpenAI model still prices against the catalog entry.
	assert.Contains(t, normalizedCandidates("gpt-4o-mini-2024-07-18"), "gpt-4o-mini")
	assert.Contains(t, normalizedCandidates("gpt-5-2025-08-07"), "gpt-5")

	// Already-canonical name yields no misleading candidates.
	assert.Empty(t, normalizedCandidates("gpt-4o-mini"))
}

func TestCostUSD_NormalizationFallback(t *testing.T) {
	// Catalog holds the NB-normalized (dotted, dated) name as a built-in (global) row...
	p := &Pricer{}
	p.cur.Store(&catalog{
		builtin: map[string]modelPrice{
			"claude-sonnet-4.5-20250929": {Input: 3, Output: 15},
		},
		byTenant: map[string]map[string]modelPrice{},
	})

	// ...a request carrying the provider's real dashed ID still prices via fallback.
	cost := p.CostUSD("", "claude-sonnet-4-5-20250929", 1_000_000, 1_000_000, 0, 0)
	assert.InDelta(t, 18.0, cost, 1e-9) // 3 (in) + 15 (out)

	// Exact match still works and an unknown model is still 0.
	assert.InDelta(t, 18.0, p.CostUSD("", "claude-sonnet-4.5-20250929", 1_000_000, 1_000_000, 0, 0), 1e-9)
	assert.Equal(t, 0.0, p.CostUSD("", "totally-unknown-model", 1_000_000, 0, 0, 0))
}

// TestCostUSD_TenantOverrideWinsAndIsolated locks the tenant-scoping contract: a tenant's own
// price override applies to that tenant, wins over the built-in, and NEVER leaks to another
// tenant (the bug when the pricer keyed on model_name only, after llm_model_pricing gained
// per-tenant rows).
func TestCostUSD_TenantOverrideWinsAndIsolated(t *testing.T) {
	const model = "google/gemma-3-27b-it-maas"
	p := &Pricer{}
	p.cur.Store(&catalog{
		builtin: map[string]modelPrice{
			model: {Input: 1, Output: 2}, // global rate → 3.0 for 1M+1M tokens
		},
		byTenant: map[string]map[string]modelPrice{
			"tenant-A": {model: {Input: 10, Output: 20}}, // A's override → 30.0
			"tenant-C": {"some-other-model": {Input: 99}},
		},
	})

	// Tenant A gets its override.
	assert.InDelta(t, 30.0, p.CostUSD("tenant-A", model, 1_000_000, 1_000_000, 0, 0), 1e-9)
	// Tenant B has no override → built-in; A's negotiated rate does NOT bleed over.
	assert.InDelta(t, 3.0, p.CostUSD("tenant-B", model, 1_000_000, 1_000_000, 0, 0), 1e-9)
	// Tenant C has an override for a DIFFERENT model → falls back to built-in for this one.
	assert.InDelta(t, 3.0, p.CostUSD("tenant-C", model, 1_000_000, 1_000_000, 0, 0), 1e-9)
	// Empty tenant (e.g. unattributed) resolves against built-ins only.
	assert.InDelta(t, 3.0, p.CostUSD("", model, 1_000_000, 1_000_000, 0, 0), 1e-9)
}
