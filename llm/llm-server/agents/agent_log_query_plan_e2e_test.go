//go:build e2e

package agents

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type logQueryPlanProbeRun struct {
	dao            core.IConversationDao
	conversationID string
	sessionID      string
	accountID      string
	userID         string
}

func newLogQueryPlanProbeRun(t *testing.T) *logQueryPlanProbeRun {
	t.Helper()
	tenantID := os.Getenv("TEST_TENANT")
	accountID := os.Getenv("TEST_ACCOUNT")
	userID := os.Getenv("TEST_USER")
	require.NotEmpty(t, tenantID, "TEST_TENANT is required")
	require.NotEmpty(t, accountID, "TEST_ACCOUNT is required")
	require.NotEmpty(t, userID, "TEST_USER is required")

	dao := core.GetConversationDao()
	require.NotNil(t, dao, "conversation DAO is unavailable; verify the test database is running")
	sessionID := "log-query-probe-" + uuid.NewString()[:8]
	conversationID, err := dao.SaveConversation(
		uuid.NewString(), sessionID, tenantID, accountID, userID,
		"", "Logs query planner and execution probe", core.ConversationStatusInProgress,
		core.ConversationSourceUserInvestigation,
		config.Config.LlmProvider, config.Config.LlmModel, nil, "",
		nil,
	)
	require.NoError(t, err, "create persisted probe conversation")

	run := &logQueryPlanProbeRun{
		dao:            dao,
		conversationID: conversationID.String(),
		sessionID:      sessionID,
		accountID:      accountID,
		userID:         userID,
	}
	t.Logf("PRESERVED probe conversation_id=%s session_id=%s", run.conversationID, run.sessionID)
	t.Cleanup(func() {
		// This probe intentionally preserves its row chain for review. Only
		// make the parent row terminal; messages and agents are finalized after
		// each planning call below.
		if err := run.dao.UpdateConversationStatus(run.conversationID, core.ConversationStatusCompleted); err != nil {
			t.Logf("could not finalize preserved conversation %s: %v", run.conversationID, err)
		}
	})
	return run
}

