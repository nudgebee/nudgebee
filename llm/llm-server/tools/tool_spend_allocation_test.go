package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComputeShares(t *testing.T) {
	// Shares are computed against the supplied grand total, not the sum of rows —
	// so when the rows are the full set the percentages still sum to 100.
	rows := []allocationRow{
		{DimensionValue: "prod", Amount: 600},
		{DimensionValue: "staging", Amount: 300},
		{DimensionValue: "dev", Amount: 100},
	}
	computeShares(rows, 1000)

	assert.InDelta(t, 60, rows[0].PctOfTotal, 0.01)
	assert.InDelta(t, 30, rows[1].PctOfTotal, 0.01)
	assert.InDelta(t, 10, rows[2].PctOfTotal, 0.01)
}

func TestComputeShares_AgainstGrandTotalNotRowSum(t *testing.T) {
	// Top-2 rows shown out of a larger total: shares are of the grand total, so
	// they correctly do NOT sum to 100 (a truncated tail is implied).
	rows := []allocationRow{
		{DimensionValue: "prod", Amount: 600},
		{DimensionValue: "staging", Amount: 300},
	}
	computeShares(rows, 1500)

	assert.InDelta(t, 40, rows[0].PctOfTotal, 0.01)
	assert.InDelta(t, 20, rows[1].PctOfTotal, 0.01)
}

func TestComputeShares_Empty(t *testing.T) {
	computeShares(nil, 0) // must not panic
}

func TestComputeShares_ZeroTotalLeavesPctZero(t *testing.T) {
	rows := []allocationRow{{DimensionValue: "x", Amount: 0}}
	computeShares(rows, 0)
	assert.InDelta(t, 0, rows[0].PctOfTotal, 0.01)
}

func TestAllocationDimensions_Whitelist(t *testing.T) {
	for _, valid := range []string{"namespace", "service", "region", "resource_type", "tag"} {
		_, ok := allocationDimensions[valid]
		assert.True(t, ok, "expected %q to be a supported dimension", valid)
	}
	_, ok := allocationDimensions["; DROP TABLE spends;--"]
	assert.False(t, ok, "injection-style group_by must not be in the whitelist")
}
