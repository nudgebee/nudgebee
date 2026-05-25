package core

import (
	"fmt"
	"nudgebee/llm/tools/core" // Assuming this is the correct import path for toolcore
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// --- Mock Tools ---

// Mock standard tool
type mockStandardTool struct{}

func (m mockStandardTool) Name() string { return "standard_tool" }
func (m mockStandardTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}
func (m mockStandardTool) Description() string { return "A standard tool description." }
func (m mockStandardTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"query": {Type: core.ToolSchemaTypeString, Description: "The query string."},
		},
		Required: []string{"query"},
	}
}
func (m mockStandardTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	return core.NBToolResponse{Data: "Standard tool called"}, nil
}

// Mock multi-command tool
type mockMultiCommandTool struct{}

func (m mockMultiCommandTool) Name() string { return "multi_tool" }
func (m mockMultiCommandTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}
func (m mockMultiCommandTool) Description() string { return "A tool with multiple subcommands." }
func (m mockMultiCommandTool) InputSchema() core.ToolSchema {
	// For multi-command, the top-level schema might be less relevant,
	// as the input is expected to be JSON specifying command and args.
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "The subcommand to execute.",
				Enum:        []any{"sub1", "sub2"},
			},
			"args": {
				Type:        core.ToolSchemaTypeObject,
				Description: "Arguments for the subcommand.",
				Items: map[string]any{
					"arg1": core.ToolSchemaProperty{Type: core.ToolSchemaTypeString, Description: "First argument."},
					"arg2": core.ToolSchemaProperty{Type: core.ToolSchemaTypeInteger, Description: "Second argument.", Enum: []any{1, 2, 3}},
				},
			},
		},
	}
}
func (m mockMultiCommandTool) GetSubCommands() ([]core.NBToolCommand, error) {
	return []core.NBToolCommand{
		{
			Name:        "sub1",
			Description: "First subcommand.",
			InputSchema: core.ToolSchema{
				Type: core.ToolSchemaTypeObject,
				Properties: map[string]core.ToolSchemaProperty{
					"arg1": {Type: core.ToolSchemaTypeString, Description: "First argument."},
					"arg2": {Type: core.ToolSchemaTypeInteger, Description: "Second argument.", Enum: []any{1, 2, 3}},
				},
				Required: []string{"arg1"},
			},
		},
		{
			Name:        "sub2",
			Description: "Second subcommand, no args.",
			InputSchema: core.ToolSchema{
				Type:       core.ToolSchemaTypeObject,
				Properties: map[string]core.ToolSchemaProperty{},
			},
		},
	}, nil
}
func (m mockMultiCommandTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	// In a real scenario, this would parse input.Command and input.Args
	return core.NBToolResponse{Data: "Multi tool called with command: " + input.Command}, nil
}

// --- Test Function ---

func TestGetPromptTemplateForMultiCommandTool(t *testing.T) {
	mockReq := NBAgentRequest{
		Query:          "Test query using tools",
		AccountId:      uuid.NewString(),
		ConversationId: uuid.NewString(),
		UserId:         uuid.NewString(),
		MessageId:      uuid.NewString(),
	}

	mockTools := []core.NBTool{
		mockStandardTool{},
		mockMultiCommandTool{},
	}

	prompt, err := UpdatePromptForMultiCommandTool(mockReq, mockTools, NBAgentPrompt{
		Examples: []NBAgentPromptExample{},
		Rag:      NBAgentPromptRag{},
		Role:     "Test Agent",
	})

	assert.NoError(t, err)
	assert.NotNil(t, prompt)

	promptTemplate := GetPromptTemplate(prompt, mockReq, AgentPlannerTypeReAct)

	// Format the prompt to get the final string (using dummy input for now)
	promptString, err := promptTemplate.Format(map[string]any{"input": mockReq.Query})
	assert.NoError(t, err)

	t.Log("Generated Prompt:\n", promptString) // Log the prompt for manual inspection

	// --- Assertions ---
	// Check for general multi-command instructions (should be wrapped in <instructions> because we updated GetPromptTemplate)
	assert.Contains(t, promptString, "<instructions>")
	assert.Contains(t, promptString, "When a tool requires a subcommand, you MUST respond *only* with a valid JSON object")
	assert.Contains(t, promptString, "`{\"command\": \"<subcommand_name>\", \"args\": {<arguments>}}`")
	assert.Contains(t, promptString, "Remember: For tools listed with subcommands, your response *must* be ONLY a JSON object")
	assert.Contains(t, promptString, "</instructions>")

	// Check standard tool description (should be wrapped in <tool_usage_instructions>)
	assert.Contains(t, promptString, "<tool_usage_instructions>")
	assert.Contains(t, promptString, "**standard_tool**")
	assert.Contains(t, promptString, "A standard tool description.")
	assert.Contains(t, promptString, "`query` (string) (required): The query string.")
	assert.Contains(t, promptString, "</tool_usage_instructions>")

	// Check multi-command tool description
	assert.Contains(t, promptString, "**multi_tool**")
	assert.Contains(t, promptString, "A tool with multiple subcommands.")
	assert.Contains(t, promptString, "**This tool uses subcommands.** Respond ONLY with JSON:")
	assert.Contains(t, promptString, "**Available Subcommands:**")

	// Check subcommand 1 details
	assert.Contains(t, promptString, "**sub1**: First subcommand.")
	assert.Contains(t, promptString, "`arg1` (string) (required): First argument.")
	assert.Contains(t, promptString, "`arg2` (integer): Second argument. (Enum: 1, 2, 3)") // Check enum formatting

	// Check subcommand 2 details
	assert.Contains(t, promptString, "**sub2**: Second subcommand, no args.")
	assert.Contains(t, promptString, "*No arguments required for this subcommand.*")

	// Check Output Format section (conditionally added for multi-tools)
	assert.Contains(t, promptString, "FINAL ANSWER REQUIREMENTS (CRITICAL):")
	assert.Contains(t, promptString, "If using a tool with subcommands, respond ONLY with a valid JSON object")
}

