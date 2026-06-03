//go:build e2e

package api

import (
	"nudgebee/llm/agents"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ============================================================
// Event analyzer integration tests
// ============================================================
//
// These tests exercise `analyzeEventUsingAgentsAndUpdateDb` against a
// populated test DB. They are SMOKE tests: assertions check that the
// analyzer ran and returned a non-empty Summary, NOT that the analyzer
// correctly identified the root cause. See `agents/TESTING.md` for the
// migration target (benchmark-fixture pattern) when stronger validation
// is needed.
//
// The previous version of this file hardcoded ~30 specific event UUIDs
// from the dev DB to seed each test. Those UUIDs are now fetched
// dynamically via `agents.FetchRecentEventID` so the suite isn't pinned
// to internal data — at the cost of "which event got tested" no longer
// being deterministic. This is acceptable because the assertions only
// validate "analyzer didn't crash + produced a summary".

// TestEventAnalyzer_Smoke exercises the default analyze path against
// TEST_ACCOUNT plus the RCA follow-up.
func TestEventAnalyzer_Smoke(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})
	accountID := os.Getenv("TEST_ACCOUNT")
	eventId := agents.FetchRecentEventID(t, accountID)

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  accountID,
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, "COMPLETED", resp.Status)

	rcaResp, err := analyzeEventRCAUsingAgentsAndUpdateDb(sc, EventRCAAnalysisRequest{
		EventId:    eventId,
		AccountId:  accountID,
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, rcaResp.Summary)
	assert.Equal(t, "COMPLETED", rcaResp.Status)
}

// TestEventAnalyzer_InstantNotification exercises the
// ConversationSourceInstantNotification code path.
func TestEventAnalyzer_InstantNotification(t *testing.T) {
	sc := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	accountID := os.Getenv("TEST_ACCOUNT")
	eventId := agents.FetchRecentEventID(t, accountID)

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  accountID,
		Regenerate: true,
		Source:     string(core.ConversationSourceInstantNotification),
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, "COMPLETED", resp.Status)
}

// TestEventAnalyzer_AWSAccount exercises the analyzer against AWS-scoped events.
func TestEventAnalyzer_AWSAccount(t *testing.T) {
	accountID := os.Getenv("TEST_AWS_ACCOUNT")
	if accountID == "" {
		t.Skip("skipping: TEST_AWS_ACCOUNT not set")
	}
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), nil)
	eventId := agents.FetchRecentEventID(t, accountID)

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  accountID,
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, "COMPLETED", resp.Status)
}

// TestEventAnalyzer_AWSAccount2 exercises a secondary AWS account.
// Skipped when TEST_AWS_ACCOUNT_2 is unset.
func TestEventAnalyzer_AWSAccount2(t *testing.T) {
	accountID := os.Getenv("TEST_AWS_ACCOUNT_2")
	if accountID == "" {
		t.Skip("skipping: TEST_AWS_ACCOUNT_2 not set")
	}
	sc := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	eventId := agents.FetchRecentEventID(t, accountID)

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  accountID,
		Regenerate: true,
		Source:     string(core.ConversationSourceInstantNotification),
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, "COMPLETED", resp.Status)
}

// TestEventAnalyzer_SignozTenant exercises the analyzer for a different
// tenant (Signoz integration). Skipped when TEST_SIGNOZ_* are unset.
func TestEventAnalyzer_SignozTenant(t *testing.T) {
	tenant := os.Getenv("TEST_SIGNOZ_TENANT")
	account := os.Getenv("TEST_SIGNOZ_ACCOUNT")
	user := os.Getenv("TEST_SIGNOZ_USER")
	if tenant == "" || account == "" || user == "" {
		t.Skip("skipping: TEST_SIGNOZ_TENANT / TEST_SIGNOZ_ACCOUNT / TEST_SIGNOZ_USER not set")
	}
	sc := security.NewRequestContextForTenantAccountAdmin(tenant, user, []string{})
	eventId := agents.FetchRecentEventID(t, account)

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  account,
		UserId:     user,
		Regenerate: true,
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, "COMPLETED", resp.Status)
}

// TestEventAgentFailures exercises the lower-level event-data + summary-agent
// flow (separate code path from the higher-level `analyzeEvent*` helpers).
// Uses a dedicated failure-scenario tenant/account/event triple, all
// env-gated.
func TestEventAgentFailures(t *testing.T) {
	tenant := os.Getenv("TEST_FAILURE_TENANT")
	account := os.Getenv("TEST_FAILURE_ACCOUNT")
	eventID := os.Getenv("TEST_FAILURE_EVENT_ID")
	if tenant == "" || account == "" || eventID == "" {
		t.Skip("skipping: TEST_FAILURE_TENANT / TEST_FAILURE_ACCOUNT / TEST_FAILURE_EVENT_ID not set")
	}

	sessionID := "ut-events-chain-failure-1.."

	ctx := security.NewRequestContextForTenantAdmin(tenant)
	err := core.DeleteConversationBySession(sessionID, account, "")
	assert.Nil(t, err)

	eventData, err := getEventData(ctx, EventAnalysisRequest{
		EventId:   eventID,
		AccountId: account,
	})
	assert.Nil(t, err)

	parsedLabels := make(map[string]any)
	if labelsStr, ok := eventData.Labels.(string); ok {
		parsedLabels = parseEventLabels(labelsStr)
	}
	if parsedLabels == nil {
		parsedLabels = make(map[string]any)
	}

	if len(parsedLabels) == 0 {
		parsedLabels["subject"] = eventData.SubjectName
		parsedLabels["subject_namespace"] = eventData.SubjectNamespace
		parsedLabels["subject_node"] = eventData.SubjectNode
		parsedLabels["subject_type"] = eventData.SubjectType
		parsedLabels["subject_owner"] = eventData.SubjectOwner
		parsedLabels["aggregation_key"] = eventData.AggregationKey
	}

	if parsedLabels["start"] == nil && eventData.StartsAt != nil {
		parsedLabels["start"] = eventData.StartsAt.UnixMilli()
	}
	if parsedLabels["end"] == nil && eventData.EndsAt != nil {
		parsedLabels["end"] = eventData.EndsAt.UnixMilli()
	} else if parsedLabels["end"] == nil {
		parsedLabels["end"] = time.Now().UnixMilli()
	}

	err = core.DeleteConversationBySession(sessionID, account, "")
	if err != nil {
		ctx.GetLogger().Error("analyzer: unable to delete conversation.", "error", err)
	}

	eventSummaryAgent, _ := core.GetNBAgent(ctx, agents.EventsAgentName, account, core.AgentStatusEnabled)
	summaryResponse, err := core.HandleConversationSessionRequest(
		ctx, eventSummaryAgent, "", account, sessionID,
		"Get the details of Event with id - "+eventData.Id,
		core.ConversationSessionRequestWithSource(core.ConversationSourceInvestigation),
		core.ConversationSessionRequestWithEnableCritique(false),
		core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{
			Labels: parsedLabels,
		}),
	)
	assert.Nil(t, err)
	assert.NotEmpty(t, summaryResponse.Response)
}
