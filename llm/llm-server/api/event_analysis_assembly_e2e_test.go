//go:build e2e

package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/agents"
	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"

	"go.opentelemetry.io/otel"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startWorkspaceProxyForE2E serves only the workspace callback routes
// (/api/v1/workspace/*) on TEST_WORKSPACE_PROXY_ADDR (e.g. ":8005") for the
// test's lifetime. Workspace CLI shims (aws, gcloud, nbctl) call back to the
// llm-server there; an in-process test serves no HTTP otherwise, so every such
// call failed with "connection refused". No-op when the variable is unset.
func startWorkspaceProxyForE2E(t *testing.T) {
	addr := os.Getenv("TEST_WORKSPACE_PROXY_ADDR")
	if addr == "" {
		return
	}
	r := gin.New()
	handleWorkspaceApis(r, otel.Tracer("e2e"), otel.Meter("e2e"))
	srv := &http.Server{Addr: addr, Handler: r, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Logf("workspace proxy on %s stopped: %v", addr, err)
		}
	}()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
}

// End-to-end check for #34659 / #41579: the full event-analysis pipeline
// PERSISTS its results to event_log_analysis (so the UI shows them), the
// investigation prompt carries the incident assembly inline, the investigation
// does not fetch it again, and the persisted investigation and detailed response
// carry the required '### Related Alerts Check' section.
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
	startWorkspaceProxyForE2E(t)
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
	var fingerprint, aggKey string
	err = dbManager.Db.QueryRow(`SELECT fingerprint, aggregation_key FROM events WHERE id = $1 AND cloud_account_id = $2`, eventID, account).Scan(&fingerprint, &aggKey)
	require.NoError(t, err)
	if fingerprint == "" {
		fingerprint = eventID
	}

	repo := events.NewEventAnalysisRepository(dbManager)

	// Persistence: the pipeline's output must be in event_log_analysis (what the
	// UI reads), not only in the HTTP response.
	inv, err := repo.GetEventAnalysis(sc, eventID, fingerprint, aggKey, account, events.AnalysisTypeInvestigation)
	require.NoError(t, err)
	require.NotNil(t, inv, "investigation row not persisted")
	assert.Equal(t, string(events.AnalysisStatusCompleted), inv.Status)
	assert.Contains(t, inv.Summary, "Related Alerts Check",
		"persisted investigation missing the required Related Alerts Check section")

	detailed, err := repo.GetEventAnalysis(sc, eventID, fingerprint, aggKey, account, events.AnalysisTypeDetailedResponse)
	require.NoError(t, err)
	require.NotNil(t, detailed, "detailed_response row not persisted")
	assert.Contains(t, detailed.Summary, "Related Alerts Check",
		"synthesis dropped the Related Alerts Check section from the final report")

	// #41579: the assembly is fetched in code and inlined in the investigation
	// prompt, so the orchestrator must not fetch it again: no
	// get_event_incident_assembly call and no events sub-agent call in this run.
	var inlined int
	err = dbManager.Db.QueryRow(`
		SELECT count(*)
		FROM llm_conversation_messages m
		JOIN llm_conversations c ON c.id = m.conversation_id
		WHERE c.session_id = $1 AND m.account_id = $2 AND m.created_at >= $3
		  AND m.message LIKE '%## Related-Alert Candidates%' AND m.message LIKE '%already included below%'`,
		events.SessionIdPrefixEvent+fingerprint, account, started).Scan(&inlined)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, inlined, 1, "investigation prompt does not carry the inlined incident assembly")

	// What the inline replaces is the orchestrator asking again for THIS
	// event's assembly. Exploring other events (an AWS orchestrator reading
	// EC2 state-change events through the events tool) is investigation, not
	// a refetch, so only this event's assembly outside step 1 counts.
	var refetches int
	err = dbManager.Db.QueryRow(`
		SELECT count(*)
		FROM llm_conversation_tool_calls tc
		JOIN llm_conversations c ON c.id = tc.conversation_id
		JOIN llm_conversation_messages m ON m.id = tc.message_id
		WHERE c.session_id = $1 AND tc.account_id = $2 AND tc.created_at >= $3
		  AND m.agent_name NOT IN ('events', 'LLM')
		  AND tc.tool_name = 'get_event_incident_assembly' AND tc.parameters LIKE '%' || $4 || '%'`,
		events.SessionIdPrefixEvent+fingerprint, account, started, eventID).Scan(&refetches)
	require.NoError(t, err)
	assert.Zero(t, refetches, "the investigation fetched this event's related alerts again despite the inlined assembly")

	if strings.TrimSpace(resp.DetailedResponse) == "" {
		t.Log("note: detailed response empty in HTTP response (still persisted separately)")
	}
}

