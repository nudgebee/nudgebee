package tools

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSavingsExceedSpend pins the impossible-savings predicate. It replaces a
// prose prompt constraint that was observed being ignored on its first live
// trigger: the agent reported $3,517/mo of open savings against $863.61/mo of
// spend without remark. Emitting the contradiction as data is what makes the
// model act on it.
func TestSavingsExceedSpend(t *testing.T) {
	assert.True(t, savingsExceedSpend(3517.21, 863.61), "4x savings-to-spend must flag")
	assert.True(t, savingsExceedSpend(410.09, 26.32), "the workflow-server case must flag")
	assert.False(t, savingsExceedSpend(200, 863.61), "plausible savings must not flag")
	assert.False(t, savingsExceedSpend(863.61, 863.61), "equal is not greater")
	assert.False(t, savingsExceedSpend(0, 863.61), "no savings must not flag")

	// Trivial rows must not trip the warning: a near-zero resource whose
	// estimate marginally exceeds its spend is noise, not a data defect.
	assert.False(t, savingsExceedSpend(0.03, 0.02), "sub-$1 spend is below the comparison floor")
	assert.False(t, savingsExceedSpend(5, 0), "zero spend carries no signal")
}

// TestRecommendationViewExposesDedupe pins the deduplication contract on the
// view the FinOps agent queries. Alternative purchase options for one
// commitment share a dedupe_group, and summing them all overstates savings.
// The flag must mark the same rows PrimaryRecommendationRank ranks first, the
// definition shared with the Optimise UI, so both surfaces produce the same
// total.
func TestRecommendationViewExposesDedupe(t *testing.T) {
	assert.Contains(t, recommendationView, "is_primary_recommendation",
		"the view must expose the dedupe flag the agent filters savings totals on")
	assert.Contains(t, recommendationView, "r.dedupe_group",
		"dedupe_group must be selectable so alternatives can be reported as one opportunity")
	assert.Contains(t, recommendationView, "("+primaryRecommendationFlag()+") AS is_primary_recommendation")

	assert.Contains(t, RecommendationExecuteTool{}.Description(), "is_primary_recommendation",
		"the tool description must tell callers totals require the dedupe filter")
}

// TestRecommendationViewPrimaryFlagMatchesRank pins primaryRecommendationFlag
// to PrimaryRecommendationRank: the flag is the window's rank-1 test computed
// per partition-key branch, so every branch of the window's CASE must have an
// arm in the flag, and both must rank a group in the same order.
func TestRecommendationViewPrimaryFlagMatchesRank(t *testing.T) {
	flag := primaryRecommendationFlag()
	rank := PrimaryRecommendationRank("r", "ca")

	partitionKey := rank[strings.Index(rank, "PARTITION BY"):strings.Index(rank, "ORDER BY")]
	whens := regexp.MustCompile(`(?s)WHEN (.+?)\s+THEN`).FindAllStringSubmatch(partitionKey, -1)
	require.Len(t, whens, 3, "the window's partition key has three keyed branches besides the row's own id")
	for _, when := range whens {
		assert.Contains(t, flag, "WHEN "+when[1]+" THEN",
			"a partition-key branch of PrimaryRecommendationRank has no arm in the view's primary flag")
	}
	assert.Contains(t, flag, "ELSE TRUE", "a row outside every keyed branch is a group of one")

	assert.Contains(t, rank, primaryRankOrder("r"))
	assert.Equal(t, 3, strings.Count(flag, "ORDER BY "+primaryRankOrder("s")),
		"every arm must rank a group in PrimaryRecommendationRank's order")
	assert.Contains(t, rank, azurePartitionKey("r"))
	assert.Contains(t, flag, "PARTITION BY "+azurePartitionKey("s")+", s.category")

	assert.NotContains(t, recommendationView, rank,
		"a window over the view's own rows fences every query into a sort of the whole account")
}

// TestRecommendationViewReadsSavingsColumn pins estimated_saving to the stored
// column: it is the value every ranking orders by, and reading the JSON payload
// instead forces a detoast of every row and keeps the savings index unused.
func TestRecommendationViewReadsSavingsColumn(t *testing.T) {
	assert.Contains(t, recommendationView, "r.estimated_savings::numeric AS estimated_saving")
	assert.NotContains(t, recommendationView, "'estimated_saving'")
}

