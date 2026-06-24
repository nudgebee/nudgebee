package core

import (
	"nudgebee/llm/config"
	toolcore "nudgebee/llm/tools/core"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// aliasedMockTool is an NBTool with name aliases — used to verify allow-list
// matching honours aliases the same way the deny-list does.
type aliasedMockTool struct {
	mockTool
	aliases []string
}

func (a aliasedMockTool) GetNameAliases() []string { return a.aliases }

// =============================================================================
// FilterTools — allowed_tools allow-list tests
// =============================================================================

func TestFilterTools_AllowedToolsKeepsOnlyListed(t *testing.T) {
	tools := []toolcore.NBTool{
		mockTool{name: "aws_execute"},
		mockTool{name: "kubectl_execute"},
		mockTool{name: "promql_query"},
	}

	capabilities := toolcore.AgentCapabilities{
		AllowedTools: []string{"aws_execute", "promql_query"},
	}

	result := FilterTools(tools, capabilities)
	require.Len(t, result, 2)
	assert.Equal(t, "aws_execute", result[0].Name())
	assert.Equal(t, "promql_query", result[1].Name())
}

func TestFilterTools_AllowedToolsCaseInsensitive(t *testing.T) {
	tools := []toolcore.NBTool{
		mockTool{name: "aws_execute"},
		mockTool{name: "kubectl_execute"},
	}

	capabilities := toolcore.AgentCapabilities{
		AllowedTools: []string{"AWS_EXECUTE"},
	}

	result := FilterTools(tools, capabilities)
	require.Len(t, result, 1)
	assert.Equal(t, "aws_execute", result[0].Name())
}

func TestFilterTools_AllowedToolsMatchesAliases(t *testing.T) {
	tools := []toolcore.NBTool{
		aliasedMockTool{mockTool: mockTool{name: "aws_execute"}, aliases: []string{"aws_cli"}},
		mockTool{name: "kubectl_execute"},
	}

	capabilities := toolcore.AgentCapabilities{
		AllowedTools: []string{"aws_cli"}, // alias of aws_execute
	}

	result := FilterTools(tools, capabilities)
	require.Len(t, result, 1)
	assert.Equal(t, "aws_execute", result[0].Name())
}

func TestFilterTools_AllowedToolsEmptyMeansNoFilter(t *testing.T) {
	tools := []toolcore.NBTool{
		mockTool{name: "aws_execute"},
		mockTool{name: "kubectl_execute"},
	}

	capabilities := toolcore.AgentCapabilities{
		AllowedTools: []string{},
	}

	result := FilterTools(tools, capabilities)
	assert.Len(t, result, 2, "empty allowed_tools must not filter anything (back-compat)")
}

func TestFilterTools_AllowedToolsUnknownNameYieldsEmpty(t *testing.T) {
	tools := []toolcore.NBTool{
		mockTool{name: "aws_execute"},
	}

	capabilities := toolcore.AgentCapabilities{
		AllowedTools: []string{"nonexistent_tool"},
	}

	result := FilterTools(tools, capabilities)
	assert.Len(t, result, 0, "allow-list with no matches should produce an empty list")
}

func TestFilterTools_DisabledWinsOverAllowed(t *testing.T) {
	// Same tool in both lists — deny must win.
	tools := []toolcore.NBTool{
		mockTool{name: "aws_execute"},
		mockTool{name: "kubectl_execute"},
	}

	capabilities := toolcore.AgentCapabilities{
		AllowedTools:  []string{"aws_execute", "kubectl_execute"},
		DisabledTools: []string{"aws_execute"},
	}

	result := FilterTools(tools, capabilities)
	require.Len(t, result, 1)
	assert.Equal(t, "kubectl_execute", result[0].Name())
}

// =============================================================================
// Integration: FilterAndInjectDefaultTools with allowed_tools
// =============================================================================

func TestFilterAndInjectDefaultTools_AllowedListBlocksShellInjection(t *testing.T) {
	// Even when shell_execute is enabled globally, an explicit allow-list that
	// omits it must prevent the post-filter injection from re-adding it.
	original := config.Config.LlmServerShellToolEnabled
	config.Config.LlmServerShellToolEnabled = true
	t.Cleanup(func() { config.Config.LlmServerShellToolEnabled = original })

	tools := []toolcore.NBTool{
		mockTool{name: "aws_execute"},
	}

	capabilities := toolcore.AgentCapabilities{
		AllowedTools: []string{"aws_execute"}, // shell_execute deliberately not allowed
	}

	result := FilterAndInjectDefaultTools("test-account", nil, "", tools, capabilities)

	assert.False(t, HasShellTool(result),
		"shell_execute must NOT be injected when allowed_tools is set and excludes it")
	require.Len(t, result, 1)
	assert.Equal(t, "aws_execute", result[0].Name())
}

func TestFilterAndInjectDefaultTools_AllowedListIncludingShellKeepsInjection(t *testing.T) {
	original := config.Config.LlmServerShellToolEnabled
	config.Config.LlmServerShellToolEnabled = true
	t.Cleanup(func() { config.Config.LlmServerShellToolEnabled = original })

	tools := []toolcore.NBTool{
		mockTool{name: "aws_execute"},
	}

	capabilities := toolcore.AgentCapabilities{
		AllowedTools: []string{"aws_execute", toolcore.ToolExecuteShellCommand},
	}

	result := FilterAndInjectDefaultTools("test-account", nil, "", tools, capabilities)

	assert.True(t, HasShellTool(result),
		"shell_execute is allow-listed and globally enabled — injection should keep it")
}

func TestFilterAndInjectDefaultTools_NoAllowedListPreservesExistingBehaviour(t *testing.T) {
	// Sanity check: no allowed_tools, no disabled_tools, shell enabled globally.
	// We must not regress the existing default-injection behaviour.
	original := config.Config.LlmServerShellToolEnabled
	config.Config.LlmServerShellToolEnabled = true
	t.Cleanup(func() { config.Config.LlmServerShellToolEnabled = original })

	tools := []toolcore.NBTool{
		mockTool{name: "aws_execute"},
	}

	result := FilterAndInjectDefaultTools("test-account", nil, "", tools, toolcore.AgentCapabilities{})
	assert.True(t, HasShellTool(result), "no capabilities — shell should still inject")
}

func TestFilterAndInjectDefaultTools_AllowedListWithNoMatchesYieldsEmpty(t *testing.T) {
	// User pinned tools that don't intersect with the agent's tool set — the
	// final list must be empty (and the runtime emits a slog warning, exercised
	// here just by the no-panic path; the log itself is verified by structure).
	original := config.Config.LlmServerShellToolEnabled
	config.Config.LlmServerShellToolEnabled = true
	t.Cleanup(func() { config.Config.LlmServerShellToolEnabled = original })

	tools := []toolcore.NBTool{
		mockTool{name: "aws_execute"},
	}

	capabilities := toolcore.AgentCapabilities{
		AllowedTools: []string{"some_other_tool"},
	}

	result := FilterAndInjectDefaultTools("test-account", nil, "", tools, capabilities)
	assert.Len(t, result, 0,
		"pinned allow-list with no overlap must produce an empty tool set after the final pass")
}
