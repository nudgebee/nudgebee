//go:build e2e

package api

import (
	"os"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/agents"
	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// End-to-end check for #34659: the full event-analysis pipeline runs with the
// incident-assembly tool available, PERSISTS its results to event_log_analysis
// (so the UI shows them), calls get_incident_assembly during the run, and the
// persisted investigation and detailed response carry the required
// '### Related Alerts Check' section.
//
// Same env contract as TestLogAnalyzer_ExecuteOOM: TEST_TENANT, TEST_USER,
// TEST_ACCOUNT, plus optional TEST_EVENT_ID (falls back to the account's most
// recent event). Requires the standard local service set (services-server,
// relay, redis, DB) reachable per CLAUDE.md.
//
// Shared-environment caveat: analysis rows are keyed per fingerprint in the
// shared DB. If another llm-server (e.g. the in-cluster one) watches the same
// DB, its recovery loop can race this run and overwrite the persisted rows
// with its own output. Run against an environment where this process is the
// authoritative llm-server for the account.
func TestEventAnalysis_AssemblyToolPersisted(t *testing.T) {
	tenant := os.Getenv("TEST_TENANT")
	account := os.Getenv("TEST_ACCOUNT")
	if tenant == "" || account == "" {
		t.Skip("TEST_TENANT / TEST_ACCOUNT not set")
	}
	userID := os.Getenv("TEST_USER")
	if userID == "" {
		userID = uuid.Nil.String()
	}
	eventID := os.Getenv("TEST_EVENT_ID")
	if eventID == "" {
		eventID = agents.FetchRecentEventID(t, account)
	}

	sc := security.NewRequestContextForTenantAccountAdmin(tenant, userID, []string{})
	started := time.Now().UTC()

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventID,
		AccountId:  account,
		UserId:     userID,
		Regenerate: true,
	})
	require.NoError(t, err)
	require.Equal(t, string(events.AnalysisStatusCompleted), resp.Status)

	dbManager, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)
	fingerprint, aggKey, err := getEventIdentity(dbManager, EventAnalysisRequest{EventId: eventID, AccountId: account})
	require.NoError(t, err)

	repo := events.NewEventAnalysisRepository(dbManager)

	// Persistence: the pipeline's output must be in event_log_analysis (what the
	// UI reads), not only in the HTTP response.
	inv, err := repo.GetEventAnalysis(sc, fingerprint, aggKey, account, events.AnalysisTypeInvestigation)
	require.NoError(t, err)
	require.NotNil(t, inv, "investigation row not persisted")
	assert.Equal(t, string(events.AnalysisStatusCompleted), inv.Status)
	assert.Contains(t, inv.Summary, "Related Alerts Check",
		"persisted investigation missing the required Related Alerts Check section")

	detailed, err := repo.GetEventAnalysis(sc, fingerprint, aggKey, account, events.AnalysisTypeDetailedResponse)
	require.NoError(t, err)
	require.NotNil(t, detailed, "detailed_response row not persisted")
	assert.Contains(t, detailed.Summary, "Related Alerts Check",
		"synthesis dropped the Related Alerts Check section from the final report")

	// The section must come from the tool, not from thin air: the run's
	// conversation (deterministic session = prefix + fingerprint) must contain
	// at least one get_incident_assembly call made during this test.
	var toolCalls int
	err = dbManager.Db.QueryRow(`
		SELECT count(*)
		FROM llm_conversation_tool_calls tc
		JOIN llm_conversations c ON c.id = tc.conversation_id
		WHERE c.session_id = $1 AND tc.account_id = $2
		  AND tc.tool_name = 'get_incident_assembly' AND tc.created_at >= $3`,
		events.SessionIdPrefixEvent+fingerprint, account, started).Scan(&toolCalls)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, toolCalls, 1, "get_incident_assembly was never called during the analysis run")

	if strings.TrimSpace(resp.DetailedResponse) == "" {
		t.Log("note: detailed response empty in HTTP response (still persisted separately)")
	}
}
