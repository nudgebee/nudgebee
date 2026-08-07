package core

import (
	"context"
	"testing"

	nbprompts "nudgebee/llm/prompts"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

func TestIsSlackReqest(t *testing.T) {
	testCases := []struct {
		name      string
		sessionID string
		expected  bool
	}{
		{
			name:      "Valid Slack Channel ID with timestamp",
			sessionID: "C064YLRJAHG-1757766276.809109",
			expected:  true,
		},
		{
			name:      "Valid Slack Channel ID without timestamp",
			sessionID: "C064YLRJAHG",
			expected:  true,
		},
		{
			name:      "Valid Slack User ID",
			sessionID: "U024BE7LH",
			expected:  true,
		},
		{
			name:      "Valid ID with minimum length",
			sessionID: "W12345678",
			expected:  true,
		},
		{
			name:      "Empty Session ID",
			sessionID: "",
			expected:  false,
		},
		{
			name:      "Non-Slack ID",
			sessionID: "not-a-slack-id",
			expected:  false,
		},
		{
			name:      "ID with wrong prefix",
			sessionID: "X024BE7LH",
			expected:  false,
		},
		{
			name:      "ID too short",
			sessionID: "C1234567",
			expected:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			request := NBAgentRequest{SessionId: tc.sessionID}
			assert.Equal(t, tc.expected, isSlackRequest(request))
		})
	}
}

func TestConvertMarkdownToSlackMarkdown(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Headings",
			input:    "# Title\n## Subtitle",
			expected: "*Title*\n*Subtitle*",
		},
		{
			name:     "Links",
			input:    "Here is a [link](https://example.com) to a website.",
			expected: "Here is a <https://example.com|link> to a website.",
		},
		{
			name:     "Strikethrough",
			input:    "This is ~~wrong~~ right.",
			expected: "This is ~wrong~ right.",
		},
		{
			name:     "Bold",
			input:    "This is **bold** and so is __this__.",
			expected: "This is *bold* and so is *this*.",
		},
		{
			name:     "Italic",
			input:    "This is *italic* and so is _this_.",
			expected: "This is _italic_ and so is _this_.",
		},
		{
			name:     "Bold and Italic",
			input:    "***This is both.***",
			expected: "_*This is both.*_",
		},
		{
			name:     "Inline code",
			input:    "Use the `my_function()` function.",
			expected: "Use the `my_function()` function.",
		},
		{
			name:     "Fenced code block strips language tag",
			input:    "```go\nfunc main() {\n\tfmt.Println(\"Hello, World!\")\n}\n```",
			expected: "```\nfunc main() {\n\tfmt.Println(\"Hello, World!\")\n}\n```",
		},
		{
			name:     "Fenced markdown block strips language tag, contents intact",
			input:    "```markdown\n# Automation\n**bold**\n```",
			expected: "```\n# Automation\n**bold**\n```",
		},
		{
			name:     "Fence with no language tag is unchanged",
			input:    "```\nplain code\n```",
			expected: "```\nplain code\n```",
		},
		{
			name:     "Single word content on its own line is not treated as a language tag",
			input:    "```\nyaml\nkey: val\n```",
			expected: "```\nyaml\nkey: val\n```",
		},
		{
			name:     "Markdown inside code block",
			input:    "```\n# This is not a heading\n**This is not bold**\n```",
			expected: "```\n# This is not a heading\n**This is not bold**\n```",
		},
		{
			name:     "Complex mixed content",
			input:    "# Report\nHere is the data you requested:\n\n- **Metric A**: 123\n- *Metric B*: 456\n\nSee `generate_report()` for details. [Dashboard](https://dashboard.link).",
			expected: "*Report*\nHere is the data you requested:\n\n- *Metric A*: 123\n- _Metric B_: 456\n\nSee `generate_report()` for details. <https://dashboard.link|Dashboard>.",
		},
		{
			name:     "Simple Bold Asterisk",
			input:    "**bold**",
			expected: "*bold*",
		},
		{
			name:     "Markdown in inline code",
			input:    "this is `**not bold**`",
			expected: "this is `**not bold**`",
		},
		{
			name:     "Markdown in fenced code",
			input:    "```\n**not bold**\n```",
			expected: "```\n**not bold**\n```",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := convertMarkdownToSlackMarkdown(tc.input)
			assert.Equal(t, tc.expected, actual)
		})
	}
}

// TestResolveEffectivePlannerType pins the declared→runtime planner mapping:
// Orchestrating and ReAct agents both execute via ReAct3; everything else runs
// as declared. Callers that reason about runtime behavior (prompt style,
// DisplayID assignment, response formatting) depend on this.
func TestResolveEffectivePlannerType(t *testing.T) {
	cases := []struct {
		declared AgentPlannerType
		want     AgentPlannerType
	}{
		{AgentPlannerTypeOrchestrating, AgentPlannerTypeReAct3},
		{AgentPlannerTypeReAct, AgentPlannerTypeReAct3},
		{AgentPlannerTypeReAct3, AgentPlannerTypeReAct3},
		{AgentPlannerTypeTool, AgentPlannerTypeTool},
		{AgentPlannerTypeCustom, AgentPlannerTypeCustom},
		{AgentPlannerTypeClassification, AgentPlannerTypeClassification},
		{AgentPlannerTypeConversational, AgentPlannerTypeConversational},
	}
	for _, c := range cases {
		t.Run(string(c.declared), func(t *testing.T) {
			assert.Equal(t, c.want, resolveEffectivePlannerType(c.declared))
		})
	}
}

