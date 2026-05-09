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

func GetNBTool(accountId string, toolName string) (NBTool, bool) {
	toolFactory := nbSystemTools[strings.ToLower(toolName)]
	if toolFactory == nil {
		return GetCustomNbTool(accountId, toolName)
	}
	tool, err := toolFactory(accountId)
	if err != nil {
		return nil, false
	}
	return tool, true
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
