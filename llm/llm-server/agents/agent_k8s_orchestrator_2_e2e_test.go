//go:build e2e

package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// These e2e tests exercise k8s_orchestrator_2 (direct kubectl) against real
// clusters. They require TEST_ACCOUNT / TEST_USER / TEST_TENANT and a working
// LLM provider, so they are gated behind the `e2e` build tag and skip when the
// environment is not configured.
//
// Two things are measured here:
//
//  1. Parity — run every query from the kubectl sub-agent's e2e suite through
//     k8s_orchestrator_2 so we can confirm the merged agent handles the same
//     kubectl behaviors (get/discovery/CRUD/command-only/log-truncate).
//
//  2. Bloat A/B — the whole point of v2 is to drop the sub-agent hop for simple
//     questions (less latency + fewer tokens). The risk is that, for multi-step
//     investigations, raw kubectl output now lands in the orchestrator's own
//     scratchpad instead of a summarized sub-agent answer. We run the same
//     queries through v1 (delegating) and v2 (direct) and diff the cost so both
//     sides of that trade are visible.

// runMetrics captures the cost signals for a single agent+query run.
type runMetrics struct {
	agent            string
	query            string
	llmCalls         int // LLM round-trips (the hop count)
	inputTokens      int // context fed to the planner across iterations — the bloat proxy
	outputTokens     int
	cachedInput      int
	toolCalls        int            // tool invocations landing in the scratchpad
	observationBytes int            // sum of raw tool-response content — direct scratchpad-bloat proxy
	toolNames        map[string]int // routing histogram (kubectl_execute vs logs/events/metrics/...)
	wallSeconds      float64
	responseChars    int
	status           core.ConversationStatus
	err              error
}

// ok reports whether the run actually executed. A dead model / infra failure
// surfaces as a FAILED conversation (or zero LLM calls) with no Go error, so we
// must check both — otherwise the harness reports a false PASS.
func (m runMetrics) ok() bool {
	return m.err == nil && m.status != core.ConversationStatusFailed && m.llmCalls > 0
}

func requireE2EEnv(t *testing.T) (accountId, userId, tenant string) {
	t.Helper()
	accountId = os.Getenv("TEST_ACCOUNT")
	userId = os.Getenv("TEST_USER")
	tenant = os.Getenv("TEST_TENANT")
	if accountId == "" || userId == "" {
		t.Skip("set TEST_ACCOUNT / TEST_USER / TEST_TENANT to run k8s_orchestrator_2 e2e tests")
	}
	return accountId, userId, tenant
}

func sessionSlug(prefix, agent string, idx int) string {
	return fmt.Sprintf("%s-%s-%d", prefix, strings.ReplaceAll(agent, "_", ""), idx)
}

// runAgentQuery executes one query end-to-end and collects cost metrics.
func runAgentQuery(t *testing.T, sc *security.RequestContext, agent core.NBAgent, userId, accountId, sessionId, query string) runMetrics {
	t.Helper()
	m := runMetrics{agent: agent.GetName(), query: query, toolNames: map[string]int{}}

	_ = core.DeleteConversationBySession(sessionId, accountId, userId)

	start := time.Now()
	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId, query)
	m.wallSeconds = time.Since(start).Seconds()
	if err != nil {
		m.err = err
		return m
	}

	m.status = resp.Status
	for _, s := range resp.Response {
		m.responseChars += len(s)
	}
	m.toolCalls = len(resp.AgentStepResponse)
	for _, inv := range resp.AgentStepResponse {
		name := inv.Response.Name
		if name == "" && inv.Call.FunctionCall != nil {
			name = inv.Call.FunctionCall.Name
		}
		if name == "" {
			name = "?"
		}
		m.toolNames[name]++
		m.observationBytes += len(inv.Response.Content)
	}

	// Token usage keys off session_id (the DAO's WHERE c.session_id), NOT the
	// conversation UUID — passing resp.ConversationId here silently returns 0 rows.
	records, rerr := core.GetConversationDao().GetConversationTokenUsageDetailed(sessionId, accountId)
	if rerr == nil {
		m.llmCalls = len(records)
		for _, r := range records {
			m.inputTokens += r.InputTokens
			m.outputTokens += r.OutputTokens
			m.cachedInput += r.CachedInputTokens
		}
	}
	return m
}

