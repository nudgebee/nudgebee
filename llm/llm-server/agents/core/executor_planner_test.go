package core

import (
	"context"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mocks ---

type MockContextCapturingTool struct {
	NameVal      string
	CapturedCtx  string
	ReturnOutput string
	ReturnStatus toolcore.NBToolResponseStatus
}

func (m *MockContextCapturingTool) Name() string {
	return m.NameVal
}

func (m *MockContextCapturingTool) Description() string {
	return "Mock tool"
}

func (m *MockContextCapturingTool) GetType() toolcore.NBToolType {
	return toolcore.NBToolTypeTool
}

func (m *MockContextCapturingTool) Call(ctx toolcore.NbToolContext, request toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	m.CapturedCtx = ctx.QueryContext
	return toolcore.NBToolResponse{
		Data:   m.ReturnOutput,
		Status: m.ReturnStatus,
	}, nil
}

func (m *MockContextCapturingTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{}
}

// MockAgent implements NBAgent
type MockAgent struct {
	SupportedTools []toolcore.NBTool
}

func (m *MockAgent) GetName() string          { return "mock_agent" }
func (m *MockAgent) GetNameAliases() []string { return []string{} }
func (m *MockAgent) GetDescription() string   { return "mock description" }
func (m *MockAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return m.SupportedTools
}
func (m *MockAgent) GetSystemPrompt(ctx *security.RequestContext, query NBAgentRequest) NBAgentPrompt {
	return NBAgentPrompt{}
}
func (m *MockAgent) GetPlannerType() AgentPlannerType { return AgentPlannerTypeReWoo }

// --- New Tests ---

func TestDoIterationSequential_ContextBuilding(t *testing.T) {
	// Setup tools
	tool1 := &MockContextCapturingTool{NameVal: "TOOL1", ReturnOutput: "Output1", ReturnStatus: toolcore.NBToolResponseStatusSuccess}
	tool2 := &MockContextCapturingTool{NameVal: "TOOL2", ReturnOutput: "Output2", ReturnStatus: toolcore.NBToolResponseStatusSuccess}
	nameToTool := map[string]toolcore.NBTool{
		"TOOL1": tool1,
		"TOOL2": tool2,
	}

	// Setup dependencies
	ctx := security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", []string{"cccccccc-cccc-cccc-cccc-cccccccccccc"})
	mockAgent := &MockAgent{SupportedTools: []toolcore.NBTool{tool1, tool2}}

	agentRequest := NBAgentRequest{
		AccountId:      "cccccccc-cccc-cccc-cccc-cccccccccccc",
		UserId:         "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		ConversationId: "conv_id",
		MessageId:      "msg_id",
		AgentId:        "agent_id",
	}

	executor := &plannerExecutor{
		ctx:           ctx,
		agent:         mockAgent,
		agentRequest:  agentRequest,
		toolCallCache: turnToolCallCache{cache: make(map[string]NBAgentPlannerToolActionStep)},
	}

	// Setup previous steps (simulating history)
	prevSteps := []NBAgentPlannerToolActionStep{
		{
			Action:      NBAgentPlannerToolAction{ToolID: "Plan1", Tool: "TOOL1", ToolInput: "Input1"},
			Observation: "Output1",
			Status:      ToolStatusSuccess,
		},
	}

	// Setup actions for this iteration
	// Action 2 depends on Plan1.
	actions := []NBAgentPlannerToolAction{
		{
			ToolID:     "Plan2",
			Tool:       "TOOL2",
			ToolInput:  "Input2",
			Dependency: []string{"Plan1"},
		},
	}

	// Execute
	_, _, err := executor.doIterationSequential(prevSteps, nameToTool, actions)
	assert.NoError(t, err)

	// Verify context captured by Tool2
	expectedPart := "\n#PlanId: Plan1\n#ToolName: TOOL1\n#Question: Input1\n#Answer: Output1\n"
	assert.Contains(t, tool2.CapturedCtx, expectedPart, "Sequential execution context should contain properly formatted previous step")
}

func TestDoIterationParallel_ContextBuilding(t *testing.T) {
	// Setup tools
	tool1 := &MockContextCapturingTool{NameVal: "TOOL1", ReturnOutput: "Output1", ReturnStatus: toolcore.NBToolResponseStatusSuccess}
	tool2 := &MockContextCapturingTool{NameVal: "TOOL2", ReturnOutput: "Output2", ReturnStatus: toolcore.NBToolResponseStatusSuccess}
	nameToTool := map[string]toolcore.NBTool{
		"TOOL1": tool1,
		"TOOL2": tool2,
	}

	// Setup dependencies
	ctx := security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", []string{"cccccccc-cccc-cccc-cccc-cccccccccccc"})
	mockAgent := &MockAgent{SupportedTools: []toolcore.NBTool{tool1, tool2}}
	agentRequest := NBAgentRequest{
		AccountId:      "cccccccc-cccc-cccc-cccc-cccccccccccc",
		UserId:         "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		ConversationId: "conv_id",
		MessageId:      "msg_id",
		AgentId:        "agent_id",
	}

	executor := &plannerExecutor{
		ctx:             ctx,
		agent:           mockAgent,
		agentRequest:    agentRequest,
		summaryToolName: "TOOL2", // Bypass rewriteToolInput for TOOL2
		toolCallCache:   turnToolCallCache{cache: make(map[string]NBAgentPlannerToolActionStep)},
	}

	// Setup actions
	// Plan1: Independent
	// Plan2: Depends on Plan1
	actions := []NBAgentPlannerToolAction{
		{
			ToolID:     "Plan1",
			Tool:       "TOOL1",
			ToolInput:  "Input1",
			Dependency: []string{},
		},
		{
			ToolID:     "Plan2",
			Tool:       "TOOL2",
			ToolInput:  "Input2",
			Dependency: []string{"Plan1"},
		},
	}

	// Execute
	// We need a timeout because it uses goroutines
	done := make(chan struct{})
	go func() {
		_, _, err := executor.doIterationParallel(context.Background(), nil, nameToTool, actions)
		assert.NoError(t, err)
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(5 * time.Second):
		t.Fatal("doIterationParallel timed out")
	}

	// Verify
	expectedPart := "\n#PlanId: Plan1\n#ToolName: TOOL1\n#Question: Input1\n#Answer: Output1\n"
	assert.Contains(t, tool2.CapturedCtx, expectedPart, "Parallel execution context for dependent tool should contain dependency result")
}

func TestDoIterationParallel_SelfDependency(t *testing.T) {
	// Setup tools
	tool1 := &MockContextCapturingTool{NameVal: "TOOL1", ReturnOutput: "Output1", ReturnStatus: toolcore.NBToolResponseStatusSuccess}
	nameToTool := map[string]toolcore.NBTool{
		"TOOL1": tool1,
	}

	// Setup dependencies
	ctx := security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", []string{"cccccccc-cccc-cccc-cccc-cccccccccccc"})
	mockAgent := &MockAgent{SupportedTools: []toolcore.NBTool{tool1}}
	agentRequest := NBAgentRequest{
		AccountId:      "cccccccc-cccc-cccc-cccc-cccccccccccc",
		UserId:         "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		ConversationId: "conv_id",
		MessageId:      "msg_id",
		AgentId:        "agent_id",
	}

	executor := &plannerExecutor{
		ctx:             ctx,
		agent:           mockAgent,
		agentRequest:    agentRequest,
		summaryToolName: "TOOL1", // Bypass rewriteToolInput
		toolCallCache:   turnToolCallCache{cache: make(map[string]NBAgentPlannerToolActionStep)},
	}

	// Setup action with SELF DEPENDENCY
	actions := []NBAgentPlannerToolAction{
		{
			ToolID:     "Plan1",
			Tool:       "TOOL1",
			ToolInput:  "Input1",
			Dependency: []string{"Plan1"}, // Depends on itself!
		},
	}

	// Execute
	done := make(chan struct{})
	var steps []NBAgentPlannerToolActionStep
	var err error
	go func() {
		steps, _, err = executor.doIterationParallel(context.Background(), nil, nameToTool, actions)
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(5 * time.Second):
		t.Fatal("doIterationParallel timed out (likely deadlock)")
	}

	assert.NoError(t, err)
	assert.Len(t, steps, 1)
	assert.Equal(t, "Plan1", steps[0].Action.ToolID)
	assert.Equal(t, ToolStatusSuccess, steps[0].Status)
}

// TestDoIterationParallel_MultipleClientToolsAggregated verifies that when a
// parallel batch dispatches N ClientToolWrappers — each returning
// WAITING_FOR_CLIENT_TOOL with single-tool AdditionalDetails — the executor
// aggregates ALL of them into AdditionalDetails["client_tools"]. Without this,
// only waitingFollowups[0]'s tool surfaces to chat_get and the rest are
// silently dropped, breaking parallel emission for client-tool agents (tbench).
func TestDoIterationParallel_MultipleClientToolsAggregated(t *testing.T) {
	clientTools := []toolcore.NBToolCommand{
		{Name: "shell_a", Description: "shell a", InputSchema: toolcore.ToolSchema{}},
		{Name: "shell_b", Description: "shell b", InputSchema: toolcore.ToolSchema{}},
		{Name: "shell_c", Description: "shell c", InputSchema: toolcore.ToolSchema{}},
	}

	ctx := security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", []string{"cccccccc-cccc-cccc-cccc-cccccccccccc"})
	mockAgent := &MockAgent{SupportedTools: []toolcore.NBTool{}} // client tools resolve via agentRequest.ClientTools fallback

	agentRequest := NBAgentRequest{
		AccountId:      "cccccccc-cccc-cccc-cccc-cccccccccccc",
		UserId:         "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		ConversationId: "conv_id",
		MessageId:      "msg_id",
		AgentId:        "agent_id",
		ClientTools:    clientTools,
	}

	executor := &plannerExecutor{
		ctx:           ctx,
		agent:         mockAgent,
		agentRequest:  agentRequest,
		toolCallCache: turnToolCallCache{cache: make(map[string]NBAgentPlannerToolActionStep)},
	}

	actions := []NBAgentPlannerToolAction{
		{ToolID: "E1", Tool: "shell_a", ToolInput: `{"command":"ls /tmp"}`},
		{ToolID: "E2", Tool: "shell_b", ToolInput: `{"command":"cat /etc/os-release"}`},
		{ToolID: "E3", Tool: "shell_c", ToolInput: `{"command":"uname -a"}`},
	}

	done := make(chan struct{})
	var steps []NBAgentPlannerToolActionStep
	var finish *NBAgentPlannerFinishAction
	var err error
	go func() {
		steps, finish, err = executor.doIterationParallel(context.Background(), nil, map[string]toolcore.NBTool{}, actions)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("doIterationParallel timed out")
	}

	assert.NoError(t, err)
	assert.NotNil(t, finish, "expected a finish action since all actions are client tools")
	assert.Equal(t, ConversationStatusWaitingForClientTool, finish.Status)

	// All three nodes ran and produced WaitingForClient steps in newStepsThisIteration.
	assert.Len(t, steps, 3)
	for _, s := range steps {
		assert.Equal(t, ToolStatusWaitingForClient, s.Status)
	}

	// Aggregation: every dispatched client-tool call is surfaced via client_tools.
	rawTools, ok := finish.AdditionalDetails["client_tools"]
	assert.True(t, ok, "AdditionalDetails['client_tools'] should be set when >1 client tools wait")
	tools, ok := rawTools.([]any)
	assert.True(t, ok, "client_tools should be []any")
	assert.Len(t, tools, 3)

	seenIds := map[string]bool{}
	for _, raw := range tools {
		tc, ok := raw.(map[string]any)
		assert.True(t, ok)
		seenIds[tc["tool_id"].(string)] = true
		assert.NotEmpty(t, tc["tool_name"])
		assert.NotEmpty(t, tc["tool_input"])
	}
	assert.True(t, seenIds["E1"] && seenIds["E2"] && seenIds["E3"], "all three tool_ids should be present")

	// Resume state preserves all N actions so the planner re-enters them after
	// the client returns results. Without this, siblings are re-planned by the
	// LLM on resume (token waste, plan drift).
	assert.Len(t, executor.currentAction, 3)
}

// TestDoIterationParallel_SameClientToolMultipleCalls verifies aggregation
// fires even when N>1 actions target the SAME client tool with different
// inputs. tbench-style workloads frequently emit "shell_execute /tmp/A" +
// "shell_execute /tmp/B" in one parallel turn; both must surface to the
// adapter, not just the first.
func TestDoIterationParallel_SameClientToolMultipleCalls(t *testing.T) {
	clientTools := []toolcore.NBToolCommand{
		{Name: "shell_only", Description: "shell only", InputSchema: toolcore.ToolSchema{}},
	}
	ctx := security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", []string{"cccccccc-cccc-cccc-cccc-cccccccccccc"})
	mockAgent := &MockAgent{SupportedTools: []toolcore.NBTool{}}
	agentRequest := NBAgentRequest{
		AccountId:      "cccccccc-cccc-cccc-cccc-cccccccccccc",
		UserId:         "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		ConversationId: "conv_id",
		MessageId:      "msg_id",
		AgentId:        "agent_id",
		ClientTools:    clientTools,
	}
	executor := &plannerExecutor{
		ctx:           ctx,
		agent:         mockAgent,
		agentRequest:  agentRequest,
		toolCallCache: turnToolCallCache{cache: make(map[string]NBAgentPlannerToolActionStep)},
	}

	actions := []NBAgentPlannerToolAction{
		{ToolID: "E1", Tool: "shell_only", ToolInput: `{"command":"echo hi"}`},
		{ToolID: "E2", Tool: "shell_only", ToolInput: `{"command":"echo bye"}`},
	}

	done := make(chan struct{})
	var finish *NBAgentPlannerFinishAction
	go func() {
		_, finish, _ = executor.doIterationParallel(context.Background(), nil, map[string]toolcore.NBTool{}, actions)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("doIterationParallel timed out")
	}

	// Two parallel calls of the same client tool — both should aggregate.
	assert.NotNil(t, finish)
	assert.Equal(t, ConversationStatusWaitingForClientTool, finish.Status)
	rawTools, ok := finish.AdditionalDetails["client_tools"]
	assert.True(t, ok)
	tools := rawTools.([]any)
	assert.Len(t, tools, 2)
}

// MockDelayingTool sleeps before returning, simulating a slow LLM/relay call.
// Used to exercise the cleanup-while-workers-still-running race.
type MockDelayingTool struct {
	NameVal      string
	Delay        time.Duration
	ReturnOutput string
}

func (m *MockDelayingTool) Name() string                 { return m.NameVal }
func (m *MockDelayingTool) Description() string          { return "Delaying mock tool" }
func (m *MockDelayingTool) GetType() toolcore.NBToolType { return toolcore.NBToolTypeTool }
func (m *MockDelayingTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{}
}
func (m *MockDelayingTool) Call(ctx toolcore.NbToolContext, request toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	time.Sleep(m.Delay)
	return toolcore.NBToolResponse{
		Data:   m.ReturnOutput,
		Status: toolcore.NBToolResponseStatusSuccess,
	}, nil
}

// TestDoIterationParallel_NoRaceOnContextCancel verifies that cancelling the
// parent context while workers are still in flight does not panic on
// channel-send-after-close (the race that the previous safeResultSend/recover()
// helper papered over). With the producerWg fix, cleanup() waits for all
// workers before closing resultsChan, so plain channel sends are panic-safe.
//
// Run with -race for full effect.
func TestDoIterationParallel_NoRaceOnContextCancel(t *testing.T) {
	const numActions = 8
	tools := make(map[string]toolcore.NBTool, numActions)
	supported := make([]toolcore.NBTool, 0, numActions)
	actions := make([]NBAgentPlannerToolAction, 0, numActions)
	for i := 0; i < numActions; i++ {
		name := "TOOL" + string(rune('A'+i))
		tool := &MockDelayingTool{
			NameVal:      name,
			Delay:        50 * time.Millisecond,
			ReturnOutput: "ok-" + name,
		}
		tools[name] = tool
		supported = append(supported, tool)
		actions = append(actions, NBAgentPlannerToolAction{
			ToolID:    "Plan" + string(rune('A'+i)),
			Tool:      name,
			ToolInput: "input",
		})
	}

	ctx := security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", []string{"cccccccc-cccc-cccc-cccc-cccccccccccc"})
	mockAgent := &MockAgent{SupportedTools: supported}
	agentRequest := NBAgentRequest{
		AccountId:      "cccccccc-cccc-cccc-cccc-cccccccccccc",
		UserId:         "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		ConversationId: "conv_id",
		MessageId:      "msg_id",
		AgentId:        "agent_id",
	}
	executor := &plannerExecutor{
		ctx:           ctx,
		agent:         mockAgent,
		agentRequest:  agentRequest,
		toolCallCache: turnToolCallCache{cache: make(map[string]NBAgentPlannerToolActionStep)},
	}

	// Cancel partway through to force cleanup() while goroutines are still running.
	parentCtx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	done := make(chan struct{})
	go func() {
		// We don't assert on err — the cancel will produce ctx.Err(). The point
		// of the test is that this returns at all (no panic, no deadlock).
		_, _, _ = executor.doIterationParallel(parentCtx, nil, tools, actions)
		close(done)
	}()

	select {
	case <-done:
		// success — function returned cleanly
	case <-time.After(5 * time.Second):
		t.Fatal("doIterationParallel deadlocked after context cancel")
	}
}

// --- Original Tests (Restored) ---

func TestEvaluateConditionsConditionLLM(t *testing.T) {
	t.Skip("Skipping long running test")
	// Setup mock plannerExecutor dependencies
	ctx := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")})
	llmAgent := LLMAgent{}
	agentRequest := NBAgentRequest{
		AccountId: "cccccccc-cccc-cccc-cccc-cccccccccccc",
	}
	// Create a minimal plannerExecutor instance
	executor := &plannerExecutor{
		ctx:           ctx,
		agent:         llmAgent,
		agentRequest:  agentRequest,
		toolCallCache: turnToolCallCache{cache: make(map[string]NBAgentPlannerToolActionStep)},
	}

	testCases := []struct {
		name            string
		action          NBAgentPlannerToolAction
		availableSteps  []NBAgentPlannerToolActionStep
		expectedExecute bool
		expectError     bool
		errorSubstring  string
	}{
		{
			name: "LLM Condition True",
			action: NBAgentPlannerToolAction{
				ToolID: "action1",
				Tool:   "tool1",
				Condition: NBAgentPlannerToolActionCondition{
					Prompt:           `"{{input_data}} \nIs this true?`,
					ExpectedResponse: "true",
				},
				Dependency: []string{"action2"},
			},
			availableSteps: []NBAgentPlannerToolActionStep{
				{Action: NBAgentPlannerToolAction{ToolID: "action2", Tool: "tool2"}, Observation: "Sky is blue."},
			},
			expectedExecute: true,
			expectError:     false,
		},
		{
			name: "LLM Condition False (Response Mismatch)",
			action: NBAgentPlannerToolAction{
				ToolID: "action1",
				Tool:   "tool1",
				Condition: NBAgentPlannerToolActionCondition{
					Prompt:           "{{input_data}} \n Is this true?",
					ExpectedResponse: "true",
				},
				Dependency: []string{"action2"},
			},
			availableSteps: []NBAgentPlannerToolActionStep{
				{Action: NBAgentPlannerToolAction{ToolID: "action2", Tool: "tool2"}, Observation: "Sky is Red."},
			},
			expectedExecute: false,
			expectError:     false,
		},
		{
			name: "LLM Condition with Input Data (String)",
			action: NBAgentPlannerToolAction{
				ToolID:     "action2",
				Tool:       "tool2",
				Dependency: []string{"dep1"},
				Condition: NBAgentPlannerToolActionCondition{
					Prompt:           "Analyze: '{{input_data}}'. Is it positive?",
					ExpectedResponse: "true",
				},
			},
			availableSteps: []NBAgentPlannerToolActionStep{
				{Action: NBAgentPlannerToolAction{ToolID: "dep1", Tool: "dep_tool"}, Observation: "vaue = 1000"},
			},
			expectedExecute: true,
			expectError:     false,
		},
		{
			name: "LLM Condition with Input Data (JSON)",
			action: NBAgentPlannerToolAction{
				ToolID:     "action3",
				Tool:       "tool3",
				Dependency: []string{"dep2"},
				Condition: NBAgentPlannerToolActionCondition{
					Prompt:           "Analyze JSON: '{{input_data}}'. \n\nIs status success?",
					ExpectedResponse: "true",
				},
			},
			availableSteps: []NBAgentPlannerToolActionStep{
				{Action: NBAgentPlannerToolAction{ToolID: "dep2", Tool: "dep_tool"}, Observation: `{"status": "success", "value": 100}`},
			},
			expectedExecute: true,
			expectError:     false,
		},
		{
			name: "LLM Condition with Input Data (Multiple Dependencies)",
			action: NBAgentPlannerToolAction{
				ToolID:     "action4",
				Tool:       "tool4",
				Dependency: []string{"dep3", "dep4"},
				Condition: NBAgentPlannerToolActionCondition{
					Prompt:           "Analyze data: '{{input_data}}'. Is the combined status good?",
					ExpectedResponse: "true",
				},
			},
			availableSteps: []NBAgentPlannerToolActionStep{
				{Action: NBAgentPlannerToolAction{ToolID: "dep3", Tool: "dep_tool_3"}, Observation: `{"status": "ok"}`},
				{Action: NBAgentPlannerToolAction{ToolID: "dep4", Tool: "dep_tool_4"}, Observation: "All checks passed."},
			},
			expectedExecute: true,
			expectError:     false,
		},
		{
			name: "LLM Condition Fails (LLM Error)",
			action: NBAgentPlannerToolAction{
				ToolID: "action5",
				Tool:   "tool5",
				Condition: NBAgentPlannerToolActionCondition{
					Prompt:           "Cause an error",
					ExpectedResponse: "false",
				},
			},
			availableSteps:  []NBAgentPlannerToolActionStep{},
			expectedExecute: false,
			expectError:     true,
			errorSubstring:  "LLM condition call failed",
		},
		{
			name: "LLM Condition Fails (Empty LLM Response)",
			action: NBAgentPlannerToolAction{
				ToolID: "action6",
				Tool:   "tool6",
				Condition: NBAgentPlannerToolActionCondition{
					Prompt:           "Return empty",
					ExpectedResponse: "false",
				},
			},
			availableSteps:  []NBAgentPlannerToolActionStep{},
			expectedExecute: true,
			expectError:     false,
			errorSubstring:  "LLM condition call failed",
		},
		{
			name: "LLM Condition with Dependency Not Found",
			action: NBAgentPlannerToolAction{
				ToolID:     "action7",
				Tool:       "tool7",
				Dependency: []string{"missing_dep"},
				Condition: NBAgentPlannerToolActionCondition{
					Prompt:           "Analyze: '{{input_data}}'. Is it good?",
					ExpectedResponse: "false",
				},
			},
			availableSteps:  []NBAgentPlannerToolActionStep{},
			expectedExecute: false,
			expectError:     false,
		},
		{
			name: "LLM Condition with Dependency Not Found (Expected True)",
			action: NBAgentPlannerToolAction{
				ToolID:     "action8",
				Tool:       "tool8",
				Dependency: []string{"missing_dep_2"},
				Condition: NBAgentPlannerToolActionCondition{
					Prompt:           "Analyze: '{{input_data}}'. Is it good?",
					ExpectedResponse: "false",
				},
			},
			availableSteps:  []NBAgentPlannerToolActionStep{},
			expectedExecute: true,
			expectError:     false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			shouldExecute, err := executor.evaluateConditions(tc.action, tc.availableSteps)

			if tc.expectError {
				assert.Error(t, err)
				if err != nil && tc.errorSubstring != "" {
					assert.Contains(t, err.Error(), tc.errorSubstring)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedExecute, shouldExecute)
			}
		})
	}
}

