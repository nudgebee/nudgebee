package core

import (
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	toolcore "nudgebee/llm/tools/core"
	"slices"
	"strings"

	"github.com/samber/lo"
)

// DefaultToolsOptOut lets an agent decline the planner's automatic default-tool injection
// (shell_execute, load_skills). Implement and return true for agents whose tool list is
// already curated by their parent — most importantly the dynamic delegate sub-agent, where
// the parent explicitly chose the toolset and any extras would defeat that scoping.
//
// Capability filtering (allowed_tools / disabled_tools) still applies regardless.
type DefaultToolsOptOut interface {
	OptOutDefaultTools() bool
}

// FilterAndInjectDefaultTools filters tools and injects default ones like shell_execute and load_skills if enabled.
//
// Order of operations:
//  1. Filter the agent's own tool list by capabilities (`disabled_tools` deny, `allowed_tools` allow).
//  2. Inject defaults (shell_execute, load_skills) governed by global config / prompt markers — skipped
//     when the agent implements DefaultToolsOptOut and returns true.
//  3. If an explicit allow-list is in effect, re-apply it so injected defaults must also be allow-listed.
//
// `disabled_tools` historically does NOT block default injection (a documented quirk preserved here).
// `allowed_tools` is a stricter, opt-in scope set by callers (e.g. runbook investigation tasks)
// and therefore overrides default injection too.
func FilterAndInjectDefaultTools(accountId string, agent NBAgent, agentPrompt string, tools []toolcore.NBTool, capabilities map[string]any) []toolcore.NBTool {
	// 1. Initial filtering based on capabilities (e.g. disabled_tools, allowed_tools)
	tools = FilterTools(tools, capabilities)

	skipInjection := false
	if agent != nil {
		if optOut, ok := agent.(DefaultToolsOptOut); ok && optOut.OptOutDefaultTools() {
			skipInjection = true
		}
	}

	// 2. Inject or Remove shell_execute based on global config
	if config.Config.LlmServerShellToolEnabled {
		if !skipInjection {
			found := lo.ContainsBy(tools, func(t toolcore.NBTool) bool {
				return strings.EqualFold(t.Name(), toolcore.ToolExecuteShellCommand)
			})
			if !found {
				if t, ok := toolcore.GetNBTool(accountId, toolcore.ToolExecuteShellCommand); ok {
					tools = append(tools, t)
				}
			}
		}
	} else {
		// If explicitly disabled globally, ensure it's removed even if an agent tried to include it
		tools = lo.Filter(tools, func(t toolcore.NBTool, _ int) bool {
			return !strings.EqualFold(t.Name(), toolcore.ToolExecuteShellCommand)
		})
	}

	// 3. Inject load_skills tool if the agent has KB mappings (indicated by skill-lists in the prompt)
	if !skipInjection && strings.Contains(agentPrompt, "<skill-lists>") {
		found := lo.ContainsBy(tools, func(t toolcore.NBTool) bool {
			return t.Name() == "load_skills"
		})
		if !found {
			if t, ok := toolcore.GetNBTool(accountId, "load_skills"); ok {
				tools = append(tools, t)
			}
		}
	}

	// 4. If callers passed an explicit allow-list, enforce it on the injected defaults too.
	if hasAllowedToolsCapability(capabilities) {
		tools = FilterTools(tools, capabilities)
		if len(tools) == 0 {
			// A pinned allow-list that produced zero tools is almost certainly a misconfiguration —
			// e.g. the allow-listed tools belong to a different agent than the one auto-selected
			// by the router. Surface a clear breadcrumb so support can diagnose without a traceback.
			slog.Warn("tools: allowed_tools allow-list produced an empty tool set",
				"account_id", accountId,
				"allowed_tools", readToolNameList(capabilities, "allowed_tools"))
		}
	}

	return tools
}

// HasShellTool checks if shell_execute is in the agent's final tool list.
// Used to set shell_tool_enabled per-agent instead of globally.
func HasShellTool(tools []toolcore.NBTool) bool {
	for _, t := range tools {
		if t.Name() == toolcore.ToolExecuteShellCommand {
			return true
		}
	}
	return false
}

