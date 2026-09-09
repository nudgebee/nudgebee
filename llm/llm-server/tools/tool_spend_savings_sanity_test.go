package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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
// commitment share a dedupe_group; summing them all inflated a live AWS
// account's savings from $1,184 to $2,806. The window must mirror
// recommendation_groupings_v2 (the query-engine view the Optimise UI reads) so
// both surfaces produce the same total.
func TestRecommendationViewExposesDedupe(t *testing.T) {
	assert.Contains(t, recommendationView, "is_primary_recommendation",
		"the view must expose the dedupe flag the agent filters savings totals on")
	assert.Contains(t, recommendationView, "r.dedupe_group",
		"dedupe_group must be selectable so alternatives can be reported as one opportunity")

	// Same partition key and ordering as recommendation_groupings_v2: highest
	// savings wins within (dedupe_group | resource_id | id, category).
	window := recommendationView[strings.Index(recommendationView, "ROW_NUMBER() OVER"):]
	assert.Contains(t, window, "PARTITION BY")
	assert.Contains(t, window, "r.dedupe_group")
	assert.Contains(t, window, "r.category")
	assert.Contains(t, window, "CASE WHEN r.status IN ('Archive', 'Closed') THEN 1 ELSE 0 END",
		"terminal rows must sort last, or one wins its group and marks the live row non-primary")
	assert.Contains(t, window, "r.estimated_savings DESC, r.updated_at DESC, r.id")

	assert.Contains(t, RecommendationExecuteTool{}.Description(), "is_primary_recommendation",
		"the tool description must tell callers totals require the dedupe filter")
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
