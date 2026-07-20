package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"sort"
	"strings"
)

const TracesDefaultV2AgentName = "traces_default_v2"

// defaultTraceQueryOperators is the comparison-operator set advertised to the LLM when
// get_default_provider omits capabilities.supported_operators (older backends / fetch
// failure). Combinators (_or/_and) are unioned in by resolveTraceQueryOperators.
var defaultTraceQueryOperators = []string{"_eq", "_neq", "_in", "_not_in", "_gt", "_gte", "_lt", "_lte", "_like", "_is_null"}

// resolveTraceQueryOperators picks the operator set named in the v2 trace prompt. Uses
// the backend's capabilities.supported_operators when present (so the model never emits
// an operator the provider can't execute), else the static default; `_or`/`_and` are
// always appended. De-duplicated, backend order preserved then combinators. Mirror of
// resolveQueryOperators (agent_log_fetch_v2.go) for traces.
func resolveTraceQueryOperators(providerOperators []string) []string {
	base := providerOperators
	if len(base) == 0 {
		base = defaultTraceQueryOperators
	}
	combinators := []string{"_or", "_and"}
	ops := make([]string, 0, len(base)+len(combinators))
	seen := make(map[string]struct{}, len(base)+len(combinators))
	add := func(op string) {
		op = strings.TrimSpace(op)
		if op == "" {
			return
		}
		if _, dup := seen[op]; dup {
			return
		}
		seen[op] = struct{}{}
		ops = append(ops, op)
	}
	for _, op := range base {
		add(op)
	}
	for _, op := range combinators {
		add(op)
	}
	return ops
}

// TracesDefaultAgentV2 is the integration-agnostic (v2) traces agent. It embeds
// TracesDefaultAgent (inheriting the ReAct planner and interface methods) and overrides
// only the tool, name, and prompt: it drives the canonical `traces_execute_v2` tool and
// builds a capability-driven prompt from the provider's advertised label mappings +
// operators (from the get_default_provider capability API). When a provider advertises
// no label mappings it falls back to the shared generic trace schema.
type TracesDefaultAgentV2 struct {
	TracesDefaultAgent
}

func newTracesDefaultAgentV2(accountId string, provider services_server.ObservabilityProvider) TracesDefaultAgentV2 {
	return TracesDefaultAgentV2{
		TracesDefaultAgent: TracesDefaultAgent{accountId: accountId, provider: provider},
	}
}

func (l TracesDefaultAgentV2) GetName() string {
	return TracesDefaultV2AgentName
}

func (l TracesDefaultAgentV2) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	toolList := []toolcore.NBTool{}
	if tracesTool, ok := toolcore.GetNBTool(l.accountId, tools.ToolTracesExecuteV2); ok {
		toolList = append(toolList, tracesTool)
	}
	return toolList
}

// maxDiscoveredTraceAttrs caps how many live-discovered attribute keys are injected
// into the prompt. Trace backends can expose hundreds of attribute keys; dumping all
// wastes tokens and drowns the useful ones.
const maxDiscoveredTraceAttrs = 40

// discoveredTraceAttributes returns the live-discovered span/resource attribute keys
// for the account (via traces_list_labels), excluding curated snake_case fields (already
// covered by the schema docs) and anything already advertised in the mapping. These are
// the dotted attribute keys the prompt surfaces as additional queryable fields — the
// trace counterpart of the log agent's live get_labels. Returns (attrs, capped).
func discoveredTraceAttributes(accountId string, provider services_server.ObservabilityProvider, advertised map[string]string) ([]string, bool) {
	return filterDiscoveredTraceAttributes(tools.FetchTraceLabelKeys(accountId, provider), advertised)
}

// filterDiscoveredTraceAttributes is the pure selection/cap logic behind
// discoveredTraceAttributes, split out so it can be unit-tested without a live
// label fetch. Keeps only dotted attribute keys not already advertised, sorted
// and capped at maxDiscoveredTraceAttrs. Returns (attrs, capped).
func filterDiscoveredTraceAttributes(labels []string, advertised map[string]string) ([]string, bool) {
	var attrs []string
	for _, k := range labels {
		if !strings.Contains(k, ".") {
			continue // curated snake_case fields are already covered by the schema docs
		}
		if _, ok := advertised[k]; ok {
			continue // already advertised via the mapping
		}
		attrs = append(attrs, k)
	}
	sort.Strings(attrs)
	if len(attrs) > maxDiscoveredTraceAttrs {
		return attrs[:maxDiscoveredTraceAttrs], true
	}
	return attrs, false
}