// TestPrimaryRecommendationRankShape pins the one dedupe definition every
// savings surface shares. It must stay byte-compatible with
// recommendation_groupings_v2's window in api-server
// (services/query/metadata.go) — same partition key, same
// highest-savings-wins ordering. If they drift, chat and the Optimise page
// report different savings again (#36673).
func TestPrimaryRecommendationRankShape(t *testing.T) {
	rank := PrimaryRecommendationRank("r2", "ca2")

	assert.Contains(t, rank, "ROW_NUMBER() OVER")
	assert.Contains(t, rank, "r2.dedupe_group IS NOT NULL AND r2.dedupe_group <> ''")
	assert.Contains(t, rank, "WHEN r2.resource_id IS NOT NULL THEN r2.resource_id::text")
	assert.Contains(t, rank, "ELSE r2.id::text")
	assert.Contains(t, rank, "r2.category")
	// Status precedence comes first in the ordering: a terminal row may only
	// win a group with nothing live in it.
	assert.Contains(t, rank, "CASE WHEN r2.status IN ('Archive', 'Closed') THEN 1 ELSE 0 END")
	assert.Contains(t, rank, "r2.estimated_savings DESC, r2.updated_at DESC, r2.id")
	statusIdx := strings.Index(rank, "CASE WHEN r2.status IN")
	savingsIdx := strings.Index(rank, "r2.estimated_savings DESC")
	assert.Less(t, statusIdx, savingsIdx,
		"status must outrank savings, otherwise a higher-saving archived row still wins")

	// Azure ingestion leaves many rows without a dedupe_group or resource_id;
	// without this branch they would not group here while the Optimise page
	// groups them, reopening the disagreement for Azure accounts.
	assert.Contains(t, rank, "LOWER(ca2.cloud_provider) = 'azure'")
	assert.Contains(t, rank, "recommendation_type_id")
	assert.Contains(t, rank, "ext_subid")
	assert.Contains(t, rank, "ext_sku")

	// Aliases must be applied consistently — a stray hardcoded alias would
	// silently reference the wrong table in a caller's query.
	assert.NotContains(t, PrimaryRecommendationRank("rec", "acct"), "r2.")
	assert.NotContains(t, PrimaryRecommendationRank("rec", "acct"), "ca2.")
}

// TestPrimarySavingsSubqueryDedupes pins that every savings roll-up filters to
// rank 1. Before this, spend_summary and the FinOps account context summed all
// alternatives: one AWS account reported $2,806.30 where the deduped total —
// and the Optimise page — said $1,183.91.
func TestPrimarySavingsSubqueryDedupes(t *testing.T) {
	for _, groupCol := range []string{"cloud_account_id", "resource_id"} {
		sub := PrimarySavingsSubquery(groupCol, " AND r2.tenant_id = $1")
		assert.Contains(t, sub, "WHERE dedupe_rank = 1", "%s roll-up must keep only primary rows", groupCol)
		assert.Contains(t, sub, "SUM(estimated_savings) AS estimated_savings")
		assert.Contains(t, sub, "r2.status = 'Open'")
		assert.Contains(t, sub, "AND r2.tenant_id = $1", "scope filter must reach the inner query")
		assert.Contains(t, sub, "JOIN cloud_accounts ca2", "the Azure partition branch needs the provider")
		assert.Contains(t, sub, "GROUP BY "+groupCol)
	}
}

// See safeSavingsScopeColumns.
func TestPrimarySavingsSubqueryRejectsUnsafeScope(t *testing.T) {
	assert.Panics(t, func() {
		PrimarySavingsSubquery("cloud_account_id", " AND r2.rule_name = $1")
	})
	assert.Panics(t, func() {
		// Unqualified column: still resolves against r2 (the only aliased
		// table scopeFilter lands against), so it must be rejected too.
		PrimarySavingsSubquery("cloud_account_id", " AND rule_name = $1")
	})
	assert.NotPanics(t, func() {
		PrimarySavingsSubquery("cloud_account_id", " AND r2.tenant_id = $1 AND r2.cloud_account_id = $2")
	})
}
