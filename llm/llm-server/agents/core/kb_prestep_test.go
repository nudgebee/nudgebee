package core

import (
	"os"
	"strings"
	"testing"

	"nudgebee/llm/common"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

func TestBuildKBSearchQuery(t *testing.T) {
	tests := []struct {
		name    string
		request NBAgentRequest
		check   func(t *testing.T, got string)
	}{
		{
			name:    "plain query, no hints, returned as-is",
			request: NBAgentRequest{OriginalQuery: "what does this error mean?"},
			check: func(t *testing.T, got string) {
				assert.Equal(t, "what does this error mean?", got)
			},
		},
		{
			name: "subject_name label is appended",
			request: NBAgentRequest{
				OriginalQuery: "investigate the alert",
				QueryConfig:   toolcore.NBQueryConfig{Labels: map[string]any{"subject_name": "payments-worker"}},
			},
			check: func(t *testing.T, got string) {
				assert.Contains(t, got, "investigate the alert")
				assert.Contains(t, got, "payments-worker")
			},
		},
		{
			name: "hint already present in the question is not duplicated",
			request: NBAgentRequest{
				OriginalQuery: "restart the orders-api pod",
				QueryConfig:   toolcore.NBQueryConfig{Labels: map[string]any{"subject_name": "orders-api"}},
			},
			check: func(t *testing.T, got string) {
				assert.Equal(t, "restart the orders-api pod", got)
			},
		},
		{
			name: "namespace and workload are appended",
			request: NBAgentRequest{
				OriginalQuery: "why is it crashing",
				QueryConfig:   toolcore.NBQueryConfig{Namespace: "production", Workload: "checkout-api"},
			},
			check: func(t *testing.T, got string) {
				assert.Contains(t, got, "why is it crashing")
				assert.Contains(t, got, "production")
				assert.Contains(t, got, "checkout-api")
			},
		},
		{
			name: "list-valued label appends each element",
			request: NBAgentRequest{
				OriginalQuery: "root cause analysis",
				QueryConfig:   toolcore.NBQueryConfig{Labels: map[string]any{"services": []any{"billing", "shipping"}}},
			},
			check: func(t *testing.T, got string) {
				assert.Contains(t, got, "billing")
				assert.Contains(t, got, "shipping")
			},
		},
		{
			name:    "falls back to Query when OriginalQuery is empty",
			request: NBAgentRequest{Query: "fallback question"},
			check: func(t *testing.T, got string) {
				assert.Equal(t, "fallback question", got)
			},
		},
		{
			name:    "empty when there is no question at all",
			request: NBAgentRequest{},
			check: func(t *testing.T, got string) {
				assert.Equal(t, "", got)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, buildKBSearchQuery(tt.request))
		})
	}
}

func TestBuildKBSearchQueryCappedLength(t *testing.T) {
	long := strings.Repeat("a", kbPrestepMaxQueryLen+200)
	got := buildKBSearchQuery(NBAgentRequest{OriginalQuery: long})
	assert.LessOrEqual(t, len(got), kbPrestepMaxQueryLen)
}

func TestBuildSkillListsMenu(t *testing.T) {
	t.Run("active KBs render with names and descriptions", func(t *testing.T) {
		kbs := []toolcore.Knowledgebase{
			{Name: "Pod Restart Runbook", Description: "Steps to safely restart a crashlooping pod", Status: "active"},
			{Name: "Database Troubleshooting", Description: "Common database connection issues", Status: "active"},
		}
		got := buildSkillListsMenu(kbs)
		assert.Contains(t, got, "<skill-lists>")
		assert.Contains(t, got, "</skill-lists>")
		assert.Contains(t, got, "name: Pod Restart Runbook - description: Steps to safely restart a crashlooping pod")
		assert.Contains(t, got, "name: Database Troubleshooting - description: Common database connection issues")
	})

	t.Run("inactive KBs are excluded", func(t *testing.T) {
		kbs := []toolcore.Knowledgebase{
			{Name: "Active KB", Description: "d", Status: "active"},
			{Name: "Processing KB", Description: "d", Status: "processing"},
		}
		got := buildSkillListsMenu(kbs)
		assert.Contains(t, got, "Active KB")
		assert.NotContains(t, got, "Processing KB")
	})

	t.Run("no active KBs returns empty string", func(t *testing.T) {
		kbs := []toolcore.Knowledgebase{{Name: "x", Status: "processing"}}
		assert.Equal(t, "", buildSkillListsMenu(kbs))
	})

	t.Run("empty slice returns empty string", func(t *testing.T) {
		assert.Equal(t, "", buildSkillListsMenu(nil))
	})
}

func TestFormatRetrievedKBBlock(t *testing.T) {
	t.Run("empty docs return empty string", func(t *testing.T) {
		assert.Equal(t, "", formatRetrievedKBBlock(nil))
		assert.Equal(t, "", formatRetrievedKBBlock(toolcore.RAGSearchResults{}))
	})

	t.Run("docs render inside a retrieved_knowledge block", func(t *testing.T) {
		docs := toolcore.RAGSearchResults{
			{Document: "Scale the deployment to add more replicas when CPU is saturated."},
		}
		got := formatRetrievedKBBlock(docs)
		assert.Contains(t, got, "<retrieved_knowledge>")
		assert.Contains(t, got, "</retrieved_knowledge>")
		assert.Contains(t, got, "Scale the deployment to add more replicas when CPU is saturated.")
	})

	t.Run("source url is included when present in metadata", func(t *testing.T) {
		docs := toolcore.RAGSearchResults{
			{Document: "content", Metadata: map[string]any{"url": "https://wiki.example.com/runbooks/scaling"}},
		}
		got := formatRetrievedKBBlock(docs)
		assert.Contains(t, got, "Source: https://wiki.example.com/runbooks/scaling")
	})

	t.Run("an oversized document is truncated", func(t *testing.T) {
		docs := toolcore.RAGSearchResults{{Document: strings.Repeat("x", 20000)}}
		got := formatRetrievedKBBlock(docs)
		assert.Contains(t, got, "[truncated]")
		assert.Less(t, len(got), 20000)
	})
}

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
//	  go test -v -run TestKBPrestepVerify ./agents/core/...
//
// Optional: KB_PRESTEP_CANARY_TOKEN (default ZEBRA-9931).
func TestKBPrestepVerify(t *testing.T) {
	convID := os.Getenv("KB_PRESTEP_VERIFY_CONVERSATION_ID")
	if convID == "" {
		t.Skip("set KB_PRESTEP_VERIFY_CONVERSATION_ID to run the KB pre-step verification")
	}
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