func TestDoActionToolNotFound(t *testing.T) {
	t.Skip("Skipping test due to missing DB environment causing panic")
	const toolNotFoundMessage = "Tool not found"
	// Setup test environment
	ctx := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")})
	llmAgent := LLMAgent{}
	agentRequest := NBAgentRequest{
		AccountId:      os.Getenv("TEST_ACCOUNT"),
		UserId:         os.Getenv("TEST_USER"),
		ConversationId: "68845d1d-aaed-469d-a261-ef0fa7676d7c",
		MessageId:      "f29b37dc-dca7-45ef-9b28-b6d004d1c871",
		AgentId:        "tickets",
		QueryContext:   "test context",
		QueryConfig:    toolcore.NBQueryConfig{},
	}

	// Create executor instance
	executor := &plannerExecutor{
		ctx:           ctx,
		agent:         llmAgent,
		agentRequest:  agentRequest,
		toolCallCache: turnToolCallCache{cache: make(map[string]NBAgentPlannerToolActionStep)},
	}

	testCases := []struct {
		name                string
		nameToTool          map[string]toolcore.NBTool
		action              NBAgentPlannerToolAction
		queryContext        string
		expectedToolID      string
		expectedTool        string
		expectedObservation string
	}{
		{
			name:       "Tool not found",
			nameToTool: map[string]toolcore.NBTool{},
			action: NBAgentPlannerToolAction{
				ToolID:    "test-tool-id-1",
				Tool:      "NONEXISTENT_TOOL",
				ToolInput: "test input",
				Log:       "test log",
			},
			queryContext:        "",
			expectedToolID:      "test-tool-id-1",
			expectedTool:        "NONEXISTENT_TOOL",
			expectedObservation: toolNotFoundMessage,
		},
		{
			name:       "ticket_master",
			nameToTool: map[string]toolcore.NBTool{},
			action: NBAgentPlannerToolAction{
				ToolID:    "ticket_master-1749643888403447000",
				Tool:      "ticket_master",
				ToolInput: "{\"command\":\"{\\\"operation_type\\\": \\\"search\\\", \\\"query\\\": \\\"status = 'To Do' ORDER BY created ASC\\\"}\",\"args\":null,\"context\":\"\"}",
				Log:       "Thought: The user wants to retrieve a list of 20 tickets with 'To Do' status from Jira. I should use the ticket_master tool with the 'search' operation type and construct a JQL query to find these tickets.\nAction: ticket_master\nAction Input: {\"operation_type\": \"search\", \"query\": \"status = 'To Do' ORDER BY created ASC\"}",
			},
			queryContext:        "",
			expectedToolID:      "ticket_master-1749643888403447000",
			expectedTool:        "ticket_master",
			expectedObservation: toolNotFoundMessage,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			step, finish, err := executor.doAction(tc.nameToTool, tc.action, tc.queryContext)

			// Should not return an error for tool not found
			assert.NoError(t, err)

			// Should not have a finish action
			assert.Nil(t, finish)

			// Should return a step with the correct action details
			assert.Equal(t, tc.expectedToolID, step.Action.ToolID)
			assert.Equal(t, tc.expectedTool, step.Action.Tool)
			assert.Equal(t, tc.action.ToolInput, step.Action.ToolInput)
			assert.Equal(t, tc.action.Log, step.Action.Log)

			// Should have "Tool not found" observation
			assert.Contains(t, step.Observation, tc.expectedObservation)
		})
	}
}

