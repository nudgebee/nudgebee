//go:build e2e

package agents

// TestKBPrestepE2E exercises create -> index -> discover -> load -> answer ->
// delete against real DB/RAG/LLM services. The historical test name is retained.
//
// Run from llm-server with TEST_TENANT, TEST_USER, TEST_ACCOUNT and service
// credentials exported:
// RUN_KB_PRESTEP_E2E=true go test -tags=e2e -count=1 -v -timeout 15m \
//   -run '^TestKBPrestepE2E$' ./agents
//
// TEST_SKILL_AGENT optionally selects an existing declarative ReAct agent by
// name. Otherwise the built-in K8s orchestrator is used. TEST_SKILL_UNMAPPED=true
// skips mapping to exercise account-wide discovery. Only knowledge tools are
// allowed during the invocation; no infrastructure tools can execute.
//
// Cleanup removes the test-owned KB/mapping. Conversations remain for inspection.
// Like all t.Cleanup callbacks, cleanup cannot run after SIGKILL/process timeout;
// the test logs the exact KB ID for recovery in that case.

import (
	"context"
	"fmt"
	"os"
	"strconv"
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

func TestKBPrestepE2E(t *testing.T) {
	runSkillLifecycle(t, false)
}

func TestSkillDelegationE2E(t *testing.T) {
	if os.Getenv("RUN_KB_PRESTEP_E2E") != "true" {
		t.Skip("set RUN_KB_PRESTEP_E2E=true to run the live delegation lifecycle test")
	}
	runSkillLifecycle(t, true)
}

func runSkillLifecycle(t *testing.T, delegated bool) {
	t.Helper()
	tenantID, userID, accountID := os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), os.Getenv("TEST_ACCOUNT")
	require.NotEmpty(t, tenantID, "TEST_TENANT is required")
	require.NotEmpty(t, userID, "TEST_USER is required")
	require.NotEmpty(t, accountID, "TEST_ACCOUNT is required")
	dbms, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err, "connect to metastore")

	sc := security.NewRequestContextForTenantAccountAdmin(tenantID, userID, []string{accountID})
	var agent core.NBAgent = newK8sOrchestratorAgent(accountID)
	if name := os.Getenv("TEST_SKILL_AGENT"); name != "" && !delegated {
		var found bool
		agent, found = core.GetNBAgent(sc, name, accountID, core.AgentStatusEnabled)
		require.True(t, found, "TEST_SKILL_AGENT must name an existing enabled agent")
	}
	require.Contains(t, []core.AgentPlannerType{core.AgentPlannerTypeReAct, core.AgentPlannerTypeOrchestrating},
		agent.GetPlannerType(), "this test exercises tool-loop agents, not direct custom planner")
	mappedAgent := agent.GetName()
	if delegated {
		mappedAgent = DelegateAgentToolName
	}

	prevTrace := config.Config.LlmTraceEnabled
	config.Config.LlmTraceEnabled = true
	t.Cleanup(func() { config.Config.LlmTraceEnabled = prevTrace })

	suffix := uuid.NewString()
	service := "checkout-e2e-" + suffix[:8]
	canary := "SKILL-CANARY-" + uuid.NewString()
	// Keep the answer marker beyond candidate previews, so reading a menu alone
	// cannot satisfy the answer assertion. The query never includes the marker.
	body := fmt.Sprintf("High memory procedure for %s\n\n", service) +
		strings.Repeat("This procedure documents expected elevated memory during the nightly reconciliation window. Consult the resolution section for the operator response and reference code.\n", 5) +
		fmt.Sprintf("\nResolution: Do not restart or scale %s. Contact the service owner and quote reference code %s. Await explicit confirmation before acting.\n", service, canary)
	if raw := os.Getenv("TEST_SKILL_LARGE_BYTES"); raw != "" {
		size, err := strconv.Atoi(raw)
		require.NoError(t, err)
		require.Greater(t, size, 4096)
		require.LessOrEqual(t, size, 1024*1024)
		split := strings.Index(body, "\nResolution:")
		require.Greater(t, split, 0)
		body = body[:split] + strings.Repeat("Background reference only; see the Resolution section for the required response.\n", size/80) + body[split:]
	}
	kb, err := toolcore.CreateKnowledgebase(sc, accountID, toolcore.Knowledgebase{
		Name: service + " memory procedure", Description: "Operator procedure and reference code for high memory on " + service,
		Data: body, DataFormat: "text", DataFilename: "skill_lifecycle_canary.txt",
	})
	require.NoError(t, err, "create test KB")
	t.Logf("CREATED test KB id=%s name=%s mapped_agent=%s", kb.Id, kb.Name, mappedAgent)
	mapped := false
	t.Cleanup(func() {
		if mapped {
			assert.NoError(t, toolcore.UnmapKBFromAgent(sc, accountID, kb.Id, mappedAgent), "cleanup: unmap test KB %s", kb.Id)
		}
		deleteErr := toolcore.DeleteKnowledgebase(sc, accountID, kb.Id)
		assert.NoError(t, deleteErr, "cleanup: delete test KB %s", kb.Id)
		var count int
		checkErr := dbms.Db.Get(&count, "SELECT count(*) FROM llm_knowledgebases WHERE id = $1 AND account_id = $2", kb.Id, accountID)
		assert.NoError(t, checkErr, "cleanup: verify KB deletion")
		assert.Zero(t, count, "cleanup: test KB %s remains", kb.Id)
		if deleteErr == nil && checkErr == nil && count == 0 {
			t.Logf("CLEANED test KB id=%s (DB deletion verified; vector cleanup requested by service)", kb.Id)
		}
	})

	var status string
	var indexingErr error
	require.Eventually(t, func() bool {
		cur, getErr := toolcore.GetKnowledgebase(sc, accountID, kb.Id)
		indexingErr = getErr
		if getErr != nil {
			return true
		}
		status = cur.Status
		return status == "active" || status == "error"
	}, 3*time.Minute, 2*time.Second, "KB indexing did not finish")
	require.NoError(t, indexingErr, "read indexing status")
	require.Equal(t, "active", status, "KB indexing failed")
	if delegated || os.Getenv("TEST_SKILL_UNMAPPED") != "true" {
		_, err = toolcore.MapKBToAgent(sc, accountID, kb.Id, mappedAgent)
		require.NoError(t, err, "map test KB")
		mapped = true
	}

	sessionID := "skill-lifecycle-" + suffix
	// Bound the agent before the Go process timeout, leaving cleanup time.
	runCtx, cancel := context.WithTimeout(sc.GetContext(), 6*time.Minute)
	defer cancel()
	invocation := security.NewRequestContext(runCtx, sc.GetSecurityContext(), sc.GetLogger(), sc.GetTracer(), sc.GetMeter())
	query := fmt.Sprintf("According to the documented high-memory procedure for %s, what should the operator do and what reference code should they quote? Do not inspect or change infrastructure.", service)
	allowedTools := []string{"load_skills", "search_skills"}
	if !delegated && os.Getenv("TEST_SKILL_SHELL") == "true" {
		allowedTools = append(allowedTools, "shell_execute")
	}
	if delegated {
		allowedTools = append(allowedTools, DelegateAgentToolName)
		// Keep the actual question first: automatic discovery caps its query
		// before the LLM sees these test-specific delegation instructions.
		query += " Delegate exactly once via delegate_agent with tools [\"search_skills\", \"load_skills\"]. " +
			"Have the specialist independently discover and load the document. " +
			"Do not load it yourself or supply its contents. Summarize the specialist's answer."
	}
	resp, runErr := core.HandleConversationSessionRequest(invocation, agent, userID, accountID, sessionID,
		query,
		core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{LlmConfigSource: os.Getenv("TEST_SKILL_CONFIG_SOURCE")}),
		core.ConversationSessionRequestWithCapabilities(toolcore.AgentCapabilities{
			AllowedTools: allowedTools,
		}))
	t.Logf("INVOCATION session=%s", sessionID)
	require.NoError(t, runErr, "agent invocation")
	convID, msgID := scenarioLastMsgID(t, sessionID, userID)
	t.Logf("INSPECT conversation=%s message=%s", convID, msgID)

	// Persistence is asynchronous. Poll for the exact fixture rather than
	// returning after the first trace row (which can precede load_skills).
	var menuCount, loadCount, refCount int
	var childID string
	var evidenceErr error
	require.Eventually(t, func() bool {
		if delegated {
			var childIDs []string
			evidenceErr = dbms.Db.Select(&childIDs, `
				SELECT DISTINCT child.id::text
				FROM llm_conversation_tool_calls call
				JOIN llm_conversation_agent child ON child.id::text = call.child_agent_id::text
				JOIN llm_conversation_agent parent ON parent.id::text = call.agent_id::text
				WHERE call.conversation_id = $1 AND call.message_id = $2
				  AND call.tool_name = 'delegate_agent' AND call.status = 'success'
				  AND child.parent_agent_id::text = parent.id::text
				  AND child.conversation_id::text = call.conversation_id::text AND child.message_id::text = call.message_id::text
				  AND parent.parent_agent_id = '00000000-0000-0000-0000-000000000000'`, convID, msgID)
			if evidenceErr != nil || len(childIDs) != 1 {
				return false
			}
			childID = childIDs[0]
		}
		var traces []string
		evidenceErr = dbms.Db.Select(&traces, `
			SELECT prompt_messages::text FROM llm_conversation_token_usage
			WHERE conversation_id = $1 AND message_id = $2
			  AND ($3 = '' OR agent_id::text = $3)
			  AND prompt_messages::text LIKE '%skill-lists%'`, convID, msgID, childID)
		if evidenceErr != nil {
			return false
		}
		menuCount = 0
		for _, trace := range traces {
			if skillLifecycleMenuContains(trace, service) {
				menuCount++
			}
		}
		evidenceErr = dbms.Db.Get(&loadCount, `
			SELECT count(*) FROM llm_conversation_tool_calls
			WHERE conversation_id = $1 AND message_id = $2
			  AND ($4 = '' OR agent_id::text = $4)
			  AND tool_name IN ('load_skills', 'shell_execute') AND status = 'success'
			  AND response LIKE $3`, convID, msgID, "%"+canary+"%", childID)
		if evidenceErr != nil {
			return false
		}
		var referenceIDs []string
		evidenceErr = dbms.Db.Select(&referenceIDs, `
			SELECT reference_id FROM llm_conversation_references
			WHERE conversation_id = $1 AND message_id = $2
			  AND ($3 = '' OR agent_id::text = $3)
			  AND reference_type IN ('knowledge_base', 'skill')`, convID, msgID, childID)
		refCount = 0
		for _, referenceID := range referenceIDs {
			if skillLifecycleReferenceMatches(referenceID, kb.Id) {
				refCount++
			}
		}
		return evidenceErr == nil && menuCount > 0 && loadCount > 0 && refCount > 0
	}, 90*time.Second, time.Second, "discovery/load/reference evidence missing")
	require.NoError(t, evidenceErr, "query persisted evidence")
	if delegated {
		require.NotEmpty(t, childID, "DELEGATION: expected exactly one successful linked root-to-child invocation")
		var leakedCanary int
		require.NoError(t, dbms.Db.Get(&leakedCanary, `
			SELECT count(*) FROM llm_conversation_tool_calls
			WHERE conversation_id = $1 AND message_id = $2
			  AND agent_id::text <> $3 AND tool_name = 'load_skills'
			  AND status = 'success' AND response LIKE $4`, convID, msgID, childID, "%"+canary+"%"))
		assert.Zero(t, leakedCanary, "DELEGATION: fixture must be loaded by the child, not the parent")
		var childQuery string
		require.NoError(t, dbms.Db.Get(&childQuery, "SELECT query FROM llm_conversation_agent WHERE id = $1", childID))
		assert.NotContains(t, childQuery, canary, "DELEGATION: canary must not be supplied in the delegated question")
	}
	assert.Positive(t, menuCount, "DISCOVERY: fixture missing from candidate-menu traces")
	assert.Positive(t, loadCount, "LOADING: no load_skills response contained the canary")
	assert.Positive(t, refCount, "REFERENCES: fixture KB reference not persisted")
	assert.Contains(t, strings.Join(resp.Response, "\n"), canary, "ADHERENCE: final answer did not use the loaded reference code")
	t.Logf("RESULT mapped=%v delegated=%v child=%s menu_traces=%d matching_load_calls=%d references=%d", mapped, delegated, childID, menuCount, loadCount, refCount)
}

