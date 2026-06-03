//go:build e2e

package agents

// Long-arc investigation scenarios for the Memory Module — drive the
// k8s_debug agent through multi-turn SRE incident investigations and
// verify that memory stays attached on every turn, that a mid-session
// preference update takes effect immediately, and that a user's seeded
// style persists across a long conversation.
//
// These tests are slower than the short scenarios in
// agent_memory_e2e_scenarios_test.go but exercise the memory stack the
// way an on-call engineer would actually use it.
//
// Gated on RUN_MEMORY_INTEGRATION=true + TEST_TENANT/USER/ACCOUNT.
// Conversation rows are preserved — never deleted.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/memory"
	"nudgebee/llm/security"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A realistic 10-turn arc for a routine health check of the nudgebee
// namespace. Uses services that actually run in the cluster (api-server,
// llm-server, rag-server, ml-k8s-server, collector-server, etc.) so the
// agent can answer from real data.
var investigationNudgebeeHealthQueries = []string{
	"list all pods in the nudgebee namespace with their status and restart counts.",
	"which deployments in nudgebee have replicas below their desired count right now?",
	"show cpu and memory usage for the api-server and llm-server deployments.",
	"have any nudgebee pods restarted in the last 6 hours? if yes, list them.",
	"pull warning events from the nudgebee namespace in the last 1 hour.",
	"check node capacity — any nodes close to cpu or memory pressure?",
	"any pods pending or unschedulable in the nudgebee namespace?",
	"look at the most recent deployment rollout in nudgebee — which workload and when?",
	"summarize the nudgebee namespace health in 3 bullets based on what you checked.",
	"which service needs attention first and what's the single kubectl command to dig deeper?",
}

// An 8-turn arc focused on a specific nudgebee workload — llm-server —
// since this test is literally running against that deployment.
var investigationLLMServerHealthQueries = []string{
	"show the llm-server deployment status in the nudgebee namespace — replicas, conditions, strategy.",
	"how many llm-server pods are running right now and what are their pod names?",
	"have any llm-server pods restarted in the last 24 hours? show restart counts.",
	"what cpu and memory requests/limits are set on the llm-server container?",
	"pull the last 50 log lines from the most recently created llm-server pod.",
	"any warning events specifically for llm-server in the last 2 hours?",
	"assess llm-server health in one sentence based on the evidence gathered.",
	"give me a copy-paste kubectl sequence to watch llm-server logs and pod status live.",
}

// ── Investigation 1: nudgebee namespace health check, 10-turn arc ───────

// Seeds a Soul and one cross-agent Preference, then drives a 10-turn
// health-check conversation against the nudgebee namespace (real
// workloads). After every turn, verifies that the memory block is still
// present in what the LLM saw — regressions in the memory bridge would
// manifest as a missing <user_style> block mid-arc.
func TestInvestigation_NudgebeeNamespaceHealth_EndToEnd(t *testing.T) {
	tenantID := os.Getenv("TEST_TENANT")
	userID := os.Getenv("TEST_USER")
	accountID := os.Getenv("TEST_ACCOUNT")
	sessionID := "invest-nudgebee-" + uuid.NewString()[:8]
	defer scenarioSetup(t, tenantID)()

	m := memory.Default()
	defer scenarioEraseUser(m, tenantID, userID)

	// Distinctive sentinel so we can grep it in every captured prompt.
	scenarioSeedSoul(t, m, tenantID, userID, "terse",
		"NUDGEBEE_HEALTH_SENTINEL on-call SRE; prefer copy-pasteable kubectl; skip prose.")
	// Cross-agent preference — visible to every agent module.
	_, err := m.Mutate(context.Background(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID,
		Layer: "preferences", Action: "set", Key: "preferred_cloud",
		ActorKind: "user", ActorID: userID,
		Value: map[string]any{"value": "k8s", "agent_module": ""},
	})
	require.NoError(t, err)

	sc := security.NewRequestContextForTenantAccountAdmin(tenantID, userID, []string{accountID})
	k8sAgent := newK8sDebugAgent(accountID)

	stats := []string{}
	for i, q := range investigationNudgebeeHealthQueries {
		turn := i + 1
		t.Logf("\n======== NUDGEBEE HEALTH TURN %d/%d ========\nquery: %s", turn, len(investigationNudgebeeHealthQueries), q)
		_, msgID, prompts := runTurnAndPollPrompts(t, sc, k8sAgent, userID, accountID, sessionID, q)
		require.NotEmpty(t, prompts, "turn %d: no prompts captured", turn)

		hasSentinel := anyPromptContains(prompts, "NUDGEBEE_HEALTH_SENTINEL")
		hasCloud := anyPromptContains(prompts, "preferred_cloud: k8s")
		hasStyle := anyPromptContains(prompts, "user_style") ||
			anyPromptContains(prompts, "\\u003cuser_style\\u003e")

		assert.True(t, hasSentinel,
			"turn %d must carry NUDGEBEE_HEALTH_SENTINEL (memory regression?) msg=%s", turn, msgID)
		assert.True(t, hasStyle,
			"turn %d must carry <user_style> block msg=%s", turn, msgID)
		assert.True(t, hasCloud,
			"turn %d must carry preferred_cloud pref msg=%s", turn, msgID)
		stats = append(stats, fmt.Sprintf("T%d sentinel=%v style=%v pref=%v", turn, hasSentinel, hasStyle, hasCloud))
	}
	t.Logf("[nudgebee-health] 10 turns, all memory invariants held; session=%s preserved", sessionID)
	for _, s := range stats {
		t.Log(s)
	}
}

