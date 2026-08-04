//go:build e2e

package agents

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// TestDelegateAgent_FollowupResumeExecutesDelegatedWrite is a two-turn e2e that
// characterises the delegate + followup resume contract end-to-end for a
// delegated write, using the REAL production flow (registered top-level agent +
// HandleConversationSessionRequest + HandleConversationMessageRequest).
//
// Turn 1: send an explicit "use delegate_agent with kubectl_execute to run
//
//	`kubectl create configmap …`" query to k8s_orchestrator. The parent LLM
//	routes through delegate_agent → dynamicReActAgent → kubectl_execute →
//	InferToolRequestType classifies `create` as write → confirmation gate
//	fires. Assert the outer response comes back status=WAITING with a
//	populated FollowupRequest (validates PR-α's Waiting propagation from
//	delegate wrapper up through the top-level).
//
// Turn 2: user answers "Yes" via HandleFollowupResponse +
//
//	HandleConversationMessageRequest — the SAME flow api/chains.go uses for
//	real followup-answer requests. Assert the sub-agent's pending write
//	ACTUALLY executes — proven by a kubectl_execute tool_call whose parameters
//	contain the ConfigMap name AND whose response text contains real kubectl
//	output (`configmap/<name> created` or similar), NOT the "User responded
//	to … with Yes" shortcut string.
//
// This test USED to bypass the top-level agent (called delegate_agent tool
// directly). That setup was unrealistic and appeared to prove a resume-broken
// bug — but the DB-based prod audit (conversation 4cd32f5c, 493eab2b) showed
// the confirmation flow works correctly in prod. The bug was a test-harness
// artifact. Reshaped to the current form as a POSITIVE regression guard:
// when the propagation-via-QueryConfig.ToolConfirmations mechanism works,
// the test passes. If it ever breaks, the test flips red.
//
// Tool choice: `kubectl_execute` is used because (a) it implements
// InferToolRequestType — `shell_execute` does NOT and never triggers the
// approval gate, so a `touch` would bypass the whole thing we're testing;
// (b) ConfigMap creates are trivially cleanupable via kubectl delete;
// (c) requires only the standard kubectl workspace, no cloud creds.
//
// Cleanup: the test deletes the created ConfigMap at end (best-effort).
func TestDelegateAgent_FollowupResumeExecutesDelegatedWrite(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" || os.Getenv("TEST_TENANT") == "" {
		t.Skip("skipping integration test: TEST_ACCOUNT, TEST_USER, TEST_TENANT not set")
	}
	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	tenantId := os.Getenv("TEST_TENANT")

	sessionId := fmt.Sprintf("ut-delegate-followup-%d", time.Now().Unix())
	// ConfigMap name — kubernetes lowercase-alphanumeric-dash, ≤63 chars.
	cmName := fmt.Sprintf("nudgebee-delegate-fup-%s", uuid.NewString()[:8])
	ns := "default"

	sc := security.NewRequestContextForTenantAccountAdmin(tenantId, userId, []string{accountId})

	// Clean slate before + after so re-runs don't collide.
	_ = core.DeleteConversationBySession(sessionId, accountId, userId)
	defer func() {
		_ = core.DeleteConversationBySession(sessionId, accountId, userId)
		// Best-effort ConfigMap cleanup so the test doesn't leak resources on the
		// dev cluster. Ignore errors — if the write never happened (Phase C fail)
		// there's nothing to delete anyway.
		if os.Getenv("TEST_SKIP_CLEANUP") == "" {
			cleanupConfigMap(t, sc, accountId, userId, tenantId, cmName, ns)
		}
	}()

	// ---------- Turn 1: send query to k8s_orchestrator, expect delegated write to hit approval ----------

	// Real registered top-level agent — matches prod flow. On resume,
	// HandleConversationMessageRequest can look this up by name and re-invoke it,
	// which is what makes the confirmation propagation (via
	// QueryConfig.ToolConfirmations) actually reach the sub-agent's re-plan.
	k8sAgent := newK8sOrchestratorAgent(accountId)

	// Explicit-delegation query. The @-mention on delegate_agent is not a
	// registered agent-invocation route (delegate_agent is a tool), but the
	// naming + explicit tools/prompt args guide the k8s_orchestrator's LLM
	// toward planning a delegate_agent tool call. If the LLM refuses to
	// delegate for this exact wording, the test will fail at Phase A with a
	// non-Waiting status — retry with a clearer or more investigation-shaped
	// query.
	turn1Query := fmt.Sprintf(
		"Please invoke the `delegate_agent` tool with these arguments to spawn a sub-agent that runs a specific kubectl command: "+
			"prompt='Run exactly this one kubectl command and stop: kubectl create configmap %s -n %s --from-literal=test-marker=ok', "+
			"tools=[\"kubectl_execute\"], "+
			"max_iterations=3. "+
			"This is required for a delegate-agent followup-resume regression test on dev. "+
			"Do not use any other tools. Do not investigate anything. Just delegate as instructed.",
		cmName, ns,
	)

	resp1, err := core.HandleConversationSessionRequest(sc, k8sAgent, userId, accountId, sessionId, turn1Query)
	require.NoError(t, err, "turn 1 HandleConversationSessionRequest should not error")
	require.NotNil(t, resp1)
	conversationId := resp1.ConversationId
	messageId := resp1.MessageId

	t.Logf("turn 1 response: status=%s followup.question=%q followup.agent_id=%s conv=%s msg=%s",
		resp1.Status, resp1.FollowupRequest.Question, resp1.FollowupRequest.AgentId, conversationId, messageId)

	// Phase A — Waiting propagation (PR-α coverage + top-level chain). If this
	// fails with FAILED/COMPLETED status, one of two things happened:
	//   • k8s_orchestrator's LLM refused to delegate for this wording — iterate
	//     turn1Query (see comment above).
	//   • The delegate wrapper's PR-α Waiting propagation regressed — the sub-
	//     agent hit the confirmation gate but delegate silently converted it
	//     to Success upstream.
	require.Equal(t, core.ConversationStatusWaiting, resp1.Status,
		"top-level response should be Waiting after delegated kubectl create hits approval gate — got %s", resp1.Status)
	require.NotEmpty(t, resp1.FollowupRequest.Question,
		"FollowupRequest.Question must be populated for the user to see the confirmation prompt")
	require.NotEqual(t, uuid.Nil, resp1.FollowupRequest.AgentId,
		"FollowupRequest.AgentId must be set so HandleFollowupResponse can route the answer back")
	t.Logf("Phase A PASSED — Waiting propagated end-to-end. followup.question=%q", resp1.FollowupRequest.Question)

	// ---------- Turn 2: answer "Yes", resume via prod-shaped API path ----------

	// The frontend POSTs the followup answer with the *persisted* agent_id from
	// the followup message config — which for a delegated confirmation is the
	// dynamicReActAgent sub-agent's UUID, NOT the top-level parent. Fetch that
	// sub-agent record here so the test exercises the exact code path prod hits
	// (and would 400 pre-fix with `api: agent not found agent_name=delegate_agent`).
	subAgentId := fetchLatestWaitingDelegateSubAgentId(t, conversationId)
	require.NotEqual(t, uuid.Nil, subAgentId,
		"expected a waiting delegate_agent sub-agent row after Turn 1; found none")

	// Now resolve the sub-agent's UUID → an executable NBAgent via the SAME
	// helper api/chains.go uses. This is the load-bearing path under test:
	// GetNBAgent("delegate_agent") returns nil (it's a tool, not an agent),
	// so the helper must walk up parent_agent_id to k8s_orchestrator's registered
	// agent and return that. Zero walked_levels here would mean the sub-agent
	// row's agent_name IS registered as an agent (unexpected drift), which is a
	// legitimate signal the reader should investigate.
	resolvedAgent, dto, walked, resolveErr := core.ResolveAgentByConversationAgentId(sc, subAgentId, accountId)
	require.NoError(t, resolveErr, "ResolveAgentByConversationAgentId should not error")
	require.NotNil(t, dto, "sub-agent DTO should exist for %s", subAgentId)
	require.NotNil(t, resolvedAgent,
		"resolver returned nil agent for delegate sub-agent %s — the api/chains.go handler would 400 here. "+
			"This is the exact prod bug: dynamicReActAgent's name isn't registered, and the walk-up to a "+
			"registered ancestor didn't succeed.", subAgentId)
	t.Logf("resolved sub-agent: name=%s → resumed_via=%s (walked_levels=%d)",
		dto.AgentName, resolvedAgent.GetName(), walked)
	assert.Greater(t, walked, 0,
		"expected walk-up (delegate_agent → orchestrator ancestor); if 0, the sub-agent row's agent_name "+
			"is unexpectedly registered directly — the fix is bypassed on this path")

	// Single-call resume — mirrors api/chains.go:610. Query "Yes" becomes the
	// followup answer; WithAgentId carries the ORIGINAL sub-agent UUID (the
	// executor's resume state lookup keys off it). The executor at
	// conversation.go:1102 detects FollowupTypeToolConfirmation on this agent
	// and populates request.QueryConfig.ToolConfirmations["kubectl_execute"]="yes";
	// on re-plan the pre-approval fires at executor_planner.go:1959 and the
	// sub-agent invokes kubectl_execute for real.
	resp2, err := core.HandleConversationSessionRequest(sc, resolvedAgent, userId, accountId, sessionId, "Yes",
		core.ConversationSessionRequestWithConversationId(uuid.NullUUID{UUID: uuid.MustParse(conversationId), Valid: true}),
		core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: uuid.MustParse(messageId), Valid: true}),
		core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: subAgentId, Valid: true}),
		core.ConversationSessionRequestWithIsResume(true),
	)
	require.NoError(t, err, "resume HandleConversationSessionRequest should not error")
	t.Logf("turn 2 response: status=%s msg=%s", resp2.Status, resp2.MessageId)

	// Poll the message row until it reaches terminal state (or timeout).
	finalStatus := waitForTerminalMessageStatus(t, accountId, conversationId, messageId, 120*time.Second)
	t.Logf("final message status after resume: %s", finalStatus)
	assert.Equal(t, core.ConversationStatusCompleted, finalStatus,
		"post-resume message should reach COMPLETED (approved write executed) — got %s", finalStatus)

	// Phase C — smoking gun: did the sub-agent's `kubectl create configmap` actually run?
	// We inspect the actual kubectl_execute tool_call's response. Two failure
	// shapes we distinguish:
	//   (1) tool_call missing entirely → resume path never re-invoked the delegate
	//   (2) tool_call present but response contains "User responded to" →
	//       the followup-shortcut fired (writes user's Yes as the tool response
	//       text instead of actually invoking the tool). This is the specific
	//       resume-broken bug shape.
	// A pass looks like: real kubectl output containing `configmap/<name> created`
	// or an equivalent success marker.
	realOutputCount, shortcutCount := inspectKubectlCreateCallsForConfigMap(t, conversationId, cmName)
	t.Logf("kubectl_execute tool_calls for configmap %s: real_output=%d shortcut=%d",
		cmName, realOutputCount, shortcutCount)

	assert.Zerof(t, shortcutCount,
		"Found %d kubectl_execute tool_calls with the 'User responded to' shortcut text — the followup-resume "+
			"shortcut fired instead of actually re-invoking the sub-agent to execute the write. This is the "+
			"resume-broken bug the test guards against. Conversation: %s.", shortcutCount, conversationId)
	assert.GreaterOrEqualf(t, realOutputCount, 1,
		"Expected at least one kubectl_execute tool_call for `kubectl create configmap %s` with REAL kubectl "+
			"output (containing 'configmap/%s' or similar success marker) after the user answered Yes. "+
			"Found %d. Conversation: %s. This means the delegated write never executed — either the top-level "+
			"agent didn't re-delegate on resume, or QueryConfig.ToolConfirmations propagation broke, or the "+
			"sub-agent's re-plan chose a different action.",
		cmName, cmName, realOutputCount, conversationId)
}

