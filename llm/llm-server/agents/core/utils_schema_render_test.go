package core

import (
	"strings"
	"testing"
	"unicode/utf8"

	"nudgebee/llm/config"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// TestRenderInputSchema_Empty pins the return-empty-string behavior for a
// tool with no properties. Prevents the renderer from spuriously appending
// "Input: object with fields:" (an empty block) to tools like
// tool_datadog_hosts that have no input schema.
func TestRenderInputSchema_Empty(t *testing.T) {
	assert.Equal(t, "", renderInputSchema(toolcore.ToolSchema{}))
	assert.Equal(t, "", renderInputSchema(toolcore.ToolSchema{
		Type: toolcore.ToolSchemaTypeObject,
	}))
}

// TestRenderInputSchema_SingleRequiredField pins the canonical shape for a
// tool like `think` (one required field with a description).
func TestRenderInputSchema_SingleRequiredField(t *testing.T) {
	schema := toolcore.ToolSchema{
		Type: toolcore.ToolSchemaTypeObject,
		Properties: map[string]toolcore.ToolSchemaProperty{
			"reasoning": {
				Type:        toolcore.ToolSchemaTypeString,
				Description: "The conflict, stuck point, candidate root causes, or tool error you face.",
			},
		},
		Required: []string{"reasoning"},
	}
	got := renderInputSchema(schema)
	assert.Equal(t, "Input: object with fields:\n  - reasoning (string, required): The conflict, stuck point, candidate root causes, or tool error you face.", got)
}

// TestRenderInputSchema_MixedRequiredOptional confirms the required/optional
// marker is per-field and matches schema.Required.
func TestRenderInputSchema_MixedRequiredOptional(t *testing.T) {
	schema := toolcore.ToolSchema{
		Properties: map[string]toolcore.ToolSchemaProperty{
			"command": {
				Type:        toolcore.ToolSchemaTypeString,
				Description: "Shell command to execute.",
			},
			"work_dir": {
				Type:        toolcore.ToolSchemaTypeString,
				Description: "Working directory.",
			},
		},
		Required: []string{"command"},
	}
	got := renderInputSchema(schema)
	assert.Contains(t, got, "- command (string, required): Shell command to execute.")
	assert.Contains(t, got, "- work_dir (string, optional): Working directory.")
}

// TestRenderInputSchema_DeterministicOrdering pins alphabetical property
// ordering WITHIN each group (required first, then optional) so the
// rendered prompt is byte-stable across runs — matters for prompt caching
// (LLM providers cache on exact prefix) and for diff-based review.
// All-optional case: pure alphabetical.
func TestRenderInputSchema_DeterministicOrdering(t *testing.T) {
	schema := toolcore.ToolSchema{
		Properties: map[string]toolcore.ToolSchemaProperty{
			"zebra":  {Type: toolcore.ToolSchemaTypeString},
			"apple":  {Type: toolcore.ToolSchemaTypeString},
			"mango":  {Type: toolcore.ToolSchemaTypeString},
			"banana": {Type: toolcore.ToolSchemaTypeString},
			"cherry": {Type: toolcore.ToolSchemaTypeString},
		},
	}
	got := renderInputSchema(schema)
	appleIdx := strings.Index(got, "- apple")
	bananaIdx := strings.Index(got, "- banana")
	cherryIdx := strings.Index(got, "- cherry")
	mangoIdx := strings.Index(got, "- mango")
	zebraIdx := strings.Index(got, "- zebra")
	assert.True(t, 0 <= appleIdx && appleIdx < bananaIdx && bananaIdx < cherryIdx && cherryIdx < mangoIdx && mangoIdx < zebraIdx,
		"all-optional properties must render alphabetically; got:\n%s", got)
}

// TestRenderInputSchema_RequiredFirstOrdering pins the required-first
// grouping — a real-world case where alphabetical alone would surface an
// OPTIONAL field before a REQUIRED one (weakening the LLM's fill-required-
// first signal). Matches the tool_resource_search shape: label_selector
// (optional) alphabetically precedes search_type (required), but the
// renderer must emit search_type first.
func TestRenderInputSchema_RequiredFirstOrdering(t *testing.T) {
	schema := toolcore.ToolSchema{
		Properties: map[string]toolcore.ToolSchemaProperty{
			"label_selector": {Type: toolcore.ToolSchemaTypeString, Description: "K8s label selector."},
			"namespace":      {Type: toolcore.ToolSchemaTypeString, Description: "Namespace to search in."},
			"resource_name":  {Type: toolcore.ToolSchemaTypeString, Description: "Resource name."},
			"resource_type":  {Type: toolcore.ToolSchemaTypeString, Description: "Resource type."},
			"search_type":    {Type: toolcore.ToolSchemaTypeString, Description: "Type of search."},
		},
		Required: []string{"search_type"},
	}
	got := renderInputSchema(schema)
	// search_type must precede all four optional fields.
	searchTypeIdx := strings.Index(got, "- search_type")
	labelSelectorIdx := strings.Index(got, "- label_selector")
	namespaceIdx := strings.Index(got, "- namespace")
	resourceNameIdx := strings.Index(got, "- resource_name")
	resourceTypeIdx := strings.Index(got, "- resource_type")
	assert.True(t, searchTypeIdx > 0, "search_type not rendered; got:\n%s", got)
	assert.Less(t, searchTypeIdx, labelSelectorIdx, "required search_type must precede optional label_selector; got:\n%s", got)
	assert.Less(t, searchTypeIdx, namespaceIdx, "required search_type must precede optional namespace; got:\n%s", got)
	assert.Less(t, searchTypeIdx, resourceNameIdx, "required search_type must precede optional resource_name; got:\n%s", got)
	assert.Less(t, searchTypeIdx, resourceTypeIdx, "required search_type must precede optional resource_type; got:\n%s", got)
	// Within the optional group, alphabetical order still holds.
	assert.True(t, labelSelectorIdx < namespaceIdx && namespaceIdx < resourceNameIdx && resourceNameIdx < resourceTypeIdx,
		"optional fields must be alphabetical among themselves; got:\n%s", got)
}

// TestRenderInputSchema_MultipleRequiredAlphabeticalWithinGroup pins the
// within-group ordering when multiple required fields exist. Prevents a
// naïve implementation from preserving Required's declaration order (which
// is unstable across schema edits).
func TestRenderInputSchema_MultipleRequiredAlphabeticalWithinGroup(t *testing.T) {
	schema := toolcore.ToolSchema{
		Properties: map[string]toolcore.ToolSchemaProperty{
			"zebra":  {Type: toolcore.ToolSchemaTypeString},
			"apple":  {Type: toolcore.ToolSchemaTypeString},
			"banana": {Type: toolcore.ToolSchemaTypeString},
		},
		// Intentionally NOT alphabetical — the renderer must sort within-group.
		Required: []string{"zebra", "apple"},
	}
	got := renderInputSchema(schema)
	appleIdx := strings.Index(got, "- apple")
	zebraIdx := strings.Index(got, "- zebra")
	bananaIdx := strings.Index(got, "- banana")
	assert.True(t, 0 <= appleIdx && appleIdx < zebraIdx,
		"required fields must be alphabetical: apple before zebra; got:\n%s", got)
	assert.Less(t, zebraIdx, bananaIdx,
		"all required must precede optional: zebra (required) before banana (optional); got:\n%s", got)
}

// TestRenderInputSchema_EnumInline confirms enum values are appended
// inline as "[one of: a, b, c]" — usually short and high-signal
// (constrains the LLM's value choice with minimal token cost).
func TestRenderInputSchema_EnumInline(t *testing.T) {
	schema := toolcore.ToolSchema{
		Properties: map[string]toolcore.ToolSchemaProperty{
			"status": {
				Type:        toolcore.ToolSchemaTypeString,
				Description: "Current status of the operation.",
				Enum:        []any{"pending", "running", "done"},
			},
		},
		Required: []string{"status"},
	}
	got := renderInputSchema(schema)
	assert.Contains(t, got, "- status (string, required): Current status of the operation. [one of: pending, running, done]")
}

// TestRenderInputSchema_DefaultOmitted confirms the Default field is
// intentionally NOT rendered. Most tools don't set it; the ones that do
// (rare) can name the default in their Description if the LLM needs to
// know. Token budget over cosmetic completeness.
func TestRenderInputSchema_DefaultOmitted(t *testing.T) {
	schema := toolcore.ToolSchema{
		Properties: map[string]toolcore.ToolSchemaProperty{
			"limit": {
				Type:        toolcore.ToolSchemaTypeString,
				Description: "Max number of rows.",
				Default:     "100",
			},
		},
	}
	got := renderInputSchema(schema)
	assert.NotContains(t, got, "default")
	assert.NotContains(t, got, "100")
}

// TestRenderInputSchema_DescriptionCap confirms per-field descriptions are
// capped at maxSchemaFieldDescriptionChars and truncated on a UTF-8 rune
// boundary. Prevents a single tool with long field descriptions (e.g.
// kg_traverse's 10 properties with ~200 char descriptions each) from
// silently ballooning the prompt.
func TestRenderInputSchema_DescriptionCap(t *testing.T) {
	longDesc := strings.Repeat("word ", 40) // 200 chars — well over the 100-char cap
	schema := toolcore.ToolSchema{
		Properties: map[string]toolcore.ToolSchemaProperty{
			"field": {Type: toolcore.ToolSchemaTypeString, Description: longDesc},
		},
	}
	got := renderInputSchema(schema)
	// The rendered field line must not exceed the cap by more than a few
	// bytes (ellipsis + "- field (string, optional): " prefix).
	fieldLine := ""
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "- field") {
			fieldLine = line
			break
		}
	}
	assert.Contains(t, fieldLine, "…", "over-cap description must end with ellipsis")
	// Description portion after the ": " marker must be <= cap + 1 (ellipsis rune).
	if idx := strings.Index(fieldLine, "): "); idx != -1 {
		descPart := fieldLine[idx+3:]
		assert.LessOrEqual(t, len(descPart), maxSchemaFieldDescriptionChars+len("…"),
			"description body exceeds cap: %q", descPart)
	}
}