// ── Investigation 2: llm-server health, 8 turns, mid-session pref update ─

// Drives an 8-turn llm-server health investigation against the actual
// llm-server deployment in the nudgebee namespace. Midway through, updates
// a preference — verifies later turns see the NEW preference value while
// earlier turns' prompts only carried the old one.
func TestInvestigation_LLMServerWithPrefUpdate_EndToEnd(t *testing.T) {
	tenantID := os.Getenv("TEST_TENANT")
	userID := os.Getenv("TEST_USER")
	accountID := os.Getenv("TEST_ACCOUNT")
	sessionID := "invest-llm-server-" + uuid.NewString()[:8]
	defer scenarioSetup(t, tenantID)()

	m := memory.Default()
	defer scenarioEraseUser(m, tenantID, userID)

	scenarioSeedSoul(t, m, tenantID, userID, "terse",
		"LLM_SERVER_SENTINEL on-call; walk diagnosis step by step; one question at a time.")

	// Initial preference: log source.
	_, err := m.Mutate(context.Background(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID,
		Layer: "preferences", Action: "set", Key: "preferred_log_source",
		ActorKind: "user", ActorID: userID,
		Value: map[string]any{"value": "loki", "agent_module": ""},
	})
	require.NoError(t, err)

	sc := security.NewRequestContextForTenantAccountAdmin(tenantID, userID, []string{accountID})
	k8sAgent := newK8sDebugAgent(accountID)

	// Phase 1 — turns 1..4 with preferred_log_source=loki.
	phase1 := investigationLLMServerHealthQueries[:4]
	for i, q := range phase1 {
		turn := i + 1
		t.Logf("\n======== LLM-SERVER TURN %d (pref=loki) ========\nquery: %s", turn, q)
		_, _, prompts := runTurnAndPollPrompts(t, sc, k8sAgent, userID, accountID, sessionID, q)
		require.NotEmpty(t, prompts)
		assert.True(t, anyPromptContains(prompts, "LLM_SERVER_SENTINEL"),
			"turn %d must carry LLM_SERVER_SENTINEL", turn)

		// Scope the pref check to the <user_preferences> block in the system
		// prompt so conversation-history echoes don't create false positives.
		foundLokiInBlock := false
		for _, p := range prompts {
			idx := strings.Index(p, "user_preferences")
			if idx < 0 {
				continue
			}
			end := idx + 600
			if end > len(p) {
				end = len(p)
			}
			block := p[idx:end]
			if strings.Contains(block, "preferred_log_source: loki") {
				foundLokiInBlock = true
				assert.NotContains(t, block, "preferred_log_source: elasticsearch",
					"turn %d: preference block must not yet contain elasticsearch", turn)
				break
			}
		}
		assert.True(t, foundLokiInBlock,
			"turn %d: <user_preferences> must carry preferred_log_source=loki", turn)
	}

	// Mid-session preference flip.
	_, err = m.Mutate(context.Background(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID,
		Layer: "preferences", Action: "set", Key: "preferred_log_source",
		ActorKind: "user", ActorID: userID,
		Value: map[string]any{"value": "elasticsearch", "agent_module": ""},
	})
	require.NoError(t, err)

	// Phase 2 — turns 5..8 with preferred_log_source=elasticsearch.
	phase2 := investigationLLMServerHealthQueries[4:]
	for i, q := range phase2 {
		turn := i + 5
		t.Logf("\n======== LLM-SERVER TURN %d (pref=elasticsearch) ========\nquery: %s", turn, q)
		_, _, prompts := runTurnAndPollPrompts(t, sc, k8sAgent, userID, accountID, sessionID, q)
		require.NotEmpty(t, prompts)
		assert.True(t, anyPromptContains(prompts, "LLM_SERVER_SENTINEL"),
			"turn %d must carry LLM_SERVER_SENTINEL across the arc", turn)

		foundNewInBlock := false
		for _, p := range prompts {
			idx := strings.Index(p, "user_preferences")
			if idx < 0 {
				continue
			}
			end := idx + 600
			if end > len(p) {
				end = len(p)
			}
			block := p[idx:end]
			if strings.Contains(block, "preferred_log_source: elasticsearch") {
				foundNewInBlock = true
				assert.NotContains(t, block, "preferred_log_source: loki",
					"turn %d: <user_preferences> block must not carry the stale loki value", turn)
				break
			}
		}
		assert.True(t, foundNewInBlock,
			"turn %d: <user_preferences> block must carry the UPDATED elasticsearch value", turn)
	}
	t.Logf("[llm-server] 8 turns; preference flipped mid-session; session=%s preserved", sessionID)
}

// ensure the core import stays live even when assertions don't reference it
// directly (some turns rely on core.HandleConversationSessionRequest via the
// shared runTurnAndPollPrompts helper).
var _ = core.HandleConversationSessionRequest