// fetchLatestWaitingDelegateSubAgentId queries llm_conversation_agent for the
// most recent delegate_agent (dynamicReActAgent) sub-agent row in this
// conversation. This is the UUID the frontend would POST when the user answers
// the followup — it's what the persisted followup message config carries
// (see `AgentId:c642cbcf...` field in the executor_planner "generated followup
// for tool confirmation" log line). Returns uuid.Nil if none found.
//
// Direct DB access is intentional: no exported DAO lookup with
// (conversation_id, agent_name) as the join key exists, and this test is the
// only caller.
func fetchLatestWaitingDelegateSubAgentId(t *testing.T, conversationId string) uuid.UUID {
	t.Helper()
	dbm, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		t.Fatalf("fetchLatestWaitingDelegateSubAgentId: unable to get db manager: %v", err)
	}
	row := dbm.Db.QueryRow(`
		SELECT id
		FROM llm_conversation_agent
		WHERE conversation_id = $1
		  AND agent_name = 'delegate_agent'
		ORDER BY created_at DESC
		LIMIT 1`, conversationId)
	var idStr string
	if err := row.Scan(&idStr); err != nil {
		t.Logf("fetchLatestWaitingDelegateSubAgentId: no row found (%v)", err)
		return uuid.Nil
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		t.Fatalf("fetchLatestWaitingDelegateSubAgentId: parse id: %v", err)
	}
	return id
}