// TestRenderInputSchema_UTF8SafeTruncation pins the rune-boundary walk-back
// in truncateSchemaFieldDescription. Cuts at every byte position and
// asserts the truncated prefix is always valid UTF-8. Regression guard
// for the Greek/CJK case that would corrupt logs and downstream prompts
// if we shipped a byte-boundary cut.
func TestRenderInputSchema_UTF8SafeTruncation(t *testing.T) {
	// Each Greek letter is 2 bytes in UTF-8 — cutting at odd byte positions
	// would produce invalid output without the walk-back.
	s := "α β γ δ ε ζ η θ ι κ λ μ ν ξ ο π ρ σ τ υ φ χ ψ ω"
	for maxBytes := 1; maxBytes < len(s); maxBytes++ {
		got := truncateSchemaFieldDescription(s, maxBytes)
		// Prefix (before the appended ellipsis) must be valid UTF-8.
		prefix := got
		if strings.HasSuffix(got, "…") {
			prefix = strings.TrimSuffix(got, "…")
		}
		if !utf8.ValidString(prefix) {
			t.Fatalf("maxBytes=%d produced invalid UTF-8 prefix: %q (full: %q)", maxBytes, prefix, got)
		}
	}
	// Under-cap: returned unchanged.
	assert.Equal(t, s, truncateSchemaFieldDescription(s, len(s)+10))
	assert.Equal(t, s, truncateSchemaFieldDescription(s, len(s)))
}

