package agents

import (
	"context"
	"strings"
	"testing"
	"text/template"

	"nudgebee/llm/prompts"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// renderAwsPromptForTest renders the embedded aws_debug prompt the same way
// renderAwsDebugReactPrompt does, without needing a RequestContext.
func renderAwsPromptForTest(t *testing.T, useDirect bool) string {
	t.Helper()
	raw := prompts.GetPrompt(context.Background(), prompts.PromptAwsOrchestrator, "")
	require.NotEmpty(t, raw)
	tmpl, err := template.New("aws_debug").Option("missingkey=zero").Parse(raw)
	require.NoError(t, err)
	var buf strings.Builder
	require.NoError(t, tmpl.Execute(&buf, map[string]any{"use_aws_cli_direct": useDirect}))
	return buf.String()
}

// TestAwsDebugPrompt_DirectVsDelegating pins the v1/v2 branching: the direct (v2)
// render drops the `aws` sub-agent routing and runs aws_execute; the delegating (v1)
// render keeps the sub-agent. Both must render with no leftover template tokens.
func TestAwsDebugPrompt_DirectVsDelegating(t *testing.T) {
	direct := renderAwsPromptForTest(t, true)
	delegating := renderAwsPromptForTest(t, false)

	assert.NotContains(t, direct, "{{", "direct render leaked template tokens")
	assert.NotContains(t, delegating, "{{", "delegating render leaked template tokens")

	// Direct: no delegation to the `aws` sub-agent; example actions use aws_execute.
	assert.NotContains(t, direct, "<tool_name>aws</tool_name>")
	assert.NotContains(t, direct, "use the `aws` sub-agent instead")
	assert.Contains(t, direct, "<tool_name>aws_execute</tool_name>")
	assert.Contains(t, direct, "run the AWS CLI directly for ALL AWS resource inspection")

	// Delegating: keeps the `aws` sub-agent routing.
	assert.Contains(t, delegating, "<tool_name>aws</tool_name>")
	assert.Contains(t, delegating, "**aws:** EC2, RDS, Lambda")

	// Both keep observability delegated.
	assert.Contains(t, direct, "aws_observability")
	assert.Contains(t, delegating, "aws_observability")
}

// TestAwsOrchestratorMode_ToolSet pins that direct mode drops the `aws` sub-agent from
// the tool-name list while delegating keeps it (both keep aws_execute).
func TestAwsOrchestratorMode_ToolSet(t *testing.T) {
	// aws_orchestrator_2 is the always-direct eval handle.
	direct := newAwsOrchestratorAgentNamed("acct", AgentAwsOrchestrator2Name, true).(*AwsOrchestratorAgent)
	deleg := newAwsOrchestratorAgentNamed("acct", AgentAwsOrchestratorName, false).(*AwsOrchestratorAgent)
	assert.True(t, direct.useAwsCliDirect)
	assert.False(t, deleg.useAwsCliDirect)
	assert.Equal(t, AgentAwsOrchestrator2Name, direct.GetName())
	assert.Contains(t, direct.GetNameAliases(), "aws_debug_2")
	assert.Contains(t, deleg.GetNameAliases(), "aws_debug")
}

// TestAwsLeanAgent pins the lean eval handle + prompt: distinct name/aliases, direct
// tool set, and the lean prompt carries the loop-shape principles + artifact carve-out.
func TestAwsLeanAgent(t *testing.T) {
	lean := newAwsLeanAgentNamed("acct", AgentAwsOrchestratorLeanName)
	assert.Equal(t, AgentAwsOrchestratorLeanName, lean.GetName())
	assert.Contains(t, lean.GetNameAliases(), "aws_debug_lean")

	// Under the primary name it answers to the primary's aliases.
	primary := newAwsLeanAgentNamed("acct", AgentAwsOrchestratorName)
	assert.Contains(t, primary.GetNameAliases(), "aws_debug")

	p := prompts.GetPrompt(context.Background(), prompts.PromptAwsLean, "")
	assert.NotEmpty(t, p)
	assert.Contains(t, p, "aws_execute")                      // direct CLI
	assert.Contains(t, p, "MECHANISM")                        // don't stop at surface value
	assert.Contains(t, p, "must actually SURVIVE")            // durable-fix principle
	assert.Contains(t, p, "Not every request is a diagnosis") // artifact carve-out
	assert.NotContains(t, p, "Causality Chain")               // lean has no rigid template
}
