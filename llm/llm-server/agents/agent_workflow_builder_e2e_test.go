//go:build e2e

package agents

import (
	"encoding/json"
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// displayText returns the text a user sees in the chat for this turn: the
// followup question when the agent is WAITING on input, otherwise the joined
// Response. WorkflowBuilder emits the question only via FollowupRequest on
// WAITING turns (Response is left empty to avoid the UI rendering it twice).

func displayText(resp core.NBAgentResponse) string {
	if resp.Status == core.ConversationStatusWaiting && resp.FollowupRequest.Question != "" {
		return resp.FollowupRequest.Question
	}
	return strings.Join(resp.Response, "\n")
}

func TestWorkflowBuilderAgent_Execute(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	agent := newWorkflowBuilderAgent(os.Getenv("TEST_ACCOUNT"))
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")})

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-wb-21",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "@workflow_builder Build a workflow that first prints 'Hello' and then prints 'World' after it.",
		},
		{
			SessionId: "ut-wb-22",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "@workflow_builder Create a workflow to transform a JSON list of users and print the admin name.",
		},
		{
			SessionId: "ut-wb-23",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "@workflow_builder Create a workflow to get the pods running more than 12 hours in test namespace && then restart them.",
		},
		{
			SessionId: "ut-wb-24",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "@workflow_builder Create a workflow to check pod health in both production and staging namespaces, then aggregate the results and send an alert if any pods are unhealthy.",
		},
	}

	for _, tc := range testCases {
		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, WorkflowBuilderAgentName, agent.GetName())

		// If followup is enabled, the first response should be WAITING with a plan
		if resp.Status == core.ConversationStatusWaiting {
			// Verify plan is in the followup question
			assert.Contains(t, displayText(resp), "plan")

			// Approve the plan
			messageId, err := uuid.Parse(resp.MessageId)
			assert.Nil(t, err)
			agentId, err := uuid.Parse(resp.AgentId)
			assert.Nil(t, err)

			resp, err = core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, PlanApprovalOptionApprove,
				core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
				core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
			assert.Nil(t, err)
			assert.NotNil(t, resp)
		}

		assert.Greater(t, len(resp.Response), 0)

		fullResponse := strings.Join(resp.Response, "\n")
		assertWorkflowResponse(t, fullResponse)
	}
}

// TestWorkflowBuilderAgent_PlanApproveFlow tests the full plan → approve → build flow

func TestWorkflowBuilderAgent_PlanApproveFlow(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	agent := newWorkflowBuilderAgent(os.Getenv("TEST_ACCOUNT"))
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")})
	sessionId := "ut-wb-plan-approve-1"
	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")

	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	// Stage 1: Send initial query — should get a plan back with WAITING status
	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		"Build a workflow that prints 'Hello World'",
		core.ConversationSessionRequestWithEnableQueryRefinement(false))
	assert.Nil(t, err)
	assert.NotNil(t, resp)

	if resp.Status != core.ConversationStatusWaiting {
		// Followup may be disabled in test env — workflow was built directly
		t.Log("Followup not enabled, workflow built directly")
		assert.Greater(t, len(resp.Response), 0)
		return
	}

	// Verify we got a plan
	assert.Equal(t, core.ConversationStatusWaiting, resp.Status)
	planText := displayText(resp)
	assert.Contains(t, planText, "plan")
	t.Log("Plan response:", planText)

	// Stage 2: Approve the plan — should build and return workflow JSON
	messageId, err := uuid.Parse(resp.MessageId)
	assert.Nil(t, err)
	agentId, err := uuid.Parse(resp.AgentId)
	assert.Nil(t, err)

	resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		PlanApprovalOptionApprove,
		core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
		core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	assert.Greater(t, len(resp.Response), 0)

	finalResponse := strings.Join(resp.Response, "\n")
	assertWorkflowResponse(t, finalResponse)
	assert.Equal(t, core.ConversationStatusCompleted, resp.Status)
	t.Log("Final workflow:", finalResponse)
}

func TestWorkflowBuilderAgent_PlanApproveFlowUsingWorkflowAgent(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	agent := newWorkflowAgent(os.Getenv("TEST_ACCOUNT"))
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")})
	sessionId := "ut-wb-plan-approve-1"
	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")

	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	// Stage 1: Send initial query — should get a plan back with WAITING status
	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		"Build a workflow that prints 'Hello World'",
		core.ConversationSessionRequestWithEnableQueryRefinement(false))
	assert.Nil(t, err)
	assert.NotNil(t, resp)

	if resp.Status != core.ConversationStatusWaiting {
		// Followup may be disabled in test env — workflow was built directly
		t.Log("Followup not enabled, workflow built directly")
		assert.Greater(t, len(resp.Response), 0)
		return
	}

	// Verify we got a plan
	assert.Equal(t, core.ConversationStatusWaiting, resp.Status)
	planText := displayText(resp)
	assert.Contains(t, planText, "plan")
	t.Log("Plan response:", planText)

	// Stage 2: Approve the plan — should build and return workflow JSON
	messageId, err := uuid.Parse(resp.MessageId)
	assert.Nil(t, err)
	agentId, err := uuid.Parse(resp.AgentId)
	assert.Nil(t, err)

	resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		PlanApprovalOptionApprove,
		core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
		core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	assert.Greater(t, len(resp.Response), 0)

	finalResponse := strings.Join(resp.Response, "\n")
	assertWorkflowResponse(t, finalResponse)
	assert.Equal(t, core.ConversationStatusCompleted, resp.Status)
	t.Log("Final workflow:", finalResponse)
}

// TestWorkflowBuilderAgent_PlanFeedbackFlow tests the plan → request changes → feedback → approve flow

func TestWorkflowBuilderAgent_PlanFeedbackFlow(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	agent := newWorkflowBuilderAgent(os.Getenv("TEST_ACCOUNT"))
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")})
	sessionId := "ut-wb-plan-feedback-1"
	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")

	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	// Stage 1: Send initial query — should get a plan back with WAITING status
	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		"Build a workflow that checks pod health in production namespace and sends a Slack alert",
		core.ConversationSessionRequestWithEnableQueryRefinement(false))
	assert.Nil(t, err)
	assert.NotNil(t, resp)

	if resp.Status != core.ConversationStatusWaiting {
		t.Log("Followup not enabled, workflow built directly")
		assert.Greater(t, len(resp.Response), 0)
		return
	}

	// Verify we got a plan
	assert.Equal(t, core.ConversationStatusWaiting, resp.Status)
	t.Log("Initial plan:", displayText(resp))

	// Stage 2: Request changes
	messageId, err := uuid.Parse(resp.MessageId)
	assert.Nil(t, err)
	agentId, err := uuid.Parse(resp.AgentId)
	assert.Nil(t, err)

	resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		PlanApprovalOptionChanges,
		core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
		core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, core.ConversationStatusWaiting, resp.Status)
	t.Log("Feedback prompt:", displayText(resp))

	// Stage 3: Provide feedback — should get an updated plan
	messageId, err = uuid.Parse(resp.MessageId)
	assert.Nil(t, err)
	agentId, err = uuid.Parse(resp.AgentId)
	assert.Nil(t, err)

	resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		"Also check staging namespace and add a schedule trigger for every hour",
		core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
		core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, core.ConversationStatusWaiting, resp.Status)
	t.Log("Updated plan:", displayText(resp))

	// Stage 4: Approve the updated plan
	messageId, err = uuid.Parse(resp.MessageId)
	assert.Nil(t, err)
	agentId, err = uuid.Parse(resp.AgentId)
	assert.Nil(t, err)

	resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		PlanApprovalOptionApprove,
		core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
		core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	assert.Greater(t, len(resp.Response), 0)

	finalResponse := strings.Join(resp.Response, "\n")
	assertWorkflowResponse(t, finalResponse)
	t.Log("Final workflow:", finalResponse)
}