// TestUnifiedLogQueryPlan_Probe exercises the production single-call
// Summary-tier planner without contacting the log backend.
func TestUnifiedLogQueryPlan_Probe(t *testing.T) {
	if os.Getenv("RUN_LOG_QUERY_NATIVE_EXEC_PROBE") == "" {
		t.Skip("gated: set RUN_LOG_QUERY_NATIVE_EXEC_PROBE=1 and the normal live log environment")
	}
	ctx := security.NewRequestContextForTenantAccountAdmin(
		os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")},
	)
	run := newLogQueryPlanProbeRun(t)
	fetchAgent := newFetchLogsAgent(run.accountID)
	fields, indices := fetchLabelsAndIndices(run.accountID, fetchAgent.provider)
	cases := []struct {
		name, query, knowledge, wantType, wantQuery, wantTimeRange string
		wantLimit                                                  int
	}{
		{"complete native preserved", `Execute Query:\n{namespace="payments", app="payment-service"} |= "error"`, "", "native", `{namespace="payments", app="payment-service"} |= "error"`, "1h", 1000},
		{"native with outside constraints", `Run {namespace="payments", app="payment-service"} |= "error" for the last 30m, limit 100`, "", "native", `{namespace="payments", app="payment-service"} |= "error"`, "30m", 100},
		{"native template completed", `Get payment-service errors in payments last 30m limit 100 using {namespace="$namespace", app="$service"} |= "$term"`, "", "native", `{namespace="payments", app="payment-service"} |= "error"`, "30m", 100},
		{"unresolved template falls back", `Get payment-service errors using {namespace="$namespace", app="$service"} |= "$term"`, "", "canonical", "", "", 0},
		{"applicable KB native", "Show payment-service errors in payments last 30m limit 100", `<knowledge>{namespace="payments", app="payment-service"} |= "error"</knowledge>`, "native", `{namespace="payments", app="payment-service"} |= "error"`, "30m", 100},
		{"ordinary request canonical", "Show payment-service errors in payments last 30m limit 100", "", "canonical", "", "", 0},
		{"exact canonical remains canonical", `{"where":{"app":{"_eq":"payment-service"},"namespace":{"_eq":"payments"}},"time_range":"30m","limit":100}`, "", "canonical", "", "", 0},
		{"unrelated KB canonical", "Show payment-service errors in payments last 30m limit 100", `<knowledge>Migration only: {namespace="legacy", app="inventory"} |= "done"</knowledge>`, "canonical", "", "", 0},
		{"conflicting native candidate canonical", `Show payment-service errors, not this legacy query: {namespace="legacy", app="inventory"} |= "done"`, "", "canonical", "", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			messageID, err := run.dao.SaveConversationMessage(
				uuid.NewString(), run.conversationID, run.accountID, run.userID,
				core.MessageRoleHuman, core.MessageTypeGeneration, tc.query, "", LogsAgentName,
				uuid.Nil, nil, "", config.Config.LlmProvider, config.Config.LlmModel,
				core.ConversationStatusInProgress,
			)
			require.NoError(t, err)
			agentID, err := run.dao.SaveConversationAgentCall(
				run.conversationID, messageID.String(), run.accountID, run.userID,
				LogsAgentName, "", tc.query, "", "", "", toolcore.NBQueryConfig{},
			)
			require.NoError(t, err)
			request := core.NBAgentRequest{
				Query: tc.query, OriginalQuery: tc.query, AccountId: run.accountID, UserId: run.userID,
				ConversationId: run.conversationID, MessageId: messageID.String(), AgentId: agentID.String(),
				KBPrestepContent: tc.knowledge,
			}
			plan, err := generateLogQueryPlan(ctx, request, fetchAgent.provider, fields, indices)
			require.NoError(t, err)
			t.Logf("PRESERVED unified-plan conversation_id=%s message_id=%s agent_id=%s plan=%+v", run.conversationID, messageID, agentID, plan)
			require.Equal(t, tc.wantType, plan.QueryType)
			if tc.wantQuery != "" {
				require.Equal(t, tc.wantQuery, plan.Native.Query)
			}
			if tc.wantTimeRange != "" {
				require.Equal(t, tc.wantTimeRange, plan.Native.TimeRange)
			}
			if tc.wantLimit != 0 {
				require.Equal(t, tc.wantLimit, plan.Native.Limit)
			}
			encoded, err := json.Marshal(plan)
			require.NoError(t, err)
			require.NoError(t, run.dao.UpdateConversationMessage(messageID.String(), string(encoded), core.ConversationStatusCompleted))
			require.NoError(t, run.dao.UpdateConversationAgentResponse(agentID.String(), string(encoded), core.AgentExecutionStatusSuccess, "", "unified query plan probe", "", ""))
		})
	}
}

