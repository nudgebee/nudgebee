package agents

import (
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// TestThinkTool_SchemaBlockAppearsWhenAllowlisted verifies the concrete
// happy path for the tool-schema-render feature: `think` in the
// allowlist causes its InputSchema to appear in the rendered tool block
// that the planner assembles for the LLM. This is the ONLY tool in the
// bootstrap default of LlmServerToolSchemaValidationTools, so if a future
// refactor changes gating or the renderer output, this test fires
// loudly.
func TestThinkTool_SchemaBlockAppearsWhenAllowlisted(t *testing.T) {
	prev := config.Config.LlmServerToolSchemaValidationTools
	config.Config.LlmServerToolSchemaValidationTools = "think"
	defer func() { config.Config.LlmServerToolSchemaValidationTools = prev }()

	think, err := toolcore.GetNBTool("test-account-think-render", tools.ThinkToolName)
	if err != false && think == nil {
		t.Skip("think tool not registered in this test binary — tools package import missing")
	}
	if think == nil {
		t.Skip("think tool factory returned nil")
	}

	rendered := core.RenderToolDescriptions([]toolcore.NBTool{think})

	// Framing: standard Tool Name + Type + Description block still emits.
	assert.Contains(t, rendered, "Tool Name: think")
	assert.Contains(t, rendered, "Type: tool")
	assert.Contains(t, rendered, "Description: Record reasoning that changes your next action.")

	// Schema block: the whole point of the allowlist path is that the
	// LLM sees the `reasoning` field as required. Regression guard.
	assert.Contains(t, rendered, "Input: object with fields:",
		"schema block must be appended when the tool is on the allowlist")
	assert.Contains(t, rendered, "- reasoning (string, required):",
		"schema block must name the required reasoning field so the LLM knows the JSON key")

	// Schema block is AFTER Description in the emitted string.
	descIdx := strings.Index(rendered, "Description:")
	schemaIdx := strings.Index(rendered, "Input: object with fields:")
	assert.True(t, 0 < descIdx && descIdx < schemaIdx,
		"schema block must render AFTER Description so the description sets context; got desc@%d schema@%d",
		descIdx, schemaIdx)
}

// TestThinkTool_SchemaBlockHiddenWhenNotAllowlisted pins the migration
// safety property: with think NOT in the allowlist (simulating the
// pre-migration state), the schema block MUST NOT leak into the
// rendered prompt. This is what protects other tools from schema-vs-
// Call() shape drift until they're individually reconciled + added.
func TestThinkTool_SchemaBlockHiddenWhenNotAllowlisted(t *testing.T) {
	prev := config.Config.LlmServerToolSchemaValidationTools
	// Empty allowlist -> gate closed for every tool.
	config.Config.LlmServerToolSchemaValidationTools = ""
	defer func() { config.Config.LlmServerToolSchemaValidationTools = prev }()

	think, ok := toolcore.GetNBTool("test-account-think-hidden", tools.ThinkToolName)
	if !ok || think == nil {
		t.Skip("think tool not registered in this test binary")
	}
	rendered := core.RenderToolDescriptions([]toolcore.NBTool{think})

	// Description survives — this is the base contract even without the flag.
	assert.Contains(t, rendered, "Tool Name: think")
	assert.Contains(t, rendered, "Description: Record reasoning that changes your next action.")

	// Schema block must NOT appear.
	assert.NotContains(t, rendered, "Input: object with fields:",
		"empty allowlist must suppress the schema block for every tool including think")
	assert.NotContains(t, rendered, "- reasoning (string, required)",
		"empty allowlist must suppress the reasoning field marker for think")
}

// TestThinkTool_WildcardAllowlist pins the "*" special-value behaviour
// — turning renderer on for every tool. Used for account-level
// experiments where an operator wants to try schema-render everywhere
// on a single dev account without listing all tool names.
func TestThinkTool_WildcardAllowlist(t *testing.T) {
	prev := config.Config.LlmServerToolSchemaValidationTools
	config.Config.LlmServerToolSchemaValidationTools = "*"
	defer func() { config.Config.LlmServerToolSchemaValidationTools = prev }()

	think, ok := toolcore.GetNBTool("test-account-think-wildcard", tools.ThinkToolName)
	if !ok || think == nil {
		t.Skip("think tool not registered in this test binary")
	}
	rendered := core.RenderToolDescriptions([]toolcore.NBTool{think})
	assert.Contains(t, rendered, "Input: object with fields:",
		`"*" allowlist must render schema for every tool including think`)
}