func TestWorkflowBuilderAgent_PlanFeedbackFlowUsingWorkflowAgent(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	agent := newWorkflowAgent(os.Getenv("TEST_ACCOUNT"))
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")})
	sessionId := "ut-wb-plan-feedback-1"
	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")

	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	// Stage 1: Send initial query — should get a plan back with WAITING status
	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		"Build a workflow that checks pod health in production namespace and sends a Slack alert",
		core.ConversationSessionRequestWithEnableQueryRefinement(false))
	assert.Nil(t, err)
	assert.NotNil(t, resp)

	if resp.Status != core.ConversationStatusWaiting {
		t.Log("Followup not enabled, workflow built directly")
		assert.Greater(t, len(resp.Response), 0)
		return
	}

	// Verify we got a plan
	assert.Equal(t, core.ConversationStatusWaiting, resp.Status)
	t.Log("Initial plan:", displayText(resp))

	// Stage 2: Request changes
	messageId, err := uuid.Parse(resp.MessageId)
	assert.Nil(t, err)
	agentId, err := uuid.Parse(resp.AgentId)
	assert.Nil(t, err)

	resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		PlanApprovalOptionChanges,
		core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
		core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, core.ConversationStatusWaiting, resp.Status)
	t.Log("Feedback prompt:", displayText(resp))

	// Stage 3: Provide feedback — should get an updated plan
	messageId, err = uuid.Parse(resp.MessageId)
	assert.Nil(t, err)
	agentId, err = uuid.Parse(resp.AgentId)
	assert.Nil(t, err)

	resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		"Also check staging namespace and add a schedule trigger for every hour",
		core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
		core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, core.ConversationStatusWaiting, resp.Status)
	t.Log("Updated plan:", displayText(resp))

	// Stage 4: Approve the updated plan
	messageId, err = uuid.Parse(resp.MessageId)
	assert.Nil(t, err)
	agentId, err = uuid.Parse(resp.AgentId)
	assert.Nil(t, err)

	resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		PlanApprovalOptionApprove,
		core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
		core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	assert.Greater(t, len(resp.Response), 0)

	finalResponse := strings.Join(resp.Response, "\n")
	assertWorkflowResponse(t, finalResponse)
	t.Log("Final workflow:", finalResponse)
}

// TestWorkflowBuilderAgent_StateMarshalUnmarshal tests that state survives marshal/unmarshal cycle

func TestWorkflowBuilderAgent_FixMode_Integration(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	tenantId := os.Getenv("TEST_TENANT")
	workflowId := "5d064c4c-bb53-4630-95ff-f6bb7b9133a6"
	sessionId := "ut-wb-fix-1"

	agent := newWorkflowBuilderAgent(accountId)
	sc := security.NewRequestContextForTenantAccountAdmin(tenantId, userId, []string{accountId})

	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	// Stage 1: Send fix request with workflow_id in QueryConfig
	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		"Fix this workflow - the notification task is not sending to the correct channel",
		core.ConversationSessionRequestWithEnableQueryRefinement(false),
		core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{WorkflowId: workflowId}))
	assert.Nil(t, err)
	assert.NotNil(t, resp)

	if resp.Status != core.ConversationStatusWaiting {
		// Followup not enabled — fix was applied directly
		t.Log("Followup not enabled, fix applied directly")
		assert.Greater(t, len(resp.Response), 0)
		fullResponse := strings.Join(resp.Response, "\n")
		assertWorkflowResponse(t, fullResponse)
		t.Log("Fix response:", fullResponse)
		return
	}

	// Verify we got a diagnosis
	assert.Equal(t, core.ConversationStatusWaiting, resp.Status)
	fixProposal := displayText(resp)
	assert.Contains(t, fixProposal, "Diagnosis")
	t.Log("Fix proposal:", fixProposal)

	// Stage 2: Approve the fix
	messageId, err := uuid.Parse(resp.MessageId)
	assert.Nil(t, err)
	agentId, err := uuid.Parse(resp.AgentId)
	assert.Nil(t, err)

	resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		FixApprovalOptionApply,
		core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
		core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	assert.Greater(t, len(resp.Response), 0)

	finalResponse := strings.Join(resp.Response, "\n")
	assertWorkflowResponse(t, finalResponse)
	t.Log("Fixed workflow:", finalResponse)
}

// TestWorkflowBuilderAgent_FixMode_FeedbackFlow tests the fix → reject → feedback → approve flow

func TestWorkflowBuilderAgent_FixMode_FeedbackFlow(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	tenantId := os.Getenv("TEST_TENANT")
	workflowId := "5d064c4c-bb53-4630-95ff-f6bb7b9133a6"
	sessionId := "ut-wb-fix-feedback-1"

	agent := newWorkflowBuilderAgent(accountId)
	sc := security.NewRequestContextForTenantAccountAdmin(tenantId, userId, []string{accountId})

	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	// Stage 1: Send fix request
	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		"The notification is going to the wrong Slack channel, fix it",
		core.ConversationSessionRequestWithEnableQueryRefinement(false),
		core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{WorkflowId: workflowId}))
	assert.Nil(t, err)
	assert.NotNil(t, resp)

	if resp.Status != core.ConversationStatusWaiting {
		t.Log("Followup not enabled, fix applied directly")
		return
	}

	// Verify diagnosis
	assert.Equal(t, core.ConversationStatusWaiting, resp.Status)
	t.Log("Initial fix proposal:", displayText(resp))

	// Stage 2: Request modifications
	messageId, err := uuid.Parse(resp.MessageId)
	assert.Nil(t, err)
	agentId, err := uuid.Parse(resp.AgentId)
	assert.Nil(t, err)

	resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		FixApprovalOptionModify,
		core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
		core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, core.ConversationStatusWaiting, resp.Status)
	t.Log("Feedback prompt:", displayText(resp))

	// Stage 3: Provide feedback
	messageId, err = uuid.Parse(resp.MessageId)
	assert.Nil(t, err)
	agentId, err = uuid.Parse(resp.AgentId)
	assert.Nil(t, err)

	resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		"Change the channel to #prod-alerts instead",
		core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
		core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, core.ConversationStatusWaiting, resp.Status)
	t.Log("Updated fix proposal:", displayText(resp))

	// Stage 4: Approve the updated fix
	messageId, err = uuid.Parse(resp.MessageId)
	assert.Nil(t, err)
	agentId, err = uuid.Parse(resp.AgentId)
	assert.Nil(t, err)

	resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		FixApprovalOptionApply,
		core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
		core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	assert.Greater(t, len(resp.Response), 0)

	finalResponse := strings.Join(resp.Response, "\n")
	assertWorkflowResponse(t, finalResponse)
	t.Log("Final fixed workflow:", finalResponse)
}

// TestWorkflowBuilderAgent_FixMode_Discard tests the discard flow

func TestWorkflowBuilderAgent_FixMode_Discard(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	tenantId := os.Getenv("TEST_TENANT")
	workflowId := "5d064c4c-bb53-4630-95ff-f6bb7b9133a6"
	sessionId := "ut-wb-fix-discard-1"

	agent := newWorkflowBuilderAgent(accountId)
	sc := security.NewRequestContextForTenantAccountAdmin(tenantId, userId, []string{accountId})

	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	// Stage 1: Send fix request
	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		"Fix the notification channel",
		core.ConversationSessionRequestWithEnableQueryRefinement(false),
		core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{WorkflowId: workflowId}))
	assert.Nil(t, err)
	assert.NotNil(t, resp)

	if resp.Status != core.ConversationStatusWaiting {
		t.Log("Followup not enabled, skipping discard test")
		return
	}

	// Stage 2: Discard changes
	messageId, err := uuid.Parse(resp.MessageId)
	assert.Nil(t, err)
	agentId, err := uuid.Parse(resp.AgentId)
	assert.Nil(t, err)

	resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		FixApprovalOptionDiscard,
		core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
		core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	assert.Greater(t, len(resp.Response), 0)

	fullResponse := strings.Join(resp.Response, "\n")
	assert.Contains(t, fullResponse, "discarded")
	t.Log("Discard response:", fullResponse)
}

func assertWorkflowResponse(t *testing.T, fullResponse string) {
	t.Helper()

	// Try JSON first (WorkflowBuilder source or summary generation failure)
	var jsonCheck map[string]interface{}
	if json.Unmarshal([]byte(fullResponse), &jsonCheck) == nil {
		assert.Contains(t, fullResponse, "definition")
		assert.Contains(t, fullResponse, "tasks")
		return
	}

	// Markdown summary format (ask-nudgebee source)
	assert.Contains(t, fullResponse, "The automation", "Summary should describe the automation")
	assert.Contains(t, fullResponse, "**Tasks:**", "Summary should list tasks")
}

