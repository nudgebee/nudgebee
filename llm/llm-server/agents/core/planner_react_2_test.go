package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/prompts"
)

// TestSystemMessageCacheStability verifies that the system message rendered by the ReAct
// planner prompt template is byte-for-byte identical across multiple Plan iterations.
// This is the key invariant that allows LLM provider caching (Google AI CachedContent,
// Anthropic cache_control) to get cache hits: the system message must not change between
// iterations within a single conversation.
//
// Previously, "today", "notebook", and "query_context" were passed via fullInputs on
// every iteration, overriding PartialVariables and producing a different timestamp each
// second — breaking the provider cache hash on every call.
func TestSystemMessageCacheStability(t *testing.T) {
	const systemTpl = "Tools: {{.tool_names}} | Date: {{.today}} | Context: {{.query_context}}"

	tmpl := prompts.NewChatPromptTemplate([]prompts.MessageFormatter{
		prompts.NewSystemMessagePromptTemplate(systemTpl, []string{"tool_names", "today", "query_context"}),
		prompts.NewHumanMessagePromptTemplate("Q: {{.input}}\n{{.scratchpad}}", []string{"input", "scratchpad"}),
	})

	// Simulate what reActCreatePrompt2 does: set stable values once in PartialVariables.
	fixedToday := time.Now().Format(time.RFC1123)
	tmpl.PartialVariables = map[string]any{
		"tool_names":    "search, kubectl",
		"today":         fixedToday,
		"query_context": "namespace=default",
		"scratchpad":    "",
	}

	// Simulate iteration 1: only human-message variables in fullInputs.
	result1, err := tmpl.FormatPrompt(map[string]any{
		"input":      "what is wrong?",
		"scratchpad": "",
	})
	assert.NoError(t, err)

	// Simulate iteration 2: scratchpad grows, but fullInputs still only has human-message vars.
	result2, err := tmpl.FormatPrompt(map[string]any{
		"input":      "what is wrong?",
		"scratchpad": "<scratchpad><observation tool=\"kubectl\">pod-abc is crashlooping</observation></scratchpad>",
	})
	assert.NoError(t, err)

	msgs1 := result1.Messages()
	msgs2 := result2.Messages()
	assert.Len(t, msgs1, 2)
	assert.Len(t, msgs2, 2)

	// The system message (index 0) MUST be identical across iterations.
	systemMsg1 := msgs1[0].GetContent()
	systemMsg2 := msgs2[0].GetContent()
	assert.Equal(t, systemMsg1, systemMsg2, "system message must be identical across iterations for LLM provider cache hits")
	assert.Contains(t, systemMsg1, fixedToday, "system message must contain the stable today value from PartialVariables")

	// The human message (index 1) MUST differ (scratchpad grew).
	assert.NotEqual(t, msgs1[1].GetContent(), msgs2[1].GetContent(), "human message must differ as scratchpad grows")
}

