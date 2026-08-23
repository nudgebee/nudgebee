package agents

import (
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
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

// TestParseDelegateInput_NotebookMisuseRejected pins the notebook-misuse
// guard added 2026-06-28. 2026-06-27 production sample (post-#32246 and
// #32334): 8 of 28 (~28.6%) delegate_agent calls started with "update the
// notebook" prompts — essentially unchanged from the 32% pre-#32246
// baseline. The description has prohibited this since #32246 ("DO NOT use
// to record findings — write to your own notebook with <update_notebook>")
// but description-only doesn't move misuse share (same lesson as the
// think-tool tighten arc; see PR #33080). This guard rejects at the tool
// boundary.
func TestParseDelegateInput_NotebookMisuseRejected(t *testing.T) {
	cases := []struct {
		name string
		// Verbatim from production samples (post-#32246 misuse window).
		prompt string
	}{
		{
			name:   "update the notebook + scope+hypothesis tree",
			prompt: "Update the notebook with the current investigation status for services-server errors in namespace nudgebee. \n\n## Scope\n- Target: services-server in namespace nudgebee\n- Symptom: Recurring errors\n",
		},
		{
			name:   "update the notebook + DONE bullets",
			prompt: "Update the notebook: [DONE] Identify pod in namespace-70 and check health. [DOING] Check namespace-70 deployment origin.",
		},
		{
			name:   "update the notebook with the final findings",
			prompt: "Update the notebook with the final findings: ## Scope ...",
		},
		{
			name:   "update the notebook with the following changes",
			prompt: "Update the notebook with the following changes: ## Hypothesis Tree ...",
		},
		{
			name:   "updating the notebook (gerund form)",
			prompt: "Updating the notebook to mark H2 as SUPPORTED based on evidence E5.",
		},
		{
			name:   "lowercase + no article",
			prompt: "update notebook with finalized status.",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, _, err := parseDelegateInput(toolcore.NBToolCallRequest{
				Arguments: map[string]any{"prompt": c.prompt},
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "starts with 'update the notebook'",
				"rejection error must name the misuse pattern explicitly")
			assert.Contains(t, err.Error(), "<update_notebook>",
				"rejection error must point at the correct path (use the XML tag in your thought)")
		})
	}
}

// TestParseDelegateInput_LegitimateInvestigationPromptsPass pins the
// false-positive bound: the legitimate "investigate / check / fetch /
// find / verify / identify / search / examine" verb-led prompts that
// dominate the production data must continue to pass.
func TestParseDelegateInput_LegitimateInvestigationPromptsPass(t *testing.T) {
	cases := []struct {
		name string
		// Verbatim from production samples (post-#32246 legitimate use).
		prompt string
	}{
		{
			name:   "Investigate verb",
			prompt: "Investigate the Azure authentication failure (error 7000222: client secret has expired) and DNS resolution issues for the cloud-collector-server workload.",
		},
		{
			name:   "Check verb",
			prompt: "Check if there are any recent logs (last 1h) for the other pods in the StatefulSet: ordered-app-1 and ordered-app-2 in namespace otel-demo.",
		},
		{
			name:   "Fetch verb",
			prompt: "The data-processor pod in namespace-70 is running a simulation script. Please fetch the raw last 50 lines of logs for pod data-processor-7695d68bb8-zwq5c.",
		},
		{
			name:   "Find verb",
			prompt: "Find any workload or pod related to 'api-limiter' or 'rate-limit' across the entire cluster.",
		},
		{
			name:   "Verify verb",
			prompt: "Verify the environment variables in curl-deployment and identify which downstream service it is calling.",
		},
		{
			name: "false-positive guard: 'notebook' mid-prompt is legitimate",
			// A prompt that mentions notebook but doesn't START with
			// the misuse verb must pass — investigating notebook-related
			// bugs is a legitimate sub-question.
			prompt: "Investigate why the notebook update logic in agent_planner.go is dropping AdditionalDetails fields on the persistence path.",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotPrompt, _, _, err := parseDelegateInput(toolcore.NBToolCallRequest{
				Arguments: map[string]any{"prompt": c.prompt},
			})
			assert.NoError(t, err,
				"legitimate investigation prompt must not be rejected by the notebook-misuse guard")
			assert.Equal(t, c.prompt, gotPrompt)
		})
	}
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
	resolved, _, unresolved := resolveToolsForDelegate(nil, "fake-account-id", []string{"tool_a", "tool_a", "TOOL_A"})
	assert.Empty(t, resolved)
	// Only one entry since duplicates are deduped
	assert.Len(t, unresolved, 1)
	assert.Equal(t, "tool_a", unresolved[0])
}