// validateWorkflowStructure validates the expected structure of a workflow JSON.

func validateWorkflowStructure(t *testing.T, workflow map[string]interface{}, minTasks int) {
	t.Helper()

	assert.Contains(t, workflow, "name", "Workflow must have a name")
	assert.Contains(t, workflow, "definition", "Workflow must have a definition")

	def, ok := workflow["definition"].(map[string]interface{})
	assert.True(t, ok, "definition must be an object")

	// Verify triggers
	triggers, ok := def["triggers"].([]interface{})
	assert.True(t, ok, "definition.triggers must be an array")
	assert.GreaterOrEqual(t, len(triggers), 1, "Must have at least one trigger")

	// Verify tasks
	tasks, ok := def["tasks"].([]interface{})
	assert.True(t, ok, "definition.tasks must be an array")
	assert.GreaterOrEqual(t, len(tasks), minTasks, "Expected at least %d tasks, got %d", minTasks, len(tasks))

	// Verify each task has required fields
	for i, taskRaw := range tasks {
		task, ok := taskRaw.(map[string]interface{})
		assert.True(t, ok, "task %d must be an object", i)
		assert.NotEmpty(t, task["id"], "task %d must have a non-empty id", i)
		assert.NotEmpty(t, task["type"], "task %d must have a non-empty type", i)
	}
}

// handlePlanApprovalIfNeeded handles plan approval when followup is enabled.
// Returns the final response after approval.

func handlePlanApprovalIfNeeded(t *testing.T, sc *security.RequestContext, agent *WorkflowBuilderAgent,
	userId, accountId, sessionId string, resp core.NBAgentResponse) core.NBAgentResponse {
	t.Helper()

	if resp.Status != core.ConversationStatusWaiting {
		return resp
	}

	// Verify we got a plan
	planText := displayText(resp)
	t.Log("Plan received:", planText[:min(300, len(planText))])

	messageId, err := uuid.Parse(resp.MessageId)
	assert.Nil(t, err)
	agentId, err := uuid.Parse(resp.AgentId)
	assert.Nil(t, err)

	// Approve the plan
	resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		PlanApprovalOptionApprove,
		core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
		core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
	assert.Nil(t, err)
	assert.NotNil(t, resp)

	return resp
}

// TestWorkflowBuilderAgent_AgenticBuild tests the full agentic build flow end-to-end.
// This exercises: extractIntent → generatePlan → [approval] → runToolLoop(init_workflow → add_task × N → validate → finalize)

func TestWorkflowBuilderAgent_AgenticBuild(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	tenantId := os.Getenv("TEST_TENANT")

	testCases := []struct {
		name           string
		sessionId      string
		query          string
		minTasks       int
		expectDeps     bool   // at least one task should have depends_on
		expectTrigger  string // expected trigger type (empty = any)
		expectTaskType string // at least one task should have this type
	}{
		{
			name:           "simple_sequential",
			sessionId:      "ut-wb-agentic-build-1",
			query:          "Build a workflow that prints 'Hello' and then prints 'World'",
			minTasks:       2,
			expectDeps:     true,
			expectTaskType: "core.print",
		},
		{
			name:           "k8s_with_condition",
			sessionId:      "ut-wb-agentic-build-2",
			query:          "Create a workflow to get pods in production namespace, check if any are in CrashLoopBackOff, and print a warning only if unhealthy pods are found",
			minTasks:       2,
			expectDeps:     true,
			expectTaskType: "k8s.cli",
		},
		{
			name:           "scheduled_trigger",
			sessionId:      "ut-wb-agentic-build-3",
			query:          "Create a workflow with a schedule trigger that runs every hour to get node status and print a summary",
			minTasks:       2,
			expectDeps:     true,
			expectTrigger:  "schedule",
			expectTaskType: "k8s.cli",
		},
		{
			name:           "hackernews_summarizer",
			sessionId:      "ut-wb-agentic-build-4",
			query:          "Create a workflow to fetch top 3 hacker news stories, fetch their comments and summarise using llm in tldr style and then print",
			minTasks:       3,
			expectDeps:     true,
			expectTaskType: "llm.investigate",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			agent := newWorkflowBuilderAgent(accountId)
			sc := security.NewRequestContextForTenantAccountAdmin(tenantId, userId, []string{accountId})

			err := core.DeleteConversationBySession(tc.sessionId, accountId, userId)
			assert.Nil(t, err)

			resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, tc.sessionId,
				tc.query,
				core.ConversationSessionRequestWithEnableQueryRefinement(false))
			assert.Nil(t, err)
			assert.NotNil(t, resp)

			// Handle plan approval if followup is enabled
			resp = handlePlanApprovalIfNeeded(t, sc, agent, userId, accountId, tc.sessionId, resp)

			// Should have a response
			assert.Greater(t, len(resp.Response), 0, "Should return at least one response")
			fullResponse := strings.Join(resp.Response, "\n")
			t.Log("Build output (first 500 chars):", fullResponse[:min(500, len(fullResponse))])

			// Response is either JSON (fallback) or markdown summary (new format)
			var workflow map[string]interface{}
			if json.Unmarshal([]byte(fullResponse), &workflow) == nil {
				// JSON format: do full structural validation
				validateWorkflowStructure(t, workflow, tc.minTasks)

				def := workflow["definition"].(map[string]interface{})
				tasks := def["tasks"].([]interface{})

				if tc.expectDeps {
					hasDeps := false
					for _, taskRaw := range tasks {
						task := taskRaw.(map[string]interface{})
						if deps, ok := task["depends_on"]; ok && deps != nil {
							hasDeps = true
							break
						}
					}
					assert.True(t, hasDeps, "At least one task should have depends_on for sequential workflows")
				}

				if tc.expectTrigger != "" {
					triggers := def["triggers"].([]interface{})
					trigger := triggers[0].(map[string]interface{})
					assert.Equal(t, tc.expectTrigger, trigger["type"], "Expected trigger type %s", tc.expectTrigger)
				}

				if tc.expectTaskType != "" {
					hasType := false
					for _, taskRaw := range tasks {
						task := taskRaw.(map[string]interface{})
						if task["type"] == tc.expectTaskType {
							hasType = true
							break
						}
					}
					assert.True(t, hasType, "Expected at least one task of type %s", tc.expectTaskType)
				}
			} else {
				// Markdown summary format
				assertWorkflowResponse(t, fullResponse)
				if tc.expectTrigger != "" {
					assert.Contains(t, fullResponse, tc.expectTrigger, "Summary should mention trigger type")
				}
				if tc.expectTaskType != "" {
					assert.Contains(t, fullResponse, tc.expectTaskType, "Summary should mention task type")
				}
			}
		})
	}
}

// TestWorkflowBuilderAgent_AgenticCreateThenFix tests the full agentic lifecycle:
// 1. Build a workflow via the agentic tool loop
// 2. Create it on the runbook server
// 3. Fix it via the agentic fix tool loop