// TestRenderInputSchema_WhitespaceCollapse confirms multi-line and multi-space
// Description fields render on ONE line — matches the "one line per field"
// contract in the format docstring and keeps the token budget predictable.
func TestRenderInputSchema_WhitespaceCollapse(t *testing.T) {
	schema := toolcore.ToolSchema{
		Properties: map[string]toolcore.ToolSchemaProperty{
			"messy": {
				Type: toolcore.ToolSchemaTypeString,
				Description: `line one
                    line two
                    line three`,
			},
		},
	}
	got := renderInputSchema(schema)
	assert.Contains(t, got, "- messy (string, optional): line one line two line three")
	// Only one newline per field — the leading one before "- messy".
	fieldOccurrences := strings.Count(got, "- messy")
	assert.Equal(t, 1, fieldOccurrences)
}

// TestRenderInputSchema_TypeDefault verifies the type falls back to "string"
// when the schema property leaves Type empty (some legacy MCP tools do
// this — MCP allows type-inference). Prevents a raw "(, required)" in the
// rendered output.
func TestRenderInputSchema_TypeDefault(t *testing.T) {
	schema := toolcore.ToolSchema{
		Properties: map[string]toolcore.ToolSchemaProperty{
			"untyped": {Description: "no type set."},
		},
	}
	got := renderInputSchema(schema)
	assert.Contains(t, got, "- untyped (string, optional): no type set.")
	assert.NotContains(t, got, "(, optional)")
}

