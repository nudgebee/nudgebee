package core

import (
	"context"
	nbprompts "nudgebee/llm/prompts"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/prompts"
)

func renderReact4BaseWithRoles(t *testing.T, notebookEnabled, hypothesisModeEnabled, orchestratorMode, executorMode bool) string {
	return renderReact4BaseWithModes(t, notebookEnabled, hypothesisModeEnabled, orchestratorMode, executorMode, true)
}

func renderReact4BaseWithModes(t *testing.T, notebookEnabled, hypothesisModeEnabled, orchestratorMode, executorMode, isInvestigation bool) string {
	t.Helper()
	base := nbprompts.GetPrompt(context.Background(), nbprompts.PromptReact4Base, "")
	assert.NotEmpty(t, base, "embedded react_4 base prompt must load")

	vars := []string{
		"notebook_enabled", "hypothesis_mode_enabled", "is_top_level", "orchestrator_mode", "executor_mode",
		"delegate_agent_enabled", "is_investigation", "grounding_enabled",
		"context_management_rules", "time_handling_rules", "data_protection_rules",
		"code_analysis_rules", "security_rules", "memory_consumption_rules", "async_completion_rules",
	}
	tmpl := prompts.NewPromptTemplate(base, vars)
	out, err := tmpl.Format(map[string]any{
		"grounding_enabled":        false,
		"delegate_agent_enabled":   true,
		"notebook_enabled":         notebookEnabled,
		"hypothesis_mode_enabled":  hypothesisModeEnabled,
		"is_top_level":             true,
		"orchestrator_mode":        orchestratorMode,
		"executor_mode":            executorMode,
		"is_investigation":         isInvestigation,
		"context_management_rules": "",
		"time_handling_rules":      "",
		"data_protection_rules":    "",
		"code_analysis_rules":      "",
		"security_rules":           "",
		"memory_consumption_rules": "",
		"async_completion_rules":   "",
	})
	assert.NoError(t, err, "react_4 base prompt must render without template errors")
	return out
}

func TestReAct4BaseInvestigationGating(t *testing.T) {
	assert.Contains(t, renderReact4BaseWithModes(t, true, true, true, false, true), "INVESTIGATION DISCIPLINE")
	assert.NotContains(t, renderReact4BaseWithModes(t, true, false, false, false, false), "INVESTIGATION DISCIPLINE")
}

func renderReact4CustomBase(t *testing.T, notebookEnabled, isInvestigation bool) string {
	t.Helper()
	base := nbprompts.GetPrompt(context.Background(), nbprompts.PromptReact4CustomBase, "")
	assert.NotEmpty(t, base, "embedded react_4 custom base prompt must load")

	tmpl := prompts.NewPromptTemplate(base, []string{
		"is_investigation", "notebook_enabled", "time_handling_rules", "security_rules",
	})
	out, err := tmpl.Format(map[string]any{
		"grounding_enabled":   false,
		"notebook_enabled":    notebookEnabled,
		"is_investigation":    isInvestigation,
		"time_handling_rules": "",
		"security_rules":      "",
	})
	assert.NoError(t, err, "react_4 custom base prompt must render")
	return out
}

func TestReAct4BasePromptNameIsolatesDatabaseBackedCustomAgents(t *testing.T) {
	assert.Equal(t, nbprompts.PromptReact4CustomBase, react4BasePromptName(&nbCustomAgent{}))
	assert.Equal(t, nbprompts.PromptReact4Base, react4BasePromptName(&MockAgent{}))
}

func TestReAct4CustomBaseIsCompactAndGeneric(t *testing.T) {
	out := renderReact4CustomBase(t, true, true)

	assert.Contains(t, out, "native functions")
	assert.Contains(t, out, "fewest planner turns and tool")
	assert.Contains(t, out, "complete, verified result")
	assert.Contains(t, out, "Multiple sibling calls may use the same tool")
	assert.Contains(t, out, "one bounded invocation")
	assert.Contains(t, out, "deliberately")
	assert.Contains(t, out, "Unexpected tool or transport truncation")
	assert.Contains(t, out, "Classify each invocation from its")
	assert.Contains(t, out, "other useful independent calls")
	assert.Contains(t, out, "use at most one bounded")
	assert.Contains(t, out, "the next tool turn must implement")
	assert.Contains(t, out, "group multiple")
	assert.Contains(t, out, "every result is clearly labeled")
	assert.Contains(t, out, "update_notebook")
	assert.Contains(t, out, "<final_answer>")
	assert.Contains(t, out, "Use native function calls for every tool invocation.")
	assert.NotContains(t, out, "<thought_action>")
	assert.NotContains(t, out, "<tool_name>")
	assert.NotContains(t, out, "<tool_input>")

	assert.NotContains(t, out, "HYPOTHESIS DISCIPLINE")
	assert.NotContains(t, out, "DELEGATION:")
	assert.NotContains(t, out, "code_analyzer")
	assert.NotContains(t, out, "kubectl")
	assert.Less(t, len(out), 7000, "custom-agent base should remain compact")
}