func TestParseOutput2(t *testing.T) {
	t.Run("should parse thought_action", func(t *testing.T) {
		output := `<thought_action>
			<thought>I need to use the search tool to find the capital of France.</thought>
			<action>
				<tool_name>search</tool_name>
				<tool_input>what is the capital of France?</tool_input>
			</action>
		</thought_action>`

		response := &llms.ContentResponse{
			Choices: []*llms.ContentChoice{
				{
					Content: output,
				},
			},
		}

		planner := &NBReActPlanner2{}
		actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

		assert.NoError(t, err)
		assert.Nil(t, finish)
		assert.NotNil(t, actions)
		assert.Len(t, actions, 1)
		assert.Equal(t, "search", actions[0].Tool)
		assert.Equal(t, "what is the capital of France?", actions[0].ToolInput)
		assert.Equal(t, "I need to use the search tool to find the capital of France.", actions[0].Log, "The sanitized thought should be in the log")
	})

	t.Run("should parse final answer", func(t *testing.T) {
		output := `<final_answer>
			<thought>I have found the answer and will now provide it.</thought>
			<content>The capital of France is Paris.</content>
		</final_answer>`

		response := &llms.ContentResponse{
			Choices: []*llms.ContentChoice{
				{
					Content: output,
				},
			},
		}

		planner := &NBReActPlanner2{}
		actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

		assert.NoError(t, err)
		assert.Nil(t, actions)
		assert.NotNil(t, finish)
		assert.Equal(t, "The capital of France is Paris.", finish.Data)
		assert.Equal(t, "I have found the answer and will now provide it.", finish.Log)
	})

	t.Run("should parse thought_action with json input", func(t *testing.T) {
		output := `<thought_action>
			<thought>I need to use a tool with JSON input</thought>
			<action>
				<tool_name>some_tool</tool_name>
				<tool_input>{\"key\": \"value\", \"number\": 123}</tool_input>
			</action>
		</thought_action>`

		response := &llms.ContentResponse{
			Choices: []*llms.ContentChoice{
				{
					Content: output,
				},
			},
		}

		planner := &NBReActPlanner2{}
		actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

		assert.NoError(t, err)
		assert.Nil(t, finish)
		assert.NotNil(t, actions)
		assert.Len(t, actions, 1)
		assert.Equal(t, "some_tool", actions[0].Tool)
		assert.Equal(t, `{\"key\": \"value\", \"number\": 123}`, actions[0].ToolInput)
		assert.Equal(t, "I need to use a tool with JSON input", actions[0].Log)
	})

	t.Run("should return error for invalid format", func(t *testing.T) {
		output := `This is just some plain text without any valid XML tags.`

		response := &llms.ContentResponse{
			Choices: []*llms.ContentChoice{
				{
					Content: output,
				},
			},
		}

		planner := &NBReActPlanner2{}
		actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

		assert.Error(t, err)
		assert.Nil(t, actions)
		assert.Nil(t, finish)
		assert.Contains(t, err.Error(), "no action, final answer, or clarification")
	})

	t.Run("should parse final_answer even if it comes first", func(t *testing.T) {
		output := `<final_answer><content>This is the final answer.</content></final_answer>`

		response := &llms.ContentResponse{
			Choices: []*llms.ContentChoice{
				{
					Content: output,
				},
			},
		}

		planner := &NBReActPlanner2{}
		actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

		assert.NoError(t, err)
		assert.Nil(t, actions)
		assert.NotNil(t, finish)
		assert.Equal(t, "This is the final answer.", finish.Data)
	})

	t.Run("should prioritize tool action over final answer when both are present", func(t *testing.T) {
		output := `<thought_action>
			<thought>I need to check something first.</thought>
			<action>
				<tool_name>search</tool_name>
				<tool_input>find data</tool_input>
			</action>
		</thought_action>
		<final_answer><content>This should be ignored.</content></final_answer>`

		response := &llms.ContentResponse{
			Choices: []*llms.ContentChoice{{Content: output}},
		}

		planner := &NBReActPlanner2{}
		actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

		assert.NoError(t, err)
		assert.NotNil(t, actions)
		assert.Nil(t, finish)
		assert.Equal(t, "search", actions[0].Tool)
	})

	t.Run("should update notebook when tag is present", func(t *testing.T) {
		output := `<update_notebook>Fact 1: service-a is healthy. Fact 2: service-b is failing.</update_notebook>
		<thought_action>
			<thought>I have updated my notes and will now search for service-b errors.</thought>
			<action>
				<tool_name>logs</tool_name>
				<tool_input>get errors for service-b</tool_input>
			</action>
		</thought_action>`

		response := &llms.ContentResponse{
			Choices: []*llms.ContentChoice{
				{
					Content: output,
				},
			},
		}

		planner := &NBReActPlanner2{}
		actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

		assert.NoError(t, err)
		assert.Nil(t, finish)
		assert.NotNil(t, actions)
		assert.Equal(t, "Fact 1: service-a is healthy. Fact 2: service-b is failing.", planner.Notebook)
	})
}

// TestReAct2NotebookAsToolCall verifies that when the LLM mistakenly emits
// update_notebook as a tool call, the notebook content is still captured
// and the tool call is filtered out.
func TestReAct2NotebookAsToolCall(t *testing.T) {
	output := `<thought_action>
<thought>I will update the notebook with findings.</thought>
<action>
	<tool_name>update_notebook</tool_name>
	<tool_input>## Investigation Plan
1. [DONE] Query metrics-* - No data
2. [DOING] List all indices

## Key Findings
- No CPU metrics found</tool_input>
</action>
</thought_action>`

	response := &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: output}},
	}

	planner := &NBReActPlanner2{}
	actions, finish, err := planner.parseOutputInternal(response, []NBAgentPlannerToolActionStep{})

	// The tool call should be filtered out — no actions to execute
	assert.Error(t, err) // parse failure because no real tool action remains
	assert.Nil(t, actions)
	assert.Nil(t, finish)

	// But the notebook should still be updated from the tool_input content
	assert.Contains(t, planner.Notebook, "Investigation Plan")
	assert.Contains(t, planner.Notebook, "[DONE] Query metrics-*")
}