func TestWorkflowBuilderAgent_AgenticCreateThenFix(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	tenantId := os.Getenv("TEST_TENANT")
	createSessionId := "ut-wb-agentic-create-fix-1"
	fixSessionId := "ut-wb-agentic-create-fix-2"

	sc := security.NewRequestContextForTenantAccountAdmin(tenantId, userId, []string{accountId})

	// ==================== Phase 1: Build workflow ====================
	t.Log("Phase 1: Building workflow via agentic tool loop...")
	agent := newWorkflowBuilderAgent(accountId)

	err := core.DeleteConversationBySession(createSessionId, accountId, userId)
	assert.Nil(t, err)

	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, createSessionId,
		"Build a workflow that gets pods in the production namespace and prints the result",
		core.ConversationSessionRequestWithEnableQueryRefinement(false))
	assert.Nil(t, err)
	assert.NotNil(t, resp)

	resp = handlePlanApprovalIfNeeded(t, sc, agent, userId, accountId, createSessionId, resp)

	assert.Greater(t, len(resp.Response), 0)
	fullResponse := strings.Join(resp.Response, "\n")
	assertWorkflowResponse(t, fullResponse)

	var workflowId string

	// The response is now a markdown summary with an auto-saved workflow link.
	// Extract the workflow ID from the "Open in Editor" link if present.
	if idx := strings.Index(fullResponse, "/workflow/"); idx >= 0 {
		rest := fullResponse[idx+len("/workflow/"):]
		if qIdx := strings.IndexAny(rest, "?#)"); qIdx > 0 {
			workflowId = rest[:qIdx]
		}
	}

	if workflowId == "" {
		// Fallback: try JSON format and create manually
		var workflow map[string]interface{}
		if json.Unmarshal([]byte(fullResponse), &workflow) == nil {
			validateWorkflowStructure(t, workflow, 2)
			t.Log("Phase 1 complete (JSON fallback): creating workflow on server manually...")

			createResp, createErr := tools.DoRunbookRequest("POST", "workflows", workflow, accountId,
				sc.GetSecurityContext().GetTenantId(), sc.GetSecurityContext().GetUserId())
			if createErr != nil {
				t.Skipf("Could not create workflow on server (runbook-server may not be running): %v", createErr)
			}

			var created map[string]interface{}
			err = json.Unmarshal(createResp, &created)
			assert.Nil(t, err)
			workflowId, _ = created["id"].(string)
		} else {
			t.Skip("Auto-save failed and response is not JSON — cannot proceed to fix phase")
		}
	} else {
		t.Log("Phase 1 complete: workflow auto-saved with id:", workflowId)
	}

	assert.NotEmpty(t, workflowId, "Must have a workflow ID to proceed to fix phase")
	t.Log("Phase 2: workflow available with id:", workflowId)

	// Cleanup: delete the workflow after test
	defer func() {
		_, _ = tools.DoRunbookRequest("DELETE", "workflows/"+workflowId, nil, accountId,
			sc.GetSecurityContext().GetTenantId(), sc.GetSecurityContext().GetUserId())
	}()

	// ==================== Phase 3: Fix workflow via agentic tool loop ====================
	t.Log("Phase 3: Fixing workflow via agentic fix tool loop...")
	fixAgent := newWorkflowBuilderAgent(accountId)

	err = core.DeleteConversationBySession(fixSessionId, accountId, userId)
	assert.Nil(t, err)

	resp, err = core.HandleConversationSessionRequest(sc, fixAgent, userId, accountId, fixSessionId,
		"Change the namespace from production to monitoring and add a Slack notification after the print step",
		core.ConversationSessionRequestWithEnableQueryRefinement(false),
		core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{WorkflowId: workflowId}))
	assert.Nil(t, err)
	assert.NotNil(t, resp)

	// Handle fix approval if followup is enabled
	if resp.Status == core.ConversationStatusWaiting {
		t.Log("Fix diagnosis received, approving...")
		messageId, _ := uuid.Parse(resp.MessageId)
		agentId, _ := uuid.Parse(resp.AgentId)
		resp, err = core.HandleConversationSessionRequest(sc, fixAgent, userId, accountId, fixSessionId,
			FixApprovalOptionApply,
			core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
			core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}

	assert.Greater(t, len(resp.Response), 0)
	fixResponse := strings.Join(resp.Response, "\n")
	assertWorkflowResponse(t, fixResponse)
	t.Log("Phase 3 complete: fix response:", fixResponse[:min(500, len(fixResponse))])
}

// TestWorkflowBuilderAgent_AgenticBuildComplexWorkflow tests building a complex workflow
// with multiple task types, data flow between tasks, and conditional execution.

func TestWorkflowBuilderAgent_AgenticBuildComplexWorkflow(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	tenantId := os.Getenv("TEST_TENANT")
	sessionId := "ut-wb-agentic-complex-1"

	agent := newWorkflowBuilderAgent(accountId)
	sc := security.NewRequestContextForTenantAccountAdmin(tenantId, userId, []string{accountId})

	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	query := `Create a workflow that:
1. Gets all pods in the production namespace using kubectl
2. Transforms the output to extract pod names and statuses
3. Checks if any pods are not Running
4. If unhealthy pods exist, sends a Slack message to #alerts with the list
5. Prints a summary at the end regardless of health status

Use a manual trigger.`

	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		query,
		core.ConversationSessionRequestWithEnableQueryRefinement(false))
	assert.Nil(t, err)
	assert.NotNil(t, resp)

	resp = handlePlanApprovalIfNeeded(t, sc, agent, userId, accountId, sessionId, resp)

	assert.Greater(t, len(resp.Response), 0)
	fullResponse := strings.Join(resp.Response, "\n")

	var workflow map[string]interface{}
	if json.Unmarshal([]byte(fullResponse), &workflow) == nil {
		// JSON fallback: full structural validation
		validateWorkflowStructure(t, workflow, 3)

		def := workflow["definition"].(map[string]interface{})
		tasks := def["tasks"].([]interface{})
		taskTypes := map[string]bool{}
		for _, taskRaw := range tasks {
			task := taskRaw.(map[string]interface{})
			if tt, ok := task["type"].(string); ok {
				taskTypes[tt] = true
			}
		}
		assert.GreaterOrEqual(t, len(taskTypes), 2, "Complex workflow should use at least 2 different task types")
		t.Log("Complex workflow built with", len(tasks), "tasks and", len(taskTypes), "task types:", taskTypes)
	} else {
		// Markdown summary format
		assertWorkflowResponse(t, fullResponse)
		assert.Contains(t, fullResponse, "manual", "Summary should mention manual trigger")
		t.Log("Complex workflow summary:", fullResponse[:min(500, len(fullResponse))])
	}
}

// ==================== CONFIG TOOLS TESTS ====================

// TestExtractConfigReferences tests the regex extraction of {{ Configs.xxx }} references

func TestWorkflowBuilderAgent_AutoCreateEmptyConfigs_Helper(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}
	defer withWorkflowServerOverride(t)()

	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	tenantId := os.Getenv("TEST_TENANT")

	agent := newWorkflowBuilderAgent(accountId)
	sc := security.NewRequestContextForTenantAccountAdmin(tenantId, userId, []string{accountId})

	keys := []string{
		fmt.Sprintf("ut_autocreate_%s", uuid.New().String()[:8]),
		fmt.Sprintf("ut_autocreate_%s", uuid.New().String()[:8]),
	}
	defer func() {
		for _, k := range keys {
			_, _ = tools.DoRunbookRequest("DELETE", fmt.Sprintf("configs/%s", k), nil, accountId, tenantId, userId)
		}
	}()

	created, failed := agent.autoCreateEmptyConfigs(sc, keys)
	if len(failed) > 0 && len(created) == 0 {
		// Server unreachable / auth issues. Skip rather than fail — this is
		// an integration test, not a unit test.
		t.Skipf("auto-create returned all failures, likely server unreachable: %v", failed)
	}
	assert.Equal(t, keys, created)
	assert.Empty(t, failed)

	// Verify each config exists on the server.
	for _, k := range keys {
		resp, err := tools.DoRunbookRequest("GET", fmt.Sprintf("configs/%s", k), nil, accountId, tenantId, userId)
		assert.Nil(t, err, "config %q should be retrievable after auto-create", k)
		assert.Contains(t, string(resp), k)
	}
}

// TestWorkflowBuilderAgent_CheckMissingConfigs_AutoCreatesAndSaves drives the
// fix end-to-end: build a workflow with a missing config ref, run it through
// checkMissingConfigs, and verify the workflow gets saved on the workflow
// server (the issue #29944 failure mode is gone) and the missing config now
// exists. Uses non-editor source so finalizeWithAutoSave actually attempts the
// save (editor source returns raw JSON for the UI to consume).

