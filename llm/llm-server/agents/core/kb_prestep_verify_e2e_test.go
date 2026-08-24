//go:build e2e

package core

import (
	"os"
	"testing"

	"nudgebee/llm/common"
)

// TestKBPrestepVerify is a manual verification harness — NOT a CI unit test.
// It inspects a real, already-completed conversation in the database to confirm
// the canary KB article was retrieved, injected into the planner prompt,
// followed in the final answer, and recorded as a reference. It self-skips
// unless KB_PRESTEP_VERIFY_CONVERSATION_ID is set, so `make test` runs it as a
// no-op. It reuses the service's own DB layer (common.GetDatabaseManager) — no
// external psql, no hand-passed connection string.
//
// Run the canary scenario first (see scripts/kb_prestep_canary.txt), then:
//
//	set -a && source .env && set +a
//	KB_PRESTEP_VERIFY_CONVERSATION_ID=<conversation_id> \
//	  go test -tags e2e -v -run TestKBPrestepVerify ./agents/core/...
//
// Optional: KB_PRESTEP_CANARY_TOKEN (default ZEBRA-9931).
func TestKBPrestepVerify(t *testing.T) {
	RequireEnv(t, "KB_PRESTEP_VERIFY_CONVERSATION_ID")
	convID := os.Getenv("KB_PRESTEP_VERIFY_CONVERSATION_ID")
	canary := os.Getenv("KB_PRESTEP_CANARY_TOKEN")
	if canary == "" {
		canary = "ZEBRA-9931"
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		t.Fatalf("could not get database manager (is LLM_SERVER_DB_URL set?): %v", err)
	}

	count := func(query string, args ...any) int {
		var n int
		if err := dbms.Db.Get(&n, query, args...); err != nil {
			t.Fatalf("query failed: %v\nquery: %s", err, query)
		}
		return n
	}

	t.Logf("KB pre-step verification — conversation %s (canary %s)", convID, canary)

	// Stages 1-2 read prompt_messages, populated only when LLM tracing is on.
	traced := count(`SELECT count(*) FROM llm_conversation_token_usage
		WHERE conversation_id = $1 AND prompt_messages IS NOT NULL`, convID)
	if traced == 0 {
		t.Log("note: no prompt traces stored — stages 1-2 are inconclusive without LLM tracing enabled")
	}

	// Stage 1 — the pre-step retrieved content and it reached the planner prompt.
	// prompt_messages is JSON-serialized, so angle brackets are escaped; match
	// the bracket-free tag name so the check is escaping-agnostic.
	stage1 := count(`SELECT count(*) FROM llm_conversation_token_usage
		WHERE conversation_id = $1 AND prompt_messages ILIKE '%retrieved_knowledge%'`, convID)
	if stage1 > 0 {
		t.Logf("[STAGE 1] PASS  pre-step retrieved KB content (retrieved_knowledge block in %d prompt(s))", stage1)
	} else {
		t.Errorf("[STAGE 1] FAIL  no retrieved_knowledge block in the prompt — pre-step did not run or returned nothing")
	}

	// Stage 2 — the canary article specifically reached the planner prompt.
	stage2 := count(`SELECT count(*) FROM llm_conversation_token_usage
		WHERE conversation_id = $1 AND prompt_messages ILIKE '%' || $2 || '%'`, convID, canary)
	if stage2 > 0 {
		t.Logf("[STAGE 2] PASS  canary present in the planner prompt (%d message(s))", stage2)
	} else {
		t.Errorf("[STAGE 2] FAIL  canary not found in the prompt — retrieved the wrong content, or dropped before the LLM call")
	}

	// Stage 3 — the canary surfaced in the final answer (adherence).
	stage3 := count(`SELECT count(*) FROM llm_conversation_messages
		WHERE conversation_id = $1 AND response ILIKE '%' || $2 || '%'`, convID, canary)
	if stage3 > 0 {
		t.Logf("[STAGE 3] PASS  canary surfaced in the final answer — KB was FOLLOWED")
	} else {
		t.Errorf("[STAGE 3] FAIL  canary not in the final answer")
	}

	// Stage 4 — the KB usage was recorded so the UI can show it.
	stage4 := count(`SELECT count(*) FROM llm_conversation_references
		WHERE conversation_id = $1 AND reference_type = 'knowledge_base'`, convID)
	if stage4 > 0 {
		t.Logf("[STAGE 4] PASS  %d knowledge_base reference(s) saved — KB usage is visible in the UI", stage4)
	} else {
		t.Errorf("[STAGE 4] FAIL  no knowledge_base references saved — KB usage would be invisible in the UI")
	}

	t.Log("Interpretation:")
	t.Log("  stage 1 FAIL          -> discovery: pre-step did not retrieve the article")
	t.Log("  stage 1 PASS, 2 FAIL  -> retrieval ran but matched the wrong content")
	t.Log("  stage 2 PASS, 3 FAIL  -> ADHERENCE: agent saw the KB and ignored it (separate fix)")
	t.Log("  all PASS              -> KB found, injected, and followed")
}