// Named loads use the KB UUID; discovered documents use KB_UUID:document_hash
// to keep separate pages from the same KB distinct in Additional Contexts.
func skillLifecycleReferenceMatches(referenceID, kbID string) bool {
	if kbID == "" {
		return false
	}
	owner, documentID, hasDocument := strings.Cut(referenceID, ":")
	return owner == kbID && (!hasDocument || documentID != "")
}

func TestSkillLifecycleReferenceMatches(t *testing.T) {
	for _, tc := range []struct {
		referenceID string
		want        bool
	}{
		{"fixture-kb", true},
		{"fixture-kb:document-hash", true},
		{"fixture-kb-other:document-hash", false},
		{"other-kb:document-hash", false},
		{"knowledge:document-hash", false},
		{"fixture-kb:", false},
		{"", false},
	} {
		t.Run(tc.referenceID, func(t *testing.T) {
			assert.Equal(t, tc.want, skillLifecycleReferenceMatches(tc.referenceID, "fixture-kb"))
		})
	}
	assert.False(t, skillLifecycleReferenceMatches("", ""))
}

// Only inspect candidate-menu content, never the user question (which always
// names the fixture service). Trace JSON can escape angle brackets.
func skillLifecycleMenuContains(trace, fixture string) bool {
	trace = strings.NewReplacer(`\u003c`, "<", `\u003e`, ">", `\n`, "\n").Replace(trace)
	const opening = "<skill-lists>\n"
	for {
		start := strings.Index(trace, opening)
		if start < 0 {
			return false
		}
		trace = trace[start+len(opening):]
		end := strings.Index(trace, "</skill-lists>")
		if end < 0 {
			return false
		}
		if strings.Contains(trace[:end], fixture) {
			return true
		}
		trace = trace[end+len("</skill-lists>"):]
	}
}

func TestSkillLifecycleMenuContains(t *testing.T) {
	assert.False(t, skillLifecycleMenuContains("<skill-lists>\nunrelated</skill-lists> question: checkout-canary", "checkout-canary"))
	assert.False(t, skillLifecycleMenuContains("tool: ids shown in <skill-lists>. question: checkout-canary <skill-lists>\nunrelated</skill-lists>", "checkout-canary"))
	assert.True(t, skillLifecycleMenuContains("<skill-lists>\ncheckout-canary</skill-lists>", "checkout-canary"))
	assert.True(t, skillLifecycleMenuContains(`\u003cskill-lists\u003e\ncheckout-canary\u003c/skill-lists\u003e`, "checkout-canary"))
}