func TestWorkflowBuilderAgent_CheckMissingConfigs_AutoCreatesAndSaves_Issue29944(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}
	defer withWorkflowServerOverride(t)()

	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	tenantId := os.Getenv("TEST_TENANT")

	missingKey := fmt.Sprintf("ut_missing_cfg_%s", uuid.New().String()[:8])
	workflowName := fmt.Sprintf("ut-issue29944-fix-%s", uuid.New().String()[:8])
	defer func() {
		_, _ = tools.DoRunbookRequest("DELETE", fmt.Sprintf("configs/%s", missingKey), nil, accountId, tenantId, userId)
	}()

	// Pre-flight: confirm the config really does not exist on the server.
	if existing, err := tools.DoRunbookRequest("GET", fmt.Sprintf("configs/%s", missingKey), nil, accountId, tenantId, userId); err == nil {
		t.Fatalf("test setup invalid: config %q already exists on server: %s", missingKey, string(existing))
	}

	workflowJSON := fmt.Sprintf(`{"name":%q,"definition":{"version":"v1","triggers":[{"type":"manual"}],"tasks":[{"id":"print-message","type":"core.print","params":{"message":"{{ Configs.%s }}"}}]}}`, workflowName, missingKey)

	agent := newWorkflowBuilderAgent(accountId)
	agent.state.Mode = "create"
	sc := security.NewRequestContextForTenantAccountAdmin(tenantId, userId, []string{accountId})

	resp, err := agent.checkMissingConfigs(sc, core.NBAgentRequest{
		AccountId: accountId,
		UserId:    userId,
		// Non-editor source so the save attempt actually happens server-side.
		ConversationSource: core.ConversationSourceUserInvestigation,
	}, workflowJSON)
	if err != nil {
		t.Skipf("checkMissingConfigs returned error (likely server unreachable): %v", err)
	}

	assert.True(t, resp.IsTerminal, "agent must terminate — no more followup waiting")
	assert.NotEqual(t, core.ConversationStatusWaiting, resp.Status, "no Waiting status — followup is gone")
	assert.Empty(t, resp.FollowupRequest.Question, "no followup question — auto-create handles it")
	assert.Equal(t, 1, len(resp.Response))
	body := resp.Response[0]

	// The missing config got auto-created and surfaced to the user.
	assert.Contains(t, body, "Created 1 placeholder config")
	assert.Contains(t, body, missingKey)
	assert.Contains(t, body, autoCreatedConfigPlaceholder)
	assert.Contains(t, body, "replace via Configs before running")

	// The workflow was saved (no more "validation failed" from the server).
	assert.Contains(t, body, "built and saved")
	assert.NotContains(t, body, "Auto-save failed")

	// Verify on the server: the missing config now exists, and the workflow
	// was created. Cleanup the workflow if found.
	cfgResp, cfgErr := tools.DoRunbookRequest("GET", fmt.Sprintf("configs/%s", missingKey), nil, accountId, tenantId, userId)
	assert.Nil(t, cfgErr)
	assert.Contains(t, string(cfgResp), missingKey)

	// Best-effort cleanup of the workflow.
	if listResp, listErr := tools.DoRunbookRequest("GET", fmt.Sprintf("workflows?name=%s", workflowName), nil, accountId, tenantId, userId); listErr == nil {
		var listed []map[string]interface{}
		if jerr := json.Unmarshal(listResp, &listed); jerr == nil {
			for _, w := range listed {
				if id, ok := w["id"].(string); ok && id != "" {
					_, _ = tools.DoRunbookRequest("DELETE", fmt.Sprintf("workflows/%s", id), nil, accountId, tenantId, userId)
				}
			}
		}
	}
}

// TestWorkflowBuilderAgent_CheckMissingConfigs_NoConfigApprovalStage is a
// fast unit-style guard: the bug was that the agent entered a "config_approval"
// waiting stage which the UI then mishandled. After the fix, no waiting stage
// is entered for missing-config workflows. We verify by inspecting the agent
// state after the call — it must not be left at "config_approval".
//
// Skipped when TEST_ACCOUNT isn't set because checkMissingConfigs talks to the
// configs API; without it we'd be testing the unreachable-server graceful-
// degrade path instead of the auto-create path.

func TestWorkflowBuilderAgent_CheckMissingConfigs_NoConfigApprovalStage_Issue29944(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}
	defer withWorkflowServerOverride(t)()

	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	tenantId := os.Getenv("TEST_TENANT")

	missingKey := fmt.Sprintf("ut_stage_check_%s", uuid.New().String()[:8])
	workflowName := fmt.Sprintf("ut-issue29944-stage-%s", uuid.New().String()[:8])
	defer func() {
		_, _ = tools.DoRunbookRequest("DELETE", fmt.Sprintf("configs/%s", missingKey), nil, accountId, tenantId, userId)
	}()

	workflowJSON := fmt.Sprintf(`{"name":%q,"definition":{"version":"v1","triggers":[{"type":"manual"}],"tasks":[{"id":"print-message","type":"core.print","params":{"message":"{{ Configs.%s }}"}}]}}`, workflowName, missingKey)

	agent := newWorkflowBuilderAgent(accountId)
	agent.state.Mode = "create"
	sc := security.NewRequestContextForTenantAccountAdmin(tenantId, userId, []string{accountId})

	_, err := agent.checkMissingConfigs(sc, core.NBAgentRequest{
		AccountId: accountId, UserId: userId,
		ConversationSource: core.ConversationSourceUserInvestigation,
	}, workflowJSON)
	if err != nil {
		t.Skipf("checkMissingConfigs returned error (likely server unreachable): %v", err)
	}

	assert.NotEqual(t, "config_approval", agent.state.Stage,
		"after the fix, missing-config workflows must not enter the config_approval waiting stage")

	// Cleanup workflow if save went through.
	if listResp, listErr := tools.DoRunbookRequest("GET", fmt.Sprintf("workflows?name=%s", workflowName), nil, accountId, tenantId, userId); listErr == nil {
		var listed []map[string]interface{}
		if jerr := json.Unmarshal(listResp, &listed); jerr == nil {
			for _, w := range listed {
				if id, ok := w["id"].(string); ok && id != "" {
					_, _ = tools.DoRunbookRequest("DELETE", fmt.Sprintf("workflows/%s", id), nil, accountId, tenantId, userId)
				}
			}
		}
	}
}

// withWorkflowServerOverride applies a WORKFLOW_SERVER_URL env-var override
// for the duration of the test so a port-forwarded workflow server can be
// used without rewriting the .env files. Returns a restore func.

func withWorkflowServerOverride(t *testing.T) func() {
	t.Helper()
	override := os.Getenv("WORKFLOW_SERVER_URL")
	if override == "" {
		return func() {}
	}
	prev := config.Config.WorkflowServerEndpoint
	config.Config.WorkflowServerEndpoint = override
	t.Logf("issue29944: routing workflow-server calls to %s (was %q)", override, prev)
	return func() {
		config.Config.WorkflowServerEndpoint = prev
	}
}

// TestWorkflowBuilderAgent_BuildWithConfigDetection tests the full build flow where the
// resulting workflow references configs, triggering the missing config detection.

func TestWorkflowBuilderAgent_BuildWithConfigDetection(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	tenantId := os.Getenv("TEST_TENANT")
	sessionId := "ut-wb-config-detection-1"

	agent := newWorkflowBuilderAgent(accountId)
	sc := security.NewRequestContextForTenantAccountAdmin(tenantId, userId, []string{accountId})

	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	// This query should produce a workflow with Configs references (e.g., Configs.slack_channel)
	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		"Build a workflow that gets pods in production namespace and sends a Slack notification with the results",
		core.ConversationSessionRequestWithEnableQueryRefinement(false))
	assert.Nil(t, err)
	assert.NotNil(t, resp)

	// Handle plan approval if needed
	resp = handlePlanApprovalIfNeeded(t, sc, agent, userId, accountId, sessionId, resp)

	// After issue #29944 fix: missing configs are auto-created, so no waiting
	// stage should appear after plan approval.
	assert.NotEqual(t, core.ConversationStatusWaiting, resp.Status,
		"missing-config workflows must not park in a waiting stage")

	assert.Greater(t, len(resp.Response), 0)
	fullResponse := strings.Join(resp.Response, "\n")

	// Response is either JSON (fallback) or markdown summary (new format)
	assertWorkflowResponse(t, fullResponse)
	t.Log("Workflow with config references built successfully")
}

// ==================== buildWorkflowSummary UNIT TESTS ====================

