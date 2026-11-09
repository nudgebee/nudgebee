//go:build e2e

package agents

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"os"
	"testing"
	"time"
)

// Runs existing skills without creating, editing, or deleting KB fixtures.
// Persisted tool traces must be inspected separately for content/adherence.
func TestObservabilityExistingSkillLive(t *testing.T) {
	if os.Getenv("RUN_OBSERVABILITY_SKILL_E2E") != "true" {
		t.Skip("opt-in local service test")
	}
	account, user, tenant := os.Getenv("TEST_ACCOUNT"), os.Getenv("TEST_USER"), os.Getenv("TEST_TENANT")
	require.NotEmpty(t, account)
	require.NotEmpty(t, user)
	require.NotEmpty(t, tenant)
	name := os.Getenv("TEST_OBSERVABILITY_AGENT")
	query := os.Getenv("TEST_OBSERVABILITY_QUERY")
	require.NotEmpty(t, name)
	require.NotEmpty(t, query)
	sc := security.NewRequestContextForTenantAccountAdmin(tenant, user, []string{account})
	agent, ok := core.GetNBAgent(sc, name, account, core.AgentStatusEnabled)
	require.True(t, ok, "agent must be enabled")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	invocation := security.NewRequestContext(ctx, sc.GetSecurityContext(), sc.GetLogger(), sc.GetTracer(), sc.GetMeter())
	session := "observability-skill-" + name + "-" + uuid.NewString()
	t.Logf("OBS_SESSION=%s agent=%s", session, name)
	response, err := core.HandleConversationSessionRequest(invocation, agent, user, account, session, query,
		core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{LlmConfigSource: "env:global"}))
	require.NoError(t, err)
	require.NotEmpty(t, response.Response)
	conversationID, messageID := response.ConversationId, response.MessageId
	require.NotEmpty(t, conversationID)
	require.NotEmpty(t, messageID)
	t.Logf("OBS_RESULT session=%s conversation=%s message=%s status=%s", session, conversationID, messageID, response.Status)
}

func TestObservabilityExistingSkillAudit(t *testing.T) {
	conversationID := os.Getenv("TEST_OBSERVABILITY_CONVERSATION_ID")
	if conversationID == "" {
		t.Skip("set TEST_OBSERVABILITY_CONVERSATION_ID")
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)
	var calls []struct {
		Name       string `db:"tool_name"`
		Status     string `db:"status"`
		Parameters string `db:"parameters"`
		Response   string `db:"response"`
	}
	require.NoError(t, dbms.Db.Select(&calls, `
		SELECT COALESCE(tool_name, '') AS tool_name, COALESCE(status, '') AS status,
		       COALESCE(parameters, '') AS parameters, COALESCE(response, '') AS response
		FROM llm_conversation_tool_calls WHERE conversation_id = $1
		ORDER BY created_at`, conversationID))
	for _, call := range calls {
		response := call.Response
		if len(response) > 500 {
			response = response[:500]
		}
		t.Logf("AUDIT_TOOL name=%s status=%s parameters=%s response_prefix=%s", call.Name, call.Status, call.Parameters, response)
	}
	var refs []struct {
		Type string `db:"reference_type"`
		ID   string `db:"reference_id"`
	}
	require.NoError(t, dbms.Db.Select(&refs, `
		SELECT reference_type, reference_id FROM llm_conversation_references
		WHERE conversation_id = $1 ORDER BY created_at`, conversationID))
	for _, ref := range refs {
		t.Logf("AUDIT_REF type=%s id=%s", ref.Type, ref.ID)
	}
}

// TestExistingLogsKBInventory verifies the real user-defined KB rows used by
// the logs native-query scenario. It is read-only and intentionally prints
// metadata and query-shaped lines only, not the complete user documents.
func TestExistingLogsKBInventory(t *testing.T) {
	if os.Getenv("RUN_OBSERVABILITY_SKILL_E2E") != "true" {
		t.Skip("opt-in local service test")
	}
	account, user, tenant := os.Getenv("TEST_ACCOUNT"), os.Getenv("TEST_USER"), os.Getenv("TEST_TENANT")
	require.NotEmpty(t, account)
	require.NotEmpty(t, user)
	require.NotEmpty(t, tenant)
	sc := security.NewRequestContextForTenantAccountAdmin(tenant, user, []string{account})
	wanted := map[string]bool{
		"Nudgebee_Namespace_Log_Correlation":  false,
		"Checkout_Service_Log_Triage_Runbook": false,
	}
	kbs, err := toolcore.ListKnowledgebases(sc, account)
	require.NoError(t, err)
	mappings, err := toolcore.ListKBAgentMappings(sc, account)
	require.NoError(t, err)
	for _, listed := range kbs {
		if _, ok := wanted[listed.Name]; !ok {
			continue
		}
		wanted[listed.Name] = true
		kb, getErr := toolcore.GetKnowledgebase(sc, account, listed.Id)
		require.NoError(t, getErr)
		var agents []string
		for _, mapping := range mappings {
			if mapping.KbId == kb.Id {
				agents = append(agents, mapping.AgentId)
			}
		}
		t.Logf("KB name=%s id=%s status=%s enabled=%t bytes=%d mappings=%v", kb.Name, kb.Id, kb.Status, kb.Enabled, kb.DataSizeBytes, agents)
		for _, line := range strings.Split(kb.Data, "\n") {
			trimmed := strings.TrimSpace(line)
			lower := strings.ToLower(trimmed)
			if strings.Contains(trimmed, "{namespace=") || strings.Contains(trimmed, "|=") || strings.Contains(lower, "query") ||
				(kb.Name == "Checkout_Service_Log_Triage_Runbook" && (strings.Contains(lower, "checkout") || strings.Contains(lower, "error") || strings.Contains(lower, "log"))) {
				t.Logf("KB_QUERY_LINE name=%s line=%q", kb.Name, trimmed)
			}
		}
	}
	for name, found := range wanted {
		require.True(t, found, "existing KB %q not found", name)
	}
}
