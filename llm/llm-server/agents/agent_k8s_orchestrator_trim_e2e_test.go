//go:build e2e

package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// assertInputTokenRatio fails if trimTok exceeds fullTok by more than the factor
// set in the named env var (unset = informational only). Mirrors the inline
// BLOAT_MAX_INPUT_TOKEN_RATIO check in agent_k8s_orchestrator_2_e2e_test.go.
func assertInputTokenRatio(t *testing.T, envVar string, trimTok, fullTok int) {
	t.Helper()
	ratioStr := os.Getenv(envVar)
	if ratioStr == "" || fullTok <= 0 {
		return
	}
	var maxRatio float64
	if _, err := fmt.Sscanf(ratioStr, "%f", &maxRatio); err != nil || maxRatio <= 0 {
		return
	}
	ratio := float64(trimTok) / float64(fullTok)
	assert.LessOrEqualf(t, ratio, maxRatio,
		"trim input tokens %d are %.2fx _2 %d (> %.2f)", trimTok, ratio, fullTok, maxRatio)
}

// These e2e tests exercise @k8s_orchestrator_trim — the lean-preloaded-context
// eval handle — against a real cluster/account. They reuse the runMetrics
// harness from agent_k8s_orchestrator_2_e2e_test.go (requireE2EEnv,
// runAgentQuery, logMetrics, sessionSlug, investigationQueries) and are gated
// behind the same `e2e` build tag, skipping when TEST_ACCOUNT/TEST_USER are unset.
//
// To exercise the search_tools discovery path, run with
// LLM_SERVER_SEARCH_TOOLS_ENABLED=true; otherwise the trim agent reaches
// specialists via delegate_agent by name (both paths are valid — see
// trimOnDemandInstruction).

// specialistNamesExcludedFromTrim are the agents intentionally NOT preloaded by
// the trim handle. They must never appear in its resolved tool set nor be emitted
// as a direct action (the dispatch auth check rejects unheld tools).
var specialistNamesExcludedFromTrim = []string{
	"postgres", "mysql", "mssql", "oracle", "redis", "rabbitmq",
	"helm", "github", "security", "server", "code_analyzer",
	"aws", "aws_observability", "gcp", "azure",
	"visualizer", "websearch", "tickets", "tickets_v2", "automation",
}

func resolvedToolNames(agent core.NBAgent, sc *security.RequestContext) map[string]bool {
	set := map[string]bool{}
	for _, tool := range agent.GetSupportedTools(sc) {
		set[strings.ToLower(tool.Name())] = true
	}
	return set
}

func sortedSetKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestK8sOrchestratorTrim_ToolSetExcludesSpecialists verifies the resolved tool
// set (against the REAL account's enabled/configured tools — not just the static
// name list the unit test checks) preloads the core + reach-back and excludes
// every specialist. Also prints the size delta vs @k8s_orchestrator_2.
func TestK8sOrchestratorTrim_ToolSetExcludesSpecialists(t *testing.T) {
	accountId, userId, tenant := requireE2EEnv(t)
	sc := security.NewRequestContextForTenantAccountAdmin(tenant, userId, []string{accountId})

	trim := newK8sTrimAgent(accountId, AgentK8sOrchestratorTrimName)
	full := newK8sOrchestratorDirectAgent(accountId)

	trimTools := resolvedToolNames(trim, sc)
	fullTools := resolvedToolNames(full, sc)

	t.Logf("trim tool set (%d): %s", len(trimTools), strings.Join(sortedSetKeys(trimTools), ", "))
	t.Logf("full (_2) tool set (%d): %s", len(fullTools), strings.Join(sortedSetKeys(fullTools), ", "))
	t.Logf("reduction: %d -> %d preloaded tools", len(fullTools), len(trimTools))

	// Core + reach-back must be present.
	assert.True(t, trimTools["kubectl_execute"], "kubectl_execute must be preloaded")
	assert.True(t, trimTools[strings.ToLower(DelegateAgentToolName)], "delegate_agent must be preloaded")
	assert.True(t, trimTools[SearchToolsToolName], "search_tools must be preloaded (always registered)")

	// Specialists must be absent.
	for _, name := range specialistNamesExcludedFromTrim {
		assert.Falsef(t, trimTools[name], "specialist %q must NOT be preloaded by the trim handle", name)
	}

	// The whole point: the trim set is strictly smaller.
	assert.Less(t, len(trimTools), len(fullTools), "trim must preload fewer tools than _2")
}

