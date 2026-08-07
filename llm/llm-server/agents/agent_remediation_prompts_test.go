package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// Regression gate for the remediation agent ahead of the base-prompt +
// attachable-blocks restructuring: the investigation-mode prompt and toolset
// must come out of that refactor unchanged. Three layers:
//
//   - Golden snapshot of the assembled NBAgentPrompt (both shell-tool config
//     branches). The executor consumes this struct as-is, so struct-identical
//     means prompt-identical for the parts the refactor touches.
//   - Guardrail-placement assertions: the execution-safety rules are
//     deliberately repeated across Instructions, Constraints, AND the execute
//     tool's usage notes. A refactor that keeps the words but collapses the
//     placements weakens the prompt without failing a plain contains-check,
//     so each placement is asserted per section.
//   - Toolset + agent-shape invariants the planner relies on.
//
// Regenerate the snapshots with:
//
//	go test ./agents -run TestRemediationPromptGolden -update-golden
//
// (reuses the package-level -update-golden flag declared in
// agent_workflow_builder_prompts_test.go)

// remediationInvestigationRequest is the fixed investigation-shaped request the
// snapshots are rendered with. Once prompt assembly becomes mode-aware, an
// investigation request is one with no recommendation reference — this fixture
// must keep producing the snapshot below, byte for byte.
func remediationInvestigationRequest() core.NBAgentRequest {
	return core.NBAgentRequest{Query: "INVESTIGATION_PLACEHOLDER"}
}

func remediationPromptJSON(t *testing.T, shellToolEnabled bool) string {
	t.Helper()
	prev := config.Config.LlmServerShellToolEnabled
	config.Config.LlmServerShellToolEnabled = shellToolEnabled
	t.Cleanup(func() { config.Config.LlmServerShellToolEnabled = prev })

	prompt := RemediationAgent{accountId: "ACCOUNT_PLACEHOLDER"}.GetSystemPrompt(nil, remediationInvestigationRequest())
	b, err := json.MarshalIndent(prompt, "", "  ")
	require.NoError(t, err)
	return string(b) + "\n"
}

func TestRemediationPromptGolden(t *testing.T) {
	dir := filepath.Join("testdata", "prompt_golden")
	cases := map[string]bool{
		"remediation_prompt":            false,
		"remediation_prompt_shell_tool": true,
	}

	for name, shellToolEnabled := range cases {
		t.Run(name, func(t *testing.T) {
			got := remediationPromptJSON(t, shellToolEnabled)
			path := filepath.Join(dir, name+".json")

			if *updateGolden {
				require.NoError(t, os.MkdirAll(dir, 0o755))
				require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
				t.Logf("wrote golden %s (%d bytes)", path, len(got))
				return
			}

			want, err := os.ReadFile(path)
			require.NoError(t, err, "missing golden file %s — run: go test ./agents -run TestRemediationPromptGolden -update-golden", path)
			assert.Equal(t, strings.ReplaceAll(string(want), "\r\n", "\n"), strings.ReplaceAll(got, "\r\n", "\n"),
				"%s changed; if the change is intentional re-run with -update-golden and review the diff before committing", name)
		})
	}
}

// TestRemediationGuardrailPlacement pins each safety guardrail to the section
// it lives in. The redundancy across sections is intentional prompt design:
// Instructions carry the workflow framing, Constraints are the planner's hard
// rules, and the execute tool's usage notes are what the model re-reads at the
// moment it considers calling the tool.
func TestRemediationGuardrailPlacement(t *testing.T) {
	cases := map[string]bool{
		"without_shell_tool": false,
		"with_shell_tool":    true,
	}

	for name, shellTool := range cases {
		t.Run(name, func(t *testing.T) {
			prev := config.Config.LlmServerShellToolEnabled
			config.Config.LlmServerShellToolEnabled = shellTool
			t.Cleanup(func() { config.Config.LlmServerShellToolEnabled = prev })

			prompt := RemediationAgent{accountId: "ACCOUNT_PLACEHOLDER"}.GetSystemPrompt(nil, remediationInvestigationRequest())
			instructions := strings.Join(prompt.Instructions, "\n")
			constraints := strings.Join(prompt.Constraints, "\n")
			executeUsage := strings.Join(prompt.ToolUsage[tools.ToolRemediationExecute], "\n")
			generateUsage := strings.Join(prompt.ToolUsage[tools.ToolRemediationGenerate], "\n")

			// Approval gate — must hold in all three placements.
			assert.Contains(t, instructions, "NEVER execute commands without explicit user approval")
			assert.Contains(t, constraints, "NEVER execute commands without explicit user approval")
			assert.Contains(t, executeUsage, "Only use AFTER user explicitly approves the plan")

			// Plan-first, both placements.
			assert.Contains(t, instructions, "ALWAYS present the plan first")
			assert.Contains(t, constraints, "ALWAYS present the plan first and ask for confirmation")

			// Tense discipline — the created-vs-executed confusion rules.
			assert.Contains(t, instructions, "NEVER say you executed something when you only created a plan")

			// Every plan must carry a rollback.
			assert.Contains(t, instructions, "Include rollback plan in every remediation plan")

			// The generate tool must keep its needed-at-all gate (healthy systems and
			// informational queries produce no plan).
			assert.Contains(t, generateUsage, "verify that remediation is actually needed")
		})
	}
}

// TestRemediationToolsetInvariance pins the exact toolset and the agent-shape
// contract the planner relies on. The restructuring must not add, drop, or
// reorder tools for the investigation flow.
func TestRemediationToolsetInvariance(t *testing.T) {
	agent := RemediationAgent{accountId: "ACCOUNT_PLACEHOLDER"}

	supported := agent.GetSupportedTools(nil)
	names := make([]string, 0, len(supported))
	for _, tool := range supported {
		names = append(names, tool.Name())
	}
	assert.Equal(t, []string{tools.ToolRemediationGenerate, tools.ToolRemediationExecute}, names)

	// Shape invariants: multi-turn conversation management (no auto-summary),
	// ReAct planning, watch-capable, reasoning tier.
	assert.Equal(t, "", agent.GetSummaryToolName())
	assert.Equal(t, core.AgentPlannerTypeReAct, agent.GetPlannerType())
	assert.True(t, agent.IsWatchCapable())
	assert.Equal(t, core.ModelTierReasoning, agent.GetModelCategory())
	assert.ElementsMatch(t, []string{"Remediation", "AutoFix", "Fixer"}, agent.GetNameAliases())

	// The agent-as-tool registration other agents delegate through.
	agentTool, found := toolcore.GetNBTool("ACCOUNT_PLACEHOLDER", RemediationAgentName)
	require.True(t, found, "remediation agent must stay registered as a delegable tool")
	assert.Contains(t, agentTool.Description(), "Remediation Expert")
}