// TestFetchLogsDirectNativeQueryPrompts_Probe sends user-authored commands through
// the real fetch_logs_direct implementation, including provider resolution,
// translation/detection, services-server execution, and response shaping.
// It deliberately supplies no provider or query-language hint.
func TestFetchLogsDirectNativeQueryPrompts_Probe(t *testing.T) {
	if os.Getenv("RUN_LOG_QUERY_NATIVE_EXEC_PROBE") == "" {
		t.Skip("gated: set RUN_LOG_QUERY_NATIVE_EXEC_PROBE=1 and the normal live log environment")
	}

	ctx := security.NewRequestContextForTenantAccountAdmin(
		os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")},
	)
	run := newLogQueryPlanProbeRun(t)
	queries := []struct {
		name        string
		command     string
		wantQuery   string
		knowledge   string
		wantRecords bool
		wantEmpty   bool
	}{
		{name: "native exact with records", command: "Execute this query for any logs from the last hour, limit 10:\n{job=~\".+\"}", wantQuery: `{job=~".+"}`, wantRecords: true},
		{name: "native exact empty", command: "Execute Query for Payments -\n{namespace=\"payments\", app=\"payment-service\"} |= \"error\"", wantQuery: `{namespace="payments", app="payment-service"} |= "error"`, wantEmpty: true},
		{name: "native conversational empty", command: "Can you get me error logs of payment-service using query =\n\n{namespace=\"payments\", app=\"payment-service\"} |= \"error\"", wantQuery: `{namespace="payments", app="payment-service"} |= "error"`, wantEmpty: true},
		{name: "native template completed empty", command: "Get error logs of payment-service in payments for the last 30m, limit 100, using query =\n{namespace=\"$namespace\", app=\"$service\"} |= \"$term\"", wantQuery: `{namespace="payments", app="payment-service"} |= "error"`, wantEmpty: true},
		{
			name:      "native from applicable knowledge empty",
			command:   "Show payment-service errors in namespace payments for the last 30m, limit 100",
			wantQuery: `{namespace="payments", app="payment-service"} |= "error"`,
			knowledge: `<knowledge name="payments-errors">For payment-service errors use: {namespace="payments", app="payment-service"} |= "error"</knowledge>`,
			wantEmpty: true,
		},
		{name: "canonical exact with records", command: `{"where":{"job":{"_neq":""}},"time_range":"1h","limit":10}`, wantRecords: true},
		{name: "canonical natural language with records", command: "Show any logs containing error from the last hour, limit 10", wantRecords: true},
		{
			name:        "canonical ignores unrelated native knowledge",
			command:     "Show any logs containing error from the last hour, limit 10",
			knowledge:   `<knowledge name="legacy-migration">Only for inventory migration checks: {namespace="legacy", app="inventory"} |= "migration complete"</knowledge>`,
			wantRecords: true,
		},
	}

	tool := &fetchLogsDirectTool{accountId: run.accountID}
	for _, tc := range queries {
		t.Run(tc.name, func(t *testing.T) {
			query := tc.command
			messageID, err := run.dao.SaveConversationMessage(
				uuid.NewString(), run.conversationID, run.accountID, run.userID,
				core.MessageRoleHuman, core.MessageTypeGeneration,
				query, "", LogsAgentName, uuid.Nil, nil, "",
				config.Config.LlmProvider, config.Config.LlmModel, core.ConversationStatusInProgress,
			)
			require.NoError(t, err)
			agentID, err := run.dao.SaveConversationAgentCall(
				run.conversationID, messageID.String(), run.accountID, run.userID,
				LogsAgentName, "", query, "", "", "", toolcore.NBQueryConfig{},
			)
			require.NoError(t, err)

			t.Logf("PRESERVED conversation_id=%s message_id=%s agent_id=%s", run.conversationID, messageID, agentID)
			started := time.Now()
			resp, callErr := tool.Call(toolcore.NbToolContext{
				AccountId:        run.accountID,
				UserId:           run.userID,
				Query:            query,
				ConversationId:   run.conversationID,
				ParentAgentId:    agentID.String(),
				MessageId:        messageID.String(),
				Ctx:              ctx,
				SessionId:        run.sessionID,
				OriginalQuery:    query,
				KBPrestepContent: tc.knowledge,
			}, toolcore.NBToolCallRequest{Command: query})

			result := resp.Data
			if callErr != nil {
				result = fmt.Sprintf("fetch_logs_direct error: %v\n%s", callErr, result)
			}
			t.Logf("actual fetch_logs_direct duration=%s status=%s response=%s", time.Since(started), resp.Status, result)
			if callErr == nil && resp.Status == toolcore.NBToolResponseStatusSuccess {
				var envelope map[string]any
				require.NoError(t, json.Unmarshal([]byte(resp.Data), &envelope))
				if tc.wantQuery != "" {
					require.Equal(t, tc.wantQuery, envelope["query"])
				}
				if tc.wantRecords {
					logs, ok := envelope["logs"].(string)
					require.True(t, ok, "logs response should be a string")
					require.NotContains(t, logs, `"logs":[]`, "live smoke query should return at least one record")
				}
				if tc.wantEmpty {
					logs, ok := envelope["logs"].(string)
					require.True(t, ok, "logs response should be a string")
					require.Contains(t, logs, `"logs":[]`, "known-empty query should retain the backend's empty result")
				}
			}
			status := core.AgentExecutionStatusSuccess
			messageStatus := core.ConversationStatusCompleted
			if callErr != nil || resp.Status == toolcore.NBToolResponseStatusError {
				status = core.AgentExecutionStatusFail
				messageStatus = core.ConversationStatusFailed
			}
			require.NoError(t, run.dao.UpdateConversationMessage(messageID.String(), result, messageStatus))
			require.NoError(t, run.dao.UpdateConversationAgentResponse(agentID.String(), result, status, "", "native-query execution probe", "", ""))
			require.NoError(t, callErr)
			require.Equal(t, toolcore.NBToolResponseStatusSuccess, resp.Status)
		})
	}
}