func TestIsInvalidToolName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"empty string", "", true},
		{"whitespace only", "   ", true},
		{"three dots", "...", true},
		{"two dots", "..", true},
		{"single dot", ".", true},
		{"unicode ellipsis", "\u2026", true},
		{"dots with spaces", " ... ", true},
		{"valid tool name", "events_execute", false},
		{"valid with dots", "my.tool.name", false},
		{"valid LLM", "LLM", false},
		{"valid kubectl", "kubectl_execute", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, isInvalidToolName(tc.input))
		})
	}
}

// mockToolWithAliases is a test tool that implements GetNameAliases.
type mockToolWithAliases struct {
	name    string
	aliases []string
}

func (m mockToolWithAliases) Name() string        { return m.name }
func (m mockToolWithAliases) Description() string { return "test tool" }
func (m mockToolWithAliases) Call(_ toolcore.NbToolContext, _ toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	return toolcore.NBToolResponse{}, nil
}
func (m mockToolWithAliases) GetType() toolcore.NBToolType { return toolcore.NBToolTypeTool }
func (m mockToolWithAliases) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{}
}
func (m mockToolWithAliases) GetNameAliases() []string { return m.aliases }

func TestGetNameToToolIncludesAliases(t *testing.T) {
	tools := []toolcore.NBTool{
		mockToolWithAliases{name: "kubectl_execute", aliases: []string{"kubectl"}},
		mockToolWithAliases{name: "events_execute", aliases: []string{"events", "Events Execute"}},
	}

	nameToTool := getNameToTool(tools)

	// Canonical names (uppercased)
	require.NotNil(t, nameToTool["KUBECTL_EXECUTE"])
	require.NotNil(t, nameToTool["EVENTS_EXECUTE"])

	// Aliases (uppercased) — require so a missing entry fails the test
	// cleanly instead of NPE-panicking on the .Name() calls below.
	require.NotNil(t, nameToTool["KUBECTL"])
	require.NotNil(t, nameToTool["EVENTS"])
	require.NotNil(t, nameToTool["EVENTS EXECUTE"])

	// Alias resolves to the correct tool
	assert.Equal(t, "kubectl_execute", nameToTool["KUBECTL"].Name())
	assert.Equal(t, "events_execute", nameToTool["EVENTS"].Name())
}