func TestWorkflowBuilderAgent_FinalizeWithAutoSave_SummaryResponse(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	agent := newWorkflowBuilderAgent(os.Getenv("TEST_ACCOUNT"))
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")})
	sessionId := "ut-wb-summary-response-1"
	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")

	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	// Stage 1: Send initial query
	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		"Build a workflow that prints 'Hello' and then prints 'World'",
		core.ConversationSessionRequestWithEnableQueryRefinement(false))
	assert.Nil(t, err)
	assert.NotNil(t, resp)

	// Handle plan approval if followup is enabled
	resp = handlePlanApprovalIfNeeded(t, sc, agent, userId, accountId, sessionId, resp)

	// After issue #29944 fix: there is no config-approval waiting stage. If the
	// flow is still parked at a Waiting status here it is a different stage and
	// would indicate a regression in this test's setup.
	assert.NotEqual(t, core.ConversationStatusWaiting, resp.Status,
		"missing-config workflows must not park in a waiting stage")

	assert.Greater(t, len(resp.Response), 0)
	fullResponse := strings.Join(resp.Response, "\n")
	t.Log("Final response:", fullResponse)

	// The response should be a markdown summary, NOT raw JSON
	// Since this runs through ask-nudgebee source (default, not WorkflowBuilder),
	// finalizeWithAutoSave should return summary format
	var jsonCheck map[string]interface{}
	isJSON := json.Unmarshal([]byte(fullResponse), &jsonCheck) == nil

	if !isJSON {
		// Summary format: verify markdown content
		assert.Contains(t, fullResponse, "The automation")
		assert.Contains(t, fullResponse, "**Tasks:**")
		// Should contain an "Open in Editor" link if auto-save succeeded
		if strings.Contains(fullResponse, "Open in Editor") {
			assert.Contains(t, fullResponse, "/workflow/")
			assert.Contains(t, fullResponse, accountId)
			assert.Contains(t, fullResponse, "built and saved")
			t.Log("Summary response with editor link verified")
		} else if strings.Contains(fullResponse, "Auto-save failed") {
			assert.Contains(t, fullResponse, "not yet saved",
				"non-saved summary must avoid the 'built and saved' headline")
			t.Log("Auto-save failed (expected in some test envs), summary still generated")
		}
	} else {
		// JSON fallback: buildWorkflowSummary might have failed, or this went through
		// a different code path. Still validate structure.
		t.Log("Response is JSON (summary generation may have been bypassed)")
		assert.Contains(t, fullResponse, "definition")
		assert.Contains(t, fullResponse, "tasks")
	}
}

// ==================== CONTEXT-AWARENESS TESTS ====================
// These tests verify that the workflow builder correctly fetches and uses
// account environment context (integrations, cloud accounts, observability providers).

// TestBuildEnvironmentContext verifies that buildEnvironmentContext returns
// formatted context with actual integrations and cloud accounts from the test account.

func TestBuildEnvironmentContext(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	agent := newWorkflowBuilderAgent(os.Getenv("TEST_ACCOUNT"))
	sc := security.NewRequestContextForTenantAccountAdmin(
		os.Getenv("TEST_TENANT"),
		os.Getenv("TEST_USER"),
		[]string{os.Getenv("TEST_ACCOUNT")},
	)

	ctx := agent.buildEnvironmentContext(sc)

	t.Log("Environment context:\n", ctx)

	// Should not be empty — test account has integrations and cloud accounts
	assert.NotEmpty(t, ctx, "Environment context should not be empty for test account")

	// Should contain the ACCOUNT ENVIRONMENT header
	assert.Contains(t, ctx, "ACCOUNT ENVIRONMENT", "Should contain header")

	// Should list cloud accounts (test account has K8s, AWS, GCP, Azure)
	assert.Contains(t, ctx, "Cloud accounts:", "Should list cloud accounts")

	// Should list integrations (test account has postgresql, loki, mssql, etc.)
	assert.Contains(t, ctx, "Integrations:", "Should list integrations")

	// Verify specific known integrations exist (from DB query)
	assert.Contains(t, ctx, "postgresql", "Should contain postgresql integration type")
	assert.Contains(t, ctx, "loki", "Should contain loki integration type")

	// Should contain the instruction about matching integrations by name
	assert.Contains(t, ctx, "Configs.", "Should contain Configs reference instruction")
}

// TestBuildEnvironmentContext_IncludesObservabilityProviders verifies that
// default log/metrics providers are included when the services server is reachable.

func TestBuildEnvironmentContext_IncludesObservabilityProviders(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}
	if os.Getenv("service_api_server_url") == "" {
		t.Skip("Skipping test: service_api_server_url not set (needed for observability provider lookup)")
	}

	agent := newWorkflowBuilderAgent(os.Getenv("TEST_ACCOUNT"))
	sc := security.NewRequestContextForTenantAccountAdmin(
		os.Getenv("TEST_TENANT"),
		os.Getenv("TEST_USER"),
		[]string{os.Getenv("TEST_ACCOUNT")},
	)

	ctx := agent.buildEnvironmentContext(sc)

	// If the services server is running, we should get provider info.
	// If not, the context will still contain integrations/cloud accounts.
	if strings.Contains(ctx, "Default log provider") {
		t.Log("Observability provider detected in context")
		assert.Contains(t, ctx, "observability.logs", "Should reference observability.logs task type")
	} else {
		t.Log("Observability provider not available (services server may not be running)")
	}
}

// TestFetchTaskTypeNames verifies that fetchTaskTypeNames returns task types
// from the runbook server when available.

func TestFetchTaskTypeNames(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	agent := newWorkflowBuilderAgent(os.Getenv("TEST_ACCOUNT"))
	sc := security.NewRequestContextForTenantAccountAdmin(
		os.Getenv("TEST_TENANT"),
		os.Getenv("TEST_USER"),
		[]string{os.Getenv("TEST_ACCOUNT")},
	)

	result := agent.fetchTaskTypeNames(sc)

	if result == "" {
		t.Log("Runbook server not reachable — fetchTaskTypeNames returned empty (expected in local dev)")
		return
	}

	t.Log("Task type names:\n", result)

	// Should contain the header
	assert.Contains(t, result, "AVAILABLE TASK TYPES", "Should contain header")

	// Should contain common task types
	assert.Contains(t, result, "core.print", "Should contain core.print")
	assert.Contains(t, result, "k8s.cli", "Should contain k8s.cli")
	assert.Contains(t, result, "observability.logs", "Should contain observability.logs")
	assert.Contains(t, result, "notifications.im", "Should contain notifications.im")
}

// TestExtractIntent_UsesCorrectTaskTypes verifies that extractIntent maps
// user requests to the correct task types (e.g., log queries → observability.logs,
// not scripting.run_script).