// TestFetchLogsDirectElasticsearchNativeQuery_Probe proves that an exact
// Elasticsearch DSL body is selected and executed as a native query for an
// Elasticsearch-backed account. The returned query must remain byte-for-byte
// equivalent after JSON decoding, which distinguishes this path from canonical
// query generation.
func TestFetchLogsDirectElasticsearchNativeQuery_Probe(t *testing.T) {
	if os.Getenv("RUN_LOG_QUERY_NATIVE_EXEC_PROBE") == "" {
		t.Skip("gated: set RUN_LOG_QUERY_NATIVE_EXEC_PROBE=1 and the normal live log environment")
	}

	ctx := security.NewRequestContextForTenantAccountAdmin(
		os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")},
	)
	run := newLogQueryPlanProbeRun(t)
	tool := &fetchLogsDirectTool{accountId: run.accountID}
	providerName := newFetchLogsAgent(run.accountID).provider.Provider
	if !strings.EqualFold(providerName, "es") && !strings.EqualFold(providerName, "elasticsearch") {
		t.Skipf("account provider is %q, not Elasticsearch", providerName)
	}

	nativeQuery := `{"query":{"match_all":{}},"size":10}`
	command := "Execute this query and summarize the returned records:\n" + nativeQuery
	messageID, err := run.dao.SaveConversationMessage(
		uuid.NewString(), run.conversationID, run.accountID, run.userID,
		core.MessageRoleHuman, core.MessageTypeGeneration,
		command, "", LogsAgentName, uuid.Nil, nil, "",
		config.Config.LlmProvider, config.Config.LlmModel, core.ConversationStatusInProgress,
	)
	require.NoError(t, err)
	agentID, err := run.dao.SaveConversationAgentCall(
		run.conversationID, messageID.String(), run.accountID, run.userID,
		LogsAgentName, "", command, "", "", "", toolcore.NBQueryConfig{},
	)
	require.NoError(t, err)
	t.Logf("PRESERVED Elasticsearch conversation_id=%s message_id=%s agent_id=%s", run.conversationID, messageID, agentID)

	resp, callErr := tool.Call(toolcore.NbToolContext{
		AccountId: run.accountID, UserId: run.userID, Query: command,
		ConversationId: run.conversationID, ParentAgentId: agentID.String(),
		MessageId: messageID.String(), Ctx: ctx, SessionId: run.sessionID,
		OriginalQuery: command,
	}, toolcore.NBToolCallRequest{Command: command})
	require.NoError(t, callErr)
	require.Equal(t, toolcore.NBToolResponseStatusSuccess, resp.Status)

	var envelope map[string]any
	require.NoError(t, json.Unmarshal([]byte(resp.Data), &envelope))
	var want, got any
	require.NoError(t, json.Unmarshal([]byte(nativeQuery), &want))
	executedQuery, ok := envelope["query"].(string)
	require.True(t, ok, "response should contain the executed query")
	require.NoError(t, json.Unmarshal([]byte(executedQuery), &got))
	require.Equal(t, want, got)
	logs, ok := envelope["logs"].(string)
	require.True(t, ok)
	require.NotContains(t, logs, `"logs":[]`, "Elasticsearch match_all query should return records")

	require.NoError(t, run.dao.UpdateConversationMessage(messageID.String(), resp.Data, core.ConversationStatusCompleted))
	require.NoError(t, run.dao.UpdateConversationAgentResponse(agentID.String(), resp.Data, core.AgentExecutionStatusSuccess, "", "native Elasticsearch execution probe", "", ""))
}

