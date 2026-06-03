package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComputeShares(t *testing.T) {
	rows := []allocationRow{
		{DimensionValue: "prod", Amount: 600},
		{DimensionValue: "staging", Amount: 300},
		{DimensionValue: "dev", Amount: 100},
	}
	total := computeShares(rows)

	assert.InDelta(t, 1000, total, 0.01)
	assert.InDelta(t, 60, rows[0].PctOfTotal, 0.01)
	assert.InDelta(t, 30, rows[1].PctOfTotal, 0.01)
	assert.InDelta(t, 10, rows[2].PctOfTotal, 0.01)
}

func TestComputeShares_Empty(t *testing.T) {
	assert.InDelta(t, 0, computeShares(nil), 0.01)
}

func TestComputeShares_ZeroTotalLeavesPctZero(t *testing.T) {
	rows := []allocationRow{{DimensionValue: "x", Amount: 0}}
	total := computeShares(rows)
	assert.InDelta(t, 0, total, 0.01)
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
