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
)

// Shared helpers for e2e tests that run an agent end-to-end and capture cost
// metrics (LLM round-trips, tokens, tool calls, observation bytes, wall time).
// Previously lived in agent_k8s_orchestrator_2_e2e_test.go; extracted when that
// file was deleted in the #32503 Phase 1 orchestrator collapse so the dependent
// e2e tests (agent_fs_evidence_recall_e2e_test.go, agent_no_progress_brake_e2e_test.go)
// continue to build under the e2e build tag.

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
		t.Skip("set TEST_ACCOUNT / TEST_USER / TEST_TENANT to run e2e tests")
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