// TestKnowledgeAwareLogQueryPlan_UnrelatedNativeStaysCanonical_Probe verifies
// the safety boundary independently of backend data: an unrelated native
// example in retrieved knowledge must not become the executed query.
func TestKnowledgeAwareLogQueryPlan_UnrelatedNativeStaysCanonical_Probe(t *testing.T) {
	if os.Getenv("RUN_LOG_QUERY_NATIVE_EXEC_PROBE") == "" {
		t.Skip("gated: set RUN_LOG_QUERY_NATIVE_EXEC_PROBE=1 and the normal live log environment")
	}

	ctx := security.NewRequestContextForTenantAccountAdmin(
		os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")},
	)
	run := newLogQueryPlanProbeRun(t)
	query := "Show payment-service errors in namespace payments for the last 30m, limit 100"
	legacyQuery := `{namespace="legacy", app="inventory"} |= "migration complete"`
	messageID, err := run.dao.SaveConversationMessage(
		uuid.NewString(), run.conversationID, run.accountID, run.userID,
		core.MessageRoleHuman, core.MessageTypeGeneration, query, "", LogsAgentName,
		uuid.Nil, nil, "", config.Config.LlmProvider, config.Config.LlmModel,
		core.ConversationStatusInProgress,
	)
	require.NoError(t, err)
	agentID, err := run.dao.SaveConversationAgentCall(
		run.conversationID, messageID.String(), run.accountID, run.userID,
		LogsAgentName, "", query, "", "", "", toolcore.NBQueryConfig{},
	)
	require.NoError(t, err)
	t.Logf("PRESERVED safety conversation_id=%s message_id=%s agent_id=%s", run.conversationID, messageID, agentID)

	fetchAgent := newFetchLogsAgent(run.accountID)
	fields, indices := fetchLabelsAndIndices(run.accountID, fetchAgent.provider)
	request := core.NBAgentRequest{
		Query: query, OriginalQuery: query, AccountId: run.accountID, UserId: run.userID,
		ConversationId: run.conversationID, MessageId: messageID.String(), AgentId: agentID.String(),
		KBPrestepContent: `<knowledge name="migration-example">Historical inventory migration verification only: ` + legacyQuery + `</knowledge>`,
	}
	plan, err := generateLogQueryPlan(ctx, request, fetchAgent.provider, fields, indices)
	require.NoError(t, err)
	require.Equal(t, "canonical", plan.QueryType)
	encoded, err := json.Marshal(plan.CanonicalQuery)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), legacyQuery)

	result := string(encoded)
	require.NoError(t, run.dao.UpdateConversationMessage(messageID.String(), result, core.ConversationStatusCompleted))
	require.NoError(t, run.dao.UpdateConversationAgentResponse(agentID.String(), result, core.AgentExecutionStatusSuccess, "", "unrelated native knowledge stayed canonical", "", ""))
}

// TestLogAgentNativeAndCanonical_E2E exercises the complete user-facing logs
// agent: ReAct planning, fetch_logs_direct, provider execution, record
// processing, and final answer composition. Conversations are intentionally
// preserved so the full row/tool-call chain can be reviewed in the UI.
func TestLogAgentNativeAndCanonical_E2E(t *testing.T) {
	if os.Getenv("RUN_LOG_AGENT_E2E") == "" {
		t.Skip("gated: set RUN_LOG_AGENT_E2E=1 and the normal live log environment")
	}

	tenantID := os.Getenv("TEST_TENANT")
	accountID := os.Getenv("TEST_ACCOUNT")
	userID := os.Getenv("TEST_USER")
	require.NotEmpty(t, tenantID, "TEST_TENANT is required")
	require.NotEmpty(t, accountID, "TEST_ACCOUNT is required")
	require.NotEmpty(t, userID, "TEST_USER is required")

	ctx := security.NewRequestContextForTenantAccountAdmin(tenantID, userID, []string{accountID})
	nativeQuery := "Execute this log query for the last hour with limit 10 and summarize the returned records:\n{job=~\".+\"}"
	providerName := newFetchLogsAgent(accountID).provider.Provider
	if strings.EqualFold(providerName, "es") || strings.EqualFold(providerName, "elasticsearch") {
		nativeQuery = `Execute this Elasticsearch query and summarize the returned records:
{"query":{"match_all":{}},"size":10}`
	}
	cases := []struct {
		name  string
		query string
	}{
		{
			name:  "native query",
			query: nativeQuery,
		},
		{
			name:  "canonical natural language",
			query: "Show and briefly summarize 10 log records containing error from the last hour.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent, err := getLogAgent(ctx, accountID)
			require.NoError(t, err)
			sessionID := "logs-agent-e2e-" + uuid.NewString()[:8]
			started := time.Now()
			resp, err := core.HandleConversationSessionRequest(ctx, agent, userID, accountID, sessionID, tc.query)
			require.NoError(t, err)
			t.Logf("PRESERVED full-agent case=%q conversation_id=%s session_id=%s duration=%s response=%v",
				tc.name, resp.ConversationId, sessionID, time.Since(started), resp.Response)
			require.NotEmpty(t, resp.ConversationId)
			require.Equal(t, core.ConversationStatusCompleted, resp.Status)
			require.NotEmpty(t, resp.Response)
			require.NotEmpty(t, strings.TrimSpace(resp.Response[0]))
		})
	}
}
