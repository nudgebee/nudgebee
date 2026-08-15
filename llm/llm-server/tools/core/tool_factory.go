package core

import (
	"log/slog"
	"strings"
)

var nbSystemTools = map[string]func(accountId string) (NBTool, error){}

// toolAliases maps a deprecated/short name → the canonical tool name that
// should resolve. Applied inside GetNBTool ONLY (resolution path). Deliberately
// NOT enumerated by ListRegisteredSystemToolNames or by search_tools — the
// canonical name is the only one the LLM should see, so aliases are back-compat
// plumbing for historical delegate calls, stored conversations, and any tool-name
// resolution the LLM produces from prior-turn cached knowledge.
var toolAliases = map[string]string{}

func RegisterNBToolFactory(tool string, toolFactory func(accountId string) (NBTool, error)) {
	slog.Info("registering tool", "tool", tool)
	if _, ok := nbSystemTools[strings.ToLower(tool)]; ok {
		slog.Warn("tool already registered", "tool", tool)
	}
	nbSystemTools[strings.ToLower(tool)] = toolFactory
}

// RegisterNBToolAlias adds a deprecated/short name that resolves to the same
// tool as `canonical`. Applied in GetNBTool only — the alias is NOT enumerated
// in the tool list, so search_tools / the planner see only the canonical name.
// Intended for Phase 3 retirement of thin CLI-wrapper agents: e.g.
// `RegisterNBToolAlias("aws", ToolExecuteAwsCliCommand)` keeps stored history
// referencing `delegate_agent(tools=["aws"])` resolving after the wrapping
// agent is unregistered.
func RegisterNBToolAlias(alias, canonical string) {
	toolAliases[strings.ToLower(alias)] = strings.ToLower(canonical)
}

// ListRegisteredSystemToolNames returns every tool name registered via
// RegisterNBToolFactory. Names are returned in stable (sorted) order for
// deterministic test iteration and log-friendly output. Excludes
// account-scoped custom tools and MCP-integration tools — this is only
// the built-in registry, which is what schema-render audits care about.
func ListRegisteredSystemToolNames() []string {
	names := make([]string, 0, len(nbSystemTools))
	for name := range nbSystemTools {
		names = append(names, name)
	}
	// Sort in-place for stable output. Callers can further filter/sort.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j-1] > names[j]; j-- {
			names[j-1], names[j] = names[j], names[j-1]
		}
	}
	return names
}

// automationToolSources provide the account's AI-invocable automations as tools.
// They cannot be ordinary factories: they live in `tools` (they call
// DoRunbookRequest) and `tools` already imports this package, so a direct
// reference here would be an import cycle. Registering a provider at init
// inverts the dependency — the same shape as RegisterNBToolFactory, just
// returning a whole account-scoped set rather than one named tool.
var automationToolSources []func(accountId string) []NBTool

// RegisterAutomationToolSource adds a provider of per-account automation tools.
// Called from package init, so no locking: registration is complete before any
// account is ever resolved.
func RegisterAutomationToolSource(fn func(accountId string) []NBTool) {
	automationToolSources = append(automationToolSources, fn)
}

// listAutomationTools returns the account's AI-invocable automations as tools.
// Providers cache; this is called on the resolution path as well as the
// enumeration path.
func listAutomationTools(accountId string) []NBTool {
	if accountId == "" {
		return nil
	}
	var out []NBTool
	for _, source := range automationToolSources {
		out = append(out, source(accountId)...)
	}
	return out
}

// ListAccountSourcedTools returns every per-account tool whose name is dynamic
// and so cannot appear in any static supported-tools list: MCP integration
// tools, and the account's AI-invocable automations. Agents that want both ask
// once rather than remembering to append two sources in the right order.
//
// Returns a fresh slice. Both underlying sources hand back their cached slices
// directly, so appending onto either would write into a live cache's backing
// array.
//
// The lean orchestrators deliberately do NOT use this — they call
// ListMCPIntegrationTools alone, because their design is a trimmed context plus
// search_tools, which reaches automations through GetEnabledNBTools instead.
func ListAccountSourcedTools(accountId string) []NBTool {
	mcpTools := ListMCPIntegrationTools(accountId)
	automationTools := listAutomationTools(accountId)
	if len(mcpTools) == 0 && len(automationTools) == 0 {
		return nil
	}
	out := make([]NBTool, 0, len(mcpTools)+len(automationTools))
	out = append(out, mcpTools...)
	out = append(out, automationTools...)
	return out
}

// GetNBTool resolves a tool name to an NBTool by consulting, in order:
//
//  1. Built-in system tools registered via RegisterNBToolFactory.
//  2. Account-scoped custom tools (the llm_tools DB table).
//  3. Account-scoped MCP integration tools discovered via ListMCPIntegrationTools.
//  4. The account's AI-invocable automations (listAutomationTools).
//
// The MCP branch is required because the agent-create / agent-update validation
// path uses this function to verify that every tool the user picked exists for
// their account. Without it, MCP tools surfaced by the UI's tool list (which
// already includes the MCP source) would fail validation, return a generic
// 500, and surface to users as an opaque "internal error".
func GetNBTool(accountId string, toolName string) (NBTool, bool) {
	// Resolve alias to canonical before lookup so back-compat names hit the
	// same factory as the canonical registration.
	if canonical, isAlias := toolAliases[strings.ToLower(toolName)]; isAlias {
		toolName = canonical
	}
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

	for _, t := range listAutomationTools(accountId) {
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