func TestExtractIntent_UsesCorrectTaskTypes(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	agent := newWorkflowBuilderAgent(os.Getenv("TEST_ACCOUNT"))
	sc := security.NewRequestContextForTenantAccountAdmin(
		os.Getenv("TEST_TENANT"),
		os.Getenv("TEST_USER"),
		[]string{os.Getenv("TEST_ACCOUNT")},
	)

	testCases := []struct {
		name             string
		query            string
		expectedTypes    []string // at least one of these should appear in task_types_needed
		notExpectedTypes []string // none of these should appear
	}{
		{
			name:             "log query should use observability.logs",
			query:            "Query Loki logs for errors in the last hour and alert on Slack",
			expectedTypes:    []string{"observability.logs", "notifications.im"},
			notExpectedTypes: []string{"scripting.run_script"},
		},
		{
			name:          "k8s operations should use k8s.cli",
			query:         "Get pods in production namespace and restart any that are crashlooping",
			expectedTypes: []string{"k8s.cli"},
		},
		{
			name:          "AI investigation should use llm.investigate",
			query:         "Use AI to analyze code for a bug and suggest a fix",
			expectedTypes: []string{"llm.investigate"},
		},
		{
			name:          "database query should use dbms.query",
			query:         "Query dev-pg database to check active users count",
			expectedTypes: []string{"dbms.query"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			request := core.NBAgentRequest{
				Query:          tc.query,
				AccountId:      os.Getenv("TEST_ACCOUNT"),
				UserId:         os.Getenv("TEST_USER"),
				ConversationId: uuid.New().String(),
				AgentId:        uuid.New().String(),
				MessageId:      uuid.New().String(),
			}

			intent, err := agent.extractIntent(sc, request)
			if err != nil {
				t.Logf("extractIntent failed (LLM may not be available): %v", err)
				t.Skip("Skipping: LLM not available")
			}

			t.Log("Intent response:", intent)

			// Parse the JSON response
			var intentData struct {
				TaskTypesNeeded []string `json:"task_types_needed"`
			}
			// Strip markdown code fences if present
			cleanIntent := strings.TrimSpace(intent)
			cleanIntent = strings.TrimPrefix(cleanIntent, "```json")
			cleanIntent = strings.TrimPrefix(cleanIntent, "```")
			cleanIntent = strings.TrimSuffix(cleanIntent, "```")
			cleanIntent = strings.TrimSpace(cleanIntent)

			err = json.Unmarshal([]byte(cleanIntent), &intentData)
			assert.Nil(t, err, "Intent should be valid JSON: %s", cleanIntent)

			// Verify expected task types are present
			taskTypesStr := strings.Join(intentData.TaskTypesNeeded, ", ")
			for _, expected := range tc.expectedTypes {
				assert.Contains(t, taskTypesStr, expected,
					"Expected task type %s in task_types_needed for query: %s (got: %s)",
					expected, tc.query, taskTypesStr)
			}

			// Verify unwanted task types are absent
			for _, notExpected := range tc.notExpectedTypes {
				assert.NotContains(t, taskTypesStr, notExpected,
					"Did NOT expect task type %s in task_types_needed for query: %s (got: %s)",
					notExpected, tc.query, taskTypesStr)
			}
		})
	}
}

// --- Clarification stage tests ---

// TestWorkflowBuilderAgent_ClarificationStateMarshal tests that clarification state fields survive marshal/unmarshal.

func drainWaitingStages(t *testing.T, sc *security.RequestContext, agent core.NBAgent, userId, accountId, sessionId string, resp core.NBAgentResponse) core.NBAgentResponse {
	t.Helper()
	maxStages := 5
	for i := 0; i < maxStages && resp.Status == core.ConversationStatusWaiting; i++ {
		answer := ""
		if len(resp.FollowupRequest.FollowupOptions) > 0 {
			answer = resp.FollowupRequest.FollowupOptions[0]
		}

		logMsg := displayText(resp)
		if len(logMsg) > 100 {
			logMsg = logMsg[:100] + "..."
		}
		t.Logf("Auto-approving post-plan stage %d: %s (answering: %s)", i+1, logMsg, answer)

		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)
		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)

		resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
			answer,
			core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
			core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
	return resp
}

// TestWorkflowBuilderAgent_ClarificationFlow tests the full clarification → plan → approve → build flow.

func TestWorkflowBuilderAgent_ClarificationFlow(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	agent := newWorkflowBuilderAgent(os.Getenv("TEST_ACCOUNT"))
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")})
	sessionId := "ut-wb-clarification-flow-1"
	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")

	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	// Stage 1: Send ambiguous query
	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		"send alert when pod crashes",
		core.ConversationSessionRequestWithEnableQueryRefinement(false))
	assert.Nil(t, err)
	assert.NotNil(t, resp)

	if resp.Status != core.ConversationStatusWaiting {
		t.Log("No clarification or plan approval — workflow built directly")
		assert.Greater(t, len(resp.Response), 0)
		return
	}

	// Walk through clarification questions (if any) by selecting the first option
	maxClarifications := 5 // safety limit
	for i := 0; i < maxClarifications && resp.Status == core.ConversationStatusWaiting; i++ {
		questionText := displayText(resp)

		// Check if this is a plan approval (not clarification)
		if strings.Contains(strings.ToLower(questionText), "plan") {
			break
		}

		t.Logf("Clarification %d: %s", i+1, questionText)

		// Answer with the first option from FollowupOptions
		answer := "Yes"
		if len(resp.FollowupRequest.FollowupOptions) > 0 {
			answer = resp.FollowupRequest.FollowupOptions[0]
		}

		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)
		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)

		resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
			answer,
			core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
			core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}

	// Approve plan
	if resp.Status == core.ConversationStatusWaiting {
		t.Log("Plan:", displayText(resp))

		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)
		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)

		resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
			PlanApprovalOptionApprove,
			core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
			core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}

	// Drain any post-plan WAITING stages (config approval, etc.)
	resp = drainWaitingStages(t, sc, agent, userId, accountId, sessionId, resp)

	assert.Greater(t, len(resp.Response), 0)
	finalResponse := strings.Join(resp.Response, "\n")
	assertWorkflowResponse(t, finalResponse)
	t.Log("Final workflow:", finalResponse)
}

// TestWorkflowBuilderAgent_ClarificationSkipAll tests skipping all clarification questions.

func TestWorkflowBuilderAgent_ClarificationSkipAll(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	agent := newWorkflowBuilderAgent(os.Getenv("TEST_ACCOUNT"))
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")})
	sessionId := "ut-wb-clarification-skip-all-1"
	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")

	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		"notify me when something goes wrong with my services",
		core.ConversationSessionRequestWithEnableQueryRefinement(false))
	assert.Nil(t, err)
	assert.NotNil(t, resp)

	if resp.Status != core.ConversationStatusWaiting {
		t.Log("No clarification — workflow built directly")
		assert.Greater(t, len(resp.Response), 0)
		return
	}

	// Skip all clarification questions
	maxClarifications := 5
	for i := 0; i < maxClarifications && resp.Status == core.ConversationStatusWaiting; i++ {
		questionText := displayText(resp)

		// If this looks like a plan, break out
		if strings.Contains(strings.ToLower(questionText), "plan") {
			break
		}

		t.Logf("Skipping clarification %d: %s", i+1, questionText)

		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)
		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)

		resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
			"Skip",
			core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
			core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}

	// Approve plan
	if resp.Status == core.ConversationStatusWaiting {
		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)
		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)

		resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
			PlanApprovalOptionApprove,
			core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
			core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}

	// Drain any post-plan WAITING stages (config approval, etc.)
	resp = drainWaitingStages(t, sc, agent, userId, accountId, sessionId, resp)

	assert.Greater(t, len(resp.Response), 0)
	finalResponse := strings.Join(resp.Response, "\n")
	assertWorkflowResponse(t, finalResponse)
}

// TestWorkflowBuilderAgent_ClarificationSkipForClearRequest tests that a specific request skips clarification entirely.

func TestWorkflowBuilderAgent_ClarificationSkipForClearRequest(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	agent := newWorkflowBuilderAgent(os.Getenv("TEST_ACCOUNT"))
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")})
	sessionId := "ut-wb-clarification-clear-1"
	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")

	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	// Very specific query — should skip clarification
	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		"Create a workflow that runs every 30 minutes, executes 'kubectl get pods -n production -o json' to list all pods, "+
			"then uses a data.transform task to filter for pods with restartCount > 3, "+
			"and prints the names of unhealthy pods using core.print",
		core.ConversationSessionRequestWithEnableQueryRefinement(false))
	assert.Nil(t, err)
	assert.NotNil(t, resp)

	// Approve plan if WAITING
	if resp.Status == core.ConversationStatusWaiting {
		t.Log("Response:", displayText(resp))

		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)
		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)

		resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
			PlanApprovalOptionApprove,
			core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
			core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}

	// Drain any post-plan WAITING stages (config approval, etc.)
	resp = drainWaitingStages(t, sc, agent, userId, accountId, sessionId, resp)

	assert.Greater(t, len(resp.Response), 0)
	finalResponse := strings.Join(resp.Response, "\n")
	assertWorkflowResponse(t, finalResponse)
}

