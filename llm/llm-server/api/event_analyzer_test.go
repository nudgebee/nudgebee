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

func TestLogAnalyzer_ExecuteOOMWithRCA(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	eventId := "2cdcae7d-aa42-4740-95a9-622207ee90b0"
	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  os.Getenv("TEST_ACCOUNT"),
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.NotEmpty(t, resp.Analysis)
	assert.NotEmpty(t, resp.DetailedResponse)
	assert.Equal(t, resp.Status, "COMPLETED")

	resp, err = analyzeEventRCAUsingAgentsAndUpdateDb(sc, EventRCAAnalysisRequest{
		EventId:    eventId,
		AccountId:  os.Getenv("TEST_ACCOUNT"),
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary) // RCA result is stored in Summary, not Analysis
	assert.Equal(t, resp.Status, "COMPLETED")
}

// TODO mock DBs
// TODO mock Tool Execution
func TestLogAnalyzer_ExecutePodMemoryReachingLimit(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	eventId := "121d44fc-c595-4a75-89e9-efcb4c4c9116"
	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  os.Getenv("TEST_ACCOUNT"),
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestLogAnalyzer_ExecuteJobFailureReachingLimit(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	eventId := "601fd06d-b83d-4507-a355-27f3dae60de8"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  os.Getenv("TEST_ACCOUNT"),
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestLogAnalyzer_ExecuteLog3(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	eventId := "121d44fc-c595-4a75-89e9-efcb4c4c9116"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  os.Getenv("TEST_ACCOUNT"),
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")

	resp, err = analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  "6c008cf8-4d79-4999-8447-573a697d0652",
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestLogAnalyzer_ExecuteWithoutLog2(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	eventId := "c554ecef-be21-4393-9f43-391ed34b2844"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  os.Getenv("TEST_ACCOUNT"),
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestLogAnalyzer_ExecuteWithLog(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	eventId := "a4ba52dc-fc3e-436d-a842-5ef08175146a"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  os.Getenv("TEST_ACCOUNT"),
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.NotEmpty(t, resp.DetailedResponse)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestLogAnalyzer_CrashLoop(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	eventId := "8d321440-cc59-4bad-b565-de13bc7d5d74"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  os.Getenv("TEST_ACCOUNT"),
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})

	if err != nil {
		t.Log(err)
	}

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestLogAnalyzer_CrashLoop2(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	eventId := "1d961bcc-e2f7-4a1c-9076-ac84af217324"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  os.Getenv("TEST_ACCOUNT"),
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})

	if err != nil {
		t.Log(err)
	}

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAnalysis_NoProcessing(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	eventId := "09ff808a-08b9-441a-b3de-2d51b04cb1bb"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  os.Getenv("TEST_ACCOUNT"),
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAnalysis_LogAnalysisUsingTraces(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	eventId := "f152ca54-63ea-46c0-a06d-2aab9ef2e275"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  os.Getenv("TEST_ACCOUNT"),
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAnalysis_WithCustomAction(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	eventId := "b8cd903e-ebc2-4dc8-b1bd-bfad2e75a13f"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  os.Getenv("TEST_ACCOUNT"),
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAnalysis_WithAdditionalDataAnalysis(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	eventId := "31029604-5832-499c-acb9-733ec6691446"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  os.Getenv("TEST_ACCOUNT"),
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAnalysis_WithAdditionalDataAnalysis2(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	eventId := "9f45575e-b251-49d2-9049-6b6c4105fa08"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  "d612a4ef-13bd-497f-8081-86b0ff0f7fb3",
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAnalysis_WithAdditionalDataAnalysis3(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	eventId := "87a514f2-6c5d-4f55-96af-cebde4f9d431"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  "ff87fbfd-5729-4474-b9d6-96beb693e3fd",
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAnalyzer_PlannerMoreAggressive(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	eventId := "1c8a5088-7a59-4b0f-a438-674f3ee22acc"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  os.Getenv("TEST_ACCOUNT"),
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAnalyzer_PlannerWithLLMResponse(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	eventId := "f2289275-30fe-40c7-a4c5-32d791d70656"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  os.Getenv("TEST_ACCOUNT"),
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAnalyzer_OOMIssueTest(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	eventId := "20ef1676-cee9-4321-8e5c-ac4072b07627"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  os.Getenv("TEST_ACCOUNT"),
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAnalyzer_IssueTest2(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	eventId := "cf6ad88e-a38b-4642-8a9b-69c4d20f2811"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  os.Getenv("TEST_ACCOUNT"),
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAnalyzer_IssueTest3(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	eventId := "80e3c03d-e216-4c8b-b346-444021675523"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  os.Getenv("TEST_ACCOUNT"),
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAnalyzer_CustomInstructions(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin("6d79c39f-920e-4167-85cf-e2ee83dcbc03", "fb1609aa-87a5-4ec0-a5f7-55673df184e8", []string{})

	eventId := "45255a42-65c9-4e5e-8a4c-dad3a79be823"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  "e9dc2b39-78d3-46da-9691-f160bd3c4c19",
		UserId:     "fb1609aa-87a5-4ec0-a5f7-55673df184e8",
		Regenerate: true,
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAnalyzer_CustomAlertInstructions(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	eventId := "0b053690-f86b-4a75-9b01-8a11cde04245"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  os.Getenv("TEST_ACCOUNT"),
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAnalyzer_SlackResponse(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	eventId := "b2129fc1-0258-4c0f-acd9-d226a4568f2d"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  os.Getenv("TEST_ACCOUNT"),
		UserId:     os.Getenv("TEST_USER"),
		Regenerate: true,
		Source:     string(core.ConversationSourceInstantNotification),
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAnalyzer_SignozLogs(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin("6d79c39f-920e-4167-85cf-e2ee83dcbc03", "fb1609aa-87a5-4ec0-a5f7-55673df184e8", []string{})

	eventId := "ca8a5e90-3790-481d-b989-0f523d21cf60"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  "e9dc2b39-78d3-46da-9691-f160bd3c4c19",
		UserId:     "fb1609aa-87a5-4ec0-a5f7-55673df184e8",
		Regenerate: true,
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAnalyzer_PostgresCacheHitIssue(t *testing.T) {
	sc := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))

	eventId := "983897e7-9afb-48c5-bd27-83a8784e5f2d"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  os.Getenv("TEST_ACCOUNT"),
		Regenerate: true,
		Source:     string(core.ConversationSourceInstantNotification),
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAnalyzer_CloudAccount(t *testing.T) {
	sc := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))

	eventId := "61184c30-225e-45f7-acd5-63e2f83fbcad"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  "49145907-981b-48ad-a67a-08a4e8099cb2",
		Regenerate: true,
		Source:     string(core.ConversationSourceInstantNotification),
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAnalyzer_VM(t *testing.T) {
	sc := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))

	eventId := "58ef94ae-4d3e-4726-bbc8-c8361c54156d"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  "a2a30b02-0f67-42e5-a2ab-c658230fd798",
		Regenerate: true,
		Source:     string(core.ConversationSourceInstantNotification),
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAnalyzer_Rabbit(t *testing.T) {
	sc := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))

	eventId := "da0f9a04-1b05-402a-983c-0068948397db"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  "a2a30b02-0f67-42e5-a2ab-c658230fd798",
		Regenerate: true,
		Source:     string(core.ConversationSourceInstantNotification),
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAnalyzer_CrashLoop(t *testing.T) {
	sc := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))

	eventId := "58d05bf3-291d-4ead-83ef-3b10ba15415c"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  "a2a30b02-0f67-42e5-a2ab-c658230fd798",
		Regenerate: true,
		Source:     string(core.ConversationSourceInstantNotification),
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAnalyzer_TCPConnection(t *testing.T) {
	sc := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))

	eventId := "398917bf-a8bf-4854-97ae-e760259827ea"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  "a2a30b02-0f67-42e5-a2ab-c658230fd798",
		Regenerate: true,
		Source:     string(core.ConversationSourceInstantNotification),
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAnalyzer_KB_DeploymentHasNoMatch(t *testing.T) {
	sc := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))

	eventId := "58487a34-0fdb-40aa-9fba-7822a62282df"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  "a2a30b02-0f67-42e5-a2ab-c658230fd798",
		Regenerate: true,
		Source:     string(core.ConversationSourceInstantNotification),
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAnalyzer_IssueWithSummary(t *testing.T) {
	sc := security.NewRequestContextForTenantAdmin("b831c017-87e4-4f5e-a380-54fd40f519d2")

	accountId := "a2a30b02-0f67-42e5-a2ab-c658230fd798"
	eventId := "9d92a450-2219-4744-a7a7-e7ba8566793f"

	resp, err := analyzeEventUsingAgentsAndUpdateDb(sc, EventAnalysisRequest{
		EventId:    eventId,
		AccountId:  accountId,
		Regenerate: true,
		Source:     string(core.ConversationSourceInstantNotification),
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")

	resp, err = analyzeEventRCAUsingAgentsAndUpdateDb(sc, EventRCAAnalysisRequest{
		EventId:    eventId,
		AccountId:  accountId,
		Regenerate: true,
		Generate:   true,
	})

	if err != nil {
		t.Logf("Error during RCA analysis: %v", err)
	}

	assert.Nil(t, err)
	assert.NotEmpty(t, resp.Summary)
	assert.Equal(t, resp.Status, "COMPLETED")
}

func TestEventAgentFailures(t *testing.T) {
	testCases :=
		[]struct {
			TenantId  string
			SessionId string
			AccountId string
			UserId    string
			EventId   string
		}{
			{
				TenantId:  "b831c017-87e4-4f5e-a380-54fd40f519d2",
				SessionId: "ut-events-chain-failure-1..",
				AccountId: "496fe460-d542-498b-8cab-a7cb9cce8eb2",
				UserId:    "",
				EventId:   "b2b86ad0-19d8-47a9-a29d-1e8e798f3584",
			},
		}
	for _, tc := range testCases {

		ctx := security.NewRequestContextForTenantAdmin(tc.TenantId)
		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		eventData, err := getEventData(ctx, EventAnalysisRequest{
			EventId:   tc.EventId,
			AccountId: tc.AccountId,
			UserId:    tc.UserId,
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

		parentConversationId := tc.SessionId

		err = core.DeleteConversationBySession(parentConversationId, tc.AccountId, tc.UserId)
		if err != nil {
			ctx.GetLogger().Error("analyzer: unable to delete conversation.", "error", err)
		}

		//generate initial summary if not present
		eventSummaryAgent, _ := core.GetNBAgent(ctx, agents.EventsAgentName, tc.AccountId, core.AgentStatusEnabled)
		summaryResponse, err := core.HandleConversationSessionRequest(ctx, eventSummaryAgent, tc.UserId, tc.AccountId, parentConversationId, "Get the details of Event with id - "+eventData.Id, core.ConversationSessionRequestWithSource(core.ConversationSourceInvestigation), core.ConversationSessionRequestWithEnableCritique(false), core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{
			Labels: parsedLabels,
		}))
		assert.Nil(t, err)
		assert.NotEmpty(t, summaryResponse.Response)
	}

}
