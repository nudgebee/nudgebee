//go:build e2e

package agents

import (
	"os"
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
)

// TestNoProgressBrake_AB measures the stats-based no-progress brake
// (LlmServerNoProgressBrakeEnabled). The A/B is the SAME agent run with the brake
// OFF then ON on a resolution-fishing query — one whose named entity doesn't
// resolve, so the planner re-formulates queries that keep coming back empty
// (the trivial-result loop the brake targets). Expectation: brake-ON holds or
// LOWERS depth (llm_calls / tool_calls) by cutting the fishing tail, without
// changing the median clean case.
//
// Reuses the shared harness in e2e_metrics_helpers_test.go. Set NOPB_ENFORCE=1
// to fail if brake-ON increases depth; default is data-capture.
var noProgressQueries = []labeledQuery{
	{"resolution-fishing", "Investigate the root cause of elevated 5xx error rates for the checkout-service in the production-eu cluster during the last 15 minutes. Correlate its application logs, distributed traces and container memory metrics."},
}

func TestNoProgressBrake_AB(t *testing.T) {
	accountId, userId, tenant := requireE2EEnv(t)
	sc := security.NewRequestContextForTenantAccountAdmin(tenant, userId, []string{accountId})
	// Post-#32503 Phase 1: the primary k8s_orchestrator IS lean; instantiate it directly.
	agent := newK8sLeanAgentNamed(accountId, AgentK8sOrchestratorName)

	for i, lq := range noProgressQueries {
		t.Run(lq.kind, func(t *testing.T) {
			t.Logf("QUERY [%s]: %s", lq.kind, lq.query)

			origEnabled := config.Config.LlmServerNoProgressBrakeEnabled
			t.Cleanup(func() { config.Config.LlmServerNoProgressBrakeEnabled = origEnabled })

			config.Config.LlmServerNoProgressBrakeEnabled = false
			mOff := runAgentQuery(t, sc, agent, userId, accountId, sessionSlug("ut-nopb-off", agent.GetName(), i), lq.query)
			t.Log("--- brake OFF (baseline: fishes on empties) ---")
			logMetrics(t, mOff)

			config.Config.LlmServerNoProgressBrakeEnabled = true
			mOn := runAgentQuery(t, sc, agent, userId, accountId, sessionSlug("ut-nopb-on", agent.GetName(), i), lq.query)
			t.Log("--- brake ON (breaks on repeated/trivial results) ---")
			logMetrics(t, mOn)

			assert.Truef(t, mOff.ok(), "brake-off did not execute (status=%s llm_calls=%d err=%v)", mOff.status, mOff.llmCalls, mOff.err)
			assert.Truef(t, mOn.ok(), "brake-on did not execute (status=%s llm_calls=%d err=%v)", mOn.status, mOn.llmCalls, mOn.err)

			if mOff.ok() && mOn.ok() {
				t.Logf("    Δ on-off: llm_calls=%+d tool_calls=%+d obs_bytes=%+d wall=%+.1fs",
					mOn.llmCalls-mOff.llmCalls, mOn.toolCalls-mOff.toolCalls, mOn.observationBytes-mOff.observationBytes, mOn.wallSeconds-mOff.wallSeconds)

				if os.Getenv("NOPB_ENFORCE") != "" {
					assert.LessOrEqualf(t, mOn.llmCalls, mOff.llmCalls,
						"brake-on llm_calls %d should not exceed brake-off %d — the brake must not increase depth", mOn.llmCalls, mOff.llmCalls)
				}
			}
		})
	}
}