// HasDelegateAgentTool returns true if the delegate_agent tool is present in the tool list.
func HasDelegateAgentTool(tools []toolcore.NBTool) bool {
	for _, t := range tools {
		if strings.EqualFold(t.Name(), "delegate_agent") {
			return true
		}
	}
	return false
}

// FilterTools applies capability-based deny and allow lists to an agent's tool list.
//
//   - `disabled_tools` (denylist): tools listed here are removed.
//   - `allowed_tools`  (allowlist): when present and non-empty, only tools listed here are kept.
//
// Both lists are matched case-insensitively against the tool's name and any of its aliases
// (`GetNameAliases`). When both lists are present, deny wins on conflict.
func FilterTools(tools []toolcore.NBTool, capabilities map[string]any) []toolcore.NBTool {
	if capabilities == nil {
		return tools
	}

	disabledTools := readToolNameList(capabilities, "disabled_tools")
	allowedTools := readToolNameList(capabilities, "allowed_tools")

	if len(disabledTools) == 0 && len(allowedTools) == 0 {
		return tools
	}

	return lo.Filter(tools, func(t toolcore.NBTool, _ int) bool {
		if matchesToolName(t, disabledTools) {
			return false
		}
		if len(allowedTools) > 0 && !matchesToolName(t, allowedTools) {
			return false
		}
		return true
	})
}

// hasAllowedToolsCapability reports whether the caller passed a non-empty `allowed_tools` allow-list.
func hasAllowedToolsCapability(capabilities map[string]any) bool {
	if capabilities == nil {
		return false
	}
	return len(readToolNameList(capabilities, "allowed_tools")) > 0
}

// readToolNameList extracts a string slice from `capabilities[key]`, accepting either
// `[]string` (Go-native) or `[]any` (JSON-deserialized) representations. Whitespace
// is trimmed and empty entries are dropped so callers don't have to repeat that work.
func readToolNameList(capabilities map[string]any, key string) []string {
	raw, ok := capabilities[key]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		out := make([]string, 0, len(v))
		for _, s := range v {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	}
	return nil
}

// matchesToolName reports whether the tool's name (or any alias) matches one of the given names,
// using case-insensitive comparison. The alias lookup and `Name()` call are hoisted outside the
// loop so a tool with aliases is type-asserted once per FilterTools pass, not once per candidate name.
func matchesToolName(t toolcore.NBTool, names []string) bool {
	if len(names) == 0 {
		return false
	}
	tName := t.Name()
	var aliases []string
	if aliased, ok := t.(interface{ GetNameAliases() []string }); ok {
		aliases = aliased.GetNameAliases()
	}

	for _, name := range names {
		if strings.EqualFold(tName, name) {
			return true
		}
		for _, alias := range aliases {
			if strings.EqualFold(alias, name) {
				return true
			}
		}
	}
	return false
}

// GetWorkspaceInstructions returns standard instructions for agents running in the workspace environment
func GetWorkspaceInstructions() []string {
	return []string{
		"**Full Shell Environment:** You are running in a full shell environment (Alpine Linux).",
		"**Base Directory:** Your working directory is `/app`.",
		"**User:** You are running as non-root user `appuser`.",
		"**Available Tools:** You have access to standard Linux utilities including `grep`, `awk`, `sed`, `curl`, `jq`, `find`, `xargs`, `tar`, `unzip`.",
		"**Capabilities:** You can use pipes (`|`), redirection (`>`, `>>`, `<`), command substitution (`$()`), and environment variables.",
		"**Isolation:** This is an isolated workspace environment for this specific task.",
		"**Cleanup:** Temporary files created in `/tmp` are generally safe but clean up large files if created.",
	}
}