func (m runMetrics) tools() string {
	if len(m.toolNames) == 0 {
		return "-"
	}
	names := make([]string, 0, len(m.toolNames))
	for n := range m.toolNames {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, fmt.Sprintf("%s×%d", n, m.toolNames[n]))
	}
	return strings.Join(parts, ", ")
}

func logMetrics(t *testing.T, m runMetrics) {
	t.Helper()
	if m.err != nil {
		t.Logf("    [%-20s] ERROR: %v", m.agent, m.err)
		return
	}
	t.Logf("    [%-20s] status=%s llm_calls=%d in_tok=%d (cached=%d) out_tok=%d tool_calls=%d obs_bytes=%d wall=%.1fs",
		m.agent, m.status, m.llmCalls, m.inputTokens, m.cachedInput, m.outputTokens, m.toolCalls, m.observationBytes, m.wallSeconds)
	t.Logf("    %22s tools: %s", "", m.tools())
}

// labeledQuery pairs a query with the behavior/kind it exercises.
type labeledQuery struct {
	kind  string // "simple" | "investigation" | the kubectl behavior name
	query string
}

// kubectlParityQueries mirrors agent_kubectl_e2e_test.go so we run the exact same
// kubectl behaviors through the merged orchestrator.
var kubectlParityQueries = []labeledQuery{
	{"get", "get the pods of app coredns in kube-system namespace."},
	{"get-multi-ns", "get the pods of app rag-server from all namespaces and get the status of each pod"},
	{"get-by-ip", "get the details for pod with IPs 10.0.0.20, 10.0.0.21, 10.0.0.22 across all namespaces"},
	{"direct-command", "kubectl exec deployment/my-agent-runner -n my-agent -- ls"},
	{"multi-command", "Get a summary of all Deployments, StatefulSets, DaemonSets, and Jobs running in the cluster. Include their names, namespaces, replica counts, and status."},
	{"log-truncate", "get logs of services-server in nudgebee namespace"},
	{"discovery-events", "Get events of rag-server pods in nudgebee namespace"},
	{"refine-logs", "get llm-server pod logs"},
	{"command-only", "what is the kubectl command to scale the rag-server deployment in nudgebee to 3 replicas?"},
	{"disambiguation-health", "check the component health of the cluster"},
}

// investigationQueries are multi-step RCA-style questions — the case where v2's
// direct execution risks bloating the orchestrator scratchpad relative to v1's
// summarized sub-agent answer.
var investigationQueries = []labeledQuery{
	{"simple", "get pods in nudgebee namespace"},
	{"simple", "what is the status of the services-server deployment in nudgebee?"},
	{"investigation", "Why is the rag-server deployment in the nudgebee namespace unhealthy? Check pod status, recent events, and logs."},
	{"investigation", "Investigate any crash-looping pods in the nudgebee namespace and find the root cause."},
}

// TestK8sOrchestrator2_KubectlParity runs the kubectl e2e query set through both
// the kubectl sub-agent and k8s_orchestrator_2, printing responses + tool routing
// so behavioral parity can be reviewed side by side.
func TestK8sOrchestrator2_KubectlParity(t *testing.T) {
	accountId, userId, tenant := requireE2EEnv(t)
	sc := security.NewRequestContextForTenantAccountAdmin(tenant, userId, []string{accountId})

	agents := []core.NBAgent{
		newKubectlAgent(accountId),
		newK8sOrchestrator2Agent(accountId),
	}

	for i, lq := range kubectlParityQueries {
		t.Run(lq.kind, func(t *testing.T) {
			t.Logf("QUERY [%s]: %s", lq.kind, lq.query)
			for _, agent := range agents {
				sid := sessionSlug("ut-v2-parity", agent.GetName(), i)
				m := runAgentQuery(t, sc, agent, userId, accountId, sid, lq.query)
				logMetrics(t, m)
				assert.Truef(t, m.ok(), "%s did not execute on %q (status=%s llm_calls=%d err=%v) — check LLM_MODEL_NAME is a live model",
					agent.GetName(), lq.query, m.status, m.llmCalls, m.err)
			}
		})
	}
}