func TestDoActionClientTool(t *testing.T) {
	// Setup test environment
	ctx := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{os.Getenv("TEST_ACCOUNT")})
	llmAgent := LLMAgent{}
	agentRequest := NBAgentRequest{
		AccountId:      "test-account",
		UserId:         "test-user",
		ConversationId: "68845d1d-aaed-469d-a261-ef0fa7676d7c",
		MessageId:      "f29b37dc-dca7-45ef-9b28-b6d004d1c871",
		AgentId:        "test-agent",
		ClientTools: []toolcore.NBToolCommand{
			{
				Name: "local_shell",
			},
		},
	}

	// Create executor instance
	executor := &plannerExecutor{
		ctx:           ctx,
		agent:         llmAgent,
		agentRequest:  agentRequest,
		toolCallCache: turnToolCallCache{cache: make(map[string]NBAgentPlannerToolActionStep)},
	}

	action := NBAgentPlannerToolAction{
		ToolID:    "test-tool-id-1",
		Tool:      "local_shell",
		ToolInput: "ls",
	}

	_, finish, err := executor.doAction(map[string]toolcore.NBTool{}, action, "")

	assert.NoError(t, err)
	assert.NotNil(t, finish)
	assert.Equal(t, ConversationStatusWaitingForClientTool, finish.Status)
	assert.Equal(t, "local_shell", finish.AdditionalDetails["tool_name"])
	assert.Equal(t, "ls", finish.AdditionalDetails["tool_input"])
	assert.Equal(t, "test-tool-id-1", finish.AdditionalDetails["tool_id"])
}