func TestWorkflowBuilder_EnvContext_RealCloudAccountsExposeUUID(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	agent := newWorkflowBuilderAgent(os.Getenv("TEST_ACCOUNT"))
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")})

	envContext := agent.buildEnvironmentContext(sc)

	// Empty is acceptable only if the test account has zero configured
	// cloud accounts and zero integrations — log so we can investigate.
	if envContext == "" {
		t.Log("buildEnvironmentContext returned empty — test account has no configured cloud accounts/integrations")
		return
	}

	t.Logf("env context:\n%s", envContext)

	// If the rendered context lists any cloud accounts, every one must
	// carry an `id=<uuid>` suffix per the fix at agent_workflow_builder.go:1221.
	if strings.Contains(envContext, "Cloud accounts:") {
		assert.Regexp(t, `id=[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`, envContext,
			"every cloud account in env context must include an id=<uuid> suffix")

		// Specifically assert the account_id-must-be-UUID trailer is present.
		assert.Contains(t, envContext, "account_id",
			"env context must reference account_id when cloud accounts are listed")
		assert.Contains(t, envContext, "UUID `id=`",
			"env context trailer must instruct LLM to use the UUID id= value")
	}
}

// TestWorkflowBuilder_ResolveCloudAccountIds_RealAccount is an integration
// test that exercises resolveCloudAccountIds against real ListAllToolConfigs
// data, swapping a known account name for its UUID.
//
// Requires: DB + services-server (no LLM, no workflow-server).

func TestWorkflowBuilder_ResolveCloudAccountIds_RealAccount(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	agent := newWorkflowBuilderAgent(os.Getenv("TEST_ACCOUNT"))
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")})

	// Discover a real cloud-account name on the test account.
	allConfigs, err := toolcore.ListAllToolConfigs(sc, os.Getenv("TEST_ACCOUNT"))
	assert.Nil(t, err)

	var sampleName, sampleId string
	for _, cfg := range allConfigs {
		ct := strings.ToLower(cfg.Schema.ConfigType)
		id := cloudAccountId(cfg)
		if cloudAccountConfigTypes[ct] && cfg.Name != "" && id != "" {
			sampleName = cfg.Name
			sampleId = id
			break
		}
	}
	if sampleName == "" {
		t.Skip("test account has no cloud-account configs; cannot exercise resolver against real data")
	}

	t.Logf("resolving cloud account name=%q to id=%q", sampleName, sampleId)

	// Build a minimal workflow JSON with account_id set to the NAME.
	workflowJSON := `{
		"name": "test-resolve",
		"definition": {
			"tasks": [
				{"id": "t1", "type": "k8s.cli", "params": {"account_id": "` + sampleName + `", "command": "get pods"}}
			],
			"triggers": [{"type": "manual"}],
			"version": "v1"
		}
	}`

	resolved, err := agent.resolveCloudAccountIds(sc, workflowJSON)
	assert.Nil(t, err)
	assert.Contains(t, resolved, sampleId, "resolver must swap account name to its UUID")
	assert.NotContains(t, resolved, `"account_id": "`+sampleName+`"`, "resolved workflow must not retain the account name")
}

// TestWorkflowBuilder_E2E_FailedScheduling_QARepro reproduces the QA scenario
// that originally surfaced the account_id-as-name bug. Drives the builder
// end-to-end through a real LLM, walking clarifying questions and approving
// the plan, then asserts the save succeeded (which proves the UUID resolver
// swapped the cloud-account name to its UUID before POSTing /workflows).
//
// Requires: TEST_ACCOUNT + LLM provider creds + port-forwards for:
//
//	postgres (5433), services-server (8888), workflow-server (8002), rag-server (9999)

func TestWorkflowBuilder_E2E_FailedScheduling_QARepro(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	agent := newWorkflowBuilderAgent(os.Getenv("TEST_ACCOUNT"))
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")})
	sessionId := "ut-wb-qa-repro-failedscheduling-1"
	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")

	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	// Original QA prompt that produced account_id="my-k8s-dev".
	query := "create automation for\nDetect FailedScheduling events for pods. Investigate node capacity, taints/tolerations, affinity rules, and autoscaler status. Recommend corrective action or trigger node scaling."

	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		query,
		core.ConversationSessionRequestWithEnableQueryRefinement(false))
	assert.Nil(t, err)
	assert.NotNil(t, resp)

	// Walk clarifying questions, then approve plan, then drain post-approval stages.
	const maxStages = 10
	for i := 0; i < maxStages && resp.Status == core.ConversationStatusWaiting; i++ {
		answer := ""
		if len(resp.FollowupRequest.FollowupOptions) > 0 {
			// If the plan-approval prompt appears, approve it.
			isApproval := false
			for _, opt := range resp.FollowupRequest.FollowupOptions {
				if opt == PlanApprovalOptionApprove {
					answer = PlanApprovalOptionApprove
					isApproval = true
					break
				}
			}
			if !isApproval {
				// Clarification — pick first option (typically "recommended").
				answer = resp.FollowupRequest.FollowupOptions[0]
			}
		}

		preview := displayText(resp)
		if len(preview) > 120 {
			preview = preview[:120] + "..."
		}
		t.Logf("stage %d: question=%q answer=%q", i+1, preview, answer)

		messageId, perr := uuid.Parse(resp.MessageId)
		assert.Nil(t, perr)
		agentId, perr := uuid.Parse(resp.AgentId)
		assert.Nil(t, perr)

		resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
			answer,
			core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
			core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}

	finalResponse := strings.Join(resp.Response, "\n")
	t.Logf("final response:\n%s", finalResponse)

	// Save should have succeeded — final response must contain the editor link
	// and must NOT contain the runbook-server UUID-validation error nor a
	// generic auto-save failure.
	assert.NotContains(t, finalResponse, "invalid input syntax for type uuid",
		"runbook server rejected non-UUID account_id — resolver did not swap name→UUID")
	assert.NotContains(t, finalResponse, "Auto-save failed",
		"workflow save failed despite the fixes")
	assert.Contains(t, finalResponse, "[Open in Editor]",
		"saved workflow summary must include the Open-in-Editor link")
}

// TestWorkflowBuilder_E2E_OOMKilled_QARepro mirrors the OOMKilled QA repro.
// Same gating + service requirements as TestWorkflowBuilder_E2E_FailedScheduling_QARepro.

func TestWorkflowBuilder_E2E_OOMKilled_QARepro(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	agent := newWorkflowBuilderAgent(os.Getenv("TEST_ACCOUNT"))
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")})
	sessionId := "ut-wb-qa-repro-oomkilled-1"
	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")

	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	query := "Create automation for Trigger automation when a pod receives OOMKilled event. Analyze memory usage trends, compare limits vs actual usage, identify memory spikes, and recommend updated resource limits."

	resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
		query,
		core.ConversationSessionRequestWithEnableQueryRefinement(false))
	assert.Nil(t, err)
	assert.NotNil(t, resp)

	const maxStages = 10
	for i := 0; i < maxStages && resp.Status == core.ConversationStatusWaiting; i++ {
		answer := ""
		if len(resp.FollowupRequest.FollowupOptions) > 0 {
			isApproval := false
			for _, opt := range resp.FollowupRequest.FollowupOptions {
				if opt == PlanApprovalOptionApprove {
					answer = PlanApprovalOptionApprove
					isApproval = true
					break
				}
			}
			if !isApproval {
				answer = resp.FollowupRequest.FollowupOptions[0]
			}
		}

		preview := displayText(resp)
		if len(preview) > 120 {
			preview = preview[:120] + "..."
		}
		t.Logf("stage %d: question=%q answer=%q", i+1, preview, answer)

		messageId, perr := uuid.Parse(resp.MessageId)
		assert.Nil(t, perr)
		agentId, perr := uuid.Parse(resp.AgentId)
		assert.Nil(t, perr)

		resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
			answer,
			core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
			core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}

	finalResponse := strings.Join(resp.Response, "\n")
	t.Logf("final response:\n%s", finalResponse)

	assert.NotContains(t, finalResponse, "invalid input syntax for type uuid")
	assert.NotContains(t, finalResponse, "Auto-save failed")
	assert.Contains(t, finalResponse, "[Open in Editor]")
}

// TestWorkflowBuilder_CatchupWindowGuidanceAccurate guards the schedule
// trigger's catchup_window guidance across every prompt surface. The
// runbook-server validator uses Go's `time.ParseDuration` (runbook-server/
// internal/model/workflow.go:208), which rejects "7d"/"1w" but accepts
// compound durations like "1h30m". A previous revision had the rule
// inverted — this test prevents that regression.