// End-to-end check for #41579 with the direct brief forced on (no feature_flag
// row): step 1 is one LLM-agent call with no events agent, the brief keeps the
// five sections and calls nothing unavailable, and the report keeps its Related
// Alerts Check. Logs the run's calls and cost per agent for the PR comparison.
// Same env contract as TestEventAnalysis_AssemblyToolPersisted.
func TestEventAnalysis_DirectBrief(t *testing.T) {
	startWorkspaceProxyForE2E(t)
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

	prev := eventDirectBriefEnabled
	eventDirectBriefEnabled = func(*security.RequestContext, string) bool { return true }
	t.Cleanup(func() { eventDirectBriefEnabled = prev })

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
	var fingerprint, aggKey string
	require.NoError(t, dbManager.Db.QueryRow(`SELECT fingerprint, aggregation_key FROM events WHERE id = $1 AND cloud_account_id = $2`, eventID, account).Scan(&fingerprint, &aggKey))
	if fingerprint == "" {
		fingerprint = eventID
	}
	session := events.SessionIdPrefixEvent + fingerprint
	repo := events.NewEventAnalysisRepository(dbManager)

	summary, err := repo.GetEventAnalysis(sc, eventID, fingerprint, aggKey, account, events.AnalysisTypeSummary)
	require.NoError(t, err)
	require.NotNil(t, summary)
	for _, h := range []string{"### What is broken", "### Since when", "### What changed just before", "### What else is firing", "### Priority"} {
		assert.Contains(t, summary.Summary, h)
	}
	// Absence claims only: the event's own errors can say "service unavailable".
	lower := strings.ToLower(summary.Summary)
	for _, claim := range []string{"is unavailable", "are unavailable", "not available", "is unknown", "are unknown", "is missing", "are missing"} {
		assert.NotContains(t, lower, claim, "the brief must not call data absent")
	}
	t.Logf("brief:\n%s", summary.Summary)

	detailed, err := repo.GetEventAnalysis(sc, eventID, fingerprint, aggKey, account, events.AnalysisTypeDetailedResponse)
	require.NoError(t, err)
	require.NotNil(t, detailed)
	assert.Contains(t, detailed.Summary, "Related Alerts Check")

	rows, err := dbManager.Db.Query(`
		SELECT coalesce(m.agent_name, '') AS msg_agent, t.agent_name, count(*), coalesce(sum(t.cost_usd), 0)
		FROM llm_conversation_token_usage t
		JOIN llm_conversations c ON c.id = t.conversation_id
		LEFT JOIN llm_conversation_messages m ON m.id = t.message_id
		WHERE c.session_id = $1 AND c.account_id = $2 AND t.created_at >= $3
		GROUP BY 1, 2 ORDER BY 4 DESC`, session, account, started)
	require.NoError(t, err)
	defer rows.Close()
	var step1LLM, step1Events, calls int
	var cost float64
	for rows.Next() {
		var msgAgent, agent string
		var n int
		var c float64
		require.NoError(t, rows.Scan(&msgAgent, &agent, &n, &c))
		t.Logf("usage: message=%s agent=%s calls=%d cost=$%.4f", msgAgent, agent, n, c)
		calls += n
		cost += c
		if msgAgent == "LLM" && agent == "LLM" {
			step1LLM += n
		}
		if agent == "events" && msgAgent == "events" {
			step1Events += n
		}
	}
	t.Logf("run total: calls=%d cost=$%.4f event=%s account=%s", calls, cost, eventID, account)
	assert.Equal(t, 1, step1LLM, "step 1 should be one LLM-agent call")
	assert.Zero(t, step1Events, "step 1 must not run the events agent")
}
