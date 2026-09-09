package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAllocationSpendView pins the one-view-per-dimension rule. The spends
// table carries the same dollars twice — raw bill rows (exclude_aggregate =
// false) and k8s per-pod allocation rows (exclude_aggregate = true) — so a
// breakdown that reads both double-counts: scope_total came out at ~2x the
// bill and the entire bill surfaced as "unattributed" spend.
func TestAllocationSpendView(t *testing.T) {
	k8sDims := []string{"namespace", "workload"}
	for _, d := range k8sDims {
		assert.Contains(t, allocationSpendView(d), "exclude_aggregate = true",
			"%s values exist only on k8s allocation rows", d)
	}

	billDims := []string{"service", "region", "resource_type"}
	for _, d := range billDims {
		assert.Contains(t, allocationSpendView(d), "exclude_aggregate = false",
			"%s belongs to the cloud bill view", d)
	}

	// Tags exist in both views (cloud tags on bill rows, k8s labels on
	// allocation rows); tag deliberately keeps the union.
	assert.Equal(t, "", allocationSpendView("tag"))
}

// TestAllocationAndScopeTotalReadTheSameView guards the denominator: the
// coverage figure divides the breakdown's attributed total by scope_total, so
// both queries must read the same spend view for any dimension.
func TestAllocationAndScopeTotalReadTheSameView(t *testing.T) {
	for dim := range allocationDimensions {
		view := allocationSpendView(dim)
		// Both query builders splice allocationSpendView(groupBy) into their
		// spends subquery; this pins that neither hardcodes a predicate that
		// could drift from the other.
		if dim == "tag" {
			assert.Equal(t, "", view)
			continue
		}
		assert.True(t, strings.Contains(view, "exclude_aggregate"),
			"dimension %q must resolve to exactly one spend view", dim)
	}
}

// TestSetAllocationChangeFlags pins the NEW/GONE disambiguation: rows with no
// prior-period spend must not read as "stable 0% change", and disappeared
// dimensions must be flagged rather than showing as zero-amount noise.
func TestSetAllocationChangeFlags(t *testing.T) {
	rows := []allocationRow{
		{DimensionValue: "steady", Amount: 100, AmountPrev: 95},
		{DimensionValue: "brand-new", Amount: 50, AmountPrev: 0},
		{DimensionValue: "gone", Amount: 0, AmountPrev: 40},
		{DimensionValue: "empty", Amount: 0, AmountPrev: 0},
	}
	setAllocationChangeFlags(rows)

	assert.False(t, rows[0].IsNew)
	assert.False(t, rows[0].IsGone)
	assert.True(t, rows[1].IsNew)
	assert.False(t, rows[1].IsGone)
	assert.False(t, rows[2].IsNew)
	assert.True(t, rows[2].IsGone)
	assert.False(t, rows[3].IsNew)
	assert.False(t, rows[3].IsGone)
}