func IsInvestigationRequestTask(input string) bool {
	lowerInput := strings.ToLower(strings.TrimSpace(input))

	// Remove common conversational prefixes to focus on the core intent
	prefixesToRemove := []string{"can you ", "can i ", "please ", "i want to ", "help me ", "could you ", "could i ", "how do i ", "show me "}
	changed := true
	for changed {
		changed = false
		for _, p := range prefixesToRemove {
			if strings.HasPrefix(lowerInput, p) {
				lowerInput = strings.TrimPrefix(lowerInput, p)
				lowerInput = strings.TrimSpace(lowerInput)
				changed = true
			}
		}
	}

	// 1. Immediate skips for common simple commands (Read-only/Discovery)
	simplePrefixes := []string{"get ", "list ", "show ", "whoami", "version", "describe "}
	for _, prefix := range simplePrefixes {
		if strings.HasPrefix(lowerInput, prefix) {
			return false
		}
	}

	// 2. Investigation Keywords (Original list)
	investigationKeywords := []string{
		"investigate", "troubleshoot", "debug", "root cause", "why", "issue", "problem",
		"analyze", "diagnose", "explain", "find out", "look into", "determine cause", "identify cause",
		"health", "restart", "oom", "error", "exception", "failed", "fail", "bug", "fix",
	}

	for _, keyword := range investigationKeywords {
		if strings.Contains(lowerInput, keyword) {
			// For very common/short keywords, we add a slight length threshold
			// to ensure it's a descriptive task and not a simple command.
			if (keyword == "why" || keyword == "issue" || keyword == "problem" || keyword == "explain") && len(lowerInput) <= 15 {
				continue
			}
			return true
		}
	}
	return false
}

// IsConversationalQuery checks if the input is a conversational query
// its keyword based checks.. it doenst have to be perfect as caller also checks other things like IsInvestigationRequestTask/IsDataRetrievalRequest before callting this

func IsConversationalQuery(input string) bool {
	lowerInput := strings.ToLower(input)
	conversationalKeywords := map[string]string{
		"hi": "exact", "hello": "exact", "hey": "exact", "hola": "exact", "greetings": "exact",
		"who are you": "contains", "what are you": "contains", "what is nubi": "contains", "what can you do": "contains", "introduce yourself": "contains",
		"help": "contains", "how to use": "contains", "commands": "contains", "available agents": "contains",
	}

	for kw, matchType := range conversationalKeywords {
		if matchType == "exact" {
			if lowerInput == kw {
				return true
			}
		} else {
			if strings.Contains(lowerInput, kw) {
				return true
			}
		}
	}
	return !IsInvestigationRequestTask(input) && !IsDataRetrievalOrActionRequest(input)
}

func IsDataRetrievalOrActionRequest(input string) bool {
	lowerInput := strings.ToLower(strings.TrimSpace(input))
	words := strings.Fields(lowerInput)
	if len(words) == 0 {
		return false
	}

	// Simple retrieval intent often starts with these verbs or contains them early
	retrievalVerbs := []string{"get", "list", "show", "fetch", "describe", "display", "logs", "events", "what", "memory", "metrics", "scale", "rollback", "restart", "delete", "create", "apply", "patch", "update", "deploy", "sync", "give", "find", "check", "search", "provide", "remember", "prefer"}

	filteredWords := make([]string, 0, len(words))
	for _, w := range words {
		// Filter out common stop words/fillers to focus on the intent
		isStopWord := common.StopWords[w]
		// If it's a stop word BUT also in our retrieval verbs (like 'what'), we keep it
		if !isStopWord || slices.Contains(retrievalVerbs, w) {
			filteredWords = append(filteredWords, w)
		}
	}

	if len(filteredWords) == 0 {
		return false
	}

	// Check the first few filtered words for retrieval intent
	for i := 0; i < len(filteredWords) && i < 2; i++ {
		if slices.Contains(retrievalVerbs, filteredWords[i]) {
			return true
		}
	}

	return false
}

// renderGlobalPreferencesBlock formats the AccountPrompt for inclusion in the
// human-message side of the planner prompt. The block is intentionally NOT
// rendered into the cacheable system prefix because AccountPrompt is only set
// by the event-analysis path and would otherwise flip the prefix per request,
// busting the Account-scope LLM cache. Empty input -> empty string so the
// surrounding template renders cleanly.
func renderGlobalPreferencesBlock(accountPrompt string) string {
	if accountPrompt == "" {
		return ""
	}
	return "<global_preferences>" + accountPrompt + "</global_preferences>"
}

func reActPromptToolDescriptions(tools []toolcore.NBTool) string {
	var sb strings.Builder
	for i, tool := range tools {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		fmt.Fprintf(&sb, "Tool Name: %s\n", tool.Name())
		fmt.Fprintf(&sb, "Type: %s\n", string(tool.GetType()))
		fmt.Fprintf(&sb, "Description: %s", tool.Description())
	}
	return sb.String()
}