// --- resolveMaxIterations tests ---

// MockIterAgent implements NBAgent + NBAgentIterationProvider
type MockIterAgent struct {
	MockAgent
	maxIterations int
}

func (m *MockIterAgent) GetMaxIterations() int { return m.maxIterations }

func TestResolveMaxIterations(t *testing.T) {
	// Save original config and restore after test
	origReAct := config.Config.LLMServerAgentReActMaxIterations
	origSubAgent := config.Config.LLMServerAgentReActSubAgentMaxIterations
	defer func() {
		config.Config.LLMServerAgentReActMaxIterations = origReAct
		config.Config.LLMServerAgentReActSubAgentMaxIterations = origSubAgent
	}()

	tests := []struct {
		name           string
		globalMax      int
		subAgentMax    int
		agentMaxIter   int // 0 means agent doesn't implement NBAgentIterationProvider
		parentAgentId  string
		agentId        string
		expectedResult int
	}{
		{
			name:           "top-level agent uses global config",
			globalMax:      50,
			subAgentMax:    10,
			parentAgentId:  "agent-1",
			agentId:        "agent-1", // same = top-level
			expectedResult: 50,
		},
		{
			name:           "sub-agent capped by sub-agent config",
			globalMax:      50,
			subAgentMax:    10,
			parentAgentId:  "parent-1",
			agentId:        "child-1", // different = sub-agent
			expectedResult: 10,
		},
		{
			name:           "sub-agent with own lower cap wins over sub-agent config",
			globalMax:      50,
			subAgentMax:    10,
			agentMaxIter:   4, // agent's own cap (e.g. promql)
			parentAgentId:  "parent-1",
			agentId:        "child-1",
			expectedResult: 4,
		},
		{
			name:           "sub-agent with own higher cap still capped by sub-agent config",
			globalMax:      50,
			subAgentMax:    10,
			agentMaxIter:   15, // agent's own cap is higher than sub-agent config
			parentAgentId:  "parent-1",
			agentId:        "child-1",
			expectedResult: 10,
		},
		{
			name:           "top-level agent with own cap uses agent cap",
			globalMax:      50,
			subAgentMax:    10,
			agentMaxIter:   8,
			parentAgentId:  "agent-1",
			agentId:        "agent-1",
			expectedResult: 8,
		},
		{
			name:           "sub-agent with empty parent ID treated as top-level",
			globalMax:      50,
			subAgentMax:    10,
			parentAgentId:  "",
			agentId:        "agent-1",
			expectedResult: 50,
		},
		{
			name:           "sub-agent with empty agent ID still capped (DB save failure case)",
			globalMax:      50,
			subAgentMax:    10,
			parentAgentId:  "parent-1",
			agentId:        "",
			expectedResult: 10,
		},
		{
			name:           "sub-agent config of 0 means no cap applied",
			globalMax:      50,
			subAgentMax:    0,
			parentAgentId:  "parent-1",
			agentId:        "child-1",
			expectedResult: 50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config.Config.LLMServerAgentReActMaxIterations = tt.globalMax
			config.Config.LLMServerAgentReActSubAgentMaxIterations = tt.subAgentMax

			var agent NBAgent
			if tt.agentMaxIter > 0 {
				agent = &MockIterAgent{maxIterations: tt.agentMaxIter}
			} else {
				agent = &MockAgent{}
			}

			request := NBAgentRequest{
				ParentAgentId: tt.parentAgentId,
				AgentId:       tt.agentId,
			}

			result := resolveMaxIterations(agent, request)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestTruncateToolResponse(t *testing.T) {
	origOut := config.Config.LlmServerMaxToolOutputLen
	origErr := config.Config.LlmServerMaxToolErrorOutputLen
	defer func() {
		config.Config.LlmServerMaxToolOutputLen = origOut
		config.Config.LlmServerMaxToolErrorOutputLen = origErr
	}()

	config.Config.LlmServerMaxToolOutputLen = 30000
	config.Config.LlmServerMaxToolErrorOutputLen = 10000

	t.Run("success under limit returns unchanged", func(t *testing.T) {
		data := "small observation"
		got := truncateToolResponse(nil, data, ToolStatusSuccess, "tool")
		assert.Equal(t, data, got)
	})

	t.Run("success over limit gets truncated", func(t *testing.T) {
		data := strings.Repeat("x", 50000)
		got := truncateToolResponse(nil, data, ToolStatusSuccess, "tool")
		assert.Less(t, len(got), len(data))
		assert.LessOrEqual(t, len(got), 30500) // cap + truncation marker overhead
		assert.Contains(t, got, "TRUNCATED")
	})

	t.Run("failure over limit uses smaller error cap", func(t *testing.T) {
		data := strings.Repeat("stack trace line\n", 2000)
		got := truncateToolResponse(nil, data, ToolStatusFailure, "tool")
		assert.LessOrEqual(t, len(got), 10500)
		assert.Contains(t, got, "TRUNCATED")
	})

	t.Run("empty sentinel values are never truncated", func(t *testing.T) {
		assert.Equal(t, "", truncateToolResponse(nil, "", ToolStatusSuccess, "tool"))
		assert.Equal(t, plannerToolNoData, truncateToolResponse(nil, plannerToolNoData, ToolStatusSuccess, "tool"))
		assert.Equal(t, "[]", truncateToolResponse(nil, "[]", ToolStatusSuccess, "tool"))
	})

	t.Run("limit of zero disables truncation", func(t *testing.T) {
		config.Config.LlmServerMaxToolOutputLen = 0
		data := strings.Repeat("x", 100000)
		got := truncateToolResponse(nil, data, ToolStatusSuccess, "tool")
		assert.Equal(t, data, got)
		config.Config.LlmServerMaxToolOutputLen = 30000
	})

	t.Run("preserves head and tail of the data", func(t *testing.T) {
		data := "HEADER_START" + strings.Repeat("x", 50000) + "FOOTER_END"
		got := truncateToolResponse(nil, data, ToolStatusSuccess, "tool")
		assert.True(t, strings.HasPrefix(got, "HEADER_START"))
		assert.True(t, strings.HasSuffix(got, "FOOTER_END"))
	})
}

// mockNBAgentPlanner is a minimal NBAgentPlanner with no-op Marshal/Unmarshal,
// used to exercise plannerExecutor.Marshal/Unmarshal in isolation.
type mockNBAgentPlanner struct{}

func (m *mockNBAgentPlanner) Plan(_ context.Context, _ []NBAgentPlannerToolActionStep, _ string) ([]NBAgentPlannerToolAction, *NBAgentPlannerFinishAction, error) {
	return nil, nil, nil
}
func (m *mockNBAgentPlanner) GetTools() []toolcore.NBTool { return nil }
func (m *mockNBAgentPlanner) Marshal() ([]byte, error)    { return nil, nil }
func (m *mockNBAgentPlanner) Unmarshal([]byte) error      { return nil }

// TestUnmarshal_DropsWaitingStepsOnResume verifies the fix for the
// confirmation-resume hallucination bug: when the saved state includes a
// WAITING step (the stub created at a confirmation pause whose Observation is
// the prompt text, not a tool result), Unmarshal must strip it so the
// resumed planner doesn't feed the prompt into the summarizer LLM as a fake
// tool response — which manifested as "the tool's configuration could not be
// resolved" answers even after the tool ran successfully.
func TestUnmarshal_DropsWaitingStepsOnResume(t *testing.T) {
	ctx := security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", []string{"cccccccc-cccc-cccc-cccc-cccccccccccc"})
	mockAgent := &MockAgent{}
	planner := &mockNBAgentPlanner{}

	original := &plannerExecutor{
		ctx:          ctx,
		agent:        mockAgent,
		agentPlanner: planner,
		stepKeys:     map[string]bool{},
		steps: []NBAgentPlannerToolActionStep{
			{
				Action:      NBAgentPlannerToolAction{ToolID: "done-1", Tool: "lookup", ToolInput: "q1"},
				Observation: "real result",
				Status:      ToolStatusSuccess,
			},
			{
				Action:      NBAgentPlannerToolAction{ToolID: "pending-1", Tool: "github_execute", ToolInput: "gh issue create ..."},
				Observation: "Tool(github_execute) is trying to create cluster resources. Do you want to continue?",
				Status:      ToolStatusWaiting,
			},
		},
		currentAction: []NBAgentPlannerToolAction{
			{ToolID: "pending-1", Tool: "github_execute", ToolInput: "gh issue create ..."},
		},
		toolCallCache: turnToolCallCache{cache: make(map[string]NBAgentPlannerToolActionStep)},
	}
	// Seed stepKeys with both IDs so we can verify the dropped one is removed.
	original.stepKeys["done-1"] = true
	original.stepKeys["pending-1"] = true

	state, err := original.Marshal()
	assert.NoError(t, err)

	restored := &plannerExecutor{
		ctx:           ctx,
		agent:         mockAgent,
		agentPlanner:  planner,
		stepKeys:      map[string]bool{},
		toolCallCache: turnToolCallCache{cache: make(map[string]NBAgentPlannerToolActionStep)},
	}
	assert.NoError(t, restored.Unmarshal(state))

	// Only the SUCCESS step survives; the WAITING stub is gone.
	assert.Len(t, restored.steps, 1, "WAITING step should be dropped on resume")
	assert.Equal(t, "done-1", restored.steps[0].Action.ToolID)
	assert.Equal(t, ToolStatusSuccess, restored.steps[0].Status)

	// stepKeys for the dropped WAITING ID must be cleared so the resume
	// restoration's doAction can append its real result into e.steps
	// (Call()'s dedup only appends when the key isn't already present).
	assert.True(t, restored.stepKeys["done-1"], "completed step's key must survive")
	assert.False(t, restored.stepKeys["pending-1"], "waiting step's key must be cleared so the real result can land")

	// currentAction is the source of truth for what to re-run on resume;
	// it must be preserved.
	assert.Len(t, restored.currentAction, 1)
	assert.Equal(t, "pending-1", restored.currentAction[0].ToolID)
}

// TestGetToolInvocations_SkipsWaitingSteps is the defense-in-depth check:
// even if a WAITING step somehow survives into a live executor, it must not
// be surfaced to the summarizer LLM as if its Observation were a tool result.
func TestGetToolInvocations_SkipsWaitingSteps(t *testing.T) {
	ctx := security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", []string{"cccccccc-cccc-cccc-cccc-cccccccccccc"})
	executor := &plannerExecutor{
		ctx:           ctx,
		agent:         &MockAgent{},
		toolCallCache: turnToolCallCache{cache: make(map[string]NBAgentPlannerToolActionStep)},
		steps: []NBAgentPlannerToolActionStep{
			{
				Action:      NBAgentPlannerToolAction{ToolID: "ok", Tool: "gh", ToolInput: "i1"},
				Observation: "https://github.com/foo/bar/issues/1",
				Status:      ToolStatusSuccess,
			},
			{
				Action:      NBAgentPlannerToolAction{ToolID: "pending", Tool: "gh", ToolInput: "i2"},
				Observation: "Do you want to continue?",
				Status:      ToolStatusWaiting,
			},
			{
				Action:      NBAgentPlannerToolAction{ToolID: "fail", Tool: "gh", ToolInput: "i3"},
				Observation: "permission denied",
				Status:      ToolStatusFailure,
			},
		},
	}

	invs := executor.GetToolInvocations()
	assert.Len(t, invs, 2, "WAITING step must be excluded; SUCCESS and FAILURE preserved")

	// Confirm the confirmation-prompt text never reaches the invocation list.
	for _, inv := range invs {
		assert.NotContains(t, inv.Response.Content, "Do you want to continue?")
	}
}