// TestReActPromptToolDescriptions_AppendsSchemaBlock is the integration
// test — a full-tool render with Name/Type/Description AND the new schema
// block, exercised through the public renderer path
// (reActPromptToolDescriptions). Regression guard against the previous
// state where the schema was completely invisible to the LLM.
func TestReActPromptToolDescriptions_AppendsSchemaBlock(t *testing.T) {
	restore := withRenderAllowlist(t, "think")
	defer restore()

	tool := &fakeSchemaTool{
		name: "think",
		desc: "Record reasoning that changes your next action.",
		schema: toolcore.ToolSchema{
			Properties: map[string]toolcore.ToolSchemaProperty{
				"reasoning": {
					Type:        toolcore.ToolSchemaTypeString,
					Description: "The conflict or stuck point.",
				},
			},
			Required: []string{"reasoning"},
		},
	}
	got := reActPromptToolDescriptions([]toolcore.NBTool{tool})
	assert.Contains(t, got, "Tool Name: think")
	assert.Contains(t, got, "Type: tool")
	assert.Contains(t, got, "Description: Record reasoning that changes your next action.")
	assert.Contains(t, got, "Input: object with fields:\n  - reasoning (string, required): The conflict or stuck point.")
}

// TestReActPromptToolDescriptions_SkipsEmptySchemaBlock confirms a tool
// with no properties renders exactly the pre-change output — no
// spurious empty "Input:" block. Uses "*" allowlist to prove the empty
// short-circuit still wins over the render gate.
func TestReActPromptToolDescriptions_SkipsEmptySchemaBlock(t *testing.T) {
	restore := withRenderAllowlist(t, "*")
	defer restore()

	tool := &fakeSchemaTool{
		name: "noargs",
		desc: "Does something.",
	}
	got := reActPromptToolDescriptions([]toolcore.NBTool{tool})
	assert.NotContains(t, got, "Input:")
	assert.Contains(t, got, "Description: Does something.")
}

// TestReActPromptToolDescriptions_AllowlistGate confirms the config
// gate wins when the tool name is NOT in the allowlist — pins the
// per-tool migration behaviour so a future refactor doesn't
// accidentally flip renderer-on for every tool at once.
func TestReActPromptToolDescriptions_AllowlistGate(t *testing.T) {
	restore := withRenderAllowlist(t, "think")
	defer restore()

	// Different name than the allowlist entry — must NOT render schema.
	tool := &fakeSchemaTool{
		name: "postgres_query_execute",
		desc: "Runs read-only SQL against Postgres.",
		schema: toolcore.ToolSchema{
			Properties: map[string]toolcore.ToolSchemaProperty{
				"command": {Type: toolcore.ToolSchemaTypeString, Description: "JSON envelope with query."},
			},
			Required: []string{"command"},
		},
	}
	got := reActPromptToolDescriptions([]toolcore.NBTool{tool})
	assert.Contains(t, got, "Description: Runs read-only SQL against Postgres.")
	assert.NotContains(t, got, "Input: object with fields:",
		"allowlist gate must suppress the schema block for tools not in the list")
}

// withRenderAllowlist swaps the global config allowlist for the duration
// of a test and returns a restore function. Also handy for exercising
// the empty-list default (renderer off).
func withRenderAllowlist(_ *testing.T, list string) func() {
	prev := config.Config.LlmServerToolSchemaValidationTools
	config.Config.LlmServerToolSchemaValidationTools = list
	return func() { config.Config.LlmServerToolSchemaValidationTools = prev }
}

// fakeSchemaTool is a minimal NBTool for exercising the renderer without
// touching the tool registry. Kept in this file (not the shared test
// helpers) because it exists only for the schema-render tests.
type fakeSchemaTool struct {
	name   string
	desc   string
	schema toolcore.ToolSchema
}

func (t *fakeSchemaTool) Name() string                 { return t.name }
func (t *fakeSchemaTool) Description() string          { return t.desc }
func (t *fakeSchemaTool) GetType() toolcore.NBToolType { return toolcore.NBToolTypeTool }
func (t *fakeSchemaTool) InputSchema() toolcore.ToolSchema {
	return t.schema
}
func (t *fakeSchemaTool) Call(_ toolcore.NbToolContext, _ toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	return toolcore.NBToolResponse{}, nil
}

