package agents

import (
	"encoding/json"
	"fmt"
	"nudgebee/llm/agents/aws"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// LogsAgentName is the `logs` log investigator. fetch_logs_direct is a plain
// NBTool that calls FetchLogsAgent.Execute() directly rather than being a
// nested sub-agent, so its LLM call attributes to this agent's own agent_id
// instead of creating a second llm_conversation_agent row.
//
// Mode classification and prompt content are this agent's own copies, kept in
// agent_log_prompts.go — see that file's doc comment for why some symbols
// there still carry a V3 suffix.
const LogsAgentName = "logs"

// FetchLogsDirectToolName is the merged fetch tool's registered name — distinct
// from FetchLogsAgentName ("fetch_logs") since that name is already bound to
// the fetch_logs agent-as-tool registration.
const FetchLogsDirectToolName = "fetch_logs_direct"

func init() {
	// Register as a tool too (not just an agent) so other agents can delegate to it by name.
	toolDescription := `Retrieves and analyzes logs from various sources (Kubernetes, Loki, Elasticsearch, Datadog, Signoz) by translating natural language questions into log queries. Handles its own resource discovery (e.g., finding the correct pod name or namespace) and runs investigation loops over saved log files when the user is asking about root causes.`
	toolInput := "Provide a log question in natural language, preserving the user's wording verbatim: investigation wording (why/root cause/diagnose/troubleshoot), enumeration wording (list/summarize errors), or routine wording (recent/tail logs) — the agent's mode classifier routes off this wording."
	toolOutput := "Markdown answer with cited log evidence (timestamps, error signatures). Investigations include a 5-Why causality chain and a time-window callout when errors cluster."

	core.RegisterTargetAwareNBAgentFactoryAndTool(LogsAgentName, func(accountId string) (core.NBAgent, error) {
		return getLogAgent(security.NewRequestContextForSuperAdmin(), accountId)
	}, toolDescription, toolInput, toolOutput)
	toolcore.RegisterNBToolFactory(FetchLogsDirectToolName, func(accountId string) (toolcore.NBTool, error) {
		return &fetchLogsDirectTool{accountId: accountId}, nil
	})
	// Back-compat: fetch_logs_v3 is this tool's pre-rename name.
	toolcore.RegisterNBToolAlias("fetch_logs_v3", FetchLogsDirectToolName)
}

// getLogAgent resolves the account's observability provider: GCP/Azure/AWS
// cloud-only accounts route to their dedicated agents, "k8s" collapses to the
// empty (kubectl-only) provider, everything else gets the generic ReAct
// investigator.
func getLogAgent(ctx *security.RequestContext, accountId string) (core.NBAgent, error) {
	provider, err := tools.GetLogProvider(accountId)
	if err != nil {
		ctx.GetLogger().Warn("log: unable to resolve log provider, defaulting to kubectl-only path", "error", err)
		provider = services_server.ObservabilityProvider{}
	}
	switch strings.ToLower(strings.TrimSpace(provider.Provider)) {
	case "gcp":
		return newGcpLogsAgent(accountId), nil
	case "azure":
		return newAzureLogsAgent(accountId), nil
	case "aws":
		return aws.NewAwsLogsAgent(accountId), nil
	}
	if provider.Provider == "k8s" {
		provider = services_server.ObservabilityProvider{}
	}
	return newLogAgent(accountId, provider), nil
}

// LogAgent is the ReAct log investigator. fetch_logs_direct is a leaf tool
// rather than a nested sub-agent — see LogsAgentName's doc comment for why.
type LogAgent struct {
	accountId string
	provider  services_server.ObservabilityProvider

	toolsOnce sync.Once
	tools     []toolcore.NBTool
}

func (l *LogAgent) AcceptsExecutionTarget() {}

func newLogAgent(accountId string, provider services_server.ObservabilityProvider) *LogAgent {
	return &LogAgent{accountId: accountId, provider: provider}
}

func (l *LogAgent) GetName() string { return LogsAgentName }

func (l *LogAgent) GetNameAliases() []string { return []string{"Logs"} }

func (l *LogAgent) GetDescription() string {
	return `Retrieves and analyzes logs from various sources (Kubernetes, Loki, Elasticsearch, Datadog, Signoz) by translating natural language questions into log queries. Handles its own resource discovery (e.g., finding the correct pod name or namespace) and runs investigation loops over saved log files when the user is asking about root causes. Use this for: fetching application or container logs, searching log entries by keyword or time range, troubleshooting pod/container errors via log output, correlating logs across services. Do NOT use for: querying performance metrics (use ` + "`metrics`" + ` agent), running kubectl commands (use ` + "`kubectl`" + ` or ` + "`kubectl_execute`" + `), or querying Kubernetes events (use ` + "`events`" + ` agent).

When invoking this agent, preserve the user's intent wording in the per-step query:
  - Investigation — phrasings that contain causal words (why / root cause / diagnose / troubleshoot / what caused / broken / failing / crash) or that ask whether issues, outages, or failures occurred. Keep that wording; the agent's classifier uses it to pick the multi-grep investigation workflow with mandatory Call A + E1/E2 recovery.
  - Enumeration — phrasings like "list errors", "show errors", "distinct errors", "summarize errors". Preserve; routes to the per-signature aggregation flow.
  - Routine — phrasings like "recent logs", "tail logs", "last N minutes". Preserve; routes to a single small fetch.
Paraphrasing an investigation as a flat "get logs for <workload>" downgrades it to routine and skips the deeper analysis.`
}

func (l *LogAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// GetModelCategory explicitly opts into Retrieval, matching agentModelCategory's default for untagged agents.
func (l *LogAgent) GetModelCategory() core.ModelTier {
	return core.ModelTierRetrieval
}

func (l *LogAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	l.toolsOnce.Do(func() {
		// Direct resource_search_execute tool, not the resource_search agent
		// — the agent-as-tool fans out to Datadog/cloud resource search even
		// for pure-Kubernetes questions that never need those backends.
		names := []string{tools.ToolResourceSearch, FetchLogsDirectToolName, toolcore.ToolExecuteShellCommand}
		var tl []toolcore.NBTool
		for _, name := range names {
			if t, ok := toolcore.GetNBTool(l.accountId, name); ok {
				tl = append(tl, t)
			}
		}
		l.tools = tl
	})
	return l.tools
}

// GetSystemPrompt builds the agent's prompt from the mode classifier and
// shared/mode-specific instruction blocks in agent_log_prompts.go, plus
// routineAppAnchor (ROUTINE mode only, see its doc comment).
func (l *LogAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	mode := classifyLogModeV3(query.Query, query.OriginalQuery)

	var instructions []string
	instructions = append(instructions,
		fmt.Sprintf("**MODE = %s** (set deterministically from the user's verbatim question; workflow below is narrowed to this mode only).", logModeNameV3(mode)),
	)
	instructions = append(instructions, sharedHeaderAndWorkflow()...)

	switch mode {
	case logModeInvestigationV3:
		instructions = append(instructions, investigationInstructions()...)
	case logModeEnumerationV3:
		instructions = append(instructions, enumerationInstructions()...)
	default:
		instructions = append(instructions, routineAppAnchor()...)
		instructions = append(instructions, canonicalQueryAuthoringForRoutine(l.accountId, l.provider))
		instructions = append(instructions, routineInstructions()...)
		instructions = append(instructions, routineParallelFollowupReads())
	}

	instructions = append(instructions, logAnalysisExecutionInstructions()...)
	instructions = append(instructions, outputFormatInstructions(mode)...)
	if query.ParentAgentId != "" && query.ParentAgentId != query.AgentId && query.ParentAgentId != uuid.Nil.String() {
		instructions = append(instructions, "**Sub-agent response:** Return compact findings for the calling agent to compose the user-facing answer: conclusion, relevant counts and time bounds, exact evidence/examples, artifact references, and limitations or unresolved questions. Preserve requested detail and causal reasoning, but omit conversational introductions, repeated summaries, and duplicated raw records. The calling agent will combine these findings with other evidence; do not claim the overall investigation is complete.")
	}
	// The original question is per-request text (for an event investigation it
	// carries the whole event payload). It goes last so everything above it is
	// identical across requests in the same mode and stays in the provider's
	// prompt cache across runs (#39633).
	if orig := strings.TrimSpace(query.OriginalQuery); orig != "" && orig != strings.TrimSpace(query.Query) {
		instructions = append(instructions,
			fmt.Sprintf("**Original user question:** %q", orig),
		)
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert that investigates logs across multiple backends",
		Instructions: instructions,
		Constraints:  sharedConstraints(mode),
	}
}

// routineAppAnchor overrides sharedHeaderAndWorkflow's step-1 resource_search
// mandate for ROUTINE mode only, case 1c (bare service/app/deployment name, no
// pod given): resource_search_execute already does a DB-first lookup, so a
// kubectl pre-check ahead of it is redundant. Cases 1a/1b are unchanged.
//
// Namespace is NOT optional: a cluster can host several similarly-named
// namespaces (e.g. per environment), so an unscoped app-anchor fetch can
// silently return a different namespace's logs. Always confirm the exact
// namespace before fetching.
func routineAppAnchor() []string {
	resolvedBullet := "  - `exact` / `unique` / `unique_owner` (or every result shares one namespace): resolved — you now have namespace + app. Build a canonical query for `fetch_logs_direct`, or use a natural-language command like `\"all logs for app <name> in <namespace>\"` if you cannot map the fields confidently."
	return []string{
		"**This refines workflow step 1c (bare service/app/deployment name, no pod).** Call `resource_search_execute` exactly as step 1 already says (`suggestions`, omit `namespace`, normalize spaces/underscores to hyphens e.g. relay_server → relay-server) — do NOT precede it with a `shell_execute` kubectl round-trip, it already does a DB-first lookup and falls back to kubectl itself on a miss, so a manual pre-check would just re-derive what one call already resolves. Then use its `match_quality` field to resolve the namespace instead of guessing:",
		resolvedBullet,
		"  - `multiple` (spans more than one namespace — common on a cluster hosting several environments): do NOT silently pick one. Prefer the PLAIN base namespace (the one with no environment suffix) ONLY when exactly one such candidate exists, and say so in your final answer. Otherwise report the ambiguity (list the candidates) instead of fetching.",
		"  - `none`: the name may not be an exact k8s identifier — broaden the call (drop `resource_type`, try a shorter fragment) before falling back to `shell_execute`.",
	}
}

// routineParallelFollowupReads extends investigationInstructions' "chain
// multiple shell_execute reads into ONE call" rule to ROUTINE mode, where a
// follow-up read is the exception rather than the rule — mandating one
// chained command instead of separate actions makes compliance structural
// rather than dependent on the model choosing to batch.
func routineParallelFollowupReads() string {
	return "**If you do need more than one shell_execute look at the file_ref, chain them into ONE call** (`cmd1; echo '---'; cmd2`) instead of separate actions — one round trip instead of two."
}

// canonicalQueryAuthoringForRoutine gives ROUTINE-mode callers the field
// guidance needed to supply a canonical query once resource_search resolves
// namespace and app. The unified planner still checks the supplied query.
func canonicalQueryAuthoringForRoutine(accountId string, provider services_server.ObservabilityProvider) string {
	fields, indices := fetchLabelsAndIndices(accountId, provider)
	labelMappings := provider.Capabilities.LabelMappings

	var b strings.Builder
	b.WriteString("**Call fetch_logs_direct with canonical JSON, not a sentence — this is the default for ROUTINE mode once you know namespace + app.** Pass the canonical JSON query as `command`; fetch_logs_direct checks it in its unified query-planning call before execution. Schema: `{\"where\": {\"<field>\": {\"<operator>\": \"<value>\"}}, \"time_range\": \"<string>\", \"limit\": <number>}` (use `_and`/`_or` arrays of filter objects for multiple conditions). Operators: _eq, _neq, _gt, _gte, _lt, _lte, _in, _like, _ilike, _contains, _is_null.\n")

	if len(labelMappings) > 0 {
		canonical := make([]string, 0, len(labelMappings))
		for k := range labelMappings {
			canonical = append(canonical, k)
		}
		sort.Strings(canonical)
		b.WriteString("Canonical fields for this backend (`canonical_name → backend_field` — use the canonical_name, LEFT side):\n")
		for _, k := range canonical {
			fmt.Fprintf(&b, "  - %s → %s\n", k, labelMappings[k])
		}
		b.WriteString("**HARD RULE:** use ONLY a canonical_name above (or, if none fits, an advertised backend label) — never invent a field name. A namespace-only question filters on the namespace canonical_name ONLY, never the app/workload field — a namespace is not an app name.\n")
	}
	if len(fields) > 0 {
		fmt.Fprintf(&b, "Backend labels (fallback only, when no canonical_name fits): %s\n", strings.Join(fields, ", "))
		b.WriteString(duplicateLabelHeuristicHint)
	}
	if len(indices) > 0 {
		keys := make([]string, 0, len(indices))
		for k := range indices {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		idx := make([]string, 0, len(keys))
		for _, k := range keys {
			idx = append(idx, fmt.Sprintf("%s (%s)", k, indices[k]))
		}
		fmt.Fprintf(&b, "Available indices (pick the most relevant, or omit `index` to use the account default): %s\n", strings.Join(idx, ", "))
	} else if provider.DefaultIndex != "" {
		fmt.Fprintf(&b, "Account default log index (used when `index` is omitted): %s.\n", provider.DefaultIndex)
	}
	b.WriteString("Copy every workload/pod/namespace/container name verbatim from the user's question — never substitute or drop one. Honour an explicit time window exactly (\"last 30m\" → `\"time_range\": \"30m\"`); default to `\"1h\"` / `\"limit\": 1000` when none was given. If you are not fully confident the canonical JSON is correct, phrase a natural-language `command` instead — the unified planner handles it before execution.\n")

	return b.String()
}

// preBuiltCanonicalQuery recognizes canonical `{"where": ...}` JSON in a
// planner response. A natural-language command cannot have this shape.
func preBuiltCanonicalQuery(s string) (string, bool) {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "{") {
		return "", false
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return "", false
	}
	if _, ok := parsed["where"]; !ok {
		return "", false
	}
	return trimmed, true
}

// duplicateLabelHeuristicHint nudges the model toward short Kubernetes-native
// label names (app/namespace/pod) over OTel-duplicate equivalents
// (k8s_deployment_name, etc.) when advertising fallback backend labels — on
// accounts with sparse label-mapping enrichment, the short form is more
// often the one with actual populated data.
const duplicateLabelHeuristicHint = "**When multiple backend labels could represent the same concept** (e.g. both `app` and `k8s_deployment_name` for workload identity, or `namespace` and `k8s_namespace_name`), prefer the SHORT Kubernetes-native form (`app`, `namespace`, `pod`) over an OTel-semantic-convention duplicate (`k8s_deployment_name`, `k8s_namespace_name`, `k8s_pod_name`, `service_name`, `service_namespace`) — on accounts without full label-mapping enrichment the short form is more often the one with actual populated values; the OTel-style duplicate can exist in the label schema with no matching data and silently returns zero rows.\n"

// buildCanonicalLogQueryPromptV3 is this agent's own copy of
// FetchLogsAgent.buildCanonicalLogQueryPrompt, with duplicateLabelHeuristicHint
// appended after each backend-labels listing.
func buildCanonicalLogQueryPromptV3(provider services_server.ObservabilityProvider, fields []string, indices map[string]string) string {
	providerName := provider.Provider
	labelMappings := provider.Capabilities.LabelMappings
	defaultIndex := provider.DefaultIndex

	supportedOperators := resolveQueryOperators(provider.Capabilities.SupportedOperators)
	opSet := make(map[string]struct{}, len(supportedOperators))
	for _, op := range supportedOperators {
		opSet[op] = struct{}{}
	}
	hasOp := func(op string) bool { _, ok := opSet[op]; return ok }

	canonical := make([]string, 0, len(labelMappings))
	for k := range labelMappings {
		canonical = append(canonical, k)
	}
	sort.Strings(canonical)
	useCanonical := len(canonical) > 0

	var b strings.Builder
	b.WriteString("**GOAL:** Only Generate Query, Cannot Execute Query.\n")
	b.WriteString("You are an expert in generating provider-independent JSON log queries from natural language.\n")
	b.WriteString("Your goal is to create a valid JSON query based on the user's question.\n")
	b.WriteString("Follow this JSON schema:\n")
	b.WriteString(`{"where": {"<field>": {"<operator>": "<value>"}}, "_or": [ ... ], "_and": [ ... ]}, "limit": <number>, "time_range": "<string>", "start_time": "<string>", "index": "<string>"}` + "\n")
	b.WriteString("The `where` clause is for filtering. For `_and` or `_or` operators, the value is an array of filter objects.\n")
	fmt.Fprintf(&b, "  - **Operators**: %s\n", strings.Join(supportedOperators, ", "))

	if useCanonical {
		b.WriteString("\n**Canonical fields for THIS backend — `canonical_name → backend_field`. Use the canonical_name (LEFT side) in your query; the server resolves it to the backend_field automatically:**\n")
		for _, k := range canonical {
			fmt.Fprintf(&b, "   - %s → %s\n", k, labelMappings[k])
		}
		b.WriteString("Map the user's wording to the closest canonical_name above, inferring its meaning from the backend_field it resolves to:\n")
		b.WriteString("   - a workload / service / app / deployment name → the canonical_name resolving to a deployment/app/service field\n")
		b.WriteString("   - a pod name → the canonical_name resolving to a pod field\n")
		b.WriteString("   - a namespace → the canonical_name resolving to a namespace field\n")
		b.WriteString("   - a container name → the canonical_name resolving to a container field\n")
		b.WriteString("   - log text / error keywords → the canonical_name resolving to the log body / message / content field\n")
		b.WriteString("ALWAYS prefer a `canonical_name` over a backend label that refers to the SAME concept — a backend label is a fallback ONLY for a concept that has NO matching canonical_name. Use ONLY names that appear in the lists above (a `canonical_name`, or an advertised backend label); never invent or guess a field name.\n")
		b.WriteString("**Namespace vs workload — do NOT confuse them.** When the question names a NAMESPACE but NO specific workload/app/pod (e.g. \"errors in nudgebee\", \"logs in the demo namespace\", \"401s in nudgebee\", \"anything failing in <ns>\"), filter on the namespace canonical_name ONLY — NEVER put that value in the workload/app/pod field. A bare environment / namespace name (nudgebee, demo, prod, staging, …) is NOT an app name.\n")
		if len(fields) > 0 {
			b.WriteString("Backend labels (use ONLY when NO canonical_name above fits the concept):\n")
			fmt.Fprintf(&b, "   %s\n", strings.Join(fields, ", "))
			b.WriteString(duplicateLabelHeuristicHint)
		}
		b.WriteString("**HARD RULE — field names:** Emit ONLY a `canonical_name` from the list above (or, if none fits, a backend label from the line above). NEVER invent a field name. In particular do NOT emit `service_name`, `service.name`, `service`, `pod`, `message`, `namespace`, or `container` unless that exact word is listed above as a canonical_name — if the word is not listed, use the listed canonical_name that maps to the same concept (e.g. for a workload/service name use the canonical_name resolving to the app/deployment field, which is often `app`).\n")
	} else {
		nativeFields := fields
		if len(nativeFields) == 0 {
			nativeFields = []string{"_body", "namespace", "pod"}
		}
		b.WriteString("AVAILABLE FIELDS for query building\n")
		fmt.Fprintf(&b, "  - **Fields**: %s\n", strings.Join(nativeFields, ", "))
		b.WriteString(duplicateLabelHeuristicHint)
		b.WriteString("- MUST use ONLY the fields listed above. Do not invent fields.\n")
	}

	if len(indices) > 0 {
		keys := make([]string, 0, len(indices))
		for k := range indices {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		indexList := make([]string, 0, len(indices))
		for _, name := range keys {
			indexList = append(indexList, fmt.Sprintf("%s (%s)", name, indices[name]))
		}
		b.WriteString("AVAILABLE ELASTICSEARCH INDICES:\n")
		fmt.Fprintf(&b, "  %s\n", strings.Join(indexList, ", "))
		if defaultIndex != "" {
			fmt.Fprintf(&b, "  Account default index (used when `index` is omitted): %s\n", defaultIndex)
		}
		b.WriteString("Pick the most relevant index based on the user's question. If unsure, omit the index field to use the account default.\n")
	} else if defaultIndex != "" {
		fmt.Fprintf(&b, "Account default log index (used when `index` is omitted): %s. Omit the `index` field unless the user's question implies a different source.\n", defaultIndex)
	}

	b.WriteString("\n**Constraints:**\n")
	b.WriteString("- Use ONLY the operators listed above. Do not invent operators.\n")
	switch {
	case hasOp("_ilike"):
		b.WriteString("- Prefer the `_ilike` operator for case-insensitive text/pattern matches over `_eq`.\n")
	case hasOp("_like"):
		b.WriteString("- Prefer the `_like` operator for text/pattern matches over `_eq`.\n")
	case hasOp("_contains"):
		b.WriteString("- Prefer the `_contains` operator for substring text matches over `_eq`.\n")
	}
	b.WriteString("- **HARD RULE — values come from the QUESTION, never from the examples.** Copy every workload / pod / namespace / container name from the user's question VERBATIM into the filter value (\"llm-server\", \"nudgebee\", \"ordered-app-0\"). NEVER substitute a different name you happen to know (do NOT turn \"ordered-app\" into \"order-service\", or \"nudgebee\" into \"production\"), and NEVER drop a name the user gave (a question naming both an app AND a namespace MUST produce a filter on BOTH). The literal values in the examples below (checkout, prod, ...) are ILLUSTRATIVE ONLY.\n")
	b.WriteString("- Do not answer questions without generating a query.\n")
	b.WriteString("- Return only the JSON query object enclosed in triple backticks.\n")

	b.WriteString("\n**Strategy is the caller's responsibility, not yours:**\n")
	b.WriteString("Translate the natural-language question into a query that reflects exactly what was asked. ")
	b.WriteString("Do NOT add an error-pattern filter (e.g. on the log-body field) unless the question explicitly asks for errors/warnings/failures. ")
	b.WriteString("If the caller asks for \"all logs\" or \"recent logs\" with no error keyword, emit a query with NO log-body filter.\n")

	b.WriteString("\n**Always emit `time_range` and `limit` (mandatory):**\n")
	b.WriteString("- A window in the question is a HARD constraint — honour it EXACTLY: \"last 1h\" → `\"time_range\": \"1h\"`, \"last 30m\" → `\"30m\"`, \"last 15m\" → `\"15m\"`. NEVER widen or shrink a window the user gave, whatever the intent (an error/investigation question that says \"last 15m\" still uses `\"15m\"`).\n")
	b.WriteString("- An explicit limit in the question is ALSO a HARD constraint — honour it EXACTLY: \"limit 25\" / \"--tail 25\" → `\"limit\": 25`. NEVER widen or increase a limit the caller gave.\n")
	b.WriteString("- ONLY when the question gives NO window or limit, pick a default from intent: investigation (\"why is X broken\", \"diagnose\", \"what caused\", \"root cause\", \"failing\", \"crash\") → `\"time_range\": \"24h\"`, `\"limit\": 5000`; routine (\"show me logs\", \"recent logs\", \"tail\") → `\"time_range\": \"1h\"`, `\"limit\": 1000`.\n")
	b.WriteString("- Explicit constraints in the CURRENT question ALWAYS override defaults and original user question intent.\n")

	b.WriteString("\n**Examples:**\n")
	examples := canonicalQueryExamples(supportedOperators)
	if !useCanonical {
		if pe := providerSpecificQueryExamples(providerName); len(pe) > 0 {
			examples = pe
		}
	}
	for i, ex := range examples {
		fmt.Fprintf(&b, "Example %d:\n  Question: %s\n  Answer: %s\n", i+1, ex.Question, ex.Answer)
		if ex.Explanation != "" {
			fmt.Fprintf(&b, "  Explanation: %s\n", ex.Explanation)
		}
	}

	return b.String()
}

// fetchLogsDirectTool wraps FetchLogsAgent's existing Execute() as a plain
// NBTool, calling it directly from Call() rather than through
// core.ExecuteAgentToolCall — so no second llm_conversation_agent row is
// created for the fetch step.
type fetchLogsDirectTool struct {
	accountId string
}

func (t *fetchLogsDirectTool) Name() string { return FetchLogsDirectToolName }

func (t *fetchLogsDirectTool) GetType() toolcore.NBToolType { return toolcore.NBToolTypeTool }

func (t *fetchLogsDirectTool) Description() string {
	return `Fetches logs for a resource and returns raw log content. Accepts a complete provider-native query, an applicable provider-native template, a canonical JSON query (` + "`{\"where\": {...}, \"time_range\": \"...\", \"limit\": <n>}`" + `), or a natural-language log question. One planner checks the request against the account's resolved default provider and selects native or canonical execution. Saves output to a workspace file so it can be downloaded or grepped via shell_execute.`
}

func (t *fetchLogsDirectTool) InputSchema() toolcore.ToolSchema {
	desc := "A natural-language log question (e.g. 'errors in <service> last 1h', 'why is <service> slow', 'logs for pod <workload>-<6-10 hex>-<5 alnum> in namespace <ns>'), a provider-native query or applicable template, or — for ROUTINE-mode fetches once namespace+app are resolved — canonical JSON `{\"where\": {...}, \"time_range\": \"...\", \"limit\": <n>}`. The unified planner checks the supplied command before execution."
	return toolcore.ToolSchema{
		Type: toolcore.ToolSchemaTypeObject,
		Properties: map[string]toolcore.ToolSchemaProperty{
			"command": {
				Type:        toolcore.ToolSchemaTypeString,
				Description: desc,
			},
			"knowledge_context": {
				Type: toolcore.ToolSchemaTypeString,
				Description: "Optional verbatim excerpt from loaded user knowledge relevant to this request. " +
					"Forward query examples, templates, field/index conventions, and mandatory scope constraints without classifying them; fetch_logs_direct decides whether they are native or canonical.",
			},
			"target": toolcore.AgentTargetSchemaProperty(),
		},
		Required: []string{"command"},
	}
}

func (t *fetchLogsDirectTool) PrepareDelegatedTargetInput(target *toolcore.ExecutionTarget, input string) (string, error) {
	return tools.AddDelegatedTargetObject(input, target)
}

// buildFetchLogsDirectRequest constructs the core.NBAgentRequest for Call.
// AgentId is set to nbCtx.ParentAgentId (the calling agent's own id) so the
// internal query-planning LLM call attributes to that turn, not a new agent.
// OriginalQuery is forwarded to preserve the user's scope and constraints.
func buildFetchLogsDirectRequest(nbCtx toolcore.NbToolContext, input toolcore.NBToolCallRequest) core.NBAgentRequest {
	knowledge := strings.TrimSpace(nbCtx.KBPrestepContent)
	if forwarded, ok := input.Arguments["knowledge_context"].(string); ok && strings.TrimSpace(forwarded) != "" {
		forwarded = strings.TrimSpace(forwarded)
		if knowledge == "" {
			knowledge = forwarded
		} else if !strings.Contains(knowledge, forwarded) {
			knowledge += "\n\n<forwarded_knowledge>\n" + forwarded + "\n</forwarded_knowledge>"
		}
	}
	request := core.NBAgentRequest{
		Query:          input.Command,
		AccountId:      nbCtx.AccountId,
		ConversationId: nbCtx.ConversationId,
		AgentId:        nbCtx.ParentAgentId,
		ParentAgentId:  nbCtx.ParentAgentId,
		MessageId:      nbCtx.MessageId,
		UserId:         nbCtx.UserId,
		QueryContext:   nbCtx.QueryContext,
		QueryConfig:    nbCtx.QueryConfig,
		SessionId:      nbCtx.SessionId,
		OriginalQuery:  nbCtx.OriginalQuery,
		// Carried with OriginalQuery so the query plan knows whether the
		// constraints in it are a person's.
		OriginalQueryFromUser: nbCtx.OriginalQueryFromUser,
		AccountContext:        nbCtx.AccountContext,
		AccountPrompt:         nbCtx.AccountPrompt,
		KBPrestepContent:      knowledge,
	}
	// Translators (including kubectl fallback) consume ConversationContext;
	// retain QueryContext as well for downstream tool execution.
	request.ConversationContext = nbCtx.QueryContext
	return request
}

// Call invokes FetchLogsAgent.Execute() directly using the request built by
// buildFetchLogsDirectRequest, and logs its own wall-clock duration
// (wall_clock_seconds) on both paths.
func (t *fetchLogsDirectTool) Call(nbCtx toolcore.NbToolContext, input toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	target, queryAccountId, err := tools.ResolveObservabilityTarget(nbCtx, input)
	if err != nil {
		return toolcore.NBToolResponse{
			Status: toolcore.NBToolResponseStatusError,
			Data:   err.Error(),
		}, nil
	}
	if target != nil {
		nbCtx.QueryConfig.DelegatedTarget = target
		// Every log query writer reads QueryContext; the request text may still
		// name the target, so say once that it is already the scope.
		nbCtx.QueryContext = strings.TrimSpace(nbCtx.QueryContext + "\n" + target.ScopeNote())
		if tools.IsCloudObservabilityProvider(target.Provider) {
			return toolcore.NBToolResponse{
				Status: toolcore.NBToolResponseStatusError,
				Data:   fmt.Sprintf("fetch_logs_direct does not query %s cloud logging directly; use %s_logs or %s_execute", target.Provider, target.Provider, target.Provider),
			}, nil
		}
	}

	provider, err := tools.GetLogProvider(queryAccountId)
	if target != nil {
		if err != nil || (provider.Provider == "" && !tools.HasConnectedK8sAgent(queryAccountId)) {
			return toolcore.NBToolResponse{
				Status: toolcore.NBToolResponseStatusError,
				Data:   fmt.Sprintf("no log provider configured for target %s %q (account %s); will not fall back to conversation origin account", target.Provider, target.Name, queryAccountId),
			}, nil
		}
	}

	if nbCtx.Ctx == nil {
		return toolcore.NBToolResponse{
			Status: toolcore.NBToolResponseStatusError,
			Data:   "fetch_logs_direct: nil request context",
		}, nil
	}

	start := time.Now()
	request := buildFetchLogsDirectRequest(nbCtx, input)

	fetchAgent := newFetchLogsAgent(queryAccountId)
	var resp core.NBAgentResponse
	resp, err = fetchAgent.ExecuteDirect(nbCtx.Ctx, request)
	wallClockSeconds := time.Since(start).Seconds()
	if err != nil {
		nbCtx.Ctx.GetLogger().Error("fetch_logs_direct: execute failed", "error", err, "wall_clock_seconds", wallClockSeconds)
	} else {
		nbCtx.Ctx.GetLogger().Info("fetch_logs_direct: execute complete", "wall_clock_seconds", wallClockSeconds, "status", resp.Status)
	}
	return buildFetchLogsDirectToolResponse(resp, err)
}

// ExecuteDirect is this agent's own entry point for every fetch_logs_direct
// call. It builds one unified native-or-canonical query plan and executes the
// selected representation. Falls back to the shared executeInner only when
// no provider is configured.
func (a *FetchLogsAgent) ExecuteDirect(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	entryStart := time.Now()
	return runFetchLogsWithTimeout(ctx, resolveFetchLogsWallClockTimeout(), func(tctx *security.RequestContext) (core.NBAgentResponse, error) {
		provider := a.effectiveProvider(request)
		providerName := strings.ToLower(strings.TrimSpace(provider.Provider))
		if providerName == "" {
			// Neither shape ExecuteDirect covers — fall back to the shared
			// NL-driven path exactly like executeInner does.
			tctx.GetLogger().Info("fetch_logs_direct: provider not covered by ExecuteDirect, falling back to executeInner", "provider", providerName, "elapsed", time.Since(entryStart).String())
			return a.executeInner(tctx, request)
		}

		fields, indices := fetchLabelsAndIndices(a.accountId, provider)
		planStart := time.Now()
		plan, planErr := generateLogQueryPlan(tctx, request, provider, fields, indices)
		tctx.GetLogger().Info("fetch_logs_direct: unified query planning complete",
			"provider", providerName,
			"query_type", plan.QueryType,
			"duration", time.Since(planStart).String(),
			"has_error", planErr != nil)
		if planErr != nil {
			// Do not fall back to executeInner: it runs another LLM translator,
			// so it cannot recover from a model outage and may reinterpret a
			// user-supplied native query as a different canonical query.
			return errorResponse(a.GetName(), fmt.Errorf("log query planning: %w", planErr)), nil
		}
		if plan.QueryType == "native" {
			return a.executeNativeQueryDirect(tctx, request, provider, plan.Native)
		}
		jsonQuery, marshalErr := json.Marshal(plan.CanonicalQuery)
		if marshalErr != nil {
			return errorResponse(a.GetName(), fmt.Errorf("encode canonical query plan: %w", marshalErr)), nil
		}

		execStart := time.Now()
		resp, canonicalQuery, err := a.executeCanonicalQueryDirect(tctx, request, provider, string(jsonQuery))
		tctx.GetLogger().Info("fetch_logs_direct: executeCanonicalQueryDirect returned", "duration", time.Since(execStart).String(), "status", resp.Status, "has_error", err != nil)
		if err != nil {
			return resp, err
		}
		if shouldFallbackToKubectl(resp, canonicalQuery) {
			tctx.GetLogger().Info("fetch_logs_direct: falling back to kubectl after canonical-direct result", "elapsed", time.Since(entryStart).String())
			kStart := time.Now()
			kResp, kErr := a.kubectlFallback(tctx, request, provider, resp)
			tctx.GetLogger().Info("fetch_logs_direct: kubectlFallback returned", "duration", time.Since(kStart).String())
			return kResp, kErr
		}
		tctx.GetLogger().Info("fetch_logs_direct: ExecuteDirect complete", "total_duration", time.Since(entryStart).String())
		return resp, nil
	})
}

// executeCanonicalQueryDirect mirrors FetchLogsAgent.generateCanonicalLogQueryAndExecute,
// except jsonQuery is already built rather than derived via translation —
// every downstream step (execution, workspace save, auto-diagnostics) is
// otherwise identical to the shared path.
func (a *FetchLogsAgent) executeCanonicalQueryDirect(ctx *security.RequestContext, request core.NBAgentRequest, provider services_server.ObservabilityProvider, jsonQuery string) (core.NBAgentResponse, string, error) {
	if provider.DefaultIndex != "" {
		jsonQuery = injectDefaultIndexIfMissing(jsonQuery, provider.DefaultIndex)
	}

	stepStart := time.Now()
	logs, toolRefs, err := callTool(ctx, a.accountId, request, tools.ToolLogsExecuteV2, jsonQuery)
	ctx.GetLogger().Info("fetch_logs_direct: step callTool(logs_execute_v2) done", "duration", time.Since(stepStart).String(), "has_error", err != nil, "response_bytes", len(logs))
	if err != nil {
		return errorResponse(a.GetName(), fmt.Errorf("logs_execute_v2: %w", err)), jsonQuery, nil
	}
	if matched, reason := looksLikeFetchError(provider.Provider, logs); matched {
		return errorResponse(a.GetName(), fmt.Errorf("%s fetch failed: %s", provider.Provider, reason)), jsonQuery, nil
	}
	if strings.EqualFold(provider.Provider, "loki") {
		stepStart = time.Now()
		logs = unwrapLokiInnerTimestamps(ctx, logs)
		ctx.GetLogger().Info("fetch_logs_direct: step unwrapLokiInnerTimestamps done", "duration", time.Since(stepStart).String())
	}

	workspaceAccount := request.AccountId
	if workspaceAccount == "" {
		workspaceAccount = a.accountId
	}
	stepStart = time.Now()
	fileRef, flattened, fileRefs := saveLogsToWorkspace(ctx, workspaceAccount, request.ConversationId, provider.Provider, logs)
	ctx.GetLogger().Info("fetch_logs_direct: step saveLogsToWorkspace done", "duration", time.Since(stepStart).String(), "file_ref", fileRef)

	stepStart = time.Now()
	bundleSignal, err := runAutoDiagnosticBundle(ctx, workspaceAccount, request, fileRef)

	ctx.GetLogger().Info("fetch_logs_direct: step runAutoDiagnosticBundle done", "duration", time.Since(stepStart).String(), "has_error", err != nil, "bundle_signal_empty", bundleSignal == "")
	if err != nil {
		return core.NBAgentResponse{}, jsonQuery, err
	}
	return makeFetchResponse(a.GetName(), executedLogQuery(logs, jsonQuery), logs, flattened, fileRef, bundleSignal, mergeRefs(toolRefs, fileRefs)), jsonQuery, nil
}

// buildFetchLogsDirectToolResponse shapes FetchLogsAgent.Execute's result
// into the NBToolResponse this tool returns — in particular, the real
// terminal status is propagated instead of always reporting success. Split
// out from Call() so it's directly unit-testable against hand-built
// resp/err values. See TestBuildFetchLogsDirectToolResponse.
func buildFetchLogsDirectToolResponse(resp core.NBAgentResponse, err error) (toolcore.NBToolResponse, error) {
	if err != nil {
		return toolcore.NBToolResponse{Status: toolcore.NBToolResponseStatusError}, err
	}

	data := ""
	if len(resp.Response) > 0 {
		data = resp.Response[0]
	}

	status := toolcore.NBToolResponseStatusSuccess
	switch resp.Status {
	case core.ConversationStatusFailed:
		status = toolcore.NBToolResponseStatusError
	case core.ConversationStatusTerminated:
		status = toolcore.NBToolResponseStatusTerminated
	case core.ConversationStatusWaiting, core.ConversationStatusWaitingForClientTool:
		status = toolcore.NBToolResponseStatusWaiting
	}

	return toolcore.NBToolResponse{
		Data:       data,
		Type:       toolcore.NBToolResponseTypeJson,
		Status:     status,
		References: resp.References,
	}, nil
}