// TestK8sOrchestratorTrim_ContextReductionAB runs the shared investigation query
// set through _2 (full) and _trim (lean) and reports the token/hop delta — the
// headline reduction metric. Set TRIM_MAX_INPUT_TOKEN_RATIO (e.g. 1.2) to fail if
// trim's input tokens exceed _2's by more than that factor on any query.
func TestK8sOrchestratorTrim_ContextReductionAB(t *testing.T) {
	accountId, userId, tenant := requireE2EEnv(t)
	sc := security.NewRequestContextForTenantAccountAdmin(tenant, userId, []string{accountId})

	full := newK8sOrchestratorDirectAgent(accountId)
	trim := newK8sTrimAgent(accountId, AgentK8sOrchestratorTrimName)

	var fullInTot, trimInTot int
	for i, lq := range investigationQueries {
		t.Run(lq.kind, func(t *testing.T) {
			t.Logf("QUERY [%s]: %s", lq.kind, lq.query)
			mf := runAgentQuery(t, sc, full, userId, accountId, sessionSlug("ut-trim-ab-full", full.GetName(), i), lq.query)
			mt := runAgentQuery(t, sc, trim, userId, accountId, sessionSlug("ut-trim-ab-trim", trim.GetName(), i), lq.query)
			logMetrics(t, mf)
			logMetrics(t, mt)
			assert.Truef(t, mf.ok(), "_2 did not execute (status=%s llm_calls=%d err=%v)", mf.status, mf.llmCalls, mf.err)
			assert.Truef(t, mt.ok(), "trim did not execute (status=%s llm_calls=%d err=%v)", mt.status, mt.llmCalls, mt.err)

			if mf.ok() && mt.ok() {
				t.Logf("    Δ trim-_2: llm_calls=%+d in_tok=%+d obs_bytes=%+d wall=%+.1fs",
					mt.llmCalls-mf.llmCalls, mt.inputTokens-mf.inputTokens, mt.observationBytes-mf.observationBytes, mt.wallSeconds-mf.wallSeconds)
				fullInTot += mf.inputTokens
				trimInTot += mt.inputTokens
				assertInputTokenRatio(t, "TRIM_MAX_INPUT_TOKEN_RATIO", mt.inputTokens, mf.inputTokens)
			}
		})
	}
	if fullInTot > 0 {
		t.Logf("TOTAL input tokens: _2=%d trim=%d (%.0f%% of _2)", fullInTot, trimInTot, 100*float64(trimInTot)/float64(fullInTot))
	}
}

// TestK8sOrchestratorTrim_ReachesSpecialistOnDemand issues a query that requires a
// non-core capability and verifies the trim handle reaches it through the
// reach-back path (delegate_agent / search_tools) — and never by emitting the
// specialist as a direct action (which auth would reject). Requires helm on the
// target cluster; adjust the query for the account under test if needed.
func TestK8sOrchestratorTrim_ReachesSpecialistOnDemand(t *testing.T) {
	accountId, userId, tenant := requireE2EEnv(t)
	// Eval blindspot (doc §5d, Finding 3): with shell_execute enabled the trim agent
	// can satisfy a specialist query by running the CLI directly via shell and never
	// touch search_tools/delegate_agent — so this test would silently pass without
	// exercising the reach-back path it exists to verify. Force the intended path.
	if config.Config.LlmServerShellToolEnabled {
		t.Skip("set LLM_SERVER_SHELL_TOOL_ENABLED=false to exercise the search_tools/delegate reach-back; with shell enabled the agent bypasses discovery")
	}
	sc := security.NewRequestContextForTenantAccountAdmin(tenant, userId, []string{accountId})

	trim := newK8sTrimAgent(accountId, AgentK8sOrchestratorTrimName)
	m := runAgentQuery(t, sc, trim, userId, accountId,
		sessionSlug("ut-trim-ondemand", trim.GetName(), 0),
		"List all helm releases in the nudgebee namespace and their chart versions.")
	logMetrics(t, m)

	assert.Truef(t, m.ok(), "trim did not execute (status=%s llm_calls=%d err=%v)", m.status, m.llmCalls, m.err)

	// It must never call a specialist directly — that path is auth-rejected.
	for _, name := range specialistNamesExcludedFromTrim {
		assert.Zerof(t, m.toolNames[name], "trim emitted specialist %q as a direct action — must go via delegate_agent", name)
	}

	// It should have reached the specialist via the reach-back path.
	reached := m.toolNames[DelegateAgentToolName] > 0 || m.toolNames[SearchToolsToolName] > 0
	assert.Truef(t, reached, "trim did not use delegate_agent/search_tools to reach the helm specialist; tools=%s", m.tools())
}