func toolStep(name, content string) ToolInvocation {
	return ToolInvocation{
		Call:     llms.ToolCall{FunctionCall: &llms.FunctionCall{Name: name}},
		Response: llms.ToolCallResponse{Content: content},
	}
}

// TestBuildStepReferenceParts_GuideForReAct3ExecutedAgents is the regression
// guard for the bug where orchestrating agents skipped the Step Reference Guide.
// An orchestrating agent resolves to the effective ReAct3 type and MUST get the
// guide (it assigns sequential DisplayIDs); a tool agent must NOT.
func TestBuildStepReferenceParts_GuideForReAct3ExecutedAgents(t *testing.T) {
	resp := NBAgentResponse{
		AgentStepResponse: []ToolInvocation{
			toolStep("aws", "region us-east-1"),
			toolStep("cloudwatch", "5xx spike at 10:00"),
		},
	}

	t.Run("orchestrating agent → effective react_3 → guide built", func(t *testing.T) {
		effective := resolveEffectivePlannerType(AgentPlannerTypeOrchestrating)
		guide, supporting := buildStepReferenceParts(resp, effective)

		assert.Contains(t, guide, "Step Reference Guide")
		assert.Contains(t, guide, "E1 = aws")
		assert.Contains(t, guide, "E2 = cloudwatch")
		// Supporting data is labeled with the same IDs so citations line up.
		assert.Contains(t, supporting, "[E1 (aws)]")
		assert.Contains(t, supporting, "[E2 (cloudwatch)]")
	})

	t.Run("react_3 agent → guide built", func(t *testing.T) {
		guide, _ := buildStepReferenceParts(resp, AgentPlannerTypeReAct3)
		assert.Contains(t, guide, "E1 = aws")
	})

	t.Run("tool agent → no guide, raw supporting data", func(t *testing.T) {
		guide, supporting := buildStepReferenceParts(resp, AgentPlannerTypeTool)
		assert.Empty(t, guide)
		assert.NotContains(t, supporting, "[E1")
		assert.Contains(t, supporting, "region us-east-1")
	})
}

// TestFormatterTemplate_GatesCausalityOnQuery pins that the formatter prompt's
// causality-chain instructions are present for investigation turns and absent for
// query/generation turns — so a query-type response is never even told how to build
// a Causality Chain.
func TestFormatterTemplate_GatesCausalityOnQuery(t *testing.T) {
	raw := nbprompts.GetPrompt(context.Background(), nbprompts.PromptResponseFormatter, "")
	inv := renderFormatterTemplate(raw, map[string]interface{}{"is_investigation": true})
	qry := renderFormatterTemplate(raw, map[string]interface{}{"is_investigation": false})

	// Investigation keeps the build spec + preserve-causality directive.
	assert.Contains(t, inv, "you MUST include a section titled")
	assert.Contains(t, inv, "PRESERVE CAUSALITY")
	// Query drops the build spec and forbids a causality chain instead.
	assert.NotContains(t, qry, "you MUST include a section titled")
	assert.NotContains(t, qry, "PRESERVE CAUSALITY")
	assert.Contains(t, qry, "No Causality Chain")
	assert.Contains(t, qry, "NO CAUSAL EMBELLISHMENT")
	// Both branches must render with no leftover template tokens.
	assert.NotContains(t, inv, "{{")
	assert.NotContains(t, qry, "{{")
}

// TestBasePromptTemplate_GatesRcaFrameworkOnQuery pins that the PLANNER base prompt
// (not just the formatter) drops the investigation RCA answer-format spec on a query
// turn — so the planner never emits a Causality Chain for a simple question in the
// first place (the formatter's query-branch can prevent adding one but not reliably
// strip one already generated). Gated on the same is_investigation signal.
func TestBasePromptTemplate_GatesRcaFrameworkOnQuery(t *testing.T) {
	raw := nbprompts.GetPrompt(context.Background(), nbprompts.PromptReact3Base, "")
	inv := renderFormatterTemplate(raw, map[string]interface{}{"is_investigation": true})
	qry := renderFormatterTemplate(raw, map[string]interface{}{"is_investigation": false})

	// Investigation keeps the full RCA framework.
	assert.Contains(t, inv, "ROOT CAUSE ANALYSIS FRAMEWORK")
	assert.Contains(t, inv, "Causality Chain (Root Cause)")
	assert.Contains(t, inv, "SYMPTOM VS. ROOT CAUSE CHECK")

	// Query drops the entire RCA framework — the planner is never told how to build one.
	// (The retained "Question type" line legitimately still MENTIONS the term to say
	// "do NOT use Causality Chain / Evidence Chain for queries" — that is guidance, not
	// the build spec, so we assert on the spec markers, not the bare term.)
	assert.NotContains(t, qry, "ROOT CAUSE ANALYSIS FRAMEWORK")
	assert.NotContains(t, qry, "Causality Chain (Root Cause)")
	assert.NotContains(t, qry, "SYMPTOM VS. ROOT CAUSE CHECK")
	// The retained guidance still tells the model not to use the RCA format for queries.
	assert.Contains(t, qry, "Do NOT use the investigation output format")

	// ...but the GENERAL final-answer mechanics stay for BOTH (not inside the gate).
	assert.Contains(t, inv, "single `<final_answer>` block")
	assert.Contains(t, qry, "single `<final_answer>` block")
	assert.Contains(t, qry, "GREETING & SIMPLE QUERIES")

	// No leftover template tokens on either branch (balanced {{if}}/{{end}}).
	assert.NotContains(t, inv, "{{")
	assert.NotContains(t, qry, "{{")
}
