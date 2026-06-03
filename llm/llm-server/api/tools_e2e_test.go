//go:build e2e

package api

import (
	"log/slog"
	agentcore "nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTools_List(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	tools := core.ListTools(sc, os.Getenv("TEST_ACCOUNT"))
	assert.NotEmpty(t, tools)
}

func TestTools_CustomTool(t *testing.T) {

	userId := os.Getenv("TEST_USER")
	accountId := os.Getenv("TEST_ACCOUNT")
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), userId, []string{accountId})

	customTool := core.ToolDto{
		Name:         "custom_tool_test",
		Description:  "Custom tool for testing",
		Type:         core.ToolTypeCustom,
		ExecutorType: core.ToolExecutorTypeContainer,
		Config: map[string]any{
			"image":   "alpine:latest",
			"command": []string{"echo", "hello"},
		},
		InputSchema: core.ToolSchema{
			Type: "object",
			Properties: map[string]core.ToolSchemaProperty{
				"command": {
					Type:        core.ToolSchemaTypeArray,
					Description: "Command to run in the container",
				},
			},
		},
	}

	err := core.DeleteCustomTool(sc, accountId, customTool.Name)
	assert.Nil(t, err)
	if err != nil {
		slog.Error("Error deleting custom tool", "error", err)
		return
	}

	customTool, err = core.CreateCustomTool(sc, accountId, customTool)
	assert.Nil(t, err)
	if err != nil {
		slog.Error("Error creating custom tool", "error", err)
		return
	}
	assert.NotEmpty(t, customTool.Id)

	tools := core.ListCustomTools(accountId)
	assert.NotEmpty(t, tools)

	foundCustomTool, ok := core.GetCustomNbTool(accountId, customTool.Name)
	assert.True(t, ok)
	assert.Equal(t, customTool.Name, foundCustomTool.Name())

	toolResponse, err := foundCustomTool.Call(core.NbToolContext{
		Ctx:       sc,
		AccountId: accountId,
		UserId:    userId,
	}, core.NBToolCallRequest{
		Command: "ls -al /",
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, toolResponse)
}

func TestTools_CustomToolWithAgent(t *testing.T) {

	userId := os.Getenv("TEST_USER")
	accountId := os.Getenv("TEST_ACCOUNT")
	conversationSessionId := "custom_agent_test_with_tool"
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), userId, []string{accountId})

	customTool := core.ToolDto{
		Name:         "custom_shell_tool",
		Description:  "Custom tool for testing shell execution",
		Type:         core.ToolTypeCustom,
		ExecutorType: core.ToolExecutorTypeContainer,
		Config: map[string]any{
			"image":   "alpine:latest",
			"command": []string{"sh", "-c"},
		},
		InputSchema: core.ToolSchema{
			Type: "object",
			Properties: map[string]core.ToolSchemaProperty{
				"command": {
					Type:        core.ToolSchemaTypeArray,
					Description: "Shell command to run in the container",
				},
			},
		},
	}

	err := core.DeleteCustomTool(sc, accountId, customTool.Name)
	assert.Nil(t, err)
	if err != nil {
		slog.Error("Error deleting custom tool", "error", err)
		return
	}

	customTool, err = core.CreateCustomTool(sc, accountId, customTool)
	assert.Nil(t, err)
	if err != nil {
		slog.Error("Error creating custom tool", "error", err)
		return
	}
	assert.NotEmpty(t, customTool.Id)

	tools := core.ListCustomTools(accountId)
	assert.NotEmpty(t, tools)

	foundCustomTool, ok := core.GetCustomNbTool(accountId, customTool.Name)
	assert.True(t, ok)
	assert.Equal(t, customTool.Name, foundCustomTool.Name())

	toolResponse, err := foundCustomTool.Call(core.NbToolContext{
		Ctx:       sc,
		AccountId: accountId,
		UserId:    userId,
	}, core.NBToolCallRequest{
		Command: "ls -al /",
	})

	assert.Nil(t, err)
	assert.NotEmpty(t, toolResponse)

	customAgent := agentcore.AgentDto{
		Name:         "custom_agent_test_with_tool",
		Description:  "Custom agent for testing",
		Type:         agentcore.AgentTypeCustom,
		ExecutorType: agentcore.AgentPlannerTypeTool,
		Tools:        []string{customTool.Name},
		SystemPrompt: `
			You are expert in terminal(shell) commands. 
			your job is to generate command based on users querry && execute them using available tools.
			
			You can use following tools:
			**custom_shell_tool** - Run Genenerated shell command
	  `,
		SystemPromptVariables: []string{},
	}

	err = agentcore.DeleteCustomAgent(sc, accountId, customAgent.Name)
	assert.Nil(t, err)

	customAgent, err = agentcore.CreateCustomAgent(sc, accountId, customAgent, nil, false)
	assert.Nil(t, err)
	if err != nil {
		slog.Error("Error creating custom agent", "error", err)
		return
	}
	assert.NotEmpty(t, customAgent.Id)

	agents := agentcore.ListCustomAgents(sc, accountId, false)
	assert.NotEmpty(t, agents)

	foundCustomAgent, ok := agentcore.GetCustomNbAgent(sc, accountId, customAgent.Name, "")
	assert.True(t, ok)
	assert.Equal(t, customAgent.Name, foundCustomAgent.GetName())

	err = agentcore.DeleteConversationBySession(conversationSessionId, accountId, userId)
	assert.Nil(t, err)

	agentResponse, err := agentcore.HandleConversationSessionRequest(sc, foundCustomAgent, userId, accountId, conversationSessionId, "can you ping google.com to check network connectivity")
	assert.Nil(t, err)
	assert.NotEmpty(t, agentResponse)
}

func TestTools_UpdateTool(t *testing.T) {
	userId := os.Getenv("TEST_USER")
	accountId := os.Getenv("TEST_ACCOUNT")
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), userId, []string{accountId})

	customTool := core.ToolDto{
		Name:         "custom_shell_tool",
		Description:  "Custom tool for testing shell execution",
		Type:         core.ToolTypeCustom,
		ExecutorType: core.ToolExecutorTypeContainer,
		Config: map[string]any{
			"image":   "alpine:latest",
			"command": []string{"sh", "-c"},
		},
		InputSchema: core.ToolSchema{
			Type: "object",
			Properties: map[string]core.ToolSchemaProperty{
				"command": {
					Type:        core.ToolSchemaTypeArray,
					Description: "Shell command to run in the container",
				},
			},
		},
	}

	err := core.DeleteCustomTool(sc, accountId, customTool.Name)
	assert.Nil(t, err)
	if err != nil {
		slog.Error("Error deleting custom tool", "error", err)
		return
	}

	customTool, err = core.CreateCustomTool(sc, accountId, customTool)
	assert.Nil(t, err)
	if err != nil {
		slog.Error("Error creating custom tool", "error", err)
		return
	}
	assert.NotEmpty(t, customTool.Id)

	// Compose minimal ToolDto for update (add other fields as needed)
	updateDto := core.ToolDto{
		Id:           customTool.Id,
		Name:         customTool.Name,
		Description:  "Updated description",
		Status:       core.ToolStatusEnabled,
		Config:       map[string]any{}, // or fetch existing config if needed
		InputSchema:  core.ToolSchema{},
		ExecutorType: core.ToolExecutorTypeContainer,
		NBToolType:   core.NBToolTypeTool,
	}

	err = core.UpdateCustomTool(sc, accountId, customTool.Name, updateDto)
	assert.Nil(t, err)
}
