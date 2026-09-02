//go:build e2e

package agents

import (
	"fmt"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// These e2e tests measure the FS evidence-recall layer (PR1: the
// compression-marker -> file_ref handle in scratchpad.go, flag-gated by
// LlmServerFsEvidenceRecallEnabled). See WORKSPACE_FS_EVIDENCE_RECALL_SPEC.md
// (§6.5 Measurement plan).
//
// The A/B here is the SAME agent run with the flag OFF then ON — not two
// agents. The feature only acts on deep turns where a large observation is
// compressed AND the tool saved a workspace file (logs/metrics/traces already
// do). On such turns the flag turns a dead-end truncation marker into a live
// "grep the file" handle, so the sub-agent should recall instead of re-run:
// expect flag-on to hold or LOWER llm_calls / obs_bytes, never raise them.
//
// Reuses the shared harness in e2e_metrics_helpers_test.go (runMetrics,
// runAgentQuery, requireE2EEnv, sessionSlug, logMetrics).
//
// Data capture is the default; set FSEV_MAX_DEPTH_RATIO (e.g. 1.05) to fail
// when flag-on llm_calls exceed flag-off by more than that factor.

// fsEvidenceQueries are logs-heavy investigations chosen to generate large
// observations (18-20k-token log firehoses) that fill the scratchpad and
// trigger compression — the only regime where the flag changes behavior. A
// query too small to trigger compression yields off==on, which is itself a
// valid "no regression on shallow turns" data point.
var fsEvidenceQueries = []labeledQuery{
	{"logs-investigation", "Investigate errors in the services-server logs in the nudgebee namespace over the last 2 hours — find the root cause of any failures."},
	{"crashloop-logs", "Investigate any crash-looping pods in the nudgebee namespace: pull their logs and recent events and find the root cause."},
}

// TestFsEvidenceRecall_AB runs each logs-heavy investigation with the flag OFF
// then ON and reports the cost delta. Expectation: on deep turns that fill the
// window, flag-on recalls saved logs (grep) instead of re-fetching, so
// llm_calls / obs_bytes hold or drop; on shallow turns the flag is a no-op.
func TestFsEvidenceRecall_AB(t *testing.T) {
	accountId, userId, tenant := requireE2EEnv(t)
	sc := security.NewRequestContextForTenantAccountAdmin(tenant, userId, []string{accountId})

	// Default agent (lean) — it delegates to the ReAct logs sub-agent whose
	// internal loop is where the compression memory-loss and re-fetch happen.
	// Post-#32503 Phase 1: the primary k8s_orchestrator IS lean.
	agent := newK8sLeanAgentNamed(accountId, AgentK8sOrchestratorName)

	orig := config.Config.LlmServerFsEvidenceRecallEnabled
	t.Cleanup(func() { config.Config.LlmServerFsEvidenceRecallEnabled = orig })

	for i, lq := range fsEvidenceQueries {
		t.Run(lq.kind, func(t *testing.T) {
			t.Logf("QUERY [%s]: %s", lq.kind, lq.query)

			// Order note: off-then-on means the on-run may hit a warmer LLM
			// prompt cache. That affects token COST, not iteration COUNT — so
			// llm_calls / re-run behavior (the signals we gate on) stay sound.
			config.Config.LlmServerFsEvidenceRecallEnabled = false
			mOff := runAgentQuery(t, sc, agent, userId, accountId, sessionSlug("ut-fsev-off", agent.GetName(), i), lq.query)
			t.Log("--- flag OFF (baseline: compression is a dead-end) ---")
			logMetrics(t, mOff)

			config.Config.LlmServerFsEvidenceRecallEnabled = true
			mOn := runAgentQuery(t, sc, agent, userId, accountId, sessionSlug("ut-fsev-on", agent.GetName(), i), lq.query)
			t.Log("--- flag ON  (compression -> file_ref grep handle) ---")
			logMetrics(t, mOn)

			assert.Truef(t, mOff.ok(), "flag-off did not execute (status=%s llm_calls=%d err=%v)", mOff.status, mOff.llmCalls, mOff.err)
			assert.Truef(t, mOn.ok(), "flag-on did not execute (status=%s llm_calls=%d err=%v)", mOn.status, mOn.llmCalls, mOn.err)

			if mOff.ok() && mOn.ok() {
				t.Logf("    Δ on-off: llm_calls=%+d in_tok=%+d obs_bytes=%+d wall=%+.1fs",
					mOn.llmCalls-mOff.llmCalls, mOn.inputTokens-mOff.inputTokens, mOn.observationBytes-mOff.observationBytes, mOn.wallSeconds-mOff.wallSeconds)

				// Guardrail (Tier-2 efficiency, §6.5): recall replaces re-run, so
				// flag-on should not INCREASE depth. Env-gated so the harness
				// captures data by default and only fails when asked to enforce.
				if ratioStr := os.Getenv("FSEV_MAX_DEPTH_RATIO"); ratioStr != "" && mOff.llmCalls > 0 {
					var maxRatio float64
					if _, err := fmt.Sscanf(ratioStr, "%f", &maxRatio); err == nil && maxRatio > 0 {
						ratio := float64(mOn.llmCalls) / float64(mOff.llmCalls)
						assert.LessOrEqualf(t, ratio, maxRatio,
							"flag-on llm_calls %d are %.2fx flag-off %d (> %.2f) on %q — recall should not increase depth",
							mOn.llmCalls, ratio, mOff.llmCalls, maxRatio, lq.query)
					}
				}
			}
		})
	}
}
