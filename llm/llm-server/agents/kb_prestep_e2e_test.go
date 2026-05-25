package agents

// End-to-end test for the KB pre-step (LLM_SERVER_KB_PRESTEP_ENABLED).
//
// It exercises the full path: create a knowledge base article carrying a
// unique canary token, wait for RAG indexing to finish, map the KB to the
// k8s_debug agent, run a real conversation, then verify the canary reached
// the planner prompt — and report whether it reached the final answer.
//
// This is a HEAVY integration test: it creates real DB rows, calls the
// rag-server to index + retrieve, and makes real LLM calls. It self-skips
// unless RUN_KB_PRESTEP_E2E=true and TEST_TENANT / TEST_USER / TEST_ACCOUNT
// are set, so `make test` runs it as a no-op.
//
// Run:
//   set -a && source .env && set +a
//   RUN_KB_PRESTEP_E2E=true go test -v -run TestKBPrestepE2E -timeout 20m ./agents/...
//
// The created KB and its agent mapping are cleaned up on exit; conversation
// rows are preserved for UI inspection.

import (
	"os"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// kbCanaryArticle is the canary KB article body. "ZEBRA-9931" is a marker token
// the agent cannot produce on its own, so finding it downstream proves the KB
// was genuinely retrieved (and, in the answer, followed).
//
// To verify by hand instead of via this test: create a KB in the Knowledge Base
// tab with this body, map it to an agent (Settings -> Agents -> Add Knowledge
// Base), run an investigation such as "investigate the high memory alert on
// checkout-api", then run TestKBPrestepVerify against that conversation id.
const kbCanaryArticle = `High Memory on checkout-api

Description: The checkout-api deployment periodically shows elevated memory
usage during the nightly batch-reconciliation window.

Resolution (CRITICAL): This is expected behaviour. Do NOT restart or scale the
deployment. Contact the service owner and quote reference code ZEBRA-9931.
Await their explicit confirmation before acting.`

func TestKBPrestepE2E(t *testing.T) {
	if os.Getenv("RUN_KB_PRESTEP_E2E") != "true" {
		t.Skip("set RUN_KB_PRESTEP_E2E=true to run the KB pre-step end-to-end test")
	}
	tenantID := os.Getenv("TEST_TENANT")
	userID := os.Getenv("TEST_USER")
	accountID := os.Getenv("TEST_ACCOUNT")
	if tenantID == "" || userID == "" || accountID == "" {
		t.Skip("TEST_TENANT / TEST_USER / TEST_ACCOUNT must be set")
	}
	if _, err := common.GetDatabaseManager(common.Metastore); err != nil {
		t.Skipf("metastore unreachable: %v", err)
	}

	// Enable the pre-step and LLM tracing (tracing persists prompt_messages,
	// which the verification reads). Restore on exit.
	prevPrestep := config.Config.LlmServerKBPrestepEnabled
	prevTrace := config.Config.LlmTraceEnabled
	config.Config.LlmServerKBPrestepEnabled = true
	config.Config.LlmTraceEnabled = true
	defer func() {
		config.Config.LlmServerKBPrestepEnabled = prevPrestep
		config.Config.LlmTraceEnabled = prevTrace
	}()

	sc := security.NewRequestContextForTenantAccountAdmin(tenantID, userID, []string{accountID})
	const canary = "ZEBRA-9931"

	// 1. Create the canary KB article from the kbCanaryArticle constant.
	kb, err := toolcore.CreateKnowledgebase(sc, accountID, toolcore.Knowledgebase{
		Name:         "kb-prestep-e2e-" + uuid.NewString()[:8],
		Description:  "E2E canary article for KB pre-step verification",
		Data:         kbCanaryArticle,
		DataFormat:   "text",
		DataFilename: "kb_prestep_canary.txt",
	})
	require.NoError(t, err, "create KB")
	t.Logf("created KB %s (%s)", kb.Id, kb.Name)
	defer func() {
		if derr := toolcore.DeleteKnowledgebase(sc, accountID, kb.Id); derr != nil {
			t.Logf("cleanup: delete KB failed: %v", derr)
		}
	}()

	// 2. Wait for RAG indexing — CreateKnowledgebase embeds asynchronously and
	//    flips status from "processing" to "active" (or "error") when done.
	deadline := time.Now().Add(3 * time.Minute)
	var status string
	for time.Now().Before(deadline) {
		cur, gErr := toolcore.GetKnowledgebase(sc, accountID, kb.Id)
		require.NoError(t, gErr, "get KB")
		status = cur.Status
		if status == "active" {
			break
		}
		if status == "error" {
			t.Fatalf("KB indexing failed (status=error) — is rag-server reachable?")
		}
		time.Sleep(2 * time.Second)
	}
	require.Equal(t, "active", status, "KB did not finish indexing within the deadline")
	// Small settle window: the vector collection can lag the status flip.
	time.Sleep(3 * time.Second)
	t.Logf("KB %s indexed (status=active)", kb.Id)

	// 3. Map the KB to the k8s_debug agent so the pre-step picks it up.
	if _, err = toolcore.MapKBToAgent(sc, accountID, kb.Id, AgentK8sDebugName); err != nil {
		t.Fatalf("map KB to agent: %v", err)
	}
	defer func() {
		if uerr := toolcore.UnmapKBFromAgent(sc, accountID, kb.Id, AgentK8sDebugName); uerr != nil {
			t.Logf("cleanup: unmap KB failed: %v", uerr)
		}
	}()

	// 4. Run a real conversation whose question the canary article answers.
	sessionID := "kb-prestep-e2e-" + uuid.NewString()[:8]
	agent := newK8sDebugAgent(accountID)
	resp, err := core.HandleConversationSessionRequest(sc, agent, userID, accountID, sessionID,
		"investigate the high memory alert on checkout-api")
	require.NoError(t, err, "conversation turn")
	require.NotEmpty(t, resp.Response, "conversation should produce an answer")

	// 5. Verify against the captured planner prompt. scenarioLastMsgID and
	//    scenarioPollPromptsForMsg are shared helpers from
	//    agent_memory_e2e_scenarios_test.go (same package).
	convID, msgID := scenarioLastMsgID(t, sessionID, userID)
	prompts := scenarioPollPromptsForMsg(t, convID, msgID, 1)
	require.NotEmpty(t, prompts, "no planner prompt captured — is LLM tracing enabled?")
	joined := strings.Join(prompts, "\n")

	// Stage 1 — the pre-step retrieved KB content and injected it. prompt_messages
	// is stored JSON-serialized, so angle brackets are escaped; match the
	// bracket-free tag name.
	assert.Contains(t, joined, "retrieved_knowledge",
		"STAGE 1 FAIL: no retrieved_knowledge block in the planner prompt")
	// Stage 2 — the canary article specifically reached the planner prompt.
	assert.Contains(t, joined, canary,
		"STAGE 2 FAIL: canary token not found in the planner prompt")

	// Stage 3 — adherence. Informational only: whether the agent FOLLOWS the
	// KB is the separate adherence fix, not the pre-step's responsibility.
	answer := strings.Join(resp.Response, "\n")
	if strings.Contains(answer, canary) {
		t.Logf("STAGE 3 PASS: canary present in the final answer — KB was followed")
	} else {
		t.Logf("STAGE 3 INFO: canary not in the final answer — adherence gap (separate fix), not a pre-step failure")
	}

	// Stage 4 — the pre-step recorded knowledge_base references so the KB
	// usage is visible in the UI's "Skills used" surface.
	dbms, dbErr := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, dbErr, "get database manager")
	var refCount int
	require.NoError(t, dbms.Db.Get(&refCount,
		`SELECT count(*) FROM llm_conversation_references
		 WHERE conversation_id = $1 AND reference_type = 'knowledge_base'`, convID),
		"query knowledge_base references")
	assert.Greater(t, refCount, 0,
		"STAGE 4 FAIL: no knowledge_base references saved — KB usage would be invisible in the UI")
}
