package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParsePromptToNBAgentPrompt_FullStructured(t *testing.T) {
	input := `# Test Agent System Prompt

## Role
You are a Redis expert, acting as an SRE.

## Instructions
**First instruction:** Do something useful.

**Second instruction:** Do something else.

## Constraints
Always use the redis_execute tool.

Never run destructive commands unless asked.

## Tool Usage
### redis_execute
Executes Redis commands.

**Input:** A valid redis-cli command.

### other_tool
Does other things.

## Output Format
Markdown format

## Examples
**Question:** Show memory usage
**Answer:** INFO memory
**Explanation:** Returns memory stats

---

**Question:** List all keys
**Answer Steps:**
1. Tool: redis_execute
   Input: SCAN 0
**Explanation:** Safe key listing

---

## RAG Configuration
- Module: redis
- Format: json
- QuestionKey: Question
- AnswerKey: Answer
- Records: 5
`

	prompt := ParsePromptToNBAgentPrompt(input)

	// Role: "You are " should be stripped
	assert.Equal(t, "a Redis expert, acting as an SRE", prompt.Role)

	// Instructions: 2 paragraphs
	assert.Len(t, prompt.Instructions, 2)
	assert.Contains(t, prompt.Instructions[0], "First instruction")
	assert.Contains(t, prompt.Instructions[1], "Second instruction")

	// Constraints: 2 paragraphs
	assert.Len(t, prompt.Constraints, 2)
	assert.Contains(t, prompt.Constraints[0], "redis_execute")
	assert.Contains(t, prompt.Constraints[1], "destructive")

	// Tool Usage: 2 tools
	assert.Len(t, prompt.ToolUsage, 2)
	assert.Contains(t, prompt.ToolUsage, "redis_execute")
	assert.Contains(t, prompt.ToolUsage, "other_tool")

	// Output Format
	assert.Equal(t, "Markdown format", prompt.OutputFormat)

	// Examples: 2 entries
	assert.Len(t, prompt.Examples, 2)
	assert.Equal(t, "Show memory usage", prompt.Examples[0].Question)
	assert.Equal(t, "INFO memory", prompt.Examples[0].Answer)
	assert.Equal(t, "Returns memory stats", prompt.Examples[0].Explanation)
	assert.Equal(t, "List all keys", prompt.Examples[1].Question)
	assert.Len(t, prompt.Examples[1].AnswerSteps, 1)
	assert.Equal(t, "redis_execute", prompt.Examples[1].AnswerSteps[0].Tool)
	assert.Equal(t, "SCAN 0", prompt.Examples[1].AnswerSteps[0].Input)

	// RAG
	assert.Equal(t, "redis", prompt.Rag.Module)
	assert.Equal(t, NBAgentPromptRagFormat("json"), prompt.Rag.Format)
	assert.Equal(t, "Question", prompt.Rag.QuestionKey)
	assert.Equal(t, "Answer", prompt.Rag.AnswerKey)
	assert.Equal(t, 5, prompt.Rag.Records)
}

func TestParsePromptToNBAgentPrompt_Unstructured(t *testing.T) {
	// k8s_debug.txt style — no ## sections, just plain paragraphs
	input := `**Primary Directive:** Create a plan of tool calls.

**Information Gathering:** All user queries require investigation.

**Tool Selection:** Prioritize data gathering tools.`

	prompt := ParsePromptToNBAgentPrompt(input)

	assert.Empty(t, prompt.Role)
	assert.Len(t, prompt.Instructions, 3)
	assert.Contains(t, prompt.Instructions[0], "Primary Directive")
	assert.Contains(t, prompt.Instructions[1], "Information Gathering")
	assert.Contains(t, prompt.Instructions[2], "Tool Selection")
}

func TestParsePromptToNBAgentPrompt_RoleVariants(t *testing.T) {
	tests := []struct {
		name     string
		roleText string
		expected string
	}{
		{"with You are prefix", "You are a Redis expert.", "a Redis expert"},
		{"without prefix", "a Redis expert.", "a Redis expert"},
		{"lowercase you are", "you are a database expert.", "a database expert"},
		{"no trailing period", "a Redis expert", "a Redis expert"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := "## Role\n" + tt.roleText
			prompt := ParsePromptToNBAgentPrompt(input)
			assert.Equal(t, tt.expected, prompt.Role)
		})
	}
}

func TestParsePromptToNBAgentPrompt_MultipleInstructionSections(t *testing.T) {
	// Some files have both ## Instructions and ## Primary Directive
	input := `## Primary Directive
Focus on data gathering.

## Instructions
Always verify resource names.

Never guess.
`
	prompt := ParsePromptToNBAgentPrompt(input)
	// Both sections merged into Instructions
	assert.Len(t, prompt.Instructions, 3)
}

func TestParsePromptToNBAgentPrompt_EmptyInput(t *testing.T) {
	prompt := ParsePromptToNBAgentPrompt("")
	assert.Empty(t, prompt.Role)
	assert.Empty(t, prompt.Instructions)
	assert.Empty(t, prompt.Constraints)
}

func TestParsePromptToNBAgentPrompt_OnlyTitleLine(t *testing.T) {
	// Files that have # Title with no ## sections are treated as unstructured.
	// The # title line becomes the single instruction (it's just a non-empty paragraph).
	// In practice this won't happen since all files should have ## sections.
	input := "# Some Agent System Prompt\n"
	prompt := ParsePromptToNBAgentPrompt(input)
	assert.Empty(t, prompt.Role)
	// Title line is treated as unstructured content → one instruction
	assert.Len(t, prompt.Instructions, 1)
}

func TestParsePromptToNBAgentPrompt_RagConfig(t *testing.T) {
	input := `## RAG Configuration
- Module: clickhouse
- Format: json
- QuestionKey: Question
- AnswerKey: Diagnostic Query
- ExplanationKey: Solution Hint
- Records: 10
`
	prompt := ParsePromptToNBAgentPrompt(input)
	assert.Equal(t, "clickhouse", prompt.Rag.Module)
	assert.Equal(t, NBAgentPromptRagFormatJson, prompt.Rag.Format)
	assert.Equal(t, "Question", prompt.Rag.QuestionKey)
	assert.Equal(t, "Diagnostic Query", prompt.Rag.AnswerKey)
	assert.Equal(t, "Solution Hint", prompt.Rag.ExplanationKey)
	assert.Equal(t, 10, prompt.Rag.Records)
}

func TestParsePromptToNBAgentPrompt_SchemaSection(t *testing.T) {
	input := `## Schema
Column id - unique identifier.

Column name - resource name.
`
	prompt := ParsePromptToNBAgentPrompt(input)
	assert.Len(t, prompt.Schema, 2)
	assert.Contains(t, prompt.Schema[0], "Column id")
}
