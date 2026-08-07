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

// renderGcpPromptForTest renders the embedded gcp_debug prompt the same way
// renderGcpDebugReactPrompt does, without needing a RequestContext.
func renderGcpPromptForTest(t *testing.T, useDirect bool) string {
	t.Helper()
	raw := prompts.GetPrompt(context.Background(), prompts.PromptGcpOrchestrator, "")
	require.NotEmpty(t, raw)
	tmpl, err := template.New("gcp_debug").Option("missingkey=zero").Parse(raw)
	require.NoError(t, err)
	var buf strings.Builder
	require.NoError(t, tmpl.Execute(&buf, map[string]any{"use_gcp_cli_direct": useDirect}))
	return buf.String()
}

// TestGcpDebugPrompt_DirectVsDelegating pins the v1/v2 branching: the direct (v2)
// render drops the `gcp` sub-agent routing and runs gcloud_execute; the delegating (v1)
// render keeps the sub-agent. Both must render with no leftover template tokens.
func TestGcpDebugPrompt_DirectVsDelegating(t *testing.T) {
	direct := renderGcpPromptForTest(t, true)
	delegating := renderGcpPromptForTest(t, false)

	assert.NotContains(t, direct, "{{", "direct render leaked template tokens")
	assert.NotContains(t, delegating, "{{", "delegating render leaked template tokens")

	// Direct: no delegation to the `gcp` sub-agent; example actions use gcloud_execute.
	assert.NotContains(t, direct, "<tool_name>gcp</tool_name>")
	assert.NotContains(t, direct, "the `gcp` sub-agent")
	assert.Contains(t, direct, "<tool_name>gcloud_execute</tool_name>")
	assert.Contains(t, direct, "via `gcloud_execute`")

	// Delegating: keeps the `gcp` sub-agent routing.
	assert.Contains(t, delegating, "<tool_name>gcp</tool_name>")
	assert.Contains(t, delegating, "the `gcp` sub-agent")
}

// TestGcpOrchestratorMode_ToolSet pins that direct mode drops the `gcp` sub-agent from
// the tool-name list while delegating keeps it.
func TestGcpOrchestratorMode_ToolSet(t *testing.T) {
	// gcp_orchestrator_2 is the always-direct eval handle.
	direct := newGcpOrchestratorAgentNamed("acct", AgentGcpOrchestrator2Name, true).(*GcpOrchestratorAgent)
	deleg := newGcpOrchestratorAgentNamed("acct", AgentGcpOrchestratorName, false).(*GcpOrchestratorAgent)
	assert.True(t, direct.useGcpCliDirect)
	assert.False(t, deleg.useGcpCliDirect)
	assert.Equal(t, AgentGcpOrchestrator2Name, direct.GetName())
	assert.Contains(t, direct.GetNameAliases(), "gcp_debug_2")
	assert.Contains(t, deleg.GetNameAliases(), "gcp_debug")
}

// TestGcpLeanAgent pins the lean eval handle + prompt: distinct name/aliases, direct
// tool set, and the lean prompt carries the loop-shape principles + artifact carve-out.
func TestGcpLeanAgent(t *testing.T) {
	lean := newGcpLeanAgentNamed("acct", AgentGcpOrchestratorLeanName)
	assert.Equal(t, AgentGcpOrchestratorLeanName, lean.GetName())
	assert.Contains(t, lean.GetNameAliases(), "gcp_debug_lean")

	// Under the primary name it answers to the primary's aliases.
	primary := newGcpLeanAgentNamed("acct", AgentGcpOrchestratorName)
	assert.Contains(t, primary.GetNameAliases(), "gcp_debug")

	p := prompts.GetPrompt(context.Background(), prompts.PromptGcpLean, "")
	assert.NotEmpty(t, p)
	assert.Contains(t, p, "gcloud_execute")                   // direct CLI
	assert.Contains(t, p, "search_tools")                     // on-demand specialist discovery
	assert.Contains(t, p, "MECHANISM")                        // don't stop at surface value
	assert.Contains(t, p, "must actually SURVIVE")            // durable-fix principle
	assert.Contains(t, p, "Not every request is a diagnosis") // artifact carve-out
	assert.NotContains(t, p, "Causality Chain")               // lean has no rigid template
}

// TestGcpLeanReducedCore pins the reduced cloud core: the lean agent preloads the gcloud
// CLI + SDG + events + resource_search + recommendations + delegate + search_tools, and drops
// the specialist agents (databases, kubectl, the gcp sub-agent) — reached on-demand via search_tools.
func TestGcpLeanReducedCore(t *testing.T) {
	names := cloudLeanCoreToolNames(tools.ToolExecuteGcpCliCommand)
	assert.Contains(t, names, tools.ToolExecuteGcpCliCommand)
	assert.Contains(t, names, SearchToolsToolName)
	assert.Contains(t, names, ServiceDependencyGraph)
	assert.Contains(t, names, ResourceSearchAgentName) // cross-cloud inventory search, mirrors k8s lean core
	assert.Contains(t, names, DelegateAgentToolName)
	// Specialists are dropped from context (reached via search_tools + delegate_agent).
	assert.NotContains(t, names, GcpAgentName)
	assert.NotContains(t, names, PostgresAgentName)
	assert.NotContains(t, names, KubectlAgentName)
}
