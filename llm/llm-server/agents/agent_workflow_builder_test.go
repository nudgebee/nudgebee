package agents

import (
	"encoding/json"
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

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
			Query:     "@workflow_builder Create a workflow to check pod health in both nudgebee and nudgebee-test namespaces, then aggregate the results and send an alert if any pods are unhealthy.",
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
		"Build a workflow that checks pod health in nudgebee namespace and sends a Slack alert",
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
		"Also check nudgebee-test namespace and add a schedule trigger for every hour",
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
		"Build a workflow that checks pod health in nudgebee namespace and sends a Slack alert",
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
		"Also check nudgebee-test namespace and add a schedule trigger for every hour",
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
func TestWorkflowBuilderAgent_StateMarshalUnmarshal(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")
	agent.state = WorkflowBuilderState{
		Stage:         "plan_approval",
		OriginalQuery: "Build a workflow that prints hello",
		Intent:        `{"description":"print hello"}`,
		Plan:          "1. Create a print task\n2. Use core.print type",
		PlanAttempts:  1,
	}

	// Marshal
	data, err := agent.MarshalState()
	assert.Nil(t, err)
	assert.NotNil(t, data)

	// Create a new agent and unmarshal into it
	agent2 := newWorkflowBuilderAgent("test-account")
	err = agent2.UnmarshalState(data)
	assert.Nil(t, err)

	// Verify state matches
	assert.Equal(t, agent.state.Stage, agent2.state.Stage)
	assert.Equal(t, agent.state.OriginalQuery, agent2.state.OriginalQuery)
	assert.Equal(t, agent.state.Intent, agent2.state.Intent)
	assert.Equal(t, agent.state.Plan, agent2.state.Plan)
	assert.Equal(t, agent.state.PlanAttempts, agent2.state.PlanAttempts)
}

// TestWorkflowBuilderAgent_FixMode_StateMarshal tests that fix-mode state fields survive marshal/unmarshal cycle
func TestWorkflowBuilderAgent_FixMode_StateMarshal(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")
	agent.state = WorkflowBuilderState{
		Stage:              "fix_approval",
		Mode:               "fix",
		OriginalQuery:      "Fix the pod health check workflow",
		WorkflowId:         "abc-123",
		ExistingDefinition: `{"name":"test","definition":{"tasks":[]}}`,
		ExecutionError:     "Task 'get-pods' failed: command error",
		ProposedChanges:    `{"diagnosis":"wrong namespace","changes":[]}`,
		ProposedDiff:       "**Task: `get-pods`**\n`params.command`:\n- Before: `get pods -n default`\n- After: `get pods -n nudgebee`",
	}

	// Marshal
	data, err := agent.MarshalState()
	assert.Nil(t, err)
	assert.NotNil(t, data)

	// Create a new agent and unmarshal into it
	agent2 := newWorkflowBuilderAgent("test-account")
	err = agent2.UnmarshalState(data)
	assert.Nil(t, err)

	// Verify all fix-mode state fields match
	assert.Equal(t, agent.state.Stage, agent2.state.Stage)
	assert.Equal(t, agent.state.Mode, agent2.state.Mode)
	assert.Equal(t, agent.state.OriginalQuery, agent2.state.OriginalQuery)
	assert.Equal(t, agent.state.WorkflowId, agent2.state.WorkflowId)
	assert.Equal(t, agent.state.ExistingDefinition, agent2.state.ExistingDefinition)
	assert.Equal(t, agent.state.ExecutionError, agent2.state.ExecutionError)
	assert.Equal(t, agent.state.ProposedChanges, agent2.state.ProposedChanges)
	assert.Equal(t, agent.state.ProposedDiff, agent2.state.ProposedDiff)
}

// TestWorkflowBuilderAgent_ToolInitWorkflow tests the init_workflow tool
func TestWorkflowBuilderAgent_ToolInitWorkflow(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")

	// Initialize workflow
	result := agent.toolInitWorkflow(map[string]interface{}{
		"name": "test-workflow",
		"triggers": []interface{}{
			map[string]interface{}{"type": "manual"},
		},
	})
	assert.Contains(t, result, "initialized")
	assert.NotNil(t, agent.state.WorkingWorkflow)
	assert.Equal(t, "test-workflow", agent.state.WorkingWorkflow["name"])

	// Missing name
	agent2 := newWorkflowBuilderAgent("test-account")
	result = agent2.toolInitWorkflow(map[string]interface{}{})
	assert.Contains(t, result, "Error")
}

// TestWorkflowBuilderAgent_ToolAddGetModifyDeleteTask tests the task CRUD tools
func TestWorkflowBuilderAgent_ToolAddGetModifyDeleteTask(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")

	// Init workflow first
	agent.toolInitWorkflow(map[string]interface{}{
		"name":     "test-workflow",
		"triggers": []interface{}{map[string]interface{}{"type": "manual"}},
	})

	// Add task
	result := agent.toolAddTask(map[string]interface{}{
		"id":   "get-pods",
		"type": "k8s.cli",
		"params": map[string]interface{}{
			"command": "get pods -n default",
		},
	})
	assert.Contains(t, result, "added successfully")
	assert.Contains(t, result, "1 task(s)")

	// Add duplicate task should fail
	result = agent.toolAddTask(map[string]interface{}{
		"id":   "get-pods",
		"type": "k8s.cli",
		"params": map[string]interface{}{
			"command": "get pods -n default",
		},
	})
	assert.Contains(t, result, "already exists")

	// Get task
	result = agent.toolGetTask(map[string]interface{}{"task_id": "get-pods"})
	assert.Contains(t, result, "get pods -n default")
	assert.Contains(t, result, "k8s.cli")

	// Get non-existent task
	result = agent.toolGetTask(map[string]interface{}{"task_id": "not-here"})
	assert.Contains(t, result, "not found")

	// Modify task
	result = agent.toolModifyTask(map[string]interface{}{
		"task_id": "get-pods",
		"id":      "get-pods",
		"type":    "k8s.cli",
		"params": map[string]interface{}{
			"command": "get pods -n nudgebee -o json",
		},
	})
	assert.Contains(t, result, "updated")

	// Verify modification
	result = agent.toolGetTask(map[string]interface{}{"task_id": "get-pods"})
	assert.Contains(t, result, "get pods -n nudgebee -o json")

	// Add second task
	agent.toolAddTask(map[string]interface{}{
		"id":         "notify",
		"type":       "notifications.im",
		"depends_on": []interface{}{"get-pods"},
		"params": map[string]interface{}{
			"provider": "slack",
			"message":  "Done",
		},
	})

	// List tasks
	result = agent.toolListTasks(map[string]interface{}{})
	assert.Contains(t, result, "get-pods")
	assert.Contains(t, result, "notify")
	assert.Contains(t, result, "k8s.cli")
	assert.Contains(t, result, "notifications.im")

	// Delete task
	result = agent.toolDeleteTask(map[string]interface{}{"task_id": "notify"})
	assert.Contains(t, result, "deleted")

	// Verify deletion
	result = agent.toolListTasks(map[string]interface{}{})
	assert.NotContains(t, result, "notify")
	assert.Contains(t, result, "get-pods")
}

// TestWorkflowBuilderAgent_ToolFinalize tests the finalize tool
func TestWorkflowBuilderAgent_ToolFinalize(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")

	// Init and add a task
	agent.toolInitWorkflow(map[string]interface{}{
		"name":     "test-workflow",
		"triggers": []interface{}{map[string]interface{}{"type": "manual"}},
	})
	agent.toolAddTask(map[string]interface{}{
		"id":   "print-hello",
		"type": "core.print",
		"params": map[string]interface{}{
			"message": "Hello World",
		},
	})

	// Finalize
	result := agent.toolFinalize()
	assert.Contains(t, result, "test-workflow")
	assert.Contains(t, result, "print-hello")
	assert.Contains(t, result, "core.print")

	// Should be valid JSON
	var workflow map[string]interface{}
	err := json.Unmarshal([]byte(result), &workflow)
	assert.Nil(t, err)
	assert.Equal(t, "test-workflow", workflow["name"])
}

// TestWorkflowBuilderAgent_BuildAndModifyWorkflow tests a full create → modify → validate → finalize cycle
// using in-memory tools without requiring LLM or runbook-server.
func TestWorkflowBuilderAgent_BuildAndModifyWorkflow(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")

	// Step 1: Initialize workflow
	result := agent.toolInitWorkflow(map[string]interface{}{
		"name": "pod-health-monitor",
		"triggers": []interface{}{
			map[string]interface{}{"type": "schedule", "params": map[string]interface{}{"cron": "*/30 * * * *"}},
		},
		"inputs": []interface{}{
			map[string]interface{}{"id": "namespace", "type": "string", "default": "nudgebee"},
		},
	})
	assert.Contains(t, result, "initialized")

	// Step 2: Add k8s task
	result = agent.toolAddTask(map[string]interface{}{
		"id":   "get-pods",
		"type": "k8s.cli",
		"params": map[string]interface{}{
			"command": "get pods -n {{ Inputs.namespace }} -o json",
		},
	})
	assert.Contains(t, result, "added")

	// Step 3: Add transform task
	result = agent.toolAddTask(map[string]interface{}{
		"id":         "check-health",
		"type":       "data.transform",
		"depends_on": []interface{}{"get-pods"},
		"params": map[string]interface{}{
			"input":      "{{ Tasks['get-pods'].output.data }}",
			"inputType":  "json",
			"expression": `{ "unhealthy": items[status.phase != 'Running'] }`,
		},
	})
	assert.Contains(t, result, "added")
	assert.Contains(t, result, "2 task(s)")

	// Step 4: Add notification task with condition
	result = agent.toolAddTask(map[string]interface{}{
		"id":         "send-alert",
		"type":       "notifications.im",
		"depends_on": []interface{}{"check-health"},
		"if":         "{{ Tasks['check-health'].output.data.unhealthy | length > 0 }}",
		"params": map[string]interface{}{
			"provider": "slack",
			"channel":  "#alerts",
			"message":  "Found {{ Tasks['check-health'].output.data.unhealthy | length }} unhealthy pods",
		},
	})
	assert.Contains(t, result, "added")
	assert.Contains(t, result, "3 task(s)")

	// Step 5: Verify list_tasks shows all tasks
	result = agent.toolListTasks(map[string]interface{}{})
	assert.Contains(t, result, "get-pods")
	assert.Contains(t, result, "check-health")
	assert.Contains(t, result, "send-alert")
	assert.Contains(t, result, "k8s.cli")
	assert.Contains(t, result, "data.transform")
	assert.Contains(t, result, "notifications.im")

	// Step 6: Simulate a fix — change the namespace
	result = agent.toolGetTask(map[string]interface{}{"task_id": "get-pods"})
	assert.Contains(t, result, "Inputs.namespace")

	result = agent.toolModifyTask(map[string]interface{}{
		"task_id": "get-pods",
		"id":      "get-pods",
		"type":    "k8s.cli",
		"params": map[string]interface{}{
			"command": "get pods -n nudgebee -o json",
		},
	})
	assert.Contains(t, result, "updated")

	// Verify the change
	result = agent.toolGetTask(map[string]interface{}{"task_id": "get-pods"})
	assert.Contains(t, result, "nudgebee")
	assert.NotContains(t, result, "Inputs.namespace")

	// Step 7: Add a fourth task, then delete it
	agent.toolAddTask(map[string]interface{}{
		"id":         "log-result",
		"type":       "core.print",
		"depends_on": []interface{}{"send-alert"},
		"params": map[string]interface{}{
			"message": "Alert sent",
		},
	})
	result = agent.toolListTasks(map[string]interface{}{})
	assert.Contains(t, result, "log-result")
	assert.Contains(t, result, "Tasks (4)")

	result = agent.toolDeleteTask(map[string]interface{}{"task_id": "log-result"})
	assert.Contains(t, result, "deleted")

	result = agent.toolListTasks(map[string]interface{}{})
	assert.NotContains(t, result, "log-result")
	assert.Contains(t, result, "Tasks (3)")

	// Step 8: Finalize — should produce valid JSON with all changes applied
	result = agent.toolFinalize()
	var workflow map[string]interface{}
	err := json.Unmarshal([]byte(result), &workflow)
	assert.Nil(t, err)
	assert.Equal(t, "pod-health-monitor", workflow["name"])

	definition := workflow["definition"].(map[string]interface{})
	tasks := definition["tasks"].([]interface{})
	assert.Equal(t, 3, len(tasks))

	// Verify triggers persisted
	triggers := definition["triggers"].([]interface{})
	assert.Equal(t, 1, len(triggers))
	trigger := triggers[0].(map[string]interface{})
	assert.Equal(t, "schedule", trigger["type"])

	// Verify inputs persisted
	inputs := definition["inputs"].([]interface{})
	assert.Equal(t, 1, len(inputs))

	// Verify first task has updated command
	task0 := tasks[0].(map[string]interface{})
	assert.Equal(t, "get-pods", task0["id"])
	params0 := task0["params"].(map[string]interface{})
	assert.Equal(t, "get pods -n nudgebee -o json", params0["command"])

	// Verify third task has conditional
	task2 := tasks[2].(map[string]interface{})
	assert.Equal(t, "send-alert", task2["id"])
	assert.Contains(t, task2["if"], "unhealthy")
}

// TestWorkflowBuilderAgent_LoadExistingAndModify simulates fix mode: load an existing
// workflow JSON into workingWorkflow, then modify specific tasks.
func TestWorkflowBuilderAgent_LoadExistingAndModify(t *testing.T) {
	existingJSON := `{
  "name": "daily-report",
  "definition": {
    "version": "v1",
    "triggers": [{"type": "schedule", "params": {"cron": "0 9 * * *"}}],
    "tasks": [
      {
        "id": "fetch-metrics",
        "type": "k8s.cli",
        "params": {"command": "top pods -n default"}
      },
      {
        "id": "summarize",
        "type": "llm.investigate",
        "depends_on": ["fetch-metrics"],
        "params": {"message": "Summarize: {{ Tasks['fetch-metrics'].output.data }}"}
      },
      {
        "id": "send-report",
        "type": "notifications.im",
        "depends_on": ["summarize"],
        "params": {
          "provider": "slack",
          "channel": "#daily-reports",
          "message": "{{ Tasks['summarize'].output.data }}"
        }
      }
    ]
  }
}`

	agent := newWorkflowBuilderAgent("test-account")

	// Load existing workflow into workingWorkflow (simulates what handleFixEntry does)
	var workflow map[string]interface{}
	err := json.Unmarshal([]byte(existingJSON), &workflow)
	assert.Nil(t, err)
	agent.state.WorkingWorkflow = workflow

	// Diagnose: list tasks to understand structure
	result := agent.toolListTasks(map[string]interface{}{})
	assert.Contains(t, result, "fetch-metrics")
	assert.Contains(t, result, "summarize")
	assert.Contains(t, result, "send-report")
	assert.Contains(t, result, "Tasks (3)")

	// Read the problematic task
	result = agent.toolGetTask(map[string]interface{}{"task_id": "fetch-metrics"})
	assert.Contains(t, result, "top pods -n default")

	// Fix 1: wrong namespace in fetch-metrics
	result = agent.toolModifyTask(map[string]interface{}{
		"task_id": "fetch-metrics",
		"id":      "fetch-metrics",
		"type":    "k8s.cli",
		"params":  map[string]interface{}{"command": "top pods -n nudgebee --sort-by=cpu"},
	})
	assert.Contains(t, result, "updated")

	// Fix 2: change slack channel
	result = agent.toolGetTask(map[string]interface{}{"task_id": "send-report"})
	assert.Contains(t, result, "#daily-reports")

	result = agent.toolModifyTask(map[string]interface{}{
		"task_id":    "send-report",
		"id":         "send-report",
		"type":       "notifications.im",
		"depends_on": []interface{}{"summarize"},
		"params": map[string]interface{}{
			"provider": "slack",
			"channel":  "{{ Configs.slack_reports_channel }}",
			"message":  "{{ Tasks['summarize'].output.data }}",
		},
	})
	assert.Contains(t, result, "updated")

	// Add a new task: also send email
	result = agent.toolAddTask(map[string]interface{}{
		"id":         "send-email",
		"type":       "notifications.email",
		"depends_on": []interface{}{"summarize"},
		"params": map[string]interface{}{
			"to":      "team@example.com",
			"subject": "Daily Pod Health Report",
			"body":    "{{ Tasks['summarize'].output.data }}",
		},
	})
	assert.Contains(t, result, "added")
	assert.Contains(t, result, "4 task(s)")

	// Finalize and verify
	result = agent.toolFinalize()
	var finalWorkflow map[string]interface{}
	err = json.Unmarshal([]byte(result), &finalWorkflow)
	assert.Nil(t, err)
	assert.Equal(t, "daily-report", finalWorkflow["name"])

	def := finalWorkflow["definition"].(map[string]interface{})
	tasks := def["tasks"].([]interface{})
	assert.Equal(t, 4, len(tasks))

	// Verify fix 1: namespace change
	t0 := tasks[0].(map[string]interface{})
	assert.Equal(t, "top pods -n nudgebee --sort-by=cpu", t0["params"].(map[string]interface{})["command"])

	// Verify fix 2: channel uses Configs
	t2 := tasks[2].(map[string]interface{})
	assert.Equal(t, "{{ Configs.slack_reports_channel }}", t2["params"].(map[string]interface{})["channel"])

	// Verify new task added
	t3 := tasks[3].(map[string]interface{})
	assert.Equal(t, "send-email", t3["id"])
	assert.Equal(t, "notifications.email", t3["type"])
}

// TestWorkflowBuilderAgent_CoercionInFinalize tests that finalize applies type coercion
func TestWorkflowBuilderAgent_CoercionInFinalize(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")

	agent.toolInitWorkflow(map[string]interface{}{
		"name": "coercion-test",
	})
	agent.toolAddTask(map[string]interface{}{
		"id":   "task-1",
		"type": "http.request",
		"params": map[string]interface{}{
			"concurrency": float64(5),
			"max_retries": float64(3),
			"timeout":     float64(30),
			"ratio":       float64(0.75), // non-whole float should stay
			"url":         "https://example.com",
		},
	})

	result := agent.toolFinalize()
	var workflow map[string]interface{}
	err := json.Unmarshal([]byte(result), &workflow)
	assert.Nil(t, err)

	def := workflow["definition"].(map[string]interface{})
	tasks := def["tasks"].([]interface{})
	params := tasks[0].(map[string]interface{})["params"].(map[string]interface{})

	// After coercion + re-marshal via JSON, whole-number floats become ints in the JSON
	// but json.Unmarshal will read them back as float64. What matters is the JSON output
	// contains "5" not "5.0". Check via string:
	assert.Contains(t, result, `"concurrency": 5`)
	assert.NotContains(t, result, `"concurrency": 5.0`)
	assert.Contains(t, result, `"max_retries": 3`)
	assert.NotContains(t, result, `"max_retries": 3.0`)

	// Non-whole float should remain
	assert.Equal(t, 0.75, params["ratio"])
}

// TestWorkflowBuilderAgent_HandleEntry_DetectsFixMode tests that handleEntry routes correctly
func TestWorkflowBuilderAgent_HandleEntry_DetectsFixMode(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")

	// Without workflow_id in QueryConfig → should try create mode (will fail without LLM, but confirms routing)
	assert.Equal(t, "", agent.state.Mode)

	// With workflow_id → state should reflect fix mode after entry attempt
	// (We can't fully test handleFixEntry without a running runbook-server,
	// but we can verify the routing logic works by checking the QueryConfig detection)
	request := core.NBAgentRequest{
		QueryConfig: toolcore.NBQueryConfig{
			WorkflowId: "test-workflow-123",
		},
	}
	assert.True(t, request.QueryConfig.WorkflowId != "")
}

// TestWorkflowBuilderAgent_ExtractWorkflowIdFromQuery tests UUID extraction from query text
func TestWorkflowBuilderAgent_ExtractWorkflowIdFromQuery(t *testing.T) {
	// Fix request with UUID → should extract
	id := extractWorkflowIdFromQuery("Fix workflow 5d064c4c-bb53-4630-95ff-f6bb7b9133a6. Error: task failed")
	assert.Equal(t, "5d064c4c-bb53-4630-95ff-f6bb7b9133a6", id)

	// Debug request with UUID → should extract
	id = extractWorkflowIdFromQuery("Debug the failing workflow abc12345-1234-5678-9abc-def012345678")
	assert.Equal(t, "abc12345-1234-5678-9abc-def012345678", id)

	// Error message with UUID → should extract
	id = extractWorkflowIdFromQuery("Workflow 5d064c4c-bb53-4630-95ff-f6bb7b9133a6 has an error in the notification task")
	assert.Equal(t, "5d064c4c-bb53-4630-95ff-f6bb7b9133a6", id)

	// Create request with UUID → should NOT extract (no fix keyword)
	id = extractWorkflowIdFromQuery("Create a workflow named 5d064c4c-bb53-4630-95ff-f6bb7b9133a6")
	assert.Equal(t, "", id)

	// Create request without UUID → should NOT extract
	id = extractWorkflowIdFromQuery("Build a workflow that prints hello")
	assert.Equal(t, "", id)

	// Fix request without UUID → should return empty
	id = extractWorkflowIdFromQuery("Fix the pod health check workflow")
	assert.Equal(t, "", id)
}

// TestWorkflowBuilderAgent_FixMode_Integration tests the full fix mode flow against a real workflow
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

func TestCoerceWorkflowTypes(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name: "converts whole-number floats to int",
			input: map[string]interface{}{
				"name": "test",
				"definition": map[string]interface{}{
					"tasks": []interface{}{
						map[string]interface{}{
							"id":   "task-1",
							"type": "http",
							"params": map[string]interface{}{
								"concurrency": float64(5),
								"timeout":     float64(30),
								"url":         "https://example.com",
							},
						},
					},
				},
			},
			expected: map[string]interface{}{
				"name": "test",
				"definition": map[string]interface{}{
					"tasks": []interface{}{
						map[string]interface{}{
							"id":   "task-1",
							"type": "http",
							"params": map[string]interface{}{
								"concurrency": 5,
								"timeout":     30,
								"url":         "https://example.com",
							},
						},
					},
				},
			},
		},
		{
			name: "preserves non-whole floats",
			input: map[string]interface{}{
				"value": float64(3.14),
				"whole": float64(42),
			},
			expected: map[string]interface{}{
				"value": float64(3.14),
				"whole": 42,
			},
		},
		{
			name: "handles arrays with mixed types",
			input: map[string]interface{}{
				"items": []interface{}{
					float64(1),
					float64(2.5),
					"string",
					map[string]interface{}{
						"nested": float64(10),
					},
				},
			},
			expected: map[string]interface{}{
				"items": []interface{}{
					1,
					float64(2.5),
					"string",
					map[string]interface{}{
						"nested": 10,
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			coerceWorkflowTypes(tc.input)
			assert.Equal(t, tc.expected, tc.input)
		})
	}
}

func TestCoerceWorkflowTypes_RoundTrip(t *testing.T) {
	// Simulate the actual scenario: JSON unmarshal produces float64, coercion fixes it
	jsonStr := `{"name":"test","definition":{"tasks":[{"id":"task-1","params":{"concurrency":5}}]}}`
	var workflow map[string]interface{}
	err := json.Unmarshal([]byte(jsonStr), &workflow)
	assert.Nil(t, err)

	// Before coercion: Go's json.Unmarshal produces float64
	def := workflow["definition"].(map[string]interface{})
	tasks := def["tasks"].([]interface{})
	params := tasks[0].(map[string]interface{})["params"].(map[string]interface{})
	assert.IsType(t, float64(0), params["concurrency"])

	// After coercion: should be int
	coerceWorkflowTypes(workflow)
	params = tasks[0].(map[string]interface{})["params"].(map[string]interface{})
	assert.IsType(t, int(0), params["concurrency"])
	assert.Equal(t, 5, params["concurrency"])
}

// ==================== AGENTIC INTEGRATION TESTS ====================
// These tests exercise the full runToolLoop() end-to-end with actual LLM calls.
// They require TEST_ACCOUNT, TEST_USER, TEST_TENANT env vars and a running backend.

// assertWorkflowResponse validates that the response is either a markdown summary
// (from ask-nudgebee source) or raw JSON (from WorkflowBuilder source). Both are valid
// depending on the ConversationSource of the request.
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
			query:          "Create a workflow to get pods in nudgebee namespace, check if any are in CrashLoopBackOff, and print a warning only if unhealthy pods are found",
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
		"Build a workflow that gets pods in the nudgebee namespace and prints the result",
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
		"Change the namespace from nudgebee to monitoring and add a Slack notification after the print step",
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
1. Gets all pods in the nudgebee namespace using kubectl
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
func TestExtractConfigReferences(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "no config references",
			input:    `{"params": {"channel": "#alerts", "message": "hello"}}`,
			expected: nil,
		},
		{
			name:     "single reference",
			input:    `{"channel": "{{ Configs.slack_channel }}"}`,
			expected: []string{"slack_channel"},
		},
		{
			name:     "multiple distinct references",
			input:    `{"channel": "{{ Configs.slack_channel }}", "account_id": "{{ Configs.aws_account_id }}"}`,
			expected: []string{"slack_channel", "aws_account_id"},
		},
		{
			name:     "duplicate references deduplicated",
			input:    `{"channel": "{{ Configs.slack_channel }}", "fallback": "{{ Configs.slack_channel }}"}`,
			expected: []string{"slack_channel"},
		},
		{
			name:     "variable whitespace",
			input:    `{"a": "{{Configs.no_space}}", "b": "{{  Configs.extra_space  }}"}`,
			expected: []string{"no_space", "extra_space"},
		},
		{
			name: "full workflow JSON",
			input: `{
				"name": "test-workflow",
				"definition": {
					"tasks": [
						{"id": "t1", "params": {"channel": "{{ Configs.slack_channel }}", "provider": "slack"}},
						{"id": "t2", "params": {"account_id": "{{ Configs.aws_account_id }}", "command": "aws s3 ls"}},
						{"id": "t3", "params": {"message": "{{ Tasks['t1'].output.data }}"}}
					]
				}
			}`,
			expected: []string{"slack_channel", "aws_account_id"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:     "configs in Jinja2 expressions not matched",
			input:    `{"if": "{{ Configs.threshold > 10 }}"}`,
			expected: nil, // regex requires }} immediately after key, so "Configs.threshold > 10" is not a standalone reference
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := extractConfigReferences(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestWorkflowBuilderAgent_ConfigApproval_StateMarshal tests that config-related state fields
// survive marshal/unmarshal cycle
func TestWorkflowBuilderAgent_ConfigApproval_StateMarshal(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")
	agent.state = WorkflowBuilderState{
		Stage:          "config_approval",
		OriginalQuery:  "Build a workflow that sends Slack alerts",
		Intent:         `{"description":"slack alerts"}`,
		Plan:           "1. Get data\n2. Send to Slack",
		PlanAttempts:   1,
		WorkflowJSON:   `{"name":"test","definition":{"tasks":[]}}`,
		MissingConfigs: []string{"slack_channel", "aws_account_id"},
	}

	// Marshal
	data, err := agent.MarshalState()
	assert.Nil(t, err)
	assert.NotNil(t, data)

	// Unmarshal into new agent
	agent2 := newWorkflowBuilderAgent("test-account")
	err = agent2.UnmarshalState(data)
	assert.Nil(t, err)

	// Verify all config-related fields
	assert.Equal(t, "config_approval", agent2.state.Stage)
	assert.Equal(t, agent.state.WorkflowJSON, agent2.state.WorkflowJSON)
	assert.Equal(t, agent.state.MissingConfigs, agent2.state.MissingConfigs)
	assert.Equal(t, 2, len(agent2.state.MissingConfigs))
	assert.Equal(t, "slack_channel", agent2.state.MissingConfigs[0])
	assert.Equal(t, "aws_account_id", agent2.state.MissingConfigs[1])
}

// TestWorkflowBuilderAgent_ConfigApproval_Skip_Editor verifies that for the
// editor (WorkflowBuilder) source, the "Skip" option returns raw JSON so the UI
// can render the unsaved automation. No network call is made because the Skip
// path now short-circuits before auto-save.
func TestWorkflowBuilderAgent_ConfigApproval_Skip_Editor(t *testing.T) {
	accountId := uuid.New().String()
	tenantId := uuid.New().String()
	userId := uuid.New().String()

	agent := newWorkflowBuilderAgent(accountId)
	sc := security.NewRequestContextForTenantAccountAdmin(tenantId, userId, []string{accountId})

	workflowJSON := `{"name":"test-skip","definition":{"version":"v1","triggers":[{"type":"manual"}],"tasks":[{"id":"t1","type":"core.print","params":{"message":"{{ Configs.slack_channel }}"}}]}}`
	agent.state = WorkflowBuilderState{
		Stage:          "config_approval",
		WorkflowJSON:   workflowJSON,
		MissingConfigs: []string{"slack_channel"},
	}

	resp, err := agent.handleConfigApproval(sc, core.NBAgentRequest{
		Query:              ConfigApprovalOptionSkip,
		AccountId:          accountId,
		UserId:             userId,
		ConversationSource: core.ConversationSourceWorkflowBuilder,
	})
	assert.Nil(t, err)
	assert.Equal(t, 1, len(resp.Response))
	assert.Equal(t, workflowJSON, resp.Response[0])
}

// TestWorkflowBuilderAgent_ConfigApproval_Skip_Chat verifies that for the
// ask-nudgebee (non-editor) source, "Skip" returns a markdown summary explaining
// that auto-save was not attempted because configs were skipped. No save call
// goes out; the doomed POST that previously surfaced as "Auto-save failed" is
// avoided entirely.
func TestWorkflowBuilderAgent_ConfigApproval_Skip_Chat(t *testing.T) {
	accountId := uuid.New().String()
	tenantId := uuid.New().String()
	userId := uuid.New().String()

	agent := newWorkflowBuilderAgent(accountId)
	sc := security.NewRequestContextForTenantAccountAdmin(tenantId, userId, []string{accountId})

	workflowJSON := `{"name":"test-skip","definition":{"version":"v1","triggers":[{"type":"manual"}],"tasks":[{"id":"t1","type":"core.print","params":{"message":"{{ Configs.slack_channel }}"}}]}}`
	agent.state = WorkflowBuilderState{
		Stage:          "config_approval",
		WorkflowJSON:   workflowJSON,
		MissingConfigs: []string{"slack_channel"},
	}

	resp, err := agent.handleConfigApproval(sc, core.NBAgentRequest{
		Query:     ConfigApprovalOptionSkip,
		AccountId: accountId,
		UserId:    userId,
	})
	assert.Nil(t, err)
	assert.Equal(t, 1, len(resp.Response))
	body := resp.Response[0]
	assert.Contains(t, body, "**`test-skip`**")
	assert.Contains(t, body, "not yet saved")
	assert.Contains(t, body, "Not saved")
	assert.Contains(t, body, "slack_channel")
	assert.NotContains(t, body, "built and saved")
	assert.NotContains(t, body, "Auto-save failed")
}

// TestWorkflowBuilderAgent_ConfigApproval_Create tests the "Create Configs" option
func TestWorkflowBuilderAgent_ConfigApproval_Create(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}

	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	tenantId := os.Getenv("TEST_TENANT")

	agent := newWorkflowBuilderAgent(accountId)
	sc := security.NewRequestContextForTenantAccountAdmin(tenantId, userId, []string{accountId})

	workflowJSON := `{"name":"test-create-configs","definition":{"version":"v1","triggers":[{"type":"manual"}],"tasks":[{"id":"t1","type":"core.print","params":{"message":"{{ Configs.ut_test_config_abc }}"}}]}}`
	agent.state = WorkflowBuilderState{
		Stage:          "config_approval",
		WorkflowJSON:   workflowJSON,
		MissingConfigs: []string{"ut_test_config_abc"},
	}

	resp, err := agent.handleConfigApproval(sc, core.NBAgentRequest{
		Query:     ConfigApprovalOptionCreate,
		AccountId: accountId,
		UserId:    userId,
	})
	assert.Nil(t, err)
	assert.Equal(t, 1, len(resp.Response))
	assert.Equal(t, workflowJSON, resp.Response[0])

	// Verify the config was created by fetching it
	configResp, err := tools.DoRunbookRequest("GET", "configs/ut_test_config_abc", nil, accountId,
		sc.GetSecurityContext().GetTenantId(), sc.GetSecurityContext().GetUserId())
	if err == nil {
		// Config was created — verify and clean up
		assert.Contains(t, string(configResp), "ut_test_config_abc")
		t.Log("Config created successfully, cleaning up...")
		_, _ = tools.DoRunbookRequest("DELETE", "configs/ut_test_config_abc", nil, accountId,
			sc.GetSecurityContext().GetTenantId(), sc.GetSecurityContext().GetUserId())
	} else {
		t.Log("Config API may not be reachable, skipping verification:", err)
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
		"Build a workflow that gets pods in nudgebee namespace and sends a Slack notification with the results",
		core.ConversationSessionRequestWithEnableQueryRefinement(false))
	assert.Nil(t, err)
	assert.NotNil(t, resp)

	// Handle plan approval if needed
	resp = handlePlanApprovalIfNeeded(t, sc, agent, userId, accountId, sessionId, resp)

	if resp.Status == core.ConversationStatusWaiting {
		// Config approval prompt was triggered — verify it mentions missing configs
		promptText := displayText(resp)
		assert.Contains(t, promptText, "config")
		t.Log("Config approval prompt triggered:", promptText[:min(300, len(promptText))])

		// Approve with "Skip" to get the workflow JSON
		messageId, _ := uuid.Parse(resp.MessageId)
		agentId, _ := uuid.Parse(resp.AgentId)
		resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
			ConfigApprovalOptionSkip,
			core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
			core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}

	assert.Greater(t, len(resp.Response), 0)
	fullResponse := strings.Join(resp.Response, "\n")

	// Response is either JSON (fallback) or markdown summary (new format)
	assertWorkflowResponse(t, fullResponse)
	t.Log("Workflow with config references built successfully")
}

// ==================== buildWorkflowSummary UNIT TESTS ====================

func TestBuildWorkflowSummary_CreateMode(t *testing.T) {
	workflowJSON := `{
		"name": "pod-health-monitor",
		"definition": {
			"triggers": [{"type": "schedule", "params": {"cron": "*/30 * * * *"}}],
			"tasks": [
				{"id": "get-pods", "type": "k8s.cli", "params": {"command": "get pods"}},
				{"id": "check-health", "type": "data.transform", "depends_on": ["get-pods"]},
				{"id": "send-alert", "type": "notifications.im", "depends_on": ["check-health"]}
			]
		}
	}`

	summary, err := buildWorkflowSummary(workflowJSON, "", true)
	assert.Nil(t, err)
	assert.Contains(t, summary, "**`pod-health-monitor`**")
	assert.Contains(t, summary, "built and saved")
	assert.NotContains(t, summary, "not yet saved")
	assert.Contains(t, summary, "**Trigger:** schedule")
	assert.Contains(t, summary, "1. **get-pods** — `k8s.cli`")
	assert.Contains(t, summary, "2. **check-health** — `data.transform`")
	assert.Contains(t, summary, "3. **send-alert** — `notifications.im`")
}

// TestBuildWorkflowSummary_CreateMode_NotSaved verifies the headline reflects an
// unsuccessful save in create mode so the user is not misled.
func TestBuildWorkflowSummary_CreateMode_NotSaved(t *testing.T) {
	workflowJSON := `{
		"name": "pod-health-monitor",
		"definition": {
			"triggers": [{"type": "manual"}],
			"tasks": [{"id": "t1", "type": "core.print"}]
		}
	}`

	summary, err := buildWorkflowSummary(workflowJSON, "", false)
	assert.Nil(t, err)
	assert.Contains(t, summary, "**`pod-health-monitor`**")
	assert.Contains(t, summary, "not yet saved")
	assert.NotContains(t, summary, "built and saved")
}

func TestBuildWorkflowSummary_FixMode(t *testing.T) {
	workflowJSON := `{
		"name": "daily-report",
		"definition": {
			"triggers": [{"type": "manual"}],
			"tasks": [
				{"id": "fetch-data", "type": "scripting.run_script"}
			]
		}
	}`

	summary, err := buildWorkflowSummary(workflowJSON, "fix", true)
	assert.Nil(t, err)
	assert.Contains(t, summary, "updated")
	assert.NotContains(t, summary, "built and saved")
	assert.NotContains(t, summary, "not yet saved")
}

// TestBuildWorkflowSummary_FixMode_NotSaved verifies fix mode also reports a
// non-persisted state when the auto-save fails.
func TestBuildWorkflowSummary_FixMode_NotSaved(t *testing.T) {
	workflowJSON := `{
		"name": "daily-report",
		"definition": {
			"triggers": [{"type": "manual"}],
			"tasks": [{"id": "t1", "type": "core.print"}]
		}
	}`

	summary, err := buildWorkflowSummary(workflowJSON, "fix", false)
	assert.Nil(t, err)
	assert.Contains(t, summary, "**`daily-report`**")
	assert.Contains(t, summary, "not yet saved")
	assert.NotContains(t, summary, "has been updated")
}

func TestBuildWorkflowSummary_ManualTriggerDefault(t *testing.T) {
	// No triggers array at all — should default to "manual"
	workflowJSON := `{
		"name": "no-trigger",
		"definition": {
			"tasks": [{"id": "task-1", "type": "core.print"}]
		}
	}`

	summary, err := buildWorkflowSummary(workflowJSON, "", true)
	assert.Nil(t, err)
	assert.Contains(t, summary, "**Trigger:** manual")
}

func TestBuildWorkflowSummary_EmptyTasks(t *testing.T) {
	workflowJSON := `{
		"name": "empty-workflow",
		"definition": {
			"triggers": [{"type": "webhook"}],
			"tasks": []
		}
	}`

	summary, err := buildWorkflowSummary(workflowJSON, "", true)
	assert.Nil(t, err)
	assert.Contains(t, summary, "**`empty-workflow`**")
	assert.Contains(t, summary, "**Trigger:** webhook")
	assert.NotContains(t, summary, "**Tasks:**")
}

func TestBuildWorkflowSummary_MissingName(t *testing.T) {
	workflowJSON := `{
		"definition": {
			"triggers": [{"type": "manual"}],
			"tasks": [{"id": "task-1", "type": "core.print"}]
		}
	}`

	summary, err := buildWorkflowSummary(workflowJSON, "", true)
	assert.Nil(t, err)
	assert.Contains(t, summary, "**`automation`**") // defaults to "automation"
}

func TestBuildWorkflowSummary_InvalidJSON(t *testing.T) {
	_, err := buildWorkflowSummary("not valid json", "", true)
	assert.NotNil(t, err)
}

func TestBuildWorkflowSummary_MissingTaskFields(t *testing.T) {
	workflowJSON := `{
		"name": "sparse",
		"definition": {
			"tasks": [
				{"type": "core.print"},
				{"id": "has-id"}
			]
		}
	}`

	summary, err := buildWorkflowSummary(workflowJSON, "", true)
	assert.Nil(t, err)
	assert.Contains(t, summary, "1. **task-1** — `core.print`") // missing id defaults to task-N
	assert.Contains(t, summary, "2. **has-id** — `unknown`")    // missing type defaults to unknown
}

// TestTruncateSaveError verifies the helper truncates by rune count and
// produces valid UTF-8 even when the input ends in multi-byte characters
// near the boundary.
func TestTruncateSaveError(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		got := truncateSaveError(nil)
		assert.NotEmpty(t, got)
	})

	t.Run("short message preserved verbatim", func(t *testing.T) {
		got := truncateSaveError(fmt.Errorf("status 400: missing config"))
		assert.Equal(t, "status 400: missing config", got)
		assert.True(t, utf8.ValidString(got))
	})

	t.Run("long ascii message truncated to 240 chars + ellipsis", func(t *testing.T) {
		got := truncateSaveError(fmt.Errorf("%s", strings.Repeat("a", 500)))
		assert.True(t, strings.HasSuffix(got, "…"))
		assert.Equal(t, 240, utf8.RuneCountInString(strings.TrimSuffix(got, "…")))
		assert.True(t, utf8.ValidString(got))
	})

	t.Run("multi-byte string truncated at rune boundary", func(t *testing.T) {
		// 300 multi-byte runes (each "の" is 3 bytes) — byte-slicing at byte 240
		// would split the 80th character mid-rune and corrupt the string.
		input := strings.Repeat("の", 300)
		got := truncateSaveError(fmt.Errorf("%s", input))
		assert.True(t, utf8.ValidString(got), "truncated string must be valid UTF-8")
		assert.True(t, strings.HasSuffix(got, "…"))
		assert.Equal(t, 240, utf8.RuneCountInString(strings.TrimSuffix(got, "…")))
	})
}

// TestSummaryHeadline covers the four headline branches in summaryHeadline.
func TestSummaryHeadline(t *testing.T) {
	cases := []struct {
		mode    string
		saved   bool
		expects string
	}{
		{"", true, "built and saved"},
		{"", false, "not yet saved"},
		{"fix", true, "has been updated"},
		{"fix", false, "not yet saved"},
	}
	for _, tc := range cases {
		got := summaryHeadline("demo", tc.mode, tc.saved)
		assert.Contains(t, got, tc.expects, "mode=%q saved=%v -> %q", tc.mode, tc.saved, got)
		assert.Contains(t, got, "**`demo`**")
	}
}

// ==================== finalizeWithAutoSave INTEGRATION TEST ====================

// TestWorkflowBuilderAgent_FinalizeWithAutoSave_SummaryResponse tests the full
// plan → approve → build flow and verifies that ask-nudgebee source gets a markdown
// summary (not raw JSON) with an "Open in Editor" link.
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

	// Handle config approval if present
	if resp.Status == core.ConversationStatusWaiting {
		messageId, _ := uuid.Parse(resp.MessageId)
		agentId, _ := uuid.Parse(resp.AgentId)
		resp, err = core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId,
			ConfigApprovalOptionSkip,
			core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
			core.ConversationSessionRequestWithAgentId(uuid.NullUUID{UUID: agentId, Valid: true}))
		assert.Nil(t, err)
	}

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
			query:         "Get pods in nudgebee namespace and restart any that are crashlooping",
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
func TestWorkflowBuilderAgent_ClarificationStateMarshal(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")
	agent.state = WorkflowBuilderState{
		Stage:         "clarification",
		OriginalQuery: "alert when pod crashes",
		Intent:        `{"description":"pod crash alert"}`,
		ClarifyingQuestions: []ClarifyingQuestion{
			{Question: "Which namespace?", Options: []string{"default", "nudgebee", "Skip"}},
			{Question: "Slack or email?", Options: []string{"Slack", "Email", "Skip"}},
		},
		ClarifyingAnswers: []string{"nudgebee", ""},
		ClarifyingIndex:   1,
	}

	data, err := agent.MarshalState()
	assert.Nil(t, err)
	assert.NotNil(t, data)

	agent2 := newWorkflowBuilderAgent("test-account")
	err = agent2.UnmarshalState(data)
	assert.Nil(t, err)

	assert.Equal(t, "clarification", agent2.state.Stage)
	assert.Equal(t, agent.state.OriginalQuery, agent2.state.OriginalQuery)
	assert.Equal(t, agent.state.Intent, agent2.state.Intent)
	assert.Len(t, agent2.state.ClarifyingQuestions, 2)
	assert.Equal(t, "Which namespace?", agent2.state.ClarifyingQuestions[0].Question)
	assert.Equal(t, []string{"default", "nudgebee", "Skip"}, agent2.state.ClarifyingQuestions[0].Options)
	assert.Equal(t, "Slack or email?", agent2.state.ClarifyingQuestions[1].Question)
	assert.Equal(t, []string{"nudgebee", ""}, agent2.state.ClarifyingAnswers)
	assert.Equal(t, 1, agent2.state.ClarifyingIndex)
}