func (l TracesDefaultAgentV2) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	operators := resolveTraceQueryOperators(l.provider.Capabilities.SupportedOperators)
	labelMappings := l.provider.Capabilities.LabelMappings

	// Live, per-account field discovery (mirrors the log agent's get_labels call):
	// the trace backend's actual span/resource attribute keys, minus anything already
	// advertised via the mapping. services-server makes these queryable as flat fields
	// (any non-curated key resolves to the attribute maps). Degrades to empty on a
	// discovery failure so query generation still works.
	discoveredAttrs, discoveredCapped := discoveredTraceAttributes(l.accountId, l.provider, labelMappings)

	instructions := []string{
		// Provider-independent framing (mirrors the log agent): the agent only emits
		// a canonical query — services-server resolves and translates it per provider.
		// Naming the backend here buys nothing (the canonical fields/operators below
		// carry everything) and risks tempting the model into backend-specific syntax.
		"You are an expert in generating provider-independent JSON trace queries from natural language.",
		"Your goal is to answer the user's question by querying the account's traces.",
		"To answer user questions, you MUST use the `traces_execute_v2` tool.",
		"The tool takes a canonical, provider-independent JSON query. services-server resolves the account's trace provider and translates the query for it.",
		"The JSON query schema is: `{\"where\": {\"<field>\":{\"<operator>\":\"<value>\"}}}`",
		fmt.Sprintf("Available operators: %s", strings.Join(operators, ", ")),
	}

	if len(labelMappings) > 0 {
		// Capability-driven: advertise this backend's canonical→backend field vocabulary.
		canonical := make([]string, 0, len(labelMappings))
		for k := range labelMappings {
			canonical = append(canonical, k)
		}
		sort.Strings(canonical)
		instructions = append(instructions, "Available fields for THIS backend — `canonical_name → backend_field`. Use the canonical_name (LEFT side) in your query; the server resolves it to the backend_field automatically:")
		for _, k := range canonical {
			instructions = append(instructions, fmt.Sprintf("   - %s → %s", k, labelMappings[k]))
		}
		instructions = append(instructions,
			"**HARD RULE — field names:** Emit ONLY a field listed in this prompt (a canonical_name above, or a discovered attribute below). NEVER invent a field name.",
			"**Duration conversion:** 1 second = 1000000000 nanoseconds, 1 millisecond = 1000000 nanoseconds.",
		)
	} else {
		// No advertised mappings — fall back to the shared generic trace schema
		// (identical to v1), including the source/destination + duration rules.
		instructions = append(instructions, "Available fields and operators:")
		instructions = append(instructions, genericTraceFieldDocLines()...)
	}

	// Live-discovered attributes (both paths): additional per-account span/resource
	// attribute keys, queryable as flat fields alongside the canonical schema.
	if len(discoveredAttrs) > 0 {
		instructions = append(instructions,
			"Additional attributes present in THIS account's traces — queryable directly as a flat field, e.g. `{\"where\": {\"http.method\": {\"_eq\": \"POST\"}}}`:",
			"   "+strings.Join(discoveredAttrs, ", "),
		)
		if discoveredCapped {
			instructions = append(instructions, "   (most common shown; more attributes exist)")
		}
	}

	instructions = append(instructions,
		"Once you have results, provide a concise, human-readable answer.",
		"**STRICT SECURITY RULE:** You MUST NOT include the JSON query, the tool name, or any internal implementation details in your final answer to the user.",
		"**Empty Results Handling:** If the tool returns no data, state 'No traces were found for the specified criteria'. Do not expose internal queries.",
	)

	constraints := []string{
		"Only use the `traces_execute_v2` tool to query trace data.",
		"Do not answer questions without using the `traces_execute_v2` tool.",
		"Ensure the JSON query is valid before calling the tool.",
		"**NO QUERY LEAKAGE:** You are strictly forbidden from including any internal JSON queries or tool names in your final user-facing response.",
	}

	toolUsage := map[string][]string{
		tools.ToolTracesExecuteV2: {
			"Use this tool to query traces via services-server, which resolves the account's provider and translates the canonical query for it.",
			"Input: a valid canonical JSON query with a 'where' clause. Optional: start_time, end_time, or range (e.g. '2d', '1w', '1h').",
			"Output: the trace data returned by the query.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "Traces expert generating canonical, provider-independent JSON queries for the Nudgebee trace API",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		OutputFormat: "A clear and concise summary of the query results.",
		Examples:     canonicalTraceQueryExamples(),
		Rag: core.NBAgentPromptRag{
			Module: "traces",
			Format: core.NBAgentPromptRagFormatJson,
		},
	}
}

// canonicalTraceQueryExamples are STRUCTURE-only few-shots. Field names use <PLACEHOLDER>
// tokens so the model learns the JSON shape + operators without anchoring on a specific
// provider's field names — it must substitute the canonical fields advertised in the
// prompt. Mirrors canonicalQueryExamples (agent_log_fetch_v2.go).
func canonicalTraceQueryExamples() []core.NBAgentPromptExample {
	return []core.NBAgentPromptExample{
		{
			Question: "show me recent 5xx failures for a service",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolTracesExecuteV2, Input: `{"where":{"<HTTP_STATUS_FIELD>": {"_gte": 500}, "<SERVICE_FIELD>": {"_eq": "<value>"}}}`, Explanation: "Combine an HTTP status filter with a service filter, using the canonical fields advertised in the prompt."},
			},
		},
		{
			Question: "requests slower than 10 seconds for a service",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolTracesExecuteV2, Input: `{"where": {"<DURATION_FIELD>": {"_gt": 10000000000}, "<SERVICE_FIELD>": {"_eq": "<value>"}}}`, Explanation: "Duration is in nanoseconds (10s = 10000000000)."},
			},
		},
		{
			Question: "show traces for a service in the last 2 hours",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolTracesExecuteV2, Input: `{"where": {"<SERVICE_FIELD>": {"_eq": "<value>"}}, "time_range": "2h"}`, Explanation: "Use time_range for relative windows."},
			},
		},
		{
			Question: "find a specific trace by id",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolTracesExecuteV2, Input: `{"where": {"<TRACE_ID_FIELD>": {"_eq": "<value>"}}}`, Explanation: "Look up a single trace by its ID field."},
			},
		},
	}
}
