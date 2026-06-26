package prompts_repo

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPlannerReact3Base_ShellGuidanceIsStrategyOnly pins the planner's
// shell-related block to strategy/routing content only (A content per
// the A/B separation):
//
//   - "DO NOT use shell_execute for code/repo work — use agent_code_2"
//   - "Specialized agents over raw shell" (kubectl/aws/gcp/azure)
//   - Cross-tool artifact handling (`<artifacts>` tag → grep on logs_*.txt)
//
// Tool mechanics (the B content — workspace cwd, /tmp/ scope,
// .nb_profile, no_matches semantic, credential auto-injection) belong
// in ShellTool.Description() instead, which auto-ships with the tool
// list whenever the tool is available. See
// TestShellToolDescription_CarriesWorkspaceContract in
// tools/tool_shell_test.go.
func TestPlannerReact3Base_ShellGuidanceIsStrategyOnly(t *testing.T) {
	// A content that must stay in the planner — routing decisions
	// across multiple tools that can't live in any single tool's
	// description.
	required := []string{
		"Specialized Agents vs. Shell",     // routing rule: prefer kubectl/aws/gcp/azure over raw shell
		"agent_code_2",                     // routing rule: code/repo work uses agent_code_2
		"Artifacts & Files",                // cross-tool: how shell consumes other tools' file output
		"`<artifacts>`",                    // names the contract surface other tools emit
		"Do the work in one shell command", // efficiency: chain steps in shell, don't iterate via per-call round-trips
		"not a sequence of separate",       // names the failure mode (round-trip per call) the rule prevents
		"your own reasoning between them",  // explicit LLM-POV framing (no "planner" jargon the LLM has no model of)
	}
	for _, snippet := range required {
		assert.Contains(t, plannerReactBase3, snippet,
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
		assert.NotContains(t, plannerReactBase3, snippet,
			"planner_react_3_base.txt should not carry tool-mechanics snippet %q — that lives in ShellTool.Description() now", snippet)
	}
}