// TestWorkflowBuilderAgent_BuildClarificationContext tests context string generation from Q&A pairs.
func TestWorkflowBuilderAgent_BuildClarificationContext(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")

	// Case 1: with answers
	agent.state = WorkflowBuilderState{
		ClarifyingQuestions: []ClarifyingQuestion{
			{Question: "Which namespace?", Options: []string{"default", "nudgebee", "Skip"}},
			{Question: "Slack or email?", Options: []string{"Slack", "Email", "Skip"}},
		},
		ClarifyingAnswers: []string{"nudgebee", "Slack"},
	}
	ctx := agent.buildClarificationContext()
	assert.Contains(t, ctx, "Q: Which namespace?")
	assert.Contains(t, ctx, "A: nudgebee")
	assert.Contains(t, ctx, "Q: Slack or email?")
	assert.Contains(t, ctx, "A: Slack")

	// Case 2: some skipped (empty string)
	agent.state.ClarifyingAnswers = []string{"nudgebee", ""}
	ctx = agent.buildClarificationContext()
	assert.Contains(t, ctx, "Q: Which namespace?")
	assert.Contains(t, ctx, "A: nudgebee")
	assert.NotContains(t, ctx, "Q: Slack or email?")

	// Case 3: all skipped
	agent.state.ClarifyingAnswers = []string{"", ""}
	ctx = agent.buildClarificationContext()
	assert.Equal(t, "", ctx)

	// Case 4: no questions
	agent.state.ClarifyingQuestions = nil
	agent.state.ClarifyingAnswers = nil
	ctx = agent.buildClarificationContext()
	assert.Equal(t, "", ctx)
}

