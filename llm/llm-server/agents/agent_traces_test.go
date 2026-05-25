package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// --- mock agent that satisfies core.NBAgent for unit tests ---

type mockTracesSubAgent struct {
	name string
}

func (m *mockTracesSubAgent) GetName() string          { return m.name }
func (m *mockTracesSubAgent) GetNameAliases() []string { return nil }
func (m *mockTracesSubAgent) GetDescription() string   { return "mock" }
func (m *mockTracesSubAgent) GetSupportedTools(_ *security.RequestContext) []toolcore.NBTool {
	return nil
}
func (m *mockTracesSubAgent) GetSystemPrompt(_ *security.RequestContext, _ core.NBAgentRequest) core.NBAgentPrompt {
	return core.NBAgentPrompt{}
}
func (m *mockTracesSubAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// cannedResponse pairs a response with an optional error for the mock executor.
type cannedResponse struct {
	resp core.NBAgentResponse
	err  error
}

// mockTracesExecutor returns an agentExecutorFunc that replays canned responses
// in order, one per call.
func mockTracesExecutor(responses []cannedResponse) agentExecutorFunc {
	callIdx := 0
	return func(_ toolcore.NbToolContext, _ core.NBAgent, _ toolcore.NBToolCallRequest) (core.NBAgentResponse, error) {
		if callIdx >= len(responses) {
			return core.NBAgentResponse{Status: core.ConversationStatusFailed}, nil
		}
		r := responses[callIdx]
		callIdx++
		return r.resp, r.err
	}
}

// --- unit tests for soft-completion detection ---

func TestIsTracesSoftCompletion(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"exact no traces", "no traces", true},
		{"mixed case", "No Traces found for the query", true},
		{"unable to retrieve", "Unable to retrieve trace data", true},
		{"real data", "Found 42 traces for service-x between 10:00 and 11:00", false},
		{"empty string", "", false},
		{"partial mismatch", "nothing found", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isTracesSoftCompletion(tt.input))
		})
	}
}

// --- unit tests for the fallback Execute loop ---

func TestTracesFallbackExecute_FirstAgentCompletesWithData(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent := &fallbackTracesAgent{
		accountId: "test-account",
		agents:    []core.NBAgent{&mockTracesSubAgent{name: "a"}, &mockTracesSubAgent{name: "b"}},
		executor: mockTracesExecutor([]cannedResponse{
			{resp: core.NBAgentResponse{Response: []string{"Here are 5 traces"}, Status: core.ConversationStatusCompleted}},
		}),
	}

	resp, err := agent.Execute(ctx, core.NBAgentRequest{Query: "show traces"})
	assert.NoError(t, err)
	assert.Equal(t, core.ConversationStatusCompleted, resp.Status)
	assert.Contains(t, resp.Response[0], "Here are 5 traces")
}

func TestTracesFallbackExecute_SoftCompleteThenRealData(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent := &fallbackTracesAgent{
		accountId: "test-account",
		agents:    []core.NBAgent{&mockTracesSubAgent{name: "a"}, &mockTracesSubAgent{name: "b"}},
		executor: mockTracesExecutor([]cannedResponse{
			{resp: core.NBAgentResponse{Response: []string{"No traces found"}, Status: core.ConversationStatusCompleted}},
			{resp: core.NBAgentResponse{Response: []string{"Found 3 traces from Jaeger"}, Status: core.ConversationStatusCompleted}},
		}),
	}

	resp, err := agent.Execute(ctx, core.NBAgentRequest{Query: "show traces"})
	assert.NoError(t, err)
	assert.Equal(t, core.ConversationStatusCompleted, resp.Status)
	assert.Contains(t, resp.Response[0], "Found 3 traces")
}

func TestTracesFallbackExecute_AllAgentsSoftComplete(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent := &fallbackTracesAgent{
		accountId: "test-account",
		agents:    []core.NBAgent{&mockTracesSubAgent{name: "a"}, &mockTracesSubAgent{name: "b"}},
		executor: mockTracesExecutor([]cannedResponse{
			{resp: core.NBAgentResponse{Response: []string{"No traces available"}, Status: core.ConversationStatusCompleted}},
			{resp: core.NBAgentResponse{Response: []string{"Unable to retrieve traces"}, Status: core.ConversationStatusCompleted}},
		}),
	}

	resp, err := agent.Execute(ctx, core.NBAgentRequest{Query: "show traces"})
	assert.NoError(t, err)
	assert.Equal(t, core.ConversationStatusCompleted, resp.Status)
	assert.Contains(t, resp.Response[0], "Unable to retrieve traces")
}

