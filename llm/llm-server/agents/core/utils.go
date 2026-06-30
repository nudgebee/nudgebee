package core

import (
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	toolcore "nudgebee/llm/tools/core"
	"regexp"
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
func FilterAndInjectDefaultTools(accountId string, agent NBAgent, agentPrompt string, tools []toolcore.NBTool, capabilities toolcore.AgentCapabilities) []toolcore.NBTool {
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
	if capabilities.HasAllowedTools() {
		tools = FilterTools(tools, capabilities)
		if len(tools) == 0 {
			// A pinned allow-list that produced zero tools is almost certainly a misconfiguration —
			// e.g. the allow-listed tools belong to a different agent than the one auto-selected
			// by the router. Surface a clear breadcrumb so support can diagnose without a traceback.
			slog.Warn("tools: allowed_tools allow-list produced an empty tool set",
				"account_id", accountId,
				"allowed_tools", capabilities.AllowedTools)
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
func FilterTools(tools []toolcore.NBTool, capabilities toolcore.AgentCapabilities) []toolcore.NBTool {
	if capabilities.IsEmpty() {
		return tools
	}

	disabledTools := toolcore.NormalizeList(capabilities.DisabledTools)
	allowedTools := toolcore.NormalizeList(capabilities.AllowedTools)

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

// Investigation-classification regexes. All matching is word-boundary based
// (not substring) so "health" no longer matches "healthy", "fail" no longer
// matches "failover", etc. Compiled once at package load.
var (
	// strongInvestigationRe — unambiguous troubleshooting intent, or a state
	// that is inherently anomalous (never neutral retrieval). Fires on its own.
	strongInvestigationRe = regexp.MustCompile(`(?i)\b(investigate|investigating|troubleshoot|troubleshooting|debug|debugging|diagnose|diagnosing|root cause|not working|isn'?t working|stopped working|won'?t start|what'?s wrong|what is wrong|going wrong|oom|oomkilled|oomkill|crashloop|crashloopbackoff|crashlooping|broken)\b`)

	// whyInvestigationRe — a causal "why ..." question. The call site filters out
	// a bare "why" / "why?" so only "why <something>" counts as an investigation.
	whyInvestigationRe = regexp.MustCompile(`(?i)\bwhy\b`)

	// weakSignalRe — failure-ish nouns that are frequently plain retrieval
	// ("error budget", "restart policy", "5xx dashboard"). These only signal an
	// investigation when paired with a problem indicator (noun-demotion).
	weakSignalRe = regexp.MustCompile(`(?i)(\b(error|errors|exception|exceptions|fail|fails|failed|failing|failure|failures|restart|restarts|restarting|crash|crashes|crashing|bug|bugs|issue|issues|problem|problems|slow|slowness|sluggish|latency|timeout|timeouts|unhealthy|pending|stuck|degraded|hang|hanging|outage|throttled|throttling|down)\b|\b5xx\b|\b5[0-9]{2}\b)`)

	// problemIndicatorRe — causal/anomaly context that promotes a weak signal
	// to an investigation.
	problemIndicatorRe = regexp.MustCompile(`(?i)(\bwhy\b|\bcaus|\bbecause\b|\bsuddenly\b|\bstarted\b|\bstopped\b|\bkeeps?\b|\bconstantly\b|\brepeatedly\b|\bintermittent|\b(don'?t|can'?t|won'?t|isn'?t|wasn'?t|aren'?t|weren'?t|hasn'?t|haven'?t|hadn'?t|doesn'?t|didn'?t|shouldn'?t|wouldn'?t|couldn'?t|mustn'?t|needn'?t|ain'?t)\b|\bnot\b|\bunable\b|\bthrow|\bgetting\b|\bseeing\b|\bhitting\b|\bexperienc|\bspik|\bsurge\b|\btoo many\b|\bhigh\b|\bincreas|\bimpact|\baffected\b|\balert|\bfiring\b)`)

	// definitionalPrefixRe — "what is …", "how do I …", "explain …": Query-type
	// (definition / how-to), unless the question is causal (see causalContextRe).
	definitionalPrefixRe = regexp.MustCompile(`(?i)^(how do (i|you|we)|how to|how can (i|we)|how should (i|we)|what is|what are|what'?s|whats|explain|define|tell me about)\b`)

	// causalContextRe — markers that turn a definitional-looking question into a
	// real investigation ("what is causing X to fail", "explain why it crashed").
	causalContextRe = regexp.MustCompile(`(?i)(\bwhy\b|\bcaus|\bwrong\b|\bfailing\b|\bfails\b|\bbroken\b|\bcrash|\bslow|\bnot working\b)`)

	// retrievalPrefixRe — plain read-only/discovery verbs → Query.
	retrievalPrefixRe = regexp.MustCompile(`(?i)^(get|list|show|display|fetch|count|how many|describe|whoami|version)\b`)
)

// IsInvestigationRequestTask reports whether a query is a troubleshooting /
// root-cause request (vs a plain retrieval, how-to, or definitional query).
// It biases toward precision over recall: ambiguous failure-ish nouns are only
// treated as investigation when paired with a problem indicator, so requests
// like "show me health checks" or "what's the restart policy" are no longer
// misclassified as investigations (which would trigger the heavier 5-Whys +
// critique path). A genuine investigation that slips through as a "query" still
// gets full tool access — it just isn't forced through the deep-RCA format.
func IsInvestigationRequestTask(input string) bool {
	lowerInput := strings.ToLower(strings.TrimSpace(input))

	// Strip leading politeness fillers so the prefix checks below see the real
	// verb. Retrieval ("show me") and how-to ("how do i") prefixes are
	// intentionally NOT stripped — they carry intent and are handled below.
	prefixesToRemove := []string{
		"can you ", "can i ", "could you ", "could i ", "would you ",
		"please ", "kindly ", "i want to ", "i need to ", "i'd like to ",
		"id like to ", "help me ",
	}
	changed := true
	for changed {
		changed = false
		for _, p := range prefixesToRemove {
			if strings.HasPrefix(lowerInput, p) {
				lowerInput = strings.TrimSpace(strings.TrimPrefix(lowerInput, p))
				changed = true
			}
		}
	}

	// Definitional / how-to questions are Query-type unless they are causal.
	if definitionalPrefixRe.MatchString(lowerInput) && !causalContextRe.MatchString(lowerInput) {
		return false
	}

	// Unambiguous troubleshooting intent or inherently-anomalous state.
	if strongInvestigationRe.MatchString(lowerInput) {
		return true
	}

	// A causal "why ..." is an investigation, but a bare "why" / "why?" on its
	// own is not (there is nothing to investigate yet). Checked before the
	// retrieval skip so "show me why X crashed" still counts.
	if whyInvestigationRe.MatchString(lowerInput) && strings.TrimRight(lowerInput, " ?!.") != "why" {
		return true
	}

	// Plain retrieval / discovery verbs → Query.
	if retrievalPrefixRe.MatchString(lowerInput) {
		return false
	}

	// Ambiguous failure-ish nouns only count alongside a problem indicator.
	if weakSignalRe.MatchString(lowerInput) && problemIndicatorRe.MatchString(lowerInput) {
		return true
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
// rendered into the cacheable system prefix because AccountPrompt varies per
// account (and per event-analysis request) and would otherwise flip the prefix,
// busting the Account-scope LLM cache. Empty input -> empty string so the
// surrounding template renders cleanly.
func renderGlobalPreferencesBlock(accountPrompt string) string {
	if accountPrompt == "" {
		return ""
	}
	return "<global_preferences>" + accountPrompt + "</global_preferences>"
}

// mergeAccountPrompts composes account-scoped prompt fragments into a single
// AccountPrompt string. Order matters: the first non-empty fragment is placed
// first. Empty fragments are skipped. Exact-match duplicates (after trimming)
// are also skipped, since an admin could plausibly paste the same text into
// both `additional_instructions` and the account-wide GlobalContext — we'd
// rather not pay double the tokens for verbatim duplication. Used to combine
// the per-event-analysis additional instructions (passed via
// WitAdditionalSystemPrompt) with the account-wide GlobalContext, so both
// surfaces reach the LLM inside the same <global_preferences> block.
func mergeAccountPrompts(parts ...string) string {
	var nonEmpty []string
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		nonEmpty = append(nonEmpty, s)
	}
	return strings.Join(nonEmpty, "\n\n")
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

// RenderToolDescriptions exposes reActPromptToolDescriptions for use
// outside the agents/core package. The internal name is kept stable so
// the 8 in-package call sites do not churn. The rendered string is what
// every planner's `{{.tool_descriptions}}` template var receives, so
// this is the bytewise integration point between Tool.Description() and
// the system prompt the LLM actually sees.
func RenderToolDescriptions(tools []toolcore.NBTool) string {
	return reActPromptToolDescriptions(tools)
}
