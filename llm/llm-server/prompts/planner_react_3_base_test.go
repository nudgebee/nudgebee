package prompts

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPlannerReact3Base_ShellGuidanceIsStrategyOnly pins the planner's
// shell-related block to strategy/routing content only (A content per
// the A/B separation):
//
//   - "DO NOT use shell_execute for code/repo work — use code_analyzer"
//   - Active domain routing before generic specialist preference
//   - Cross-tool artifact handling (tool observations name the exact saved
//     file / the "Evidence already gathered" index → grep that exact file)
//
// Tool mechanics (the B content — workspace cwd, /tmp/ scope,
// stateless environment handling, no_matches semantic, credential auto-injection) belong
// in ShellTool.Description() instead, which auto-ships with the tool
// list whenever the tool is available. See
// TestShellToolDescription_CarriesWorkspaceContract in
// tools/tool_shell_test.go.
func TestPlannerReact3Base_ShellGuidanceIsStrategyOnly(t *testing.T) {
	// A content that must stay in the planner — routing decisions
	// across multiple tools that can't live in any single tool's
	// description.
	required := []string{
		"Follow explicit tool-choice instructions",                   // concrete system-prompt precedence, without inferred ownership
		"Never delegate to the agent currently handling the request", // aliases must not cause self-delegation
		"code_analyzer",                         // routing rule: code/repo work uses code_analyzer
		"Artifacts & Files",                     // cross-tool: how shell consumes other tools' file output
		"Evidence already gathered",             // points at the real file-ref channel (the evidence index), not a dead <artifacts> tag
		"never guess or reconstruct a filename", // anti-hallucination: use the exact name, don't invent one
		"EXECUTION EFFICIENCY & PARALLELISM",    // centralized efficiency policy shared across tool types
		"a bounded script",                      // efficiency: chain mechanical shell steps without extra model turns
		"require no model interpretation",       // explicit boundary between mechanical work and model reasoning
	}
	for _, snippet := range required {
		assert.Contains(t, GetPromptForTest(PromptReact3Base), snippet,
			"planner_react_3_base.txt is missing required cross-tool strategy snippet %q", snippet)
	}

	// B content that must NOT be in the planner — these describe how
	// shell_execute itself works and belong in ShellTool.Description().
	// Re-introducing them here creates two sources of truth and the
	// dual-source drift that #32007 originally chased.
	forbidden := []string{
		"per-conversation directory",
		"`.nb_profile`",
		"no_matches",
		"auto-injected",
		"aws configure",
		"Workspace state persists",
		"Cross-conversation caveat",
	}
	for _, snippet := range forbidden {
		assert.NotContains(t, GetPromptForTest(PromptReact3Base), snippet,
			"planner_react_3_base.txt should not carry tool-mechanics snippet %q — that lives in ShellTool.Description() now", snippet)
	}

	assert.NotContains(t, GetPromptForTest(PromptReact3Base), "Specialized Agents vs. Shell",
		"the duplicate absolute shell rule conflicts with agent-specific routing")
	assert.NotContains(t, GetPromptForTest(PromptReact3Base), "Prefer specialized agents",
		"generic delegation must not override an active agent's domain routing")
}

func TestPlannerReact3Base_DelegationReusesResolvedResourceIdentity(t *testing.T) {
	prompt := GetPromptForTest(PromptReact3Base)
	assert.Contains(t, prompt, "reuse its exact provider-native identity and scope")
	assert.Contains(t, prompt, "do not make the specialist rediscover it")
	assert.Contains(t, prompt, "input schema exposes `resolved_targets`")
	assert.Contains(t, prompt, "kind + namespace + exact pod/workload names")
	assert.Contains(t, prompt, "account/project/subscription + region + exact name/ID/ARN")
}

func TestPlannerReact3Base_ParallelismUsesInvocationDependencies(t *testing.T) {
	prompt := GetPromptForTest(PromptReact3Base)
	assert.Contains(t, prompt, "same tool with different inputs")
	assert.Contains(t, prompt, "If two or more such calls exist, emit them as siblings")
	assert.Contains(t, prompt, "A parallel batch is one step")
	assert.Contains(t, prompt, "conflicting effects on the same target")
	assert.Contains(t, prompt, "another planner turn only at a reasoning boundary")
	assert.NotContains(t, prompt, "read-only")
}

func TestPlannerReact3Base_NotebookIsInlineMetadataOutsideToolInput(t *testing.T) {
	prompt := GetPromptForTest(PromptReact3Base)
	assert.Contains(t, prompt, "inline response metadata parsed by the planner, NOT a tool or shell command")
	assert.Contains(t, prompt, "inside the same `<thought_action>` block")
	assert.Contains(t, prompt, "It is not an `<action>` and must never appear inside `<tool_input>`")
	assert.Contains(t, prompt, "step or parallel evidence batch you are executing THIS turn")
	assert.Contains(t, prompt, "Independent checks that test the same scope or hypothesis belong in one parallel batch")
}

func TestPlannerReact3CustomBase_PreservesProtocolAndPromotesFanout(t *testing.T) {
	prompt := GetPromptForTest(PromptReact3CustomBase)

	for _, snippet := range []string{
		"<thought_action>",
		"<final_answer>",
		"<actions>",
		"same tool with different",
		"Shared purpose, shared target, or use of the same",
		"skip discovery",
		"fan out the cheapest independent checks",
		"generic approach, not a requirement",
		"agent instructions supplied after this message",
		"When no model interpretation is required between mechanical steps",
		"background jobs plus `wait`",
	} {
		assert.Contains(t, prompt, snippet)
	}
	assert.NotContains(t, prompt, "read-only")
}

func TestPlannerReact3CustomBase_DoesNotCarryBuiltInAgentPolicy(t *testing.T) {
	prompt := GetPromptForTest(PromptReact3CustomBase)

	for _, snippet := range []string{
		"kubectl_execute",
		"resource_search_execute",
		"Specialized Agents vs. Shell",
		"remediation tool",
		"code_analyzer",
		"Avoid JSON/YAML for Global Queries",
		"watch_resource",
	} {
		assert.NotContains(t, prompt, snippet)
	}

	assert.Less(t, len(prompt), 12000, "custom-agent base should remain compact")
}