func TestReAct4CustomBaseNotebookGating(t *testing.T) {
	assert.Contains(t, renderReact4CustomBase(t, true, true), "YOUR NOTEBOOK")
	assert.NotContains(t, renderReact4CustomBase(t, false, true), "YOUR NOTEBOOK")
}

func TestReAct4CustomBaseInvestigationGating(t *testing.T) {
	assert.Contains(t, renderReact4CustomBase(t, true, true), "INVESTIGATION PATTERN")
	assert.NotContains(t, renderReact4CustomBase(t, true, false), "INVESTIGATION PATTERN")
}

// react_4 is a HYBRID contract: actions are native tool calls, but the final
// answer keeps react_3's <final_answer> envelope so thought and user-facing
// content stay separable (the model emits that shape unprompted anyway, and
// bare text conflated the two — answers began "The user is asking for…").
// The legacy action grammar must stay absent. Naming forbidden syntax in a
// negative example can prime the model to reproduce it.
func TestReAct4Base_ActionsNativeAnswerStructured(t *testing.T) {
	out := renderReact4BaseWithRoles(t, true, true, true, false)

	// Native tool calling is still the action protocol.
	assert.Contains(t, out, "native functions")

	// The final-answer envelope is taught.
	assert.Contains(t, out, "FINAL ANSWER FORMAT")
	assert.Contains(t, out, "<final_answer>")
	assert.Contains(t, out, "<content>")

	assert.Contains(t, out, "Use native function calls for every tool invocation.")
	assert.NotContains(t, out, "<thought_action>")
	assert.NotContains(t, out, "<tool_name>")
	assert.NotContains(t, out, "<tool_input>")
}

func TestReAct4MemoryRules_DoNotTeachLegacyActionGrammar(t *testing.T) {
	out, err := nbprompts.GetPromptStrict(context.Background(), nbprompts.PromptMemoryConsumptionRules, "")
	assert.NoError(t, err)
	assert.NotContains(t, out, "<action>")
	assert.NotContains(t, out, "<tool_name>")
	assert.NotContains(t, out, "<tool_input>")
}

func TestReAct4Base_NotebookGating(t *testing.T) {
	withNotebook := renderReact4BaseWithRoles(t, true, false, false, false)
	assert.Contains(t, withNotebook, "update_notebook")
	assert.Contains(t, withNotebook, "YOUR NOTEBOOK")

	withoutNotebook := renderReact4BaseWithRoles(t, false, false, false, false)
	assert.NotContains(t, withoutNotebook, "YOUR NOTEBOOK")
}

func TestReAct4Base_HypothesisGating(t *testing.T) {
	withHypothesis := renderReact4BaseWithRoles(t, true, true, false, false)
	assert.Contains(t, withHypothesis, "HYPOTHESIS DISCIPLINE")

	withoutHypothesis := renderReact4BaseWithRoles(t, true, false, false, false)
	assert.NotContains(t, withoutHypothesis, "HYPOTHESIS DISCIPLINE")
}

// react_4's base is deliberately far shorter than react_3's, but the CONVERGENCE
// rules are not optional trimming. Their absence was measured, not theorised:
// on the k8s_pod_rca comparison case react_4 issued 21 tool calls to react_3's 4
// for the same 0.95 correctness, because it inherited "broaden the search" with
// nothing telling it when to stop, and it answered one fixture by listing
// kubectl commands for the user to run. Both arms load the identical k8s_lean
// agent prompt, so the planner base is the only place these can live — and lean
// agents depend on it precisely because their own prompt is minimal.
func TestReAct4Base_CarriesConvergenceRules(t *testing.T) {
	out := renderReact4BaseWithRoles(t, true, true, true, false)

	// The stop rule: more calls cannot manufacture absent evidence.
	assert.Contains(t, out, "Know when to stop.")
	assert.Contains(t, out, "synthesize a best-effort answer")

	// Broadening must be bounded, or a not-found resource drives endless widening.
	assert.Contains(t, out, "bounded")
	assert.Contains(t, out, "do not keep widening")

	// No repeated calls / reuse saved artifacts instead of re-running tools.
	assert.Contains(t, out, "Never repeat a call you have already made.")

	// The answer must carry findings, not homework for the user.
	assert.Contains(t, out, "Results, not instructions.")

	// Finalize when the question is answered instead of chasing every branch,
	// and finalize honestly at the iteration limit rather than looping.
	assert.Contains(t, out, "Do not manufacture work.")

	// The k8s_pod_rca case broadened for 21 calls against a pod name that was
	// never resolved to a real entity.
	assert.Contains(t, out, "Resolve concrete targets first.")

	// Completeness scored 0.85 vs react_3's 0.95 — sub-questions must be closed
	// or explicitly marked unanswerable, never silently dropped.
	assert.Contains(t, out, "Completion self-check.")

	// An empty result confirms nothing.
	assert.Contains(t, out, "non-empty evidence")
}

