//go:build e2e

package agents

import (
	"context"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"nudgebee/llm/agents/core"
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
	t.Logf("OBS_RESULT session=%s status=%s", session, response.Status)
}