func TestResolveToolsForDelegate_EmptyList(t *testing.T) {
	resolved, _, unresolved := resolveToolsForDelegate(nil, "fake-account-id", nil)
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

// TestResolveAgentForDelegate_FlattensThinAgent pins the flatten path: a thin agent
// that opts into flattenableAgent (helm/redis) is inlined to its leaf tool(s) with its
// static instructions carried, instead of nested as an agent-tool (avoids the redundant
// delegation hop).
func TestResolveAgentForDelegate_FlattensThinAgent(t *testing.T) {
	tools, instructions := resolveAgentForDelegate(nil, HelmAgent{})
	assert.Len(t, tools, 1)
	assert.Equal(t, toolcore.NBToolTypeTool, tools[0].GetType(), "flattened agent must yield a leaf tool, not a nested agent-tool")
	assert.NotEqual(t, HelmAgentName, tools[0].Name(), "flattened tool must be the leaf tool, not the agent itself")
	assert.NotEmpty(t, instructions, "flattened agent must carry its GetSystemPrompt guidance")

	_, redisInstr := resolveAgentForDelegate(nil, RedisAgent{})
	assert.NotEmpty(t, redisInstr, "flattened redis must carry its GetSystemPrompt guidance")
	assert.Contains(t, strings.ToLower(strings.Join(redisInstr, " ")), "redis",
		"carried guidance must be redis's own (read from GetSystemPrompt, not hand-duplicated)")
}

// TestResolveAgentForDelegate_NestsNonFlattenable pins the default: an agent that does
// NOT implement flattenableAgent stays nested (today's behavior), preserving its own
// dynamically-composed prompt/context — how postgres/aws "earn their hop".
func TestResolveAgentForDelegate_NestsNonFlattenable(t *testing.T) {
	tools, instructions := resolveAgentForDelegate(nil, nestOnlyAgent{})
	assert.Len(t, tools, 1)
	assert.Equal(t, toolcore.NBToolTypeAgent, tools[0].GetType(), "non-flattenable agent must be nested as an agent-tool")
	assert.Empty(t, instructions)
}

// TestDelegateToolFiltering_AppliesCapabilities pins the capability filter the delegate
// Call applies on the resolved set: drop delegate_agent (recursion guard), then apply the
// account's allowed/disabled filter — without which a disabled/non-allow-listed tool named
// in a delegate call would execute in the sub-agent (the dispatch auth gate only checks
// set membership).
func TestDelegateToolFiltering_AppliesCapabilities(t *testing.T) {
	resolved := []toolcore.NBTool{
		&mockTool{name: "kubectl_execute"},
		&mockTool{name: "postgres_query_execute"},
		&mockTool{name: DelegateAgentToolName},
	}
	step1 := filterOutTool(resolved, DelegateAgentToolName)
	names := func(ts []toolcore.NBTool) []string {
		out := make([]string, 0, len(ts))
		for _, tl := range ts {
			out = append(out, tl.Name())
		}
		return out
	}
	denied := core.FilterTools(step1, toolcore.AgentCapabilities{DisabledTools: []string{"postgres_query_execute"}})
	assert.ElementsMatch(t, []string{"kubectl_execute"}, names(denied), "disabled tool must be filtered out of the delegated set")

	allowed := core.FilterTools(step1, toolcore.AgentCapabilities{AllowedTools: []string{"kubectl_execute"}})
	assert.ElementsMatch(t, []string{"kubectl_execute"}, names(allowed), "with an allow-list, non-listed tools must be dropped")

	unfiltered := core.FilterTools(step1, toolcore.AgentCapabilities{})
	assert.ElementsMatch(t, []string{"kubectl_execute", "postgres_query_execute"}, names(unfiltered))
}

// TestDynamicReActAgent_RunsOnCheapTier pins the cost decision: a delegated sub-agent runs
// on the cheap Retrieval tier (scoped grunt work), not the orchestrator's Reasoning tier.
func TestDynamicReActAgent_RunsOnCheapTier(t *testing.T) {
	a := &dynamicReActAgent{name: DelegateAgentToolName}
	assert.Equal(t, core.ModelTierRetrieval, a.GetModelCategory(),
		"delegated sub-agents must run on the cheap Retrieval tier, not Reasoning")
}

// TestFlattenAgentGuidance_CarriesInstructionsConstraintsExamples pins that flattening a
// thin agent carries its full static guidance — instructions, constraints, AND examples
// (Answer and AnswerSteps forms) — since a CLI wrapper's examples (e.g. rabbitmq's curl/jq
// recipes) are its real value and must survive flattening.
func TestFlattenAgentGuidance_CarriesInstructionsConstraintsExamples(t *testing.T) {
	p := core.NBAgentPrompt{
		Instructions: []string{"do X"},
		Constraints:  []string{"only Y"},
		ToolUsage:    map[string][]string{"rabbitmq_execute": {"you can pipe curl output through jq"}},
		Examples: []core.NBAgentPromptExample{
			{Question: "list queues", Answer: "curl .../api/queues", Explanation: "returns queue depths"},
			{Question: "backlog?", AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: "rabbitmq", Input: "curl .../api/queues | jq 'select(.messages>0)'"},
			}},
		},
	}
	joined := strings.Join(flattenAgentGuidance(p), "\n")
	assert.Contains(t, joined, "do X")
	assert.Contains(t, joined, "only Y")
	assert.Contains(t, joined, "you can pipe curl output through jq", "ToolUsage guidance must be carried")
	assert.Contains(t, joined, "list queues")
	assert.Contains(t, joined, "curl .../api/queues")
	assert.Contains(t, joined, "returns queue depths")
	assert.Contains(t, joined, "jq 'select(.messages>0)'", "AnswerSteps input must be carried")
}

// nestOnlyAgent is a minimal NBAgent that does NOT implement flattenableAgent.
type nestOnlyAgent struct{}

func (nestOnlyAgent) GetName() string          { return "nest_only" }
func (nestOnlyAgent) GetNameAliases() []string { return nil }
func (nestOnlyAgent) GetDescription() string   { return "nest-only test agent" }
func (nestOnlyAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}
func (nestOnlyAgent) GetSupportedTools(*security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{&mockTool{name: "inner_tool"}}
}
func (nestOnlyAgent) GetSystemPrompt(*security.RequestContext, core.NBAgentRequest) core.NBAgentPrompt {
	return core.NBAgentPrompt{}
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