// Hypothesis mode without prior discipline and pruning degenerates into testing
// every branch, which is the other half of the tool-call explosion.
func TestReAct4Base_HypothesisDisciplineHasPriorsAndPruning(t *testing.T) {
	out := renderReact4BaseWithRoles(t, true, true, false, false)
	assert.Contains(t, out, "at most 2-3 as High")
	assert.Contains(t, out, "Prune")

	// Still gated: an agent without hypothesis mode must not receive them.
	withoutHypothesis := renderReact4BaseWithRoles(t, true, false, false, false)
	assert.NotContains(t, withoutHypothesis, "at most 2-3 as High")
}

// The step's thought is the only record of WHY a tool ran — it persists on the
// tool_calls row and renders beside the step. react_3's XML grammar forced a
// <thought> on every action; native tool calling makes prose optional, and the
// model duly omitted it: 100% of react_4 tool calls across three A/B sessions
// persisted an empty thought (19/19, 98/98, 21/21) against 0% for react_3. The
// prompt must therefore REQUIRE the intent line, not merely permit it.
func TestReAct4Base_RequiresIntentAlongsideToolCalls(t *testing.T) {
	out := renderReact4BaseWithRoles(t, true, true, true, false)

	assert.Contains(t, out, "Always state your intent in one short sentence alongside the tool call")
	assert.Contains(t, out, "This is not optional")

	// The old permissive phrasing must be gone — "may include" is precisely what
	// let the model skip it on every call.
	assert.NotContains(t, out, "You may include a short sentence of reasoning",
		"optional phrasing is what produced 100% empty thoughts")
}

// react_4 shipped with the delegate_agent TOOL available but ZERO guidance on
// when to use it — react_3's base mentions delegation 8 times, react_4's
// mentioned it 0 times. The model duly kept multi-step discovery inline, and
// inline work is the expensive kind: on one comparison case the orchestrator
// made 31 LLM calls under react_4 against 9 under react_3, each carrying the
// full accumulated context, which is what drove react_4 to ~5x the cost on that
// run. The section is gated on the tool actually being present, like react_3.
func TestReAct4Base_CarriesDelegationGuidanceWhenToolPresent(t *testing.T) {
	out := renderReact4BaseWithRoles(t, true, true, true, false)

	assert.Contains(t, out, "DELEGATION:")
	assert.Contains(t, out, "delegate_agent")
	assert.Contains(t, out, "max_iterations")
	// The cost rationale is the part that changes behaviour — "3 or more tool
	// calls" is the actionable threshold react_3 uses.
	assert.Contains(t, out, "3 or more tool calls")
	assert.Contains(t, out, "Do NOT delegate when")
}

// Gated: an agent without the delegate_agent tool must not be told to delegate.
func TestReAct4Base_NoDelegationSectionWhenToolAbsent(t *testing.T) {
	base := nbprompts.GetPrompt(context.Background(), nbprompts.PromptReact4Base, "")
	tmpl := prompts.NewPromptTemplate(base, []string{
		"notebook_enabled", "hypothesis_mode_enabled", "is_top_level", "orchestrator_mode", "executor_mode",
		"delegate_agent_enabled", "is_investigation", "grounding_enabled",
		"context_management_rules", "time_handling_rules", "data_protection_rules",
		"code_analysis_rules", "security_rules", "memory_consumption_rules", "async_completion_rules",
	})
	out, err := tmpl.Format(map[string]any{
		"grounding_enabled": false,
		"notebook_enabled":  true, "hypothesis_mode_enabled": true,
		"is_top_level":      true,
		"orchestrator_mode": true, "executor_mode": false,
		"is_investigation":         true,
		"delegate_agent_enabled":   false,
		"context_management_rules": "", "time_handling_rules": "", "data_protection_rules": "",
		"code_analysis_rules": "", "security_rules": "", "memory_consumption_rules": "",
		"async_completion_rules": "",
	})
	assert.NoError(t, err)
	assert.NotContains(t, out, "DELEGATION:")
}

