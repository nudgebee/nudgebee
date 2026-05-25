package agents

import (
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/prompts"
)

// Repro for issue #30120: the WorkflowAgent's "Configs" instruction contained
// a literal `{{ Configs.key_name }}` that Go's text/template parsed as a call
// to an undefined function `Configs`, making GetPromptTemplate.Format() fail
// silently. The empty systemMessage was then sent to Bedrock Converse, which
// rejected the request with `system[N].text` length-violation 400.
//
// The fix escapes the `{{ ... }}` literal in the instruction so Format renders
// the literal automation syntax the LLM expects.
func TestWorkflowAgent_SystemPromptRendersWithoutTemplateError(t *testing.T) {
	t.Parallel()

	agent := newWorkflowAgent("test_account")
	ctx := security.NewRequestContextForTenantAccountAdmin("t", "u", []string{"test_account"})

	basePrompt := agent.GetSystemPrompt(ctx, core.NBAgentRequest{AccountId: "test_account"})

	// Render via the same path the executor uses (executor.go:~428).
	rendered, err := core.GetPromptTemplate(basePrompt, core.NBAgentRequest{AccountId: "test_account"}, core.AgentPlannerTypeReAct3).
		Format(map[string]any{"history": ""})

	require.NoError(t, err, "Format must not fail on the WorkflowAgent's instructions")
	require.NotEmpty(t, strings.TrimSpace(rendered), "rendered system prompt must not be empty")

	// The LLM still needs to see the literal automation syntax `{{ Configs.key_name }}`.
	assert.Contains(t, rendered, "{{ Configs.key_name }}",
		"rendered prompt should contain the literal automation syntax the LLM expects")

	// Second-pass behaviour: planner_react_3.go must NOT route the already-rendered
	// systemMessage through Go's text/template a second time, otherwise literal
	// `{{ Configs.key_name }}` is re-parsed and FormatMessages fails with
	// `formatting system message: template parse failure: function "Configs" not defined`.
	// LiteralSystemMessage is the wrapper that bypasses templating for already-rendered content.
	secondRender, err := core.LiteralSystemMessage{Content: rendered}.FormatMessages(map[string]any{})
	require.NoError(t, err, "LiteralSystemMessage must not re-render rendered content")
	require.Len(t, secondRender, 1)
	assert.Contains(t, secondRender[0].GetContent(), "{{ Configs.key_name }}",
		"the literal automation syntax must reach the LLM unchanged")

	// Sanity: confirm the old NewSystemMessagePromptTemplate path would still fail on the
	// rendered content — this is what the LiteralSystemMessage swap protects against.
	_, errOld := prompts.NewSystemMessagePromptTemplate(rendered, []string{}).
		FormatMessages(map[string]any{})
	require.Error(t, errOld, "guardrail: pre-fix path must still fail so we notice if behaviour drifts")
	assert.Contains(t, errOld.Error(), `function "Configs" not defined`)
}
