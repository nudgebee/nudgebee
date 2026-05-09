package core

import (
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockOptOutAgent is a minimal NBAgent that opts out of default-tool injection.
// Used to verify FilterAndInjectDefaultTools honors the DefaultToolsOptOut interface.
type mockOptOutAgent struct{ name string }

func (a mockOptOutAgent) GetName() string                                               { return a.name }
func (a mockOptOutAgent) GetNameAliases() []string                                      { return nil }
func (a mockOptOutAgent) GetDescription() string                                        { return "mock opt-out agent" }
func (a mockOptOutAgent) GetSupportedTools(_ *security.RequestContext) []toolcore.NBTool { return nil }
func (a mockOptOutAgent) GetSystemPrompt(_ *security.RequestContext, _ NBAgentRequest) NBAgentPrompt {
	return NBAgentPrompt{}
}
func (a mockOptOutAgent) GetPlannerType() AgentPlannerType { return AgentPlannerTypeReAct }
func (a mockOptOutAgent) OptOutDefaultTools() bool         { return true }

// mockPlainAgent satisfies NBAgent without implementing DefaultToolsOptOut.
type mockPlainAgent struct{ name string }

func (a mockPlainAgent) GetName() string                                               { return a.name }
func (a mockPlainAgent) GetNameAliases() []string                                      { return nil }
func (a mockPlainAgent) GetDescription() string                                        { return "mock plain agent" }
func (a mockPlainAgent) GetSupportedTools(_ *security.RequestContext) []toolcore.NBTool { return nil }
func (a mockPlainAgent) GetSystemPrompt(_ *security.RequestContext, _ NBAgentRequest) NBAgentPrompt {
	return NBAgentPrompt{}
}
func (a mockPlainAgent) GetPlannerType() AgentPlannerType { return AgentPlannerTypeReAct }

// mockTool is a minimal NBTool implementation for testing FilterAndInjectDefaultTools.
type mockTool struct {
	name string
}

func (m mockTool) Name() string        { return m.name }
func (m mockTool) Description() string { return "mock " + m.name }
func (m mockTool) Call(_ toolcore.NbToolContext, _ toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	return toolcore.NBToolResponse{}, nil
}
func (m mockTool) GetType() toolcore.NBToolType     { return toolcore.NBToolTypeTool }
func (m mockTool) InputSchema() toolcore.ToolSchema { return toolcore.ToolSchema{} }

// registerMockShellTool registers a mock shell_execute tool in the tool factory
// and returns a cleanup function to deregister it.
func registerMockShellTool() {
	toolcore.RegisterNBToolFactory(toolcore.ToolExecuteShellCommand, func(accountId string) (toolcore.NBTool, error) {
		return mockTool{name: toolcore.ToolExecuteShellCommand}, nil
	})
}

func init() {
	// Register mock shell_execute so FilterAndInjectDefaultTools can resolve it.
	registerMockShellTool()
}

// =============================================================================
// HasShellTool tests
// =============================================================================

func TestHasShellTool_Present(t *testing.T) {
	tools := []toolcore.NBTool{
		mockTool{name: "aws_execute"},
		mockTool{name: toolcore.ToolExecuteShellCommand},
		mockTool{name: "gcloud_execute"},
	}
	assert.True(t, HasShellTool(tools))
}

func TestHasShellTool_Absent(t *testing.T) {
	tools := []toolcore.NBTool{
		mockTool{name: "aws_execute"},
		mockTool{name: "gcloud_execute"},
	}
	assert.False(t, HasShellTool(tools))
}

func TestHasShellTool_EmptyList(t *testing.T) {
	assert.False(t, HasShellTool(nil))
	assert.False(t, HasShellTool([]toolcore.NBTool{}))
}

// =============================================================================
// FilterAndInjectDefaultTools — shell_execute injection tests
// =============================================================================

func TestFilterAndInjectDefaultTools_InjectsShellWhenEnabled(t *testing.T) {
	original := config.Config.LlmServerShellToolEnabled
	config.Config.LlmServerShellToolEnabled = true
	t.Cleanup(func() { config.Config.LlmServerShellToolEnabled = original })

	tools := []toolcore.NBTool{
		mockTool{name: "aws_execute"},
	}

	result := FilterAndInjectDefaultTools("test-account", nil, "", tools, nil)

	assert.True(t, HasShellTool(result), "shell_execute should be injected when enabled")
	// Original tool should still be present
	found := false
	for _, t := range result {
		if t.Name() == "aws_execute" {
			found = true
			break
		}
	}
	assert.True(t, found, "original aws_execute tool should still be present")
}

func TestFilterAndInjectDefaultTools_DoesNotInjectShellWhenDisabled(t *testing.T) {
	original := config.Config.LlmServerShellToolEnabled
	config.Config.LlmServerShellToolEnabled = false
	t.Cleanup(func() { config.Config.LlmServerShellToolEnabled = original })

	tools := []toolcore.NBTool{
		mockTool{name: "aws_execute"},
	}

	result := FilterAndInjectDefaultTools("test-account", nil, "", tools, nil)

	assert.False(t, HasShellTool(result), "shell_execute should NOT be injected when disabled")
}

func TestFilterAndInjectDefaultTools_ShellCoexistsWithCloudTools(t *testing.T) {
	// Key test: shell_execute should be injected even when cloud-specific tools
	// (aws_execute, gcloud_execute, azure_execute) are already present.
	// This verifies the old hasCloudTool guard was removed.
	original := config.Config.LlmServerShellToolEnabled
	config.Config.LlmServerShellToolEnabled = true
	t.Cleanup(func() { config.Config.LlmServerShellToolEnabled = original })

	tests := []struct {
		name  string
		tools []toolcore.NBTool
	}{
		{
			name: "coexists with aws_execute",
			tools: []toolcore.NBTool{
				mockTool{name: "aws_execute"},
			},
		},
		{
			name: "coexists with gcloud_execute",
			tools: []toolcore.NBTool{
				mockTool{name: "gcloud_execute"},
			},
		},
		{
			name: "coexists with azure_execute",
			tools: []toolcore.NBTool{
				mockTool{name: "azure_execute"},
			},
		},
		{
			name: "coexists with all cloud tools",
			tools: []toolcore.NBTool{
				mockTool{name: "aws_execute"},
				mockTool{name: "gcloud_execute"},
				mockTool{name: "azure_execute"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FilterAndInjectDefaultTools("test-account", nil, "", tt.tools, nil)
			assert.True(t, HasShellTool(result),
				"shell_execute must be injected alongside cloud tools: %s", tt.name)

			// All original tools should still be present
			for _, originalTool := range tt.tools {
				found := false
				for _, resultTool := range result {
					if resultTool.Name() == originalTool.Name() {
						found = true
						break
					}
				}
				assert.True(t, found, "original tool %q should still be present", originalTool.Name())
			}
		})
	}
}

func TestFilterAndInjectDefaultTools_DoesNotDuplicateShell(t *testing.T) {
	// If shell_execute is already in the tool list, it should not be added again
	original := config.Config.LlmServerShellToolEnabled
	config.Config.LlmServerShellToolEnabled = true
	t.Cleanup(func() { config.Config.LlmServerShellToolEnabled = original })

	tools := []toolcore.NBTool{
		mockTool{name: "aws_execute"},
		mockTool{name: toolcore.ToolExecuteShellCommand},
	}

	result := FilterAndInjectDefaultTools("test-account", nil, "", tools, nil)

	shellCount := 0
	for _, t := range result {
		if t.Name() == toolcore.ToolExecuteShellCommand {
			shellCount++
		}
	}
	assert.Equal(t, 1, shellCount, "shell_execute should not be duplicated")
}

// =============================================================================
// FilterTools — disabled_tools capability tests
// =============================================================================

func TestFilterTools_DisablesShellViaCapabilities(t *testing.T) {
	// Even if shell is enabled globally, capabilities can disable it per-agent
	tools := []toolcore.NBTool{
		mockTool{name: "aws_execute"},
		mockTool{name: toolcore.ToolExecuteShellCommand},
	}

	capabilities := map[string]any{
		"disabled_tools": []string{toolcore.ToolExecuteShellCommand},
	}

	result := FilterTools(tools, capabilities)

	assert.False(t, HasShellTool(result), "shell_execute should be removed when in disabled_tools")
	// aws_execute should remain
	require.Len(t, result, 1)
	assert.Equal(t, "aws_execute", result[0].Name())
}

func TestFilterTools_DisablesShellViaCapabilities_AnySlice(t *testing.T) {
	// disabled_tools may come as []any from JSON deserialization
	tools := []toolcore.NBTool{
		mockTool{name: "aws_execute"},
		mockTool{name: toolcore.ToolExecuteShellCommand},
	}

	capabilities := map[string]any{
		"disabled_tools": []any{"shell_execute"},
	}

	result := FilterTools(tools, capabilities)
	assert.False(t, HasShellTool(result), "shell_execute should be removed via []any disabled_tools")
}

func TestFilterTools_CaseInsensitive(t *testing.T) {
	tools := []toolcore.NBTool{
		mockTool{name: toolcore.ToolExecuteShellCommand},
	}

	capabilities := map[string]any{
		"disabled_tools": []string{"Shell_Execute"},
	}

	result := FilterTools(tools, capabilities)
	assert.False(t, HasShellTool(result), "disabled_tools matching should be case-insensitive")
}

func TestFilterTools_NilCapabilities(t *testing.T) {
	tools := []toolcore.NBTool{
		mockTool{name: toolcore.ToolExecuteShellCommand},
	}

	result := FilterTools(tools, nil)
	assert.True(t, HasShellTool(result), "nil capabilities should not filter anything")
}

func TestFilterTools_EmptyDisabledList(t *testing.T) {
	tools := []toolcore.NBTool{
		mockTool{name: toolcore.ToolExecuteShellCommand},
		mockTool{name: "aws_execute"},
	}

	capabilities := map[string]any{
		"disabled_tools": []string{},
	}

	result := FilterTools(tools, capabilities)
	assert.Len(t, result, 2, "empty disabled_tools should not filter anything")
}

// =============================================================================
// Integration: FilterAndInjectDefaultTools with disabled_tools
// =============================================================================

func TestFilterAndInjectDefaultTools_ShellInjectedThenFilteredByCapabilities(t *testing.T) {
	// Shell is enabled globally but disabled by agent capabilities.
	// The injection happens after filtering, so shell gets injected.
	// This tests the order of operations.
	original := config.Config.LlmServerShellToolEnabled
	config.Config.LlmServerShellToolEnabled = true
	t.Cleanup(func() { config.Config.LlmServerShellToolEnabled = original })

	tools := []toolcore.NBTool{
		mockTool{name: "aws_execute"},
	}

	capabilities := map[string]any{
		"disabled_tools": []string{toolcore.ToolExecuteShellCommand},
	}

	result := FilterAndInjectDefaultTools("test-account", nil, "", tools, capabilities)

	// Note: Current implementation filters FIRST, then injects shell.
	// So disabled_tools won't prevent injection (shell isn't in the list yet when filter runs).
	// This is the expected behavior — FilterAndInjectDefaultTools is about default injection,
	// and the global config flag is the control mechanism.
	// If the agent truly needs to disable shell, it should not include it in its tool list
	// and set LlmServerShellToolEnabled = false.
	assert.True(t, HasShellTool(result),
		"shell_execute is injected AFTER filtering, so disabled_tools doesn't block injection")
}

// =============================================================================
// AWS debug agent tool list composition test
// =============================================================================

func TestAwsDebugAgent_ShellToolInToolList(t *testing.T) {
	// Verify that when LlmServerShellToolEnabled is true, the AWS debug agent's
	// supported tool name list includes shell_execute.
	// This is a unit test for the tool name inclusion — the actual tool resolution
	// from registry requires running services and is covered by integration tests.
	original := config.Config.LlmServerShellToolEnabled
	config.Config.LlmServerShellToolEnabled = true
	t.Cleanup(func() { config.Config.LlmServerShellToolEnabled = original })

	// We can't call getAwsPlannerSupportedTools directly (it's in agents package
	// and requires DB), but we CAN verify the FilterAndInjectDefaultTools logic:
	// AWS debug agents provide aws_execute, aws_observability etc. and shell should be auto-injected.
	awsTools := []toolcore.NBTool{
		mockTool{name: "aws_execute"},
		mockTool{name: "aws_observability"},
	}

	result := FilterAndInjectDefaultTools("test-aws-account", nil, "", awsTools, nil)
	assert.True(t, HasShellTool(result),
		"AWS debug agent tools should include shell_execute when enabled")
}

func TestGcpDebugAgent_ShellToolInToolList(t *testing.T) {
	original := config.Config.LlmServerShellToolEnabled
	config.Config.LlmServerShellToolEnabled = true
	t.Cleanup(func() { config.Config.LlmServerShellToolEnabled = original })

	gcpTools := []toolcore.NBTool{
		mockTool{name: "gcloud_execute"},
	}

	result := FilterAndInjectDefaultTools("test-gcp-account", nil, "", gcpTools, nil)
	assert.True(t, HasShellTool(result),
		"GCP debug agent tools should include shell_execute when enabled")
}

func TestAzureDebugAgent_ShellToolInToolList(t *testing.T) {
	original := config.Config.LlmServerShellToolEnabled
	config.Config.LlmServerShellToolEnabled = true
	t.Cleanup(func() { config.Config.LlmServerShellToolEnabled = original })

	azureTools := []toolcore.NBTool{
		mockTool{name: "azure_execute"},
	}

	result := FilterAndInjectDefaultTools("test-azure-account", nil, "", azureTools, nil)
	assert.True(t, HasShellTool(result),
		"Azure debug agent tools should include shell_execute when enabled")
}

// =============================================================================
// DefaultToolsOptOut interface — per-agent opt-out from default injection
// =============================================================================

func TestFilterAndInjectDefaultTools_AgentOptOutSkipsShellInjection(t *testing.T) {
	// Shell is enabled globally, but an agent that implements DefaultToolsOptOut
	// returning true must not get shell_execute injected. This is the path the
	// dynamic delegate sub-agent uses to keep parent-supplied tool curation honest.
	original := config.Config.LlmServerShellToolEnabled
	config.Config.LlmServerShellToolEnabled = true
	t.Cleanup(func() { config.Config.LlmServerShellToolEnabled = original })

	tools := []toolcore.NBTool{mockTool{name: "postgres_execute"}}

	result := FilterAndInjectDefaultTools("test-account", mockOptOutAgent{name: "delegate_agent"}, "", tools, nil)

	assert.False(t, HasShellTool(result),
		"agents implementing DefaultToolsOptOut must not get shell_execute injected")
	require.Len(t, result, 1)
	assert.Equal(t, "postgres_execute", result[0].Name())
}

func TestFilterAndInjectDefaultTools_AgentOptOutSkipsLoadSkillsInjection(t *testing.T) {
	// load_skills is normally injected when the agent prompt contains <skill-lists>.
	// An opt-out agent must skip this injection too.
	original := config.Config.LlmServerShellToolEnabled
	config.Config.LlmServerShellToolEnabled = false
	t.Cleanup(func() { config.Config.LlmServerShellToolEnabled = original })

	tools := []toolcore.NBTool{mockTool{name: "postgres_execute"}}

	// Prompt contains the skill-lists marker that would normally trigger load_skills injection.
	result := FilterAndInjectDefaultTools("test-account", mockOptOutAgent{name: "delegate_agent"}, "<skill-lists>foo</skill-lists>", tools, nil)

	for _, tool := range result {
		assert.NotEqual(t, "load_skills", tool.Name(),
			"opt-out agent must not get load_skills injected even when prompt has <skill-lists>")
	}
}

func TestFilterAndInjectDefaultTools_CustomAgentOptsOut(t *testing.T) {
	// nbCustomAgent must opt out of default injection so the operator's tool selection
	// (set via UI/API) is honored verbatim. Without the opt-out, a custom agent
	// configured with tools=[postgres_execute] would silently also get shell_execute.
	original := config.Config.LlmServerShellToolEnabled
	config.Config.LlmServerShellToolEnabled = true
	t.Cleanup(func() { config.Config.LlmServerShellToolEnabled = original })

	custom := &nbCustomAgent{
		agent:     AgentDto{Name: "user_defined_db_agent"},
		accountId: "test-account",
	}
	tools := []toolcore.NBTool{mockTool{name: "postgres_execute"}}

	result := FilterAndInjectDefaultTools("test-account", custom, "", tools, nil)

	assert.False(t, HasShellTool(result),
		"custom agent must not get shell_execute injected — user's tool selection is the curation contract")
	require.Len(t, result, 1)
	assert.Equal(t, "postgres_execute", result[0].Name())
}

func TestFilterAndInjectDefaultTools_PlainAgentStillGetsInjection(t *testing.T) {
	// Sanity: a regular NBAgent that doesn't implement the opt-out interface
	// must still get shell injection — we don't want a silent regression for
	// the 27+ agents relying on default behavior.
	original := config.Config.LlmServerShellToolEnabled
	config.Config.LlmServerShellToolEnabled = true
	t.Cleanup(func() { config.Config.LlmServerShellToolEnabled = original })

	tools := []toolcore.NBTool{mockTool{name: "aws_execute"}}

	result := FilterAndInjectDefaultTools("test-account", mockPlainAgent{name: "aws_debug"}, "", tools, nil)

	assert.True(t, HasShellTool(result),
		"plain agent without opt-out interface must still receive shell_execute injection")
}

func TestCloudDebugAgents_ShellNotInjectedWhenDisabled(t *testing.T) {
	original := config.Config.LlmServerShellToolEnabled
	config.Config.LlmServerShellToolEnabled = false
	t.Cleanup(func() { config.Config.LlmServerShellToolEnabled = original })

	tests := []struct {
		name  string
		tools []toolcore.NBTool
	}{
		{"aws", []toolcore.NBTool{mockTool{name: "aws_execute"}}},
		{"gcp", []toolcore.NBTool{mockTool{name: "gcloud_execute"}}},
		{"azure", []toolcore.NBTool{mockTool{name: "azure_execute"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FilterAndInjectDefaultTools("test-account", nil, "", tt.tools, nil)
			assert.False(t, HasShellTool(result),
				"%s debug agent should NOT have shell_execute when disabled", tt.name)
		})
	}
}