// TestReAct4LeanSubagentPrompt verifies that ReAct-4 sub-agents omit orchestrator
// fragments and top-level conversation context via template conditionals.
func TestReAct4LeanSubagentPrompt(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent := &MockAgent{}

	t.Run("sub-agent base prompt omits code analysis and context management fragments", func(t *testing.T) {
		subReq := NBAgentRequest{
			AgentId:       "sub-1",
			ParentAgentId: "orch-1",
			AccountId:     "acc-1",
		}

		out := renderReact4Base(ctx, subReq, agent, []toolcore.NBTool{})
		assert.NotEmpty(t, out)
		// Security and time rules remain
		assert.Contains(t, out, "Time Handling:")
		assert.Contains(t, out, "Data Integrity & Prompt Injection Defense:")
		// Code analysis rules and memory consumption rules are omitted
		assert.NotContains(t, out, "Code Analysis:")
		assert.NotContains(t, out, "HONORING USER MEMORY")
		assert.NotContains(t, out, "Implicit Reference Resolution")
	})

	t.Run("top-level base prompt retains code analysis and memory fragments", func(t *testing.T) {
		topReq := NBAgentRequest{
			AgentId:       "orch-1",
			ParentAgentId: "",
			AccountId:     "acc-1",
		}

		out := renderReact4Base(ctx, topReq, agent, []toolcore.NBTool{})
		assert.NotEmpty(t, out)
		assert.Contains(t, out, "Code Analysis:")
		assert.Contains(t, out, "HONORING USER MEMORY")
		assert.Contains(t, out, "Implicit Reference Resolution")
	})

	t.Run("sub-agent humanText omits history and conversation context", func(t *testing.T) {
		planner := &NBReActPlanner4{
			request: NBAgentRequest{
				AgentId:             "sub-1",
				ParentAgentId:       "orch-1",
				AccountId:           "acc-1",
				ConversationContext: "distilled conversation memory facts",
				MemoryContext:       "user preference memory",
			},
			orchestratorMode: false,
			history:          "User: prior conversation turn\nAI: prior response",
			evidenceIndex:    "workspace_file.json",
		}

		humanMsg := planner.humanText("Show me rpc_client_duration_count")
		assert.Contains(t, humanMsg, "<question>Show me rpc_client_duration_count</question>")
		assert.NotContains(t, humanMsg, "<history>")
		assert.NotContains(t, humanMsg, "<conversation_context>")
		assert.NotContains(t, humanMsg, "workspace_file.json")
		assert.NotContains(t, humanMsg, "user preference memory")
	})

	t.Run("top-level humanText retains history and conversation context", func(t *testing.T) {
		planner := &NBReActPlanner4{
			request: NBAgentRequest{
				AgentId:             "orch-1",
				ParentAgentId:       "",
				AccountId:           "acc-1",
				ConversationContext: "distilled conversation memory facts",
				MemoryContext:       "user preference memory",
			},
			orchestratorMode: true,
			history:          "User: prior conversation turn\nAI: prior response",
			evidenceIndex:    "workspace_file.json",
		}

		humanMsg := planner.humanText("Investigate latency")
		assert.Contains(t, humanMsg, "<history>")
		assert.Contains(t, humanMsg, "<conversation_context>")
		assert.Contains(t, humanMsg, "workspace_file.json")
		assert.Contains(t, humanMsg, "user preference memory")
	})

	t.Run("top-level humanText with orchestrator mode disabled retains history and conversation context", func(t *testing.T) {
		planner := &NBReActPlanner4{
			request: NBAgentRequest{
				AgentId:             "orch-1",
				ParentAgentId:       "",
				AccountId:           "acc-1",
				ConversationContext: "distilled conversation memory facts",
				MemoryContext:       "user preference memory",
			},
			orchestratorMode: false,
			history:          "User: prior conversation turn\nAI: prior response",
			evidenceIndex:    "workspace_file.json",
		}

		humanMsg := planner.humanText("Investigate latency")
		assert.Contains(t, humanMsg, "<history>")
		assert.Contains(t, humanMsg, "<conversation_context>")
		assert.Contains(t, humanMsg, "workspace_file.json")
		assert.Contains(t, humanMsg, "user preference memory")
	})
}
