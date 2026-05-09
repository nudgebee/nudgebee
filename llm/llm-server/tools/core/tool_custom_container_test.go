package core

import (
	"nudgebee/llm/security"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestCustomContainerTool_Call(t *testing.T) {
	testAccountId := os.Getenv("TEST_ACCOUNT")
	testTenantId := os.Getenv("TEST_TENANT")
	testUserId := os.Getenv("TEST_USER")

	if testAccountId == "" || testTenantId == "" || testUserId == "" {
		t.Skip("Skipping integration test: TEST_ACCOUNT, TEST_TENANT, or TEST_USER not set")
	}

	sc := security.NewRequestContextForTenantAccountAdmin(testTenantId, testUserId, []string{testAccountId})
	toolId := uuid.NewString()

	// Base tool configuration
	baseToolDto := ToolDto{
		Id:           toolId,
		Name:         "test_container_tool_1",
		Type:         ToolTypeCustom,
		ExecutorType: ToolExecutorTypeContainer,
		NBToolType:   NBToolTypeTool,
		Config: map[string]any{
			"image":   "alpine:latest",
			"command": []string{"sh", "-c", "echo 'default_output_signal' && echo \"Env DEFAULT_VAR is $DEFAULT_VAR\""},
			"env_vars": map[string]any{
				"DEFAULT_VAR": "default_env_value",
			},
		},
	}

	containerTool := nbCustomContainerTool{
		tool: baseToolDto,
	}

	// Create a unique toolCallId for each sub-test if needed, or reuse.
	// For simplicity, we'll create a base context and update toolCallId if strict uniqueness per call is desired by underlying systems.
	baseToolCallId := uuid.NewString()
	toolContext := NewNbToolContext(sc, containerTool, testAccountId, testUserId, uuid.NewString(), uuid.NewString(), "", "", nil, "", NBQueryConfig{}, baseToolCallId)

	t.Run("Successful execution with default command and env", func(t *testing.T) {
		request := NBToolCallRequest{
			Arguments: map[string]any{},
		}
		currentToolCallId := uuid.NewString()
		ctx := toolContext
		ctx.ToolCallId = currentToolCallId

		response, err := containerTool.Call(ctx, request)

		assert.Nil(t, err, "Call should not return an error")
		assert.Equal(t, NBToolResponseStatusSuccess, response.Status, "Response status should be success")
		assert.Contains(t, response.Data, "default_output_signal", "Response data should contain default output signal")
		assert.Contains(t, response.Data, "Env DEFAULT_VAR is default_env_value", "Response data should show default env var value")
	})

	t.Run("Successful execution with command override", func(t *testing.T) {
		request := NBToolCallRequest{
			Arguments: map[string]any{
				"command": []string{"ls", "/etc"},
			},
		}
		currentToolCallId := uuid.NewString()
		ctx := toolContext
		ctx.ToolCallId = currentToolCallId

		response, err := containerTool.Call(ctx, request)

		assert.Nil(t, err, "Call should not return an error")
		assert.Equal(t, NBToolResponseStatusSuccess, response.Status, "Response status should be success")
		assert.Contains(t, response.Data, "alpine-release", "Response data should list /etc content like 'alpine-release'")
		assert.Contains(t, response.Data, "hosts", "Response data should list /etc content like 'hosts'")
	})

	t.Run("Successful execution with env_vars override and command printing env", func(t *testing.T) {
		request := NBToolCallRequest{
			Arguments: map[string]any{
				"command":  []string{"sh", "-c", "echo \"Test Var: $TEST_VAR, Default Var: $DEFAULT_VAR\""},
				"env_vars": map[string]any{"TEST_VAR": "overridden_value"},
			},
		}
		currentToolCallId := uuid.NewString()
		ctx := toolContext
		ctx.ToolCallId = currentToolCallId

		response, err := containerTool.Call(ctx, request)

		assert.Nil(t, err, "Call should not return an error")
		assert.Equal(t, NBToolResponseStatusSuccess, response.Status, "Response status should be success")
		// Trim space because echo might add a newline
		assert.Contains(t, strings.TrimSpace(response.Data), "Test Var: overridden_value, Default Var: default_env_value", "Response data should show overridden and default env vars")
	})

	t.Run("Successful execution with image override", func(t *testing.T) {
		request := NBToolCallRequest{
			Arguments: map[string]any{
				"image":   "busybox:latest",
				"command": []string{"echo", "hello from busybox"},
			},
		}
		currentToolCallId := uuid.NewString()
		ctx := toolContext
		ctx.ToolCallId = currentToolCallId

		response, err := containerTool.Call(ctx, request)

		assert.Nil(t, err, "Call should not return an error")
		assert.Equal(t, NBToolResponseStatusSuccess, response.Status, "Response status should be success")
		assert.Contains(t, strings.TrimSpace(response.Data), "hello from busybox", "Response data should confirm execution in busybox")
	})

	t.Run("Execution failure when base image is missing in tool config", func(t *testing.T) {
		badToolDto := baseToolDto
		badToolDto.Config = map[string]any{}
		badContainerTool := nbCustomContainerTool{tool: badToolDto}

		request := NBToolCallRequest{Arguments: map[string]any{}}
		currentToolCallId := uuid.NewString()
		ctx := toolContext
		ctx.ToolCallId = currentToolCallId

		response, err := badContainerTool.Call(ctx, request)

		assert.NotNil(t, err, "Call should return an error for missing base image")
		assert.ErrorContains(t, err, "base image not found", "Error message should indicate missing base image")
		assert.Equal(t, NBToolResponseStatusError, response.Status, "Response status should be error")
	})

	// This test assumes that if a command inside the container writes to stderr,
	// the parsePodScriptRunEnricherResponse function will capture it in the 'response.Data'.
	t.Run("Execution with command failing/writing to stderr inside container", func(t *testing.T) {
		request := NBToolCallRequest{
			Arguments: map[string]any{
				"command": []string{"cat", "/nonexistentfile"},
			},
		}
		currentToolCallId := uuid.NewString()
		ctx := toolContext
		ctx.ToolCallId = currentToolCallId

		response, err := containerTool.Call(ctx, request)

		assert.Nil(t, err, "Tool execution (relay call, parsing) itself should be successful")
		assert.Equal(t, NBToolResponseStatusSuccess, response.Status, "Tool status should be success even if command inside fails but output is captured")
		assert.Contains(t, response.Data, "cat: can't open '/nonexistentfile': No such file or directory", "Response data should contain stderr from the container command")
	})
}
