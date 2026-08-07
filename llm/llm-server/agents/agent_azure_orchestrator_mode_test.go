package agents

import (
	"context"
	"strings"
	"testing"
	"text/template"

	"nudgebee/llm/prompts"
	"nudgebee/llm/tools"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// renderAzurePromptForTest renders the embedded azure_debug prompt the same way
// renderAzureDebugReactPrompt does, without needing a RequestContext.
func renderAzurePromptForTest(t *testing.T, useDirect bool) string {
	t.Helper()
	raw := prompts.GetPrompt(context.Background(), prompts.PromptAzureOrchestrator, "")
	require.NotEmpty(t, raw)
	tmpl, err := template.New("azure_debug").Option("missingkey=zero").Parse(raw)
	require.NoError(t, err)
	var buf strings.Builder
	require.NoError(t, tmpl.Execute(&buf, map[string]any{"use_azure_cli_direct": useDirect}))
	return buf.String()
}

// TestAzureDebugPrompt_DirectVsDelegating pins the v1/v2 branching: the direct (v2)
// render drops the `azure` sub-agent routing and runs azure_execute; the delegating (v1)
// render keeps the sub-agent. Both must render with no leftover template tokens.
func TestAzureDebugPrompt_DirectVsDelegating(t *testing.T) {
	direct := renderAzurePromptForTest(t, true)
	delegating := renderAzurePromptForTest(t, false)

	assert.NotContains(t, direct, "{{", "direct render leaked template tokens")
	assert.NotContains(t, delegating, "{{", "delegating render leaked template tokens")

	// Direct: no delegation to the `azure` sub-agent; example actions use azure_execute.
	assert.NotContains(t, direct, "<tool_name>azure</tool_name>")
	assert.NotContains(t, direct, "the `azure` sub-agent")
	assert.Contains(t, direct, "<tool_name>azure_execute</tool_name>")
	assert.Contains(t, direct, "via `azure_execute`")

	// Delegating: keeps the `azure` sub-agent routing.
	assert.Contains(t, delegating, "<tool_name>azure</tool_name>")
	assert.Contains(t, delegating, "the `azure` sub-agent")
}

// TestAzureOrchestratorMode_ToolSet pins that direct mode drops the `azure` sub-agent from
// the tool-name list while delegating keeps it.
func TestAzureOrchestratorMode_ToolSet(t *testing.T) {
	// azure_orchestrator_2 is the always-direct eval handle.
	direct := newAzureOrchestratorAgentNamed("acct", AgentAzureOrchestrator2Name, true).(*AzureOrchestratorAgent)
	deleg := newAzureOrchestratorAgentNamed("acct", AgentAzureOrchestratorName, false).(*AzureOrchestratorAgent)
	assert.True(t, direct.useAzureCliDirect)
	assert.False(t, deleg.useAzureCliDirect)
	assert.Equal(t, AgentAzureOrchestrator2Name, direct.GetName())
	assert.Contains(t, direct.GetNameAliases(), "azure_debug_2")
	assert.Contains(t, deleg.GetNameAliases(), "azure_debug")
}

// TestAzureLeanAgent pins the lean eval handle + prompt: distinct name/aliases, direct
// tool set, and the lean prompt carries the loop-shape principles + artifact carve-out.
func TestAzureLeanAgent(t *testing.T) {
	lean := newAzureLeanAgentNamed("acct", AgentAzureOrchestratorLeanName)
	assert.Equal(t, AgentAzureOrchestratorLeanName, lean.GetName())
	assert.Contains(t, lean.GetNameAliases(), "azure_debug_lean")

	// Under the primary name it answers to the primary's aliases.
	primary := newAzureLeanAgentNamed("acct", AgentAzureOrchestratorName)
	assert.Contains(t, primary.GetNameAliases(), "azure_debug")

	p := prompts.GetPrompt(context.Background(), prompts.PromptAzureLean, "")
	assert.NotEmpty(t, p)
	assert.Contains(t, p, "azure_execute")                    // direct CLI
	assert.Contains(t, p, "search_tools")                     // on-demand specialist discovery
	assert.Contains(t, p, "MECHANISM")                        // don't stop at surface value
	assert.Contains(t, p, "must actually SURVIVE")            // durable-fix principle
	assert.Contains(t, p, "Not every request is a diagnosis") // artifact carve-out
	assert.NotContains(t, p, "Causality Chain")               // lean has no rigid template
}

// TestAzureLeanReducedCore pins the reduced cloud core: the lean agent preloads the az
// CLI + SDG + events + recommendations + delegate + search_tools, and drops the specialist
// agents (databases, kubectl, the azure sub-agent) — reached on-demand via search_tools.
func TestAzureLeanReducedCore(t *testing.T) {
	names := cloudLeanCoreToolNames(tools.ToolExecuteAzureCliCommand)
	assert.Contains(t, names, tools.ToolExecuteAzureCliCommand)
	assert.Contains(t, names, SearchToolsToolName)
	assert.Contains(t, names, ServiceDependencyGraph)
	assert.Contains(t, names, ResourceSearchAgentName) // cross-cloud inventory search, mirrors k8s lean core
	assert.Contains(t, names, DelegateAgentToolName)
	// Specialists are dropped from context (reached via search_tools + delegate_agent).
	assert.NotContains(t, names, AzureAgentName)
	assert.NotContains(t, names, PostgresAgentName)
	assert.NotContains(t, names, KubectlAgentName)
}
