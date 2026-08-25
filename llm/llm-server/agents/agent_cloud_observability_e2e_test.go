//go:build e2e

package agents

import (
	"os"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
)

// runObservabilityAgentCase drives one cloud CLI observability agent through a
// real conversation against a live account, asserting the round-trip succeeds
// and returns a non-empty answer from the expected agent. Shared by the GCP and
// Azure e2e tables. Requires a workspace pod + real cloud credentials, so it
// only runs under `-tags e2e` with the account env vars set.
func runObservabilityAgentCase(t *testing.T, sc *security.RequestContext, agent core.NBAgent, userId, accountId, sessionId, query string) {
	t.Helper()
	if err := core.DeleteConversationBySession(sessionId, accountId, userId); err != nil {
		t.Fatalf("cleanup failed for session %s: %v", sessionId, err)
	}
	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId, query)
	assert.NoError(t, err, "conversation failed for %s", agent.GetName())
	assert.Equal(t, agent.GetName(), resp.AgentName, "agent name mismatch")
	assert.NotEmpty(t, resp.Response, "expected a non-empty answer from %s", agent.GetName())
}

// TestGcpObservabilityAgents_Execute exercises the GCP metrics/logs/traces CLI
// fallback agents end-to-end. Set TEST_GCP_ACCOUNT (+ TEST_TENANT/TEST_USER) to a
// cloud-only GCP account to run; skipped otherwise.
func TestGcpObservabilityAgents_Execute(t *testing.T) {
	accountId := os.Getenv("TEST_GCP_ACCOUNT")
	if accountId == "" {
		t.Skip("TEST_GCP_ACCOUNT not set — skipping GCP observability e2e")
	}
	userId := os.Getenv("TEST_USER")
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), userId, []string{})

	cases := []struct {
		session string
		agent   core.NBAgent
		query   string
	}{
		{"ut-gcp-metrics-1", newGcpMetricsAgent(accountId), "What is the CPU utilization of my compute instances in the last hour?"},
		{"ut-gcp-logs-1", newGcpLogsAgent(accountId), "Show me recent Cloud Logging entries with severity ERROR in the last hour"},
		{"ut-gcp-traces-1", newGcpTracesAgent(accountId), "Find any traces slower than 500ms in the last hour"},
	}
	for _, tc := range cases {
		t.Run(tc.session, func(t *testing.T) {
			runObservabilityAgentCase(t, sc, tc.agent, userId, accountId, tc.session, tc.query)
		})
	}
}

// TestAzureObservabilityAgents_Execute exercises the Azure metrics/logs/traces
// CLI fallback agents end-to-end. Set TEST_AZURE_ACCOUNT (+ TEST_TENANT/TEST_USER)
// to a cloud-only Azure account to run; skipped otherwise. Azure traces depend on
// Application Insights being configured for the account.
func TestAzureObservabilityAgents_Execute(t *testing.T) {
	accountId := os.Getenv("TEST_AZURE_ACCOUNT")
	if accountId == "" {
		t.Skip("TEST_AZURE_ACCOUNT not set — skipping Azure observability e2e")
	}
	userId := os.Getenv("TEST_USER")
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), userId, []string{})

	cases := []struct {
		session string
		agent   core.NBAgent
		query   string
	}{
		{"ut-azure-metrics-1", newAzureMetricsAgent(accountId), "What is the CPU percentage of my virtual machines in the last hour?"},
		{"ut-azure-logs-1", newAzureLogsAgent(accountId), "Who modified resources in the last 24 hours?"},
		{"ut-azure-traces-1", newAzureTracesAgent(accountId), "Find any slow requests in the last hour"},
	}
	for _, tc := range cases {
		t.Run(tc.session, func(t *testing.T) {
			runObservabilityAgentCase(t, sc, tc.agent, userId, accountId, tc.session, tc.query)
		})
	}
}