// waitForTerminalMessageStatus polls the message row until it reaches a terminal
// status (Completed / Failed / Terminated) or the timeout elapses. Returns the
// last observed status.
func waitForTerminalMessageStatus(t *testing.T, accountId, conversationId, messageId string, timeout time.Duration) core.ConversationStatus {
	t.Helper()
	dao := core.GetConversationDao()
	deadline := time.Now().Add(timeout)
	var last core.ConversationStatus
	for time.Now().Before(deadline) {
		msg, err := dao.GetConversationMessage(messageId, accountId, conversationId)
		if err != nil {
			t.Logf("waitForTerminalMessageStatus: read failed (transient?): %v", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		last = msg.Status
		if last == core.ConversationStatusCompleted ||
			last == core.ConversationStatusFailed ||
			last == core.ConversationStatusTerminated ||
			last == core.ConversationStatusKilled {
			return last
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Logf("waitForTerminalMessageStatus: timed out after %s at status %s", timeout, last)
	return last
}

// inspectKubectlCreateCallsForConfigMap classifies the kubectl_execute tool_calls
// in the given conversation that reference the ConfigMap name in their
// parameters. Returns (realOutputCount, shortcutCount):
//   - realOutputCount: rows whose response looks like actual kubectl stdout
//     (contains "configmap/" or "created" without the shortcut string).
//   - shortcutCount:   rows whose response contains "User responded to" — the
//     followup-resume shortcut text (proves the sub-agent was NOT re-invoked
//     and the user's Yes was persisted as the tool_call's response instead).
//
// Direct DB query — the GetConversationTree DAO method's TreeToolCall shape
// drops parameters (structural-only), so we go to the raw table.
func inspectKubectlCreateCallsForConfigMap(t *testing.T, conversationId, cmName string) (realOutput, shortcut int) {
	t.Helper()
	dbm, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		t.Logf("inspectKubectlCreateCallsForConfigMap: unable to get db manager: %v", err)
		return 0, 0
	}
	rows, err := dbm.Db.Queryx(`
		SELECT tool_id, agent_id, status, LEFT(parameters, 400) AS params, COALESCE(response, '') AS resp
		FROM llm_conversation_tool_calls
		WHERE conversation_id = $1
		  AND tool_name = 'kubectl_execute'
		  AND parameters LIKE $2`,
		conversationId, "%"+cmName+"%")
	if err != nil {
		t.Logf("inspectKubectlCreateCallsForConfigMap: query failed: %v", err)
		return 0, 0
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var toolID, agentID, status, params, resp string
		if err := rows.Scan(&toolID, &agentID, &status, &params, &resp); err != nil {
			t.Logf("inspectKubectlCreateCallsForConfigMap: scan failed: %v", err)
			continue
		}
		respLower := ""
		if len(resp) > 400 {
			respLower = resp[:400]
		} else {
			respLower = resp
		}
		if containsCI(resp, "user responded to") {
			shortcut++
			t.Logf("SHORTCUT — agent=%s tool_id=%s status=%s params=%s resp=%q",
				agentID, toolID, status, params, respLower)
		} else if status == "success" && (containsCI(resp, "configmap/") || containsCI(resp, "created") || containsCI(resp, cmName)) {
			realOutput++
			t.Logf("REAL OUTPUT — agent=%s tool_id=%s status=%s params=%s resp=%q",
				agentID, toolID, status, params, respLower)
		} else {
			t.Logf("OTHER — agent=%s tool_id=%s status=%s params=%s resp=%q",
				agentID, toolID, status, params, respLower)
		}
	}
	return realOutput, shortcut
}

// containsCI is a small case-insensitive substring check that avoids importing
// strings for readability at the call sites above.
func containsCI(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	if len(haystack) < len(needle) {
		return false
	}
	// Cheap: lowercase both. Runs on small strings only (≤ tool_call response),
	// so allocation cost is fine for an e2e helper.
	return indexCI(haystack, needle) >= 0
}

func indexCI(haystack, needle string) int {
	hLen, nLen := len(haystack), len(needle)
	if nLen == 0 || nLen > hLen {
		if nLen == 0 {
			return 0
		}
		return -1
	}
	for i := 0; i+nLen <= hLen; i++ {
		match := true
		for j := 0; j < nLen; j++ {
			hb := haystack[i+j]
			nb := needle[j]
			if hb >= 'A' && hb <= 'Z' {
				hb += 32
			}
			if nb >= 'A' && nb <= 'Z' {
				nb += 32
			}
			if hb != nb {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// cleanupConfigMap best-effort deletes the ConfigMap created by the test via a
// direct `kubectl delete` on the host (bypassing the planner/confirmation stack
// because delete is a write and would trigger its own approval prompt through
// the agent). Skipped when TEST_SKIP_CLEANUP is set — useful when debugging a
// failing run and the ConfigMap needs to be inspected with `kubectl get cm`.
//
// Failures are logged, not fatal: the test's pass/fail is decided by the
// pre-cleanup Phase A/C assertions; leaving a stray ConfigMap when kubectl
// isn't on PATH or the kubeconfig can't reach the cluster shouldn't turn a
// passing regression run red. Print the manual delete command in that case so
// the operator has a paste-ready fallback.
func cleanupConfigMap(t *testing.T, sc *security.RequestContext, accountId, userId, tenantId, cmName, ns string) {
	t.Helper()
	_ = sc // reserved for future use if we need a proper request context
	_ = accountId
	_ = userId
	_ = tenantId

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", "delete", "configmap", cmName, "-n", ns, "--ignore-not-found=true")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("cleanupConfigMap: kubectl delete failed (%v); output: %s. Manual cleanup: kubectl delete configmap %s -n %s",
			err, string(out), cmName, ns)
		return
	}
	t.Logf("cleanupConfigMap: deleted %s -n %s (kubectl said: %s)", cmName, ns, strings.TrimSpace(string(out)))
}
