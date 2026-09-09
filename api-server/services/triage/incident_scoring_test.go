package triage

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The grouping-on term: a child is a symptom, likely_root_cause still surfaces
// the root, and the weak co-occurrence types stop moving scores at all.
func TestIncidentCorrelationAdjustment(t *testing.T) {
	cases := []struct {
		name    string
		child   bool
		corr    string
		score   float64
		wantAdj int
		wantTyp string
	}{
		{"group child is dampened", true, "", 0, IncidentChildPenalty, SameIncidentCorrelationType},
		{"child beats a pairwise root-cause row", true, "likely_root_cause", 1.0, IncidentChildPenalty, SameIncidentCorrelationType},
		{"root cause still surfaces", false, "likely_root_cause", 0.9, CorrelationBonusRootCause, "likely_root_cause"},
		{"root cause below the floor is ignored", false, "likely_root_cause", 0.4, 0, "likely_root_cause"},
		{"same_resource no longer scores", false, "same_resource", 1.0, 0, "same_resource"},
		{"same_service no longer scores", false, "same_service", 1.0, 0, "same_service"},
		{"upstream_dependency no longer scores", false, "upstream_dependency", 1.0, 0, "upstream_dependency"},
		{"downstream_impact no longer scores", false, "downstream_impact", 1.0, 0, "downstream_impact"},
		{"uncorrelated", false, "", 0, 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			adj, typ := incidentCorrelationAdjustment(c.child, c.corr, c.score)
			assert.Equal(t, c.wantAdj, adj)
			assert.Equal(t, c.wantTyp, typ)
		})
	}
}

// The kill switch has to revert scoring too, and each scorer keeps the mapping
// it had before grouping — they are NOT the same function. The legacy scorer
// never scored same_resource; the LLM scorer dampens it by -5. A regression that
// converged them on the off path would silently rescore every legacy tenant.
func TestResolveCorrelationAdjustmentHonoursKillSwitch(t *testing.T) {
	t.Setenv(incidentGroupingEnvFlag, "false")

	adj, _ := resolveCorrelationAdjustment(true, "same_resource", 1.0, getCorrelationAdjustment)
	assert.Equal(t, 0, adj, "legacy fallback: same_resource falls through to 0")

	adj, _ = resolveCorrelationAdjustment(true, "same_resource", 1.0, llmCorrelationAdjustment)
	assert.Equal(t, CorrelationPenaltySameService, adj, "llm fallback: same_resource keeps its -5")

	adj, _ = resolveCorrelationAdjustment(true, "", 0, getCorrelationAdjustment)
	assert.Equal(t, 0, adj, "grouping off: the child flag must not reach the score")

	t.Setenv(incidentGroupingEnvFlag, "true")
	adj, _ = resolveCorrelationAdjustment(true, "same_resource", 1.0, getCorrelationAdjustment)
	assert.Equal(t, IncidentChildPenalty, adj, "grouping on: both paths agree on the child term")
	adj, _ = resolveCorrelationAdjustment(true, "same_resource", 1.0, llmCorrelationAdjustment)
	assert.Equal(t, IncidentChildPenalty, adj)
}

// Below the confidence floor the LLM fallback must not consult the type table.
func TestLLMCorrelationAdjustmentFloor(t *testing.T) {
	adj, typ := llmCorrelationAdjustment("likely_root_cause", MinCorrelationScoreForAdjustment-0.01)
	assert.Equal(t, 0, adj)
	assert.Equal(t, "likely_root_cause", typ)

	adj, _ = llmCorrelationAdjustment("likely_root_cause", MinCorrelationScoreForAdjustment)
	assert.Equal(t, CorrelationBonusRootCause, adj)
}
