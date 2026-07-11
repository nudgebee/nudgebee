package agents

import (
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

func TestParseDelegateInput_StructuredArguments(t *testing.T) {
	input := toolcore.NBToolCallRequest{
		Arguments: map[string]any{
			"prompt":         "Investigate MySQL connection pool exhaustion",
			"tools":          []any{"mysql_query", "prometheus"},
			"max_iterations": float64(8),
		},
	}

	prompt, toolNames, maxIter, err := parseDelegateInput(input)
	assert.NoError(t, err)
	assert.Equal(t, "Investigate MySQL connection pool exhaustion", prompt)
	assert.Equal(t, []string{"mysql_query", "prometheus"}, toolNames)
	assert.Equal(t, 8, maxIter)
}

func TestParseDelegateInput_JSONCommand(t *testing.T) {
	input := toolcore.NBToolCallRequest{
		Command: `{"prompt": "Check slow queries", "tools": ["mysql_query"], "max_iterations": 3}`,
	}

	prompt, toolNames, maxIter, err := parseDelegateInput(input)
	assert.NoError(t, err)
	assert.Equal(t, "Check slow queries", prompt)
	assert.Equal(t, []string{"mysql_query"}, toolNames)
	assert.Equal(t, 3, maxIter)
}

func TestParseDelegateInput_PlainTextCommand(t *testing.T) {
	input := toolcore.NBToolCallRequest{
		Command: "Analyze the database logs for errors",
	}

	prompt, toolNames, maxIter, err := parseDelegateInput(input)
	assert.NoError(t, err)
	assert.Equal(t, "Analyze the database logs for errors", prompt)
	assert.Nil(t, toolNames)
	assert.Equal(t, defaultDelegateMaxIterations, maxIter)
}

func TestParseDelegateInput_EmptyPrompt(t *testing.T) {
	input := toolcore.NBToolCallRequest{
		Arguments: map[string]any{
			"tools": []any{"mysql_query"},
		},
	}

	_, _, _, err := parseDelegateInput(input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "'prompt' is required")
}

func TestParseDelegateInput_MaxIterationsCapped(t *testing.T) {
	input := toolcore.NBToolCallRequest{
		Arguments: map[string]any{
			"prompt":         "Test prompt",
			"max_iterations": float64(100),
		},
	}

	_, _, maxIter, err := parseDelegateInput(input)
	assert.NoError(t, err)
	assert.Equal(t, maxDelegateMaxIterations, maxIter)
}

func TestParseDelegateInput_MaxIterationsBelowMinRejected(t *testing.T) {
	// max_iterations=1 is the empirical tell for misuse: pre-finish narration or
	// text-formatting work that should have been a plain LLM call. The parser must
	// reject it with a clear error so the caller revisits whether to delegate at all.
	input := toolcore.NBToolCallRequest{
		Arguments: map[string]any{
			"prompt":         "I have enough information to answer.",
			"max_iterations": float64(1),
		},
	}

	_, _, _, err := parseDelegateInput(input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "max_iterations")
	assert.Contains(t, err.Error(), "single-iteration delegation")
}

func TestParseDelegateInput_MaxIterationsAtMinAccepted(t *testing.T) {
	input := toolcore.NBToolCallRequest{
		Arguments: map[string]any{
			"prompt":         "Investigate something with two steps",
			"max_iterations": float64(2),
		},
	}

	_, _, maxIter, err := parseDelegateInput(input)
	assert.NoError(t, err)
	assert.Equal(t, minDelegateMaxIterations, maxIter)
}

func TestParseDelegateInput_DefaultMaxIterations(t *testing.T) {
	input := toolcore.NBToolCallRequest{
		Command: `{"prompt": "Test prompt"}`,
	}

	_, _, maxIter, err := parseDelegateInput(input)
	assert.NoError(t, err)
	assert.Equal(t, defaultDelegateMaxIterations, maxIter)
}

func TestParseDelegateInput_ArgumentsTakePrecedence(t *testing.T) {
	input := toolcore.NBToolCallRequest{
		Command: `{"prompt": "from command"}`,
		Arguments: map[string]any{
			"prompt": "from arguments",
		},
	}

	prompt, _, _, err := parseDelegateInput(input)
	assert.NoError(t, err)
	assert.Equal(t, "from arguments", prompt)
}

func TestParseDelegateInput_NoToolsDefaultsToEmpty(t *testing.T) {
	input := toolcore.NBToolCallRequest{
		Arguments: map[string]any{
			"prompt": "Test prompt",
		},
	}

	_, toolNames, _, err := parseDelegateInput(input)
	assert.NoError(t, err)
	assert.Nil(t, toolNames)
}

func TestDynamicReActAgent_Interface(t *testing.T) {
	agent := &dynamicReActAgent{
		name:          DelegateAgentToolName,
		prompt:        "Investigate connection pool exhaustion",
		tools:         nil,
		maxIterations: 5,
		accountId:     "test-account",
	}

	assert.Equal(t, DelegateAgentToolName, agent.GetName())
	assert.Nil(t, agent.GetNameAliases())
	assert.Contains(t, agent.GetDescription(), DelegateAgentToolName)
	assert.Equal(t, "react", string(agent.GetPlannerType()))
	assert.Equal(t, 5, agent.GetMaxIterations())
	assert.Equal(t, "LLM", agent.GetSummaryToolName())

	prompt := agent.GetSystemPrompt(nil, core.NBAgentRequest{})
	assert.Contains(t, prompt.Instructions[0], "Investigate connection pool exhaustion")
	assert.Len(t, prompt.Constraints, 4)
}

// TestDynamicReActAgent_SystemPromptIncludesBudgetAndTools verifies the system prompt
// includes both the exact iteration budget and the tool names so the sub-agent can
// plan its investigation up front.
func TestDynamicReActAgent_SystemPromptIncludesBudgetAndTools(t *testing.T) {
	agent := &dynamicReActAgent{
		name:   DelegateAgentToolName,
		prompt: "Investigate MySQL pool exhaustion",
		tools: []toolcore.NBTool{
			&mockTool{name: "mysql_query"},
			&mockTool{name: "prometheus"},
			&mockTool{name: "LLM"}, // should be filtered from tool list
		},
		maxIterations: 7,
		accountId:     "test-account",
	}

	prompt := agent.GetSystemPrompt(nil, core.NBAgentRequest{})

	var budgetLine string
	for _, c := range prompt.Constraints {
		if strings.Contains(c, "budget of 7") {
			budgetLine = c
			break
		}
	}
	assert.NotEmpty(t, budgetLine, "system prompt should contain an iteration-budget constraint")
	assert.Contains(t, budgetLine, "mysql_query", "tool names should appear in budget line")
	assert.Contains(t, budgetLine, "prometheus", "tool names should appear in budget line")
	assert.NotContains(t, budgetLine, "LLM", "LLM reasoning tool should be excluded from public tool list")
}

// TestDynamicReActAgent_SystemPromptBudgetWithNoTools verifies the budget line
// gracefully degrades when no external tools are provided (e.g., when the silent
// LLM-only fallback hands the sub-agent just the LLM tool).
func TestDynamicReActAgent_SystemPromptBudgetWithNoTools(t *testing.T) {
	agent := &dynamicReActAgent{
		name:          DelegateAgentToolName,
		prompt:        "Reason through a question",
		tools:         nil,
		maxIterations: 3,
		accountId:     "test-account",
	}

	prompt := agent.GetSystemPrompt(nil, core.NBAgentRequest{})

	var budgetLine string
	for _, c := range prompt.Constraints {
		if strings.Contains(c, "budget of 3") {
			budgetLine = c
			break
		}
	}
	assert.NotEmpty(t, budgetLine, "system prompt should contain an iteration-budget constraint")
	assert.Contains(t, budgetLine, "no external tools")
}

func TestIsLLMReasoningTool(t *testing.T) {
	assert.True(t, isLLMReasoningTool("LLM"))
	assert.True(t, isLLMReasoningTool("llm"))
	assert.True(t, isLLMReasoningTool("Llm"))
	assert.False(t, isLLMReasoningTool("mysql_query"))
	assert.False(t, isLLMReasoningTool(""))
	assert.False(t, isLLMReasoningTool("LLM_query"))
}

func TestCollectToolsUsed_EmptySteps(t *testing.T) {
	assert.Nil(t, collectToolsUsed(nil))
	assert.Nil(t, collectToolsUsed([]core.ToolInvocation{}))
}

func TestCollectToolsUsed_BuildsHistogram(t *testing.T) {
	steps := []core.ToolInvocation{
		{Call: llms.ToolCall{FunctionCall: &llms.FunctionCall{Name: "mysql_query"}}},
		{Call: llms.ToolCall{FunctionCall: &llms.FunctionCall{Name: "prometheus"}}},
		{Call: llms.ToolCall{FunctionCall: &llms.FunctionCall{Name: "mysql_query"}}},
		{Call: llms.ToolCall{FunctionCall: &llms.FunctionCall{Name: "LLM"}}}, // filtered
		{Call: llms.ToolCall{FunctionCall: nil}},                             // filtered
		{Call: llms.ToolCall{FunctionCall: &llms.FunctionCall{Name: ""}}},    // filtered
	}

	usage := collectToolsUsed(steps)
	assert.Equal(t, 2, usage["mysql_query"])
	assert.Equal(t, 1, usage["prometheus"])
	assert.NotContains(t, usage, "LLM")
	assert.Len(t, usage, 2)
}

func TestCollectToolsUsed_OnlyLLMStepsReturnsNil(t *testing.T) {
	steps := []core.ToolInvocation{
		{Call: llms.ToolCall{FunctionCall: &llms.FunctionCall{Name: "LLM"}}},
		{Call: llms.ToolCall{FunctionCall: &llms.FunctionCall{Name: "llm"}}},
	}
	assert.Nil(t, collectToolsUsed(steps))
}

func TestSumHistogram(t *testing.T) {
	assert.Equal(t, 0, sumHistogram(nil))
	assert.Equal(t, 0, sumHistogram(map[string]int{}))
	assert.Equal(t, 1, sumHistogram(map[string]int{"mysql_query": 1}))
	assert.Equal(t, 5, sumHistogram(map[string]int{"mysql_query": 2, "prometheus": 3}))
}

// TestIterationsUsed_ExcludesLLMReasoningSteps documents the semantic gap between
// iterations_used (external tool calls) and total_steps (raw ReAct steps including
// LLM reasoning). The cap is enforced on total_steps, so a sub-agent can be force-
// terminated with iterations_used < iteration_budget — which is why we expose
// budget_exhausted explicitly to the parent.
func TestIterationsUsed_ExcludesLLMReasoningSteps(t *testing.T) {
	steps := []core.ToolInvocation{
		{Call: llms.ToolCall{FunctionCall: &llms.FunctionCall{Name: "mysql_query"}}},
		{Call: llms.ToolCall{FunctionCall: &llms.FunctionCall{Name: "LLM"}}}, // reasoning
		{Call: llms.ToolCall{FunctionCall: &llms.FunctionCall{Name: "prometheus"}}},
		{Call: llms.ToolCall{FunctionCall: &llms.FunctionCall{Name: "LLM"}}}, // final summary
	}

	iterationsUsed := sumHistogram(collectToolsUsed(steps))
	totalSteps := len(steps)

	assert.Equal(t, 2, iterationsUsed, "iterations_used should count only external tool calls")
	assert.Equal(t, 4, totalSteps, "total_steps should retain the full ReAct step history")
}

func TestDelegateAgentTool_Metadata(t *testing.T) {
	tool := &delegateAgentTool{accountId: "test-account"}

	assert.Equal(t, DelegateAgentToolName, tool.Name())
	assert.Equal(t, toolcore.NBToolTypeTool, tool.GetType())
	assert.Contains(t, tool.Description(), "scoped specialist sub-investigator")

	schema := tool.InputSchema()
	assert.Equal(t, toolcore.ToolSchemaTypeObject, schema.Type)
	assert.Contains(t, schema.Properties, "prompt")
	assert.Contains(t, schema.Properties, "tools")
	assert.Contains(t, schema.Properties, "max_iterations")
	assert.Equal(t, []string{"prompt"}, schema.Required)
}

func TestResolveToolsForDelegate_DeduplicatesNames(t *testing.T) {
	// With no tools registered, all should be unresolved
	resolved, unresolved := resolveToolsForDelegate(nil, "fake-account-id", []string{"tool_a", "tool_a", "TOOL_A"})
	assert.Empty(t, resolved)
	// Only one entry since duplicates are deduped
	assert.Len(t, unresolved, 1)
	assert.Equal(t, "tool_a", unresolved[0])
}

func TestResolveToolsForDelegate_EmptyList(t *testing.T) {
	resolved, unresolved := resolveToolsForDelegate(nil, "fake-account-id", nil)
	assert.Nil(t, resolved)
	assert.Nil(t, unresolved)
}

func TestFilterOutTool_RemovesDelegateAgent(t *testing.T) {
	mockTools := []toolcore.NBTool{
		&mockTool{name: "mysql_query"},
		&mockTool{name: DelegateAgentToolName},
		&mockTool{name: "prometheus"},
	}

	filtered := filterOutTool(mockTools, DelegateAgentToolName)
	assert.Len(t, filtered, 2)
	for _, tool := range filtered {
		assert.NotEqual(t, DelegateAgentToolName, tool.Name())
	}
}

func TestFilterOutTool_CaseInsensitive(t *testing.T) {
	mockTools := []toolcore.NBTool{
		&mockTool{name: "DELEGATE_AGENT"},
		&mockTool{name: "mysql"},
	}

	filtered := filterOutTool(mockTools, DelegateAgentToolName)
	assert.Len(t, filtered, 1)
	assert.Equal(t, "mysql", filtered[0].Name())
}

func TestFilterOutTool_NoMatch(t *testing.T) {
	mockTools := []toolcore.NBTool{
		&mockTool{name: "mysql"},
		&mockTool{name: "prometheus"},
	}

	filtered := filterOutTool(mockTools, DelegateAgentToolName)
	assert.Len(t, filtered, 2)
}

// mockTool is a minimal NBTool implementation for testing.
type mockTool struct {
	name string
}

func (m *mockTool) Name() string                     { return m.name }
func (m *mockTool) Description() string              { return "" }
func (m *mockTool) GetType() toolcore.NBToolType     { return toolcore.NBToolTypeTool }
func (m *mockTool) InputSchema() toolcore.ToolSchema { return toolcore.ToolSchema{} }
func (m *mockTool) Call(_ toolcore.NbToolContext, _ toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	return toolcore.NBToolResponse{}, nil
}