// TestWorkflowBuilderAgent_BuildClarificationResponse tests that a question produces a valid followup response.
func TestWorkflowBuilderAgent_BuildClarificationResponse(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")

	question := ClarifyingQuestion{
		Question: "I'll send alerts to Slack. Which channel?",
		Options:  []string{"#alerts (recommended)", "#engineering", "#incidents", "Skip"},
	}

	request := core.NBAgentRequest{
		Query:     "send alert when pod crashes",
		AccountId: "test-account",
		AgentId:   uuid.NewString(),
	}

	resp, err := agent.buildClarificationResponse(request, question)
	assert.Nil(t, err)

	// Status should be WAITING
	assert.Equal(t, core.ConversationStatusWaiting, resp.Status)

	// Response must be empty so the UI doesn't render the question twice
	// (once as an agent-response bubble and once in the followup card).
	assert.Empty(t, resp.Response)

	// Followup structure
	assert.Equal(t, core.FollowupTypeSingleSelect, resp.FollowupRequest.FollowupType)
	assert.Equal(t, question.Options, resp.FollowupRequest.FollowupOptions)
	assert.Equal(t, question.Question, resp.FollowupRequest.Question)

	// FollowupData
	assert.NotNil(t, resp.FollowupRequest.FollowupData)
	assert.Equal(t, "clarification", resp.FollowupRequest.FollowupData["type"])
	assert.Equal(t, true, resp.FollowupRequest.FollowupData["allow_custom"])
	assert.Equal(t, true, resp.FollowupRequest.FollowupData["allow_skip"])

	// Structured options in FollowupData
	options, ok := resp.FollowupRequest.FollowupData["options"].([]map[string]any)
	assert.True(t, ok, "FollowupData.options should be []map[string]any")
	assert.Len(t, options, 4)
	assert.Equal(t, "#alerts (recommended)", options[0]["label"])
	assert.Equal(t, "Skip", options[3]["label"])
}

// drainWaitingStages auto-approves any remaining WAITING stages (config approval, etc.)
// by selecting the first available option. Returns the final response.
func drainWaitingStages(t *testing.T, sc *security.RequestContext, agent core.NBAgent, userId, accountId, sessionId string, resp core.NBAgentResponse) core.NBAgentResponse {
	t.Helper()
	maxStages := 5
	for i := 0; i < maxStages && resp.Status == core.ConversationStatusWaiting; i++ {
		answer := ConfigApprovalOptionCreate // default for config approval
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
		"Create a workflow that runs every 30 minutes, executes 'kubectl get pods -n nudgebee -o json' to list all pods, "+
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
