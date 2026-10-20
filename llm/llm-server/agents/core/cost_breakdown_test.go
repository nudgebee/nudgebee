package core

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"

	llmruntime "nudgebee/llm/llms/runtime"
)

func sumComponents(b llmruntime.CostBreakdown) float64 {
	var s float64
	for _, c := range b.Components {
		s += c.CostUsd
	}
	return s
}

// TestCalculateCostBreakdown_ReconcilesStandard verifies the per-component split
// sums exactly to CalculateTotalCost on the standard tier, and labels it.
func TestCalculateCostBreakdown_ReconcilesStandard(t *testing.T) {
	p := llmruntime.ModelPricing{
		CostPerMillionInput:       3.0,
		CostPerMillionOutput:      15.0,
		CostPerMillionCachedInput: sql.NullFloat64{Float64: 0.3, Valid: true},
	}
	nonCached, cached, creation, output, thinking := 90000, 10000, 0, 5000, 1000

	total := llmruntime.CalculateTotalCost(&p, nonCached, cached, creation, output, thinking)
	bd := llmruntime.CalculateCostBreakdown(&p, nonCached, cached, creation, output, thinking)

	assert.Equal(t, "standard", bd.Tier)
	assert.Len(t, bd.Components, 4)
	assert.InDelta(t, total, sumComponents(bd), 1e-12, "components must sum to CalculateTotalCost")

	// output component folds in thinking tokens (billed at output rate).
	for _, c := range bd.Components {
		if c.Kind == "output" {
			assert.Equal(t, int64(output+thinking), c.Tokens)
		}
	}
}

// TestCalculateCostBreakdown_LongContextTier verifies the tier flag flips and the
// long-ctx rates are applied (and still reconcile) once the prompt crosses the
// threshold.
func TestCalculateCostBreakdown_LongContextTier(t *testing.T) {
	p := llmruntime.ModelPricing{
		CostPerMillionInput:         3.0,
		CostPerMillionOutput:        15.0,
		ContextThresholdTokens:      sql.NullInt64{Int64: 200000, Valid: true},
		CostPerMillionInputLongCtx:  sql.NullFloat64{Float64: 6.0, Valid: true},
		CostPerMillionOutputLongCtx: sql.NullFloat64{Float64: 22.5, Valid: true},
	}
	// totalPrompt = 250k > 200k threshold → long-ctx.
	nonCached, cached, creation, output, thinking := 250000, 0, 0, 1000, 0

	total := llmruntime.CalculateTotalCost(&p, nonCached, cached, creation, output, thinking)
	bd := llmruntime.CalculateCostBreakdown(&p, nonCached, cached, creation, output, thinking)

	assert.Equal(t, "long_context", bd.Tier)
	assert.InDelta(t, total, sumComponents(bd), 1e-12)
	// input component should use the long-ctx rate (6.0), not 3.0.
	for _, c := range bd.Components {
		if c.Kind == "input" {
			assert.InDelta(t, float64(nonCached)/1e6*6.0, c.CostUsd, 1e-12)
		}
	}
}

// TestCalculateCostBreakdown_NilPricing returns an empty, standard breakdown.
func TestCalculateCostBreakdown_NilPricing(t *testing.T) {
	bd := llmruntime.CalculateCostBreakdown(nil, 10, 0, 0, 5, 0)
	assert.Equal(t, "standard", bd.Tier)
	assert.Empty(t, bd.Components)
}
