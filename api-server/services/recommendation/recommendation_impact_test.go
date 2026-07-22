package recommendation

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// annotateBreakdownWithImpact must be a safe no-op when the knowledge-graph
// service is unavailable or the recommendation's identity is incomplete, so the
// always-on recompute cron never panics or stamps a band it can't justify.
// Resolution against a live graph is exercised by the post-deploy cron run (this
// path now derives ns+name from the cloud_resourses join, not the rec JSONB), not
// by a unit test.
func TestAnnotateBreakdownWithImpactNoOp(t *testing.T) {
	cases := []struct {
		name      string
		kg        *core.Service
		namespace string
		workload  string
	}{
		{"nil graph service", nil, "shop", "checkout"},
		{"blank namespace", &core.Service{}, "   ", "checkout"},
		{"blank workload", &core.Service{}, "shop", ""},
	}

	cache := map[string]*recommendationImpact{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bd := map[string]any{"base_score": 10}
			annotateBreakdownWithImpact(tc.kg, "tenant", "acct", tc.namespace, tc.workload, bd, cache)
			if _, ok := bd["safety_band"]; ok {
				t.Error("must not stamp safety_band on a no-op path")
			}
			if _, ok := bd["impact_summary"]; ok {
				t.Error("must not stamp impact_summary on a no-op path")
			}
		})
	}
	if len(cache) != 0 {
		t.Errorf("no-op paths must not populate the cache, got %d entries", len(cache))
	}
}