func TestGetPromptTemplateForReAct(t *testing.T) {
	mockReq := NBAgentRequest{
		Query:          "Test query for ReAct",
		AccountId:      uuid.NewString(),
		ConversationId: uuid.NewString(),
		UserId:         uuid.NewString(),
		MessageId:      uuid.NewString(),
	}

	p := NBAgentPrompt{
		Role: "Expert ReAct Agent",
		Instructions: []string{
			"Instruction 1",
			"Instruction 2",
		},
		Constraints: []string{
			"Constraint 1",
		},
		Schema: []string{
			"Schema 1",
		},
		ToolUsage: map[string][]string{
			"tool1": {"Usage 1"},
		},
		Examples: []NBAgentPromptExample{
			{
				Question:    "Question 1?",
				Answer:      "Final Answer 1",
				Explanation: "Thought 1",
			},
			{
				Question: "Question 2?",
				AnswerSteps: []NBAgentPromptExampleAnswerStep{
					{
						Tool:        "tool1",
						Input:       "input1",
						Explanation: "Step Thought 1",
					},
				},
				Explanation: "Example Plan 2",
			},
		},
		OutputFormat: "Custom Markdown",
	}

	promptTemplate := GetPromptTemplate(p, mockReq, AgentPlannerTypeReAct)
	promptString, err := promptTemplate.Format(map[string]any{"input": mockReq.Query})
	assert.NoError(t, err)

	fmt.Println("Generated ReAct Prompt:\n", promptString)

	// Verify sections are wrapped in XML tags
	assert.Contains(t, promptString, "<instructions>")
	assert.Contains(t, promptString, "- Instruction 1")
	assert.Contains(t, promptString, "</instructions>")

	assert.Contains(t, promptString, "<constraints>")
	assert.Contains(t, promptString, "- Constraint 1")
	assert.Contains(t, promptString, "</constraints>")

	assert.Contains(t, promptString, "<schema>")
	assert.Contains(t, promptString, "- Schema 1")
	assert.Contains(t, promptString, "</schema>")

	assert.Contains(t, promptString, "<tool_usage_instructions>")
	assert.Contains(t, promptString, "**tool1**")
	assert.Contains(t, promptString, "- Usage 1")
	assert.Contains(t, promptString, "</tool_usage_instructions>")

	// Verify examples are wrapped in XML tags
	assert.Contains(t, promptString, "<examples>")
	assert.Contains(t, promptString, "<example>")
	assert.Contains(t, promptString, "<question>Question 1?</question>")
	assert.Contains(t, promptString, "<answer>")
	assert.Contains(t, promptString, "<final_answer>")
	assert.Contains(t, promptString, "<thought>Thought 1</thought>")
	assert.Contains(t, promptString, "<content>Final Answer 1</content>")
	assert.Contains(t, promptString, "</final_answer>")
	assert.Contains(t, promptString, "</answer>")
	assert.Contains(t, promptString, "</example>")

	assert.Contains(t, promptString, "<question>Question 2?</question>")
	assert.Contains(t, promptString, "<thought>Example Plan 2</thought>") // Top level explanation
	assert.Contains(t, promptString, "<thought_action>")
	assert.Contains(t, promptString, "<thought>Step Thought 1</thought>")
	assert.Contains(t, promptString, "<action>")
	assert.Contains(t, promptString, "<tool_name>tool1</tool_name>")
	assert.Contains(t, promptString, "<tool_input>input1</tool_input>")
	assert.Contains(t, promptString, "</action>")
	assert.Contains(t, promptString, "</thought_action>")
	assert.Contains(t, promptString, "</examples>")
}