// TestK8sOrchestrator2_BloatAB runs simple + investigation queries through v1
// (delegating) and v2 (direct) and reports the cost delta. Expectation: v2 wins
// on `simple` (fewer llm_calls/tokens/latency — the dropped hop) and is the one
// to watch on `investigation` (higher obs_bytes/in_tok — raw output in-context).
// Set BLOAT_MAX_INPUT_TOKEN_RATIO (e.g. 1.5) to fail if v2 investigation input
// tokens exceed v1 by more than that factor.
func TestK8sOrchestrator2_BloatAB(t *testing.T) {
	accountId, userId, tenant := requireE2EEnv(t)
	sc := security.NewRequestContextForTenantAccountAdmin(tenant, userId, []string{accountId})

	v1 := newK8sOrchestratorAgentMode(accountId, false)
	v2 := newK8sOrchestrator2Agent(accountId)

	// Per-kind token totals for a final summary.
	type kindAgg struct{ v1In, v2In, v1Calls, v2Calls int }
	byKind := map[string]*kindAgg{}

	for i, lq := range investigationQueries {
		t.Run(lq.kind, func(t *testing.T) {
			t.Logf("QUERY [%s]: %s", lq.kind, lq.query)
			m1 := runAgentQuery(t, sc, v1, userId, accountId, sessionSlug("ut-v1-ab", v1.GetName(), i), lq.query)
			m2 := runAgentQuery(t, sc, v2, userId, accountId, sessionSlug("ut-v2-ab", v2.GetName(), i), lq.query)
			logMetrics(t, m1)
			logMetrics(t, m2)
			assert.Truef(t, m1.ok(), "v1 did not execute (status=%s llm_calls=%d err=%v)", m1.status, m1.llmCalls, m1.err)
			assert.Truef(t, m2.ok(), "v2 did not execute (status=%s llm_calls=%d err=%v)", m2.status, m2.llmCalls, m2.err)

			if m1.ok() && m2.ok() {
				t.Logf("    Δ v2-v1: llm_calls=%+d in_tok=%+d obs_bytes=%+d wall=%+.1fs",
					m2.llmCalls-m1.llmCalls, m2.inputTokens-m1.inputTokens, m2.observationBytes-m1.observationBytes, m2.wallSeconds-m1.wallSeconds)

				agg := byKind[lq.kind]
				if agg == nil {
					agg = &kindAgg{}
					byKind[lq.kind] = agg
				}
				agg.v1In += m1.inputTokens
				agg.v2In += m2.inputTokens
				agg.v1Calls += m1.llmCalls
				agg.v2Calls += m2.llmCalls

				if ratioStr := os.Getenv("BLOAT_MAX_INPUT_TOKEN_RATIO"); ratioStr != "" && lq.kind == "investigation" && m1.inputTokens > 0 {
					var maxRatio float64
					if _, err := fmt.Sscanf(ratioStr, "%f", &maxRatio); err == nil && maxRatio > 0 {
						ratio := float64(m2.inputTokens) / float64(m1.inputTokens)
						assert.LessOrEqualf(t, ratio, maxRatio,
							"v2 input tokens %d are %.2fx v1 %d (> %.2f) on %q", m2.inputTokens, ratio, m1.inputTokens, maxRatio, lq.query)
					}
				}
			}
		})
	}

	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	t.Log("=== SUMMARY (v1 delegating vs v2 direct) ===")
	for _, k := range kinds {
		a := byKind[k]
		t.Logf("  %-14s v1 in_tok=%d calls=%d | v2 in_tok=%d calls=%d", k, a.v1In, a.v1Calls, a.v2In, a.v2Calls)
	}
}
