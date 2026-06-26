package agents

import (
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// TestShellTool_DescriptionReachesRenderedPrompt is the integration test
// that the workspace contract — defined once in ShellTool.Description() —
// actually survives the planner's tool-list rendering and lands in the
// system prompt the LLM sees. It complements two narrower tests:
//
//   - TestShellToolDescription_CarriesWorkspaceContract (in
//     tools/tool_shell_test.go) pins the description content alone.
//   - TestPlannerReact3Base_ShellGuidanceIsStrategyOnly (in
//     agents/prompts_repo/planner_react_3_base_test.go) pins the
//     planner-level A-content strategy block.
//
// This test connects the two by exercising core.RenderToolDescriptions
// — the exact code path every planner uses to build the
// `{{.tool_descriptions}}` template var — against a real ShellTool
// instance. If a future refactor renames Description(), changes the
// renderer's format, or drops the tool from the planner's tool-list
// assembly, this test fails loudly.
//
// The contract checks here are intentionally narrower than the
// description-content test: we only assert the load-bearing snippets
// the LLM most needs (per-conversation cwd, /tmp scope, no_matches
// semantic, credential auto-injection). Full-coverage of every
// description line lives in the dedicated tool_shell_test.go test
// where regressions are easier to localize.
func TestShellTool_DescriptionReachesRenderedPrompt(t *testing.T) {
	shell := tools.ShellTool{AccountId: "test-account-rendered-prompt"}
	rendered := core.RenderToolDescriptions([]toolcore.NBTool{shell})

	// Tool framing — what the renderer wraps every Description() in.
	// If this drops, the entire tool-list rendering is broken and every
	// tool — not just shell — silently disappears from the prompt.
	frameRequired := map[string]string{
		"Tool Name: shell_execute": "the renderer must emit the tool name header — without it the planner cannot reference the tool by name",
		"Description: ":            "the renderer must emit the Description: prefix — without it the tool description is unreachable",
	}
	for snippet, why := range frameRequired {
		assert.Contains(t, rendered, snippet,
			"rendered tool list is missing framing snippet %q — %s", snippet, why)
	}

	// Workspace contract — the load-bearing facts that must survive the
	// trip from ShellTool.Description() → reActPromptToolDescriptions →
	// {{.tool_descriptions}} template var → rendered system prompt.
	contractRequired := map[string]string{
		"per-conversation directory":                          "the cwd-scope fact must reach the LLM via the rendered tool list",
		"shared with other conversations on the same account": "the /tmp leak warning must reach the LLM via the rendered tool list",
		"no_matches":    "the grep/find/jq exit-1 success semantic must reach the LLM via the rendered tool list",
		"auto-injected": "the cloud + GITHUB_TOKEN credential injection rule must reach the LLM via the rendered tool list",
		"`.nb_profile`": "the env-persistence pattern must reach the LLM via the rendered tool list",
	}
	for snippet, why := range contractRequired {
		assert.Contains(t, rendered, snippet,
			"rendered tool list is missing workspace contract snippet %q — %s", snippet, why)
	}

	// The pre-fix wrong example must NEVER make it into the rendered
	// prompt — that was the root lie ShellTool.Description() shipped
	// before PR #32007.
	assert.NotContains(t, rendered, "/app/workspaces",
		"rendered tool list reintroduced the pre-fix `/app/workspaces` example — execution_handler.go runs each shell_execute with cwd = baseDir/<conversationId>, not /app")

	// Sanity: the description occupies a non-trivial fraction of the
	// rendered output. Catches a regression where Description() returns
	// "" or the renderer truncates.
	descLen := len(shell.Description())
	if descLen < 500 {
		t.Fatalf("ShellTool.Description() returned suspiciously short text (%d chars) — workspace contract was probably gutted", descLen)
	}
	if !strings.Contains(rendered, shell.Description()) {
		t.Fatalf("rendered tool list does not contain the full ShellTool.Description() verbatim — renderer is dropping or mutating the description")
	}
}