func TestTracesFallbackExecute_AllAgentsFail(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent := &fallbackTracesAgent{
		accountId: "test-account",
		agents:    []core.NBAgent{&mockTracesSubAgent{name: "clickhouse"}, &mockTracesSubAgent{name: "jaeger"}},
		executor: mockTracesExecutor([]cannedResponse{
			{resp: core.NBAgentResponse{Response: []string{"timeout"}, Status: core.ConversationStatusFailed}},
			{resp: core.NBAgentResponse{Response: []string{"auth error"}, Status: core.ConversationStatusFailed}},
		}),
	}

	resp, err := agent.Execute(ctx, core.NBAgentRequest{Query: "show traces"})
	assert.NoError(t, err)
	assert.Equal(t, core.ConversationStatusFailed, resp.Status)
	assert.Contains(t, resp.Response[0], "All traces agents failed")
	assert.Contains(t, resp.Response[0], "clickhouse Agent Effort")
	assert.Contains(t, resp.Response[0], "jaeger Agent Effort")
}

func TestTracesFallbackExecute_WaitingPropagatedImmediately(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent := &fallbackTracesAgent{
		accountId: "test-account",
		agents:    []core.NBAgent{&mockTracesSubAgent{name: "a"}, &mockTracesSubAgent{name: "b"}},
		executor: mockTracesExecutor([]cannedResponse{
			{
				resp: core.NBAgentResponse{Response: []string{"waiting for confirmation"}, Status: core.ConversationStatusWaiting},
				err:  assert.AnError, // transient error should be discarded
			},
		}),
	}

	resp, err := agent.Execute(ctx, core.NBAgentRequest{Query: "show traces"})
	assert.NoError(t, err, "Waiting status should discard transient errors")
	assert.Equal(t, core.ConversationStatusWaiting, resp.Status)
}

func TestTracesFallbackExecute_CompletedEmptyResponseTreatedAsSoftCompletion(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent := &fallbackTracesAgent{
		accountId: "test-account",
		agents:    []core.NBAgent{&mockTracesSubAgent{name: "a"}},
		executor: mockTracesExecutor([]cannedResponse{
			{resp: core.NBAgentResponse{Response: []string{}, Status: core.ConversationStatusCompleted}},
		}),
	}

	resp, err := agent.Execute(ctx, core.NBAgentRequest{Query: "show traces"})
	assert.NoError(t, err)
	assert.Equal(t, core.ConversationStatusCompleted, resp.Status)
	assert.Contains(t, resp.Response[0], "No traces were found")
}

// TODO mock DBs
// TODO mock Tool Execution
func TestTracesAgent_ExecutePartialResource(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	tracesAgent, err := getTracesAgent(sc, os.Getenv("TEST_ACCOUNT"))
	assert.Nil(t, err)

	// Get me recent traces where savings is more than 1$
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-traces-chain-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER_1"),
				Query:     "Can you get me recent api failures of LLM server in nudgebee namespace",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, tracesAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query, core.ConversationSessionRequestWithSource(core.ConversationSourceInvestigation))
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, tracesAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestTracesAgent_Execute(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	tracesAgent, err := getTracesAgent(sc, os.Getenv("TEST_ACCOUNT"))
	assert.Nil(t, err)

	// Get me recent traces where savings is more than 1$
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-traces-chain-2",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "What are latest failure on services-server in nudgebee namespace?",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, tracesAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, tracesAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestTracesAgent_K8sDebug(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	// Get me recent traces where savings is more than 1$
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-traces-chain-3",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "can you analyse k8s-collector traces in nudgebee namespace for last 24 hours?",
			},
		}
	for _, tc := range testCases {
		debugAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, debugAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		toolNames := []string{}

		for _, toolInvocation := range resp.AgentStepResponse {
			toolNames = append(toolNames, toolInvocation.Call.FunctionCall.Name)
		}
		assert.Contains(t, toolNames, TracesAgentName)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, debugAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestTracesClickhouseAgent_NamespaceFiltering(t *testing.T) {
	tracesAgent := TracesClickhouseAgent{accountId: os.Getenv("TEST_ACCOUNT")}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-traces-namespace-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "get traces of llm server in nudgebee namespace",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, tracesAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, tracesAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestTracesClickhouseAgent_EndpointFiltering(t *testing.T) {
	tracesAgent := TracesClickhouseAgent{accountId: os.Getenv("TEST_ACCOUNT")}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-traces-endpoint-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "get traces for frontend-service in demo namespace of '/api/web/search/analytics' endpoint",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, tracesAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, tracesAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}
