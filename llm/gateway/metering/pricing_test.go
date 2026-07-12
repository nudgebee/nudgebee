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

	// Already-canonical name yields no misleading candidates.
	assert.Empty(t, normalizedCandidates("gpt-4o-mini"))
}

func TestCostUSD_NormalizationFallback(t *testing.T) {
	// Catalog holds the NB-normalized (dotted, dated) name...
	cat := map[string]modelPrice{
		"claude-sonnet-4.5-20250929": {Input: 3, Output: 15},
	}
	p := &Pricer{}
	p.cur.Store(&cat)

	// ...a request carrying the provider's real dashed ID still prices via fallback.
	cost := p.CostUSD("claude-sonnet-4-5-20250929", 1_000_000, 1_000_000, 0, 0)
	assert.InDelta(t, 18.0, cost, 1e-9) // 3 (in) + 15 (out)

	// Exact match still works and an unknown model is still 0.
	assert.InDelta(t, 18.0, p.CostUSD("claude-sonnet-4.5-20250929", 1_000_000, 1_000_000, 0, 0), 1e-9)
	assert.Equal(t, 0.0, p.CostUSD("totally-unknown-model", 1_000_000, 0, 0, 0))
}
