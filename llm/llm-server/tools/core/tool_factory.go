package core

import (
	"log/slog"
	"strings"
)

var nbSystemTools = map[string]func(accountId string) (NBTool, error){}

func RegisterNBToolFactory(tool string, toolFactory func(accountId string) (NBTool, error)) {
	slog.Info("registering tool", "tool", tool)
	if _, ok := nbSystemTools[strings.ToLower(tool)]; ok {
		slog.Warn("tool already registered", "tool", tool)
	}
	nbSystemTools[strings.ToLower(tool)] = toolFactory
}

// GetNBTool resolves a tool name to an NBTool by consulting, in order:
//
//  1. Built-in system tools registered via RegisterNBToolFactory.
//  2. Account-scoped custom tools (the llm_tools DB table).
//  3. Account-scoped MCP integration tools discovered via ListMCPIntegrationTools.
//
// The MCP branch is required because the agent-create / agent-update validation
// path uses this function to verify that every tool the user picked exists for
// their account. Without it, MCP tools surfaced by the UI's tool list (which
// already includes the MCP source) would fail validation, return a generic
// 500, and surface to users as an opaque "internal error".
func GetNBTool(accountId string, toolName string) (NBTool, bool) {
	if toolFactory := nbSystemTools[strings.ToLower(toolName)]; toolFactory != nil {
		if tool, err := toolFactory(accountId); err == nil {
			return tool, true
		}
		// Factory exists but failed to build for this account — fall through
		// to the custom and MCP sources rather than returning false, so a
		// transient build failure on one source does not mask a valid tool
		// with the same name registered elsewhere.
	}

	if tool, ok := GetCustomNbTool(accountId, toolName); ok {
		return tool, true
	}

	// MCP integration tools: ListMCPIntegrationTools is cached for 30 minutes
	// per account, so the typical cost here is a slice scan over already-loaded
	// tool metadata rather than a network call.
	for _, t := range ListMCPIntegrationTools(accountId) {
		if t != nil && strings.EqualFold(t.Name(), toolName) {
			return t, true
		}
	}

	return nil, false
}

var toolCacheInvalidators []func(accountId string)

func RegisterToolCacheInvalidator(fn func(accountId string)) {
	toolCacheInvalidators = append(toolCacheInvalidators, fn)
}

func InvalidateAllCaches(accountId string) {
	for _, fn := range toolCacheInvalidators {
		fn(accountId)
	}
}

func NewCustomTool(tool ToolDto) NBTool {
	switch tool.ExecutorType {
	case ToolExecutorTypeMCP:
		// MCP tools from llm_tools are deprecated — use MCP integrations instead
		slog.Warn("tools: MCP executor type in llm_tools is deprecated, use MCP integrations", "tool_id", tool.Id)
		return nil
	case ToolExecutorTypeContainer:
		return nbCustomContainerTool{tool: tool}
	default:
		// Workflow executor type is deprecated
		slog.Warn("tools: unsupported executor type in NewCustomTool", "executor_type", tool.ExecutorType)
		return nil
	}
}