// TestRenderInputSchema_KnownToolShapes pins the exact rendered output for
// the two motivating tools from PR #33820. Snapshot-style — if either
// think or shell_execute changes its schema, this test forces a conscious
// update instead of silent LLM-facing drift. Uses the same field name +
// description text the real tools set today.
func TestRenderInputSchema_KnownToolShapes(t *testing.T) {
	t.Run("think — one required 'reasoning' field", func(t *testing.T) {
		schema := toolcore.ToolSchema{
			Type: toolcore.ToolSchemaTypeObject,
			Properties: map[string]toolcore.ToolSchemaProperty{
				"reasoning": {
					Type:        toolcore.ToolSchemaTypeString,
					Description: "The conflict, stuck point, candidate root causes, or tool error you face — and the next action it leads to. Not for restating findings or announcing the final answer.",
				},
			},
			Required: []string{"reasoning"},
		}
		got := renderInputSchema(schema)
		// Cap at 100 chars means the reasoning description is truncated with
		// an ellipsis. The "Not for restating findings…" tail is dropped —
		// that's a known trade-off tracked in the PR notes.
		assert.Equal(t,
			"Input: object with fields:\n  - reasoning (string, required): "+
				"The conflict, stuck point, candidate root causes, or tool error you face — and the next action it …",
			got)
	})

	t.Run("shell_execute — command required, work_dir optional", func(t *testing.T) {
		schema := toolcore.ToolSchema{
			Type: toolcore.ToolSchemaTypeObject,
			Properties: map[string]toolcore.ToolSchemaProperty{
				"command": {
					Type:        toolcore.ToolSchemaTypeString,
					Description: "Shell command to execute. Must be non-interactive.",
				},
				"work_dir": {
					Type:        toolcore.ToolSchemaTypeString,
					Description: "Working directory to execute the command in (optional).",
				},
			},
			Required: []string{"command"},
		}
		got := renderInputSchema(schema)
		assert.Equal(t,
			"Input: object with fields:\n"+
				"  - command (string, required): Shell command to execute. Must be non-interactive.\n"+
				"  - work_dir (string, optional): Working directory to execute the command in (optional).",
			got)
	})
}

// TestRegisteredTools_AllSchemasRenderAndFitBudget iterates every tool
// registered via RegisterNBToolFactory, renders its InputSchema, and logs
// the size. Fails only when a single field's rendered line is egregiously
// over the description cap — the sanity guard against a tool sneaking in
// a 2000-char field description that would balloon the whole prompt.
// Nominal-size over-cap fields are reported via t.Log so future authors
// see the cost without breaking CI.
func TestRegisteredTools_AllSchemasRenderAndFitBudget(t *testing.T) {
	const (
		perFieldHardMax   = 600 // rendered line ceiling — anything over this suggests a tool put a mini-doc in one field
		perToolBudgetSoft = 2000
		perToolBudgetHard = 4000
		globalCharBudget  = 25000 // across all registered tools; ~6250 tokens — informational only
	)
	names := toolcore.ListRegisteredSystemToolNames()
	if len(names) == 0 {
		t.Skip("no registered tools — the tools package wasn't imported into this test binary")
	}
	var (
		totalChars         int
		overSoft, overHard []string
	)
	for _, name := range names {
		tool, ok := toolcore.GetNBTool("test-account", name)
		if !ok || tool == nil {
			continue
		}
		rendered := renderInputSchema(tool.InputSchema())
		totalChars += len(rendered)

		// Enforce the per-field hard max — this is the real regression guard.
		for _, line := range strings.Split(rendered, "\n") {
			if !strings.HasPrefix(line, "  - ") {
				continue
			}
			if len(line) > perFieldHardMax {
				t.Errorf("tool %q has a rendered field line of %d chars (max %d) — a single field is carrying too much info:\n%s",
					name, len(line), perFieldHardMax, line)
			}
		}

		if len(rendered) > perToolBudgetHard {
			overHard = append(overHard, name)
		} else if len(rendered) > perToolBudgetSoft {
			overSoft = append(overSoft, name)
		}
	}
	if len(overHard) > 0 {
		t.Errorf("tools rendered schema exceeds hard budget (%d chars): %v", perToolBudgetHard, overHard)
	}
	if len(overSoft) > 0 {
		t.Logf("informational — tools rendered schema exceeds soft budget (%d chars): %v", perToolBudgetSoft, overSoft)
	}
	t.Logf("informational — total rendered chars across %d tools: %d (~%d tokens, budget hint = %d chars)",
		len(names), totalChars, totalChars/4, globalCharBudget)
}
