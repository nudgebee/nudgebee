package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"testing"

	"github.com/stretchr/testify/assert"
)

// --- mock agent that satisfies core.NBAgent for unit tests ---

type mockTracesSubAgent struct {
	name string
}

// cannedResponse pairs a response with an optional error for the mock executor.
type cannedResponse struct {
	resp core.NBAgentResponse
	err  error
}

func (m *mockTracesSubAgent) GetName() string { return m.name }

func (m *mockTracesSubAgent) GetNameAliases() []string { return nil }

func (m *mockTracesSubAgent) GetDescription() string { return "mock" }

func (m *mockTracesSubAgent) GetSupportedTools(_ *security.RequestContext) []toolcore.NBTool {
	return nil
}

func (m *mockTracesSubAgent) GetSystemPrompt(_ *security.RequestContext, _ core.NBAgentRequest) core.NBAgentPrompt {
	return core.NBAgentPrompt{}
}

func (m *mockTracesSubAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
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