func TestGetPromptTemplateForReWoo(t *testing.T) {
	mockReq := NBAgentRequest{
		Query:          "Test query for ReWoo",
		AccountId:      uuid.NewString(),
		ConversationId: uuid.NewString(),
		UserId:         uuid.NewString(),
		MessageId:      uuid.NewString(),
	}

	p := NBAgentPrompt{
		Role: "Expert ReWoo Agent",
		Instructions: []string{
			"Instruction 1",
		},
		Examples: []NBAgentPromptExample{
			{
				Question: "Question 1?",
				Answer:   "Final Answer 1",
			},
		},
	}

	promptTemplate := GetPromptTemplate(p, mockReq, AgentPlannerTypeReWoo)
	promptString, err := promptTemplate.Format(map[string]any{"input": mockReq.Query})
	assert.NoError(t, err)

	// Verify sections are NOT wrapped in XML tags for ReWoo
	assert.NotContains(t, promptString, "<instructions>")
	assert.Contains(t, promptString, "Instructions:")
	assert.Contains(t, promptString, "- Instruction 1")

	// Verify examples are NOT wrapped in XML tags for ReWoo
	assert.NotContains(t, promptString, "<examples>")
	assert.Contains(t, promptString, "Examples:")
	assert.Contains(t, promptString, "question: Question 1?")
	assert.Contains(t, promptString, "answer: Final Answer 1")

	// No image instructions when no images
	assert.NotContains(t, promptString, "Image Analysis Instructions")
}

func TestGetPromptTemplate_ImageInstructions_ReWoo(t *testing.T) {
	enableImageSupport(t)
	mockReq := NBAgentRequest{
		Query:          "What's wrong with this pod?",
		AccountId:      uuid.NewString(),
		ConversationId: uuid.NewString(),
		UserId:         uuid.NewString(),
		MessageId:      uuid.NewString(),
		Images: []ImageAttachment{
			{Data: "dGVzdA==", MIMEType: "image/png"},
		},
	}

	p := NBAgentPrompt{
		Role:         "K8s Debug Agent",
		Instructions: []string{"Debug Kubernetes issues"},
	}

	promptTemplate := GetPromptTemplate(p, mockReq, AgentPlannerTypeReWoo)
	promptString, err := promptTemplate.Format(map[string]any{"input": mockReq.Query})
	assert.NoError(t, err)

	// Should contain image analysis instructions (non-XML for ReWoo)
	assert.Contains(t, promptString, "Image Analysis Instructions:")
	assert.Contains(t, promptString, "attached image(s)")
	assert.Contains(t, promptString, "extract all visible technical details")
	assert.Contains(t, promptString, "first-class evidence")
	assert.NotContains(t, promptString, "<image_analysis_instructions>")
}

func TestGetPromptTemplate_ImageInstructions_ReAct(t *testing.T) {
	enableImageSupport(t)
	mockReq := NBAgentRequest{
		Query:          "Check this error screenshot",
		AccountId:      uuid.NewString(),
		ConversationId: uuid.NewString(),
		UserId:         uuid.NewString(),
		MessageId:      uuid.NewString(),
		Images: []ImageAttachment{
			{URL: "https://example.com/screenshot.png", MIMEType: "image/png"},
		},
	}

	p := NBAgentPrompt{
		Role:         "Debug Agent",
		Instructions: []string{"Investigate errors"},
	}

	promptTemplate := GetPromptTemplate(p, mockReq, AgentPlannerTypeReAct)
	promptString, err := promptTemplate.Format(map[string]any{"input": mockReq.Query})
	assert.NoError(t, err)

	// Should contain image analysis instructions wrapped in XML tags for ReAct
	assert.Contains(t, promptString, "<image_analysis_instructions>")
	assert.Contains(t, promptString, "</image_analysis_instructions>")
	assert.Contains(t, promptString, "attached image(s)")
	assert.Contains(t, promptString, "error codes, metric values")
}
