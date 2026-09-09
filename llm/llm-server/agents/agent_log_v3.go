package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"sort"
	"strings"
	"sync"
	"time"
)

// LogsAgentV3Name is a side-by-side variant of LogsAgentName for A/B testing.
// Unlike v1/v2, fetch_logs is not a separately-invoked sub-agent — it's a plain
// NBTool that calls FetchLogsAgentV2.Execute() directly, so its internal LLM
// call (generateCanonicalLogQuery) attributes to THIS agent's own agent_id
// instead of creating a second llm_conversation_agent row. That removes the
// child_agent_id nesting class of bug by construction (see log_analysis_bugs
// notes: A1-A5 were all consequences of a bespoke agent-as-tool wrapper
// drifting from the generic path — there is no wrapper here to drift).
//
// Mode classification and prompt content started as a verbatim reuse of
// agent_log.go's helpers and are now v3's own copies (agent_log_v3_prompts.go
// — classifyLogModeV3, sharedHeaderAndWorkflowV3, routineInstructionsV3,
// investigationInstructionsV3, enumerationInstructionsV3,
// outputFormatInstructionsV3, sharedConstraintsV3) so v3-specific curation
// (the canonical-JSON fast path, fastPathAppAnchor) can edit this agent's own
// prompt text without fighting or drifting v1's identical-looking blocks.
// See that file's doc comment for what has actually diverged so far.
//
// Registered under a distinct name so it never receives production traffic
// until explicitly invoked (@logs_v3) or wired into an orchestrator's tool list.
const LogsAgentV3Name = "logs_v3"

// FetchLogsV3ToolName is the merged fetch tool's registered name — distinct
// from FetchLogsAgentName ("fetch_logs") since that name is already bound to
// the v1/v2 agent-as-tool registration.
const FetchLogsV3ToolName = "fetch_logs_v3"

func init() {
	core.RegisterNBAgentFactory(LogsAgentV3Name, func(accountId string) (core.NBAgent, error) {
		return getLogAgentV3(security.NewRequestContextForSuperAdmin(), accountId)
	})
	toolcore.RegisterNBToolFactory(FetchLogsV3ToolName, func(accountId string) (toolcore.NBTool, error) {
		return &fetchLogsV3Tool{accountId: accountId}, nil
	})
}

// getLogAgentV3 mirrors getLogAgent's provider resolution exactly (agent_log.go)
// — GCP/Azure cloud-only accounts still route to their dedicated agents (v3 does
// not reimplement those paths), "k8s" collapses to the empty (kubectl-only)
// provider, everything else gets the generic ReAct investigator.
func getLogAgentV3(ctx *security.RequestContext, accountId string) (core.NBAgent, error) {
	provider, err := tools.GetLogProvider(accountId)
	if err != nil {
		ctx.GetLogger().Warn("log_v3: unable to resolve log provider, defaulting to kubectl-only path", "error", err)
		provider = services_server.ObservabilityProvider{}
	}
	switch strings.ToLower(strings.TrimSpace(provider.Provider)) {
	case "gcp":
		return newGcpLogsAgent(accountId), nil
	case "azure":
		return newAzureLogsAgent(accountId), nil
	}
	if provider.Provider == "k8s" {
		provider = services_server.ObservabilityProvider{}
	}
	return newLogAgentV3(accountId, provider), nil
}

// LogAgentV3 is the merged ReAct log investigator: same investigation
// methodology as LogAgent, but fetch_logs_v3 is a leaf tool rather than a
// nested sub-agent.
type LogAgentV3 struct {
	accountId string
	provider  services_server.ObservabilityProvider

	toolsOnce sync.Once
	tools     []toolcore.NBTool
}

func newLogAgentV3(accountId string, provider services_server.ObservabilityProvider) *LogAgentV3 {
	return &LogAgentV3{accountId: accountId, provider: provider}
}

func (l *LogAgentV3) GetName() string { return LogsAgentV3Name }

func (l *LogAgentV3) GetNameAliases() []string { return nil }

func (l *LogAgentV3) GetDescription() string {
	return `Retrieves and analyzes logs from various sources (Kubernetes, Loki, Elasticsearch, Datadog, Signoz) by translating natural language questions into log queries. Handles its own resource discovery (e.g., finding the correct pod name or namespace) and runs investigation loops over saved log files when the user is asking about root causes. Use this for: fetching application or container logs, searching log entries by keyword or time range, troubleshooting pod/container errors via log output, correlating logs across services. Do NOT use for: querying performance metrics (use ` + "`metrics`" + ` agent), running kubectl commands (use ` + "`kubectl`" + ` or ` + "`kubectl_execute`" + `), or querying Kubernetes events (use ` + "`events`" + ` agent).

When invoking this agent, preserve the user's intent wording in the per-step query:
  - Investigation — phrasings that contain causal words (why / root cause / diagnose / troubleshoot / what caused / broken / failing / crash) or that ask whether issues, outages, or failures occurred. Keep that wording; the agent's classifier uses it to pick the multi-grep investigation workflow with mandatory Call A + E1/E2 recovery.
  - Enumeration — phrasings like "list errors", "show errors", "distinct errors", "summarize errors". Preserve; routes to the per-signature aggregation flow.
  - Routine — phrasings like "recent logs", "tail logs", "last N minutes". Preserve; routes to a single small fetch.
Paraphrasing an investigation as a flat "get logs for <workload>" downgrades it to routine and skips the deeper analysis.`
}

func (l *LogAgentV3) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// GetModelCategory explicitly opts into Retrieval, matching LogAgent.
func (l *LogAgentV3) GetModelCategory() core.ModelTier {
	return core.ModelTierRetrieval
}

func (l *LogAgentV3) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	l.toolsOnce.Do(func() {
		// tools.ToolResourceSearch ("resource_search_execute") instead of
		// ResourceSearchAgentName ("resource_search"): the agent-as-tool
		// version fans out to Datadog and cloud (AWS/GCP/Azure) resource
		// search on every call regardless of relevance — measured at ~14-17%
		// of total wall time whenever it fires, on a purely-Kubernetes log
		// question that never needed those backends. resource_search_execute
		// is the plain Kubernetes-only tool underneath it: same discovery
		// capability (fuzzy/suggestions/namespace/label search), no LLM-
		// orchestrated fan-out, no nested-agent DB row. See
		// logs-agent-slowness investigation, §"resource_search fan-out".
		names := []string{tools.ToolResourceSearch, FetchLogsV3ToolName, toolcore.ToolExecuteShellCommand}
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

// GetSystemPrompt builds logs_v3's own prompt from the v3-owned copies in
// agent_log_v3_prompts.go (mode classifier + shared/mode-specific instruction
// blocks) plus fastPathAppAnchor (ROUTINE mode only, see its doc comment).
// These are full copies of agent_log.go's (`logs` v1) equivalents, not a
// shared dependency — see agent_log_v3_prompts.go's file doc comment for why:
// v1's identical-looking blocks fought v3-specific curation (fastPathAppAnchor
// and the canonical-JSON fast path) because both told the model to default to
// NL phrasing. Editing the V3 copies never changes v1's prompt or behavior.
func (l *LogAgentV3) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	mode := classifyLogModeV3(query.Query, query.OriginalQuery)

	var instructions []string
	instructions = append(instructions,
		fmt.Sprintf("**MODE = %s** (set deterministically from the user's verbatim question; workflow below is narrowed to this mode only).", logModeNameV3(mode)),
	)
	if orig := strings.TrimSpace(query.OriginalQuery); orig != "" && orig != strings.TrimSpace(query.Query) {
		instructions = append(instructions,
			fmt.Sprintf("**Original user question:** %q", orig),
		)
	}

	instructions = append(instructions, sharedHeaderAndWorkflowV3()...)

	switch mode {
	case logModeInvestigationV3:
		instructions = append(instructions, investigationInstructionsV3()...)
	case logModeEnumerationV3:
		instructions = append(instructions, enumerationInstructionsV3()...)
	default:
		instructions = append(instructions, fastPathAppAnchor()...)
		if canonicalFastPathEnabled() {
			instructions = append(instructions, canonicalQueryAuthoringForRoutine(l.accountId, l.provider))
		}
		instructions = append(instructions, routineInstructionsV3()...)
		instructions = append(instructions, routineParallelFollowupReads())
	}

	instructions = append(instructions, outputFormatInstructionsV3(mode)...)

	return core.NBAgentPrompt{
		Role:         "an SRE expert that investigates logs across multiple backends",
		Instructions: instructions,
		Constraints:  sharedConstraintsV3(mode),
	}
}

// fastPathAppAnchor overrides sharedHeaderAndWorkflowV3's step-1 resource_search
// mandate for logs_v3's ROUTINE mode only, case 1c (bare service/app/deployment
// name, no pod given).
//
// resource_search_execute's own "suggestions" search already does a DB-first
// lookup against cloud_resourses (the collector's near-real-time inventory —
// a new pod is queryable within ~1s) before ever falling back to live kubectl
// internally (tool_resource_search.go:533 "1. DB-first" — see
// searchDbForResources/handleResourceSuggestions). A manual shell_execute
// kubectl round-trip in front of it was therefore pure redundancy — it paid
// for a live relay-server hop (~7-18s observed) to re-derive what a single DB
// read already gives the tool for free. Calling resource_search_execute
// directly is both simpler and faster than the two-step
// kubectl-then-fallback shape this override used before.
//
// v3-only: this is the one point where v3's prompt diverges from v1's; cases
// 1a/1b (ambiguous name, or a hashed pod name is specifically needed) are
// unchanged — resource_search_execute still runs first for those, exactly as
// step 1 (sharedHeaderAndWorkflowV3) says.
//
// Namespace is NOT optional. An earlier version of this override tried an
// unscoped app-anchor fetch ("all logs for app <name>", no namespace) to skip
// discovery entirely — live-tested against this account's own dev cluster
// (which runs relay-server in several similarly-named namespaces for
// different environments, all in the SAME cluster) and it silently returned
// a DIFFERENT namespace's logs while presenting them as correct. See
// logs-agent-slowness investigation. That risk isn't dev-cluster-specific —
// any account running dev/staging/prod in one cluster hits the same failure
// mode — so this version always confirms the exact namespace before
// fetching and never silently guesses across a genuinely ambiguous match.
func fastPathAppAnchor() []string {
	resolvedBullet := "  - `exact` / `unique` / `unique_owner` (or every result shares one namespace): resolved — call `fetch_logs_v3` phrased as `\"all logs for app <name> in <namespace>\"` (namespace-scoped app-anchor, per the Label-anchor rules above)."
	if canonicalFastPathEnabled() {
		resolvedBullet = "  - `exact` / `unique` / `unique_owner` (or every result shares one namespace): resolved — you now have namespace + app, exactly what the canonical-JSON fast path below needs. Build the canonical query yourself and call `fetch_logs_v3` with it directly (skips its internal translation call). Only fall back to a phrased NL command like `\"all logs for app <name> in <namespace>\"` if you are not confident constructing the canonical JSON."
	}
	return []string{
		"**logs_v3 refines workflow step 1c (bare service/app/deployment name, no pod).** Call `resource_search_execute` exactly as step 1 already says (`suggestions`, omit `namespace`, normalize spaces/underscores to hyphens e.g. relay_server → relay-server) — do NOT precede it with a `shell_execute` kubectl round-trip, it already does a DB-first lookup and falls back to kubectl itself on a miss, so a manual pre-check would just re-derive what one call already resolves. Then use its `match_quality` field to resolve the namespace instead of guessing:",
		resolvedBullet,
		"  - `multiple` (spans more than one namespace — common on a cluster hosting several environments): do NOT silently pick one. Prefer the PLAIN base namespace (the one with no environment suffix) ONLY when exactly one such candidate exists, and say so in your final answer. Otherwise report the ambiguity (list the candidates) instead of fetching.",
		"  - `none`: the name may not be an exact k8s identifier — broaden the call (drop `resource_type`, try a shorter fragment) before falling back to `shell_execute`.",
	}
}

// routineParallelFollowupReads extends investigationInstructions' "chain into
// ONE shell_execute call" rule (see its Sweep A/B pattern) to ROUTINE mode,
// which had no equivalent. routineInstructions' "envelope is the answer" step
// means a follow-up shell_execute is the exception rather than the rule for
// routine fetches — but in every routine run captured during the
// logs-agent-slowness investigation, the model chose at least one follow-up
// read anyway, and when it wanted more than one (e.g. a quick look at the
// head of the file AND a grep for WARN/ERROR), those went out as separate,
// sequential ReAct iterations even though both just read the same
// already-saved file_ref and don't depend on each other's result.
//
// Originally phrased as "ask the model to emit both as one parallel batch."
// #36123 measured that exact instruction shape (applied to
// investigationInstructions' Sweep A/B) against 30 days of production
// shell_execute pairs and found it was followed only ~28% of the time — 72%
// still landed in separate turns, because compliance depends on the model
// choosing to batch. The fix there, ported here, is to stop asking for two
// batched *actions* and instead mandate one shell_execute call whose command
// chains every pattern with an echo separator: that's structurally one
// action, one turn, regardless of model behavior.
//
// Kept deliberately terse (~1 line, not a worked-out block with its own
// justification prose): this fires on the exception path only (routine's
// step 5 already tells the model the envelope alone should be enough), but
// its prompt cost is paid on every ROUTINE turn, the dominant query shape in
// A/B testing — see the logs_v3-vs-logs prompt-size measurement in the
// slowness investigation doc. Unlike investigationInstructions' version, this
// one hasn't been measured against real production non-compliance data; a
// short mandate earns its keep more cheaply than a fully-justified block
// would while that's still true.
func routineParallelFollowupReads() string {
	return "**If you do need more than one shell_execute look at the file_ref, chain them into ONE call** (`cmd1; echo '---'; cmd2`) instead of separate actions — one round trip instead of two."
}

// canonicalQueryAuthoringForRoutine lets ROUTINE-mode fetches skip
// fetch_logs_v3's internal generateCanonicalLogQuery translation call: once
// resource_search has resolved namespace+app (fastPathAppAnchor), or the
// question named both explicitly, the model already has every fact that call
// would derive, so phrasing an NL sentence and paying a second LLM call to
// re-derive the same JSON is redundant.
//
// This is additive, not a hard switch: preBuiltCanonicalQuery (below) detects
// the JSON shape on the tool_input and takes the fast path only when it
// matches; anything else (a plain NL sentence, or JSON the model wasn't
// confident enough to emit) still goes through the original translation path
// unchanged. See fetchLogsV3Tool.Call / ExecuteCanonicalDirect.
//
// The field-mapping rules below are ported from buildCanonicalLogQueryPrompt
// (agent_log_fetch_v2.go) — that prompt is what the internal translator uses
// today, and correctness there is exactly as important here: a wrong
// canonical query silently returns the wrong or empty log set. Trimmed to the
// hard rules only (no worked examples, no operator-preference tiering) since
// this is appended to an already-large ROUTINE-mode prompt paid on every
// routine turn, not a dedicated single-purpose prompt.
func canonicalQueryAuthoringForRoutine(accountId string, provider services_server.ObservabilityProvider) string {
	fields, indices := fetchLabelsAndIndices(accountId, provider)
	labelMappings := provider.Capabilities.LabelMappings

	var b strings.Builder
	b.WriteString("**Call fetch_logs_v3 with canonical JSON, not a sentence — this is the default for ROUTINE mode once you know namespace + app, not an optional shortcut.** Pass the canonical JSON query directly as `command`. fetch_logs_v3 detects the JSON shape and executes it immediately, skipping its internal translation call. Schema: `{\"where\": {\"<field>\": {\"<operator>\": \"<value>\"}}, \"time_range\": \"<string>\", \"limit\": <number>}` (use `_and`/`_or` arrays of filter objects for multiple conditions). Operators: _eq, _neq, _gt, _gte, _lt, _lte, _in, _like, _ilike, _contains, _is_null.\n")

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
	b.WriteString("Copy every workload/pod/namespace/container name verbatim from the user's question — never substitute or drop one. Honour an explicit time window exactly (\"last 30m\" → `\"time_range\": \"30m\"`); default to `\"1h\"` / `\"limit\": 1000` when none was given. If you are not fully confident the canonical JSON is correct, phrase a natural-language `command` instead — fetch_logs_v3 falls back to the translator automatically.\n")

	return b.String()
}

// preBuiltCanonicalQuery detects when the caller already supplied a
// ready-to-execute canonical `{"where": ...}` query (per
// canonicalQueryAuthoringForRoutine) instead of a natural-language question,
// so fetchLogsV3Tool.Call can skip fetch_logs_v3's internal translation LLM
// call. A natural-language command is never valid JSON with a top-level
// "where" key, so that combination is an unambiguous signal — no separate
// schema field needed.
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

// duplicateLabelHeuristicHint is appended after any flat backend-labels list
// buildCanonicalLogQueryPromptV3 and canonicalQueryAuthoringForRoutine show
// the model (both the "no canonical_name at all" case and the "canonical_name
// exists but doesn't cover this concept, fall back to raw labels" case).
//
// Root cause this addresses: accounts without full label-mapping enrichment
// (provider.Capabilities.LabelMappings sparse or empty — an explicitly
// anticipated state, see buildCanonicalLogQueryPrompt's original doc comment)
// fall back to advertising the RAW backend label list with no signal about
// which of several plausible entries actually has populated data. Confirmed
// live against this account (a2a30b02, Loki): label_mappings had exactly one
// entry (`content`→`log`); the raw field list included BOTH short
// Kubernetes-native labels (app, namespace, pod) AND duplicate OTel-semantic-
// convention labels (k8s_deployment_name, k8s_namespace_name, k8s_pod_name)
// for the SAME concepts. The model picked k8s_deployment_name for a
// deployment-name filter; services-server's own validation came back with
// `value "relay-server" for label "k8s_deployment_name" was not found for
// this log provider` — the label exists in the schema, the value doesn't,
// forcing an unnecessary kubectl fallback. app/namespace/pod worked
// correctly in every other run. This is a heuristic based on that one
// account's observed pattern, not a guarantee — kept v3-scoped (not ported to
// the shared buildCanonicalLogQueryPrompt) until validated across more
// accounts.
const duplicateLabelHeuristicHint = "**When multiple backend labels could represent the same concept** (e.g. both `app` and `k8s_deployment_name` for workload identity, or `namespace` and `k8s_namespace_name`), prefer the SHORT Kubernetes-native form (`app`, `namespace`, `pod`) over an OTel-semantic-convention duplicate (`k8s_deployment_name`, `k8s_namespace_name`, `k8s_pod_name`, `service_name`, `service_namespace`) — on accounts without full label-mapping enrichment the short form is more often the one with actual populated values; the OTel-style duplicate can exist in the label schema with no matching data and silently returns zero rows.\n"

// generateCanonicalLogQueryV3 is v3's own copy of
// FetchLogsAgentV2.generateCanonicalLogQuery (agent_log_fetch_v2.go) —
// identical LLM-call plumbing (same ModelTierSummary downgrade, same
// GenerateAndTrackLLMContent call), except it renders the prompt via
// buildCanonicalLogQueryPromptV3 instead of the shared buildCanonicalLogQueryPrompt.
// Used by ExecuteV3 whenever the outer ReAct decision didn't already supply
// canonical JSON (preBuiltCanonicalQuery returned false) — i.e. the ROUTINE
// fast path's own fallback, and every INVESTIGATION/ENUMERATION fetch (they
// never author canonical JSON directly, so this is their only translation
// step). Defined here, not in agent_log_fetch_v2.go, so the fix is v3-only
// and v1/v2 callers of the shared generateCanonicalLogQuery are unaffected.
func generateCanonicalLogQueryV3(ctx *security.RequestContext, request core.NBAgentRequest, provider services_server.ObservabilityProvider, fields []string, indices map[string]string) (string, error) {
	prompt := buildCanonicalLogQueryPromptV3(provider, fields, indices)
	messages := buildLogIntentMessages(prompt, request)

	summaryCtx := security.NewRequestContext(
		context.WithValue(ctx.GetContext(), core.ContextKeyModelTier, core.ModelTierSummary),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)
	res, err := core.GenerateAndTrackLLMContent(summaryCtx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, false, messages, true)
	if err != nil {
		return "", err
	}
	if res == nil || len(res.Choices) == 0 {
		return "", fmt.Errorf("empty LLM response")
	}
	return strings.TrimSpace(res.Choices[0].Content), nil
}

// buildCanonicalLogQueryPromptV3 is v3's own copy of
// FetchLogsAgentV2.buildCanonicalLogQueryPrompt (agent_log_fetch_v2.go) —
// byte-identical except duplicateLabelHeuristicHint is appended after each
// flat backend-labels listing (both the "canonical_name exists but this
// concept falls back to raw labels" branch and the "no canonical_name at
// all" branch). Reuses the shared resolveQueryOperators/canonicalQueryExamples/
// providerSpecificQueryExamples helpers as-is — they don't need to diverge,
// only the labels-ambiguity guidance does.
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
	b.WriteString("- A window in the question is a HARD constraint — honour it EXACTLY: \"last 1h\" → `\"time_range\": \"1h\"`, \"last 30m\" → `\"30m\"`, \"last 6h\" → `\"6h\"`. NEVER widen or shrink a window the user gave, whatever the intent (an error/investigation question that says \"last 1h\" still uses `\"1h\"`).\n")
	b.WriteString("- ONLY when the question gives NO window, pick a default from intent: investigation (\"why is X broken\", \"diagnose\", \"what caused\", \"root cause\", \"failing\", \"crash\") → `\"time_range\": \"24h\"`, `\"limit\": 5000`; routine (\"show me logs\", \"recent logs\", \"tail\") → `\"time_range\": \"1h\"`, `\"limit\": 1000`.\n")
	b.WriteString("- Read the caller's ORIGINAL user question (when provided) to classify intent.\n")

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

// fetchLogsV3Tool is a plain NBTool wrapping FetchLogsAgentV2's existing,
// unchanged Execute() — canonical query generation, services-server dispatch,
// kubectl fallback, Datadog facet path, workspace save, bundle_signal, and the
// 300s wall-clock guard are all reused as-is. The only thing that changes is
// the wrapping: this calls Execute() as a plain function from within a tool
// Call(), instead of routing through core.ExecuteAgentToolCall — so no second
// llm_conversation_agent row is created for the fetch step.
type fetchLogsV3Tool struct {
	accountId string
}

func (t *fetchLogsV3Tool) Name() string { return FetchLogsV3ToolName }

func (t *fetchLogsV3Tool) GetType() toolcore.NBToolType { return toolcore.NBToolTypeTool }

func (t *fetchLogsV3Tool) Description() string {
	if !canonicalFastPathEnabled() {
		return `Fetches logs for a resource and returns raw log content. Translates a natural-language log question into the right backend query (Loki/Signoz/ES JSON, Datadog facet syntax, or kubectl flags) and runs it. Saves output to a workspace file so it can be downloaded or grepped via shell_execute. The caller is responsible for the strategy — fetch_logs_v3 runs whatever query the question implies; it does not add implicit error filters or widen windows on its own. For investigations, ask for a broad chronological window so the trigger (config reload, deploy, antecedent context) surfaces before the symptom storm.`
	}
	return `Fetches logs for a resource and returns raw log content. Accepts EITHER a ready-to-execute canonical JSON query (` + "`{\"where\": {...}, \"time_range\": \"...\", \"limit\": <n>}`" + ` — the default in ROUTINE mode once you know the resource; see your mode's fast-path rules for the field names) OR a natural-language log question, which it translates into the right backend query (Loki/Signoz/ES JSON, Datadog facet syntax, or kubectl flags) before running it. Saves output to a workspace file so it can be downloaded or grepped via shell_execute. The caller is responsible for the strategy — fetch_logs_v3 runs whatever query/question implies; it does not add implicit error filters or widen windows on its own. For investigations, ask for a broad chronological window so the trigger (config reload, deploy, antecedent context) surfaces before the symptom storm.`
}

func (t *fetchLogsV3Tool) InputSchema() toolcore.ToolSchema {
	desc := "Provide a natural-language log question (e.g. 'errors in <service> last 1h', 'why is <service> slow', 'logs for pod <workload>-<6-10 hex>-<5 alnum> in namespace <ns>')."
	if canonicalFastPathEnabled() {
		desc = "A natural-language log question (e.g. 'errors in <service> last 1h', 'why is <service> slow', 'logs for pod <workload>-<6-10 hex>-<5 alnum> in namespace <ns>'), OR — for ROUTINE-mode fetches once namespace+app are resolved, see the fast-path rules in your instructions — a ready-to-execute canonical JSON query `{\"where\": {...}, \"time_range\": \"...\", \"limit\": <n>}`. A canonical JSON query is detected automatically and executed directly, skipping the internal translation call."
	}
	return toolcore.ToolSchema{
		Type: toolcore.ToolSchemaTypeObject,
		Properties: map[string]toolcore.ToolSchemaProperty{
			"command": {
				Type:        toolcore.ToolSchemaTypeString,
				Description: desc,
			},
		},
		Required: []string{"command"},
	}
}

// buildFetchLogsV3Request constructs the core.NBAgentRequest for Call.
// AgentId is set to nbCtx.ParentAgentId (the calling agent's own id) so the
// internal canonical-query LLM call attributes to that turn, not a new agent.
// OriginalQuery and AccountPrompt are forwarded because buildLogIntentMessages
// uses both in that translator call — dropping them silently degrades intent
// framing and account field guidance rather than erroring.
func buildFetchLogsV3Request(nbCtx toolcore.NbToolContext, input toolcore.NBToolCallRequest) core.NBAgentRequest {
	return core.NBAgentRequest{
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
		AccountPrompt:  nbCtx.AccountPrompt,
	}
}

// Call invokes FetchLogsAgentV2.Execute() directly using the request built by
// buildFetchLogsV3Request.
//
// Logs its own wall-clock duration (wall_clock_seconds) on both paths — the
// logs-agent-slowness investigation found this call's tool-exec time is one
// of the three dominant cost buckets (alongside LLM think time and
// resource_search), and it wasn't independently observable before without
// reconstructing it from surrounding planner log lines.
func (t *fetchLogsV3Tool) Call(nbCtx toolcore.NbToolContext, input toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	if nbCtx.Ctx == nil {
		return toolcore.NBToolResponse{Status: toolcore.NBToolResponseStatusError}, fmt.Errorf("fetch_logs_v3: nil request context")
	}

	start := time.Now()
	request := buildFetchLogsV3Request(nbCtx, input)

	fetchAgent := newFetchLogsAgentV2(t.accountId)
	var resp core.NBAgentResponse
	var err error
	if canonicalFastPathEnabled() {
		resp, err = fetchAgent.ExecuteV3(nbCtx.Ctx, request)
	} else {
		resp, err = fetchAgent.Execute(nbCtx.Ctx, request)
	}
	wallClockSeconds := time.Since(start).Seconds()
	if err != nil {
		nbCtx.Ctx.GetLogger().Error("fetch_logs_v3: execute failed", "error", err, "wall_clock_seconds", wallClockSeconds)
	} else {
		nbCtx.Ctx.GetLogger().Info("fetch_logs_v3: execute complete", "wall_clock_seconds", wallClockSeconds, "status", resp.Status)
	}
	return buildFetchLogsV3ToolResponse(resp, err)
}

// ExecuteV3 is logs_v3's own entry point for every fetch_logs_v3 call when
// the canonical fast path is enabled — replaces routing through the shared
// FetchLogsAgentV2.Execute/executeInner (agent_log_fetch_v2.go) so ALL of
// v3's canonical-query generation (not just the ROUTINE fast path) uses the
// v3-owned, fixed translator (generateCanonicalLogQueryV3 /
// buildCanonicalLogQueryPromptV3, see duplicateLabelHeuristicHint) instead of
// the shared one. Two ways jsonQuery gets produced:
//   - request.Query already IS canonical JSON (the ROUTINE fast path,
//     canonicalQueryAuthoringForRoutine, produced it) — use it directly,
//     skipping translation entirely.
//   - Otherwise — a natural-language command, from ROUTINE's own fallback or
//     from INVESTIGATION/ENUMERATION (which never author canonical JSON
//     directly) — translate it via generateCanonicalLogQueryV3.
//
// Mirrors FetchLogsAgentV2.Execute/executeInner's routing otherwise exactly —
// same wall-clock guard, same canonicalEnabled gate, same provider dispatch
// (empty/datadog fall back to the shared executeInner, since neither this nor
// the fast path cover those). Defined here (not in agent_log_fetch_v2.go) so
// this is entirely additive to v3 and never changes behavior for v1/v2
// callers.
func (a *FetchLogsAgentV2) ExecuteV3(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	entryStart := time.Now()
	return runFetchLogsWithTimeout(ctx, resolveFetchLogsWallClockTimeout(), func(tctx *security.RequestContext) (core.NBAgentResponse, error) {
		if !a.canonicalEnabled(tctx) {
			tctx.GetLogger().Info("fetch_logs_v3: canonical path disabled, falling back to executeInner", "elapsed", time.Since(entryStart).String())
			return a.executeInner(tctx, request)
		}
		provider := a.effectiveProvider(request)
		providerName := strings.ToLower(strings.TrimSpace(provider.Provider))
		if providerName == "" || providerName == "datadog" {
			// Neither shape ExecuteV3 covers — fall back to the shared
			// NL-driven path exactly like executeInner does.
			tctx.GetLogger().Info("fetch_logs_v3: provider not covered by ExecuteV3, falling back to executeInner", "provider", providerName, "elapsed", time.Since(entryStart).String())
			return a.executeInner(tctx, request)
		}

		jsonQuery, ok := preBuiltCanonicalQuery(request.Query)
		if ok {
			tctx.GetLogger().Info("fetch_logs_v3: ExecuteV3 using pre-built canonical query", "conversation_id", request.ConversationId, "json_query", jsonQuery)
		} else {
			translateStart := time.Now()
			fields, indices := fetchLabelsAndIndices(a.accountId, provider)
			var genErr error
			jsonQuery, genErr = generateCanonicalLogQueryV3(tctx, request, provider, fields, indices)
			tctx.GetLogger().Info("fetch_logs_v3: step generateCanonicalLogQueryV3 done", "duration", time.Since(translateStart).String(), "has_error", genErr != nil)
			if genErr != nil {
				return errorResponse(a.GetName(), fmt.Errorf("canonical query extraction: %w", genErr)), nil
			}
		}

		execStart := time.Now()
		resp, canonicalQuery, err := a.executeCanonicalQueryDirect(tctx, request, provider, jsonQuery)
		tctx.GetLogger().Info("fetch_logs_v3: executeCanonicalQueryDirect returned", "duration", time.Since(execStart).String(), "status", resp.Status, "has_error", err != nil)
		if err != nil {
			return resp, err
		}
		if shouldFallbackToKubectl(resp, canonicalQuery) {
			tctx.GetLogger().Info("fetch_logs_v3: falling back to kubectl after canonical-direct result", "elapsed", time.Since(entryStart).String())
			kStart := time.Now()
			kResp, kErr := a.kubectlFallback(tctx, request, provider, resp)
			tctx.GetLogger().Info("fetch_logs_v3: kubectlFallback returned", "duration", time.Since(kStart).String())
			return kResp, kErr
		}
		tctx.GetLogger().Info("fetch_logs_v3: ExecuteV3 complete", "total_duration", time.Since(entryStart).String())
		return resp, nil
	})
}

// executeCanonicalQueryDirect mirrors FetchLogsAgentV2.generateCanonicalLogQueryAndExecute
// (agent_log_fetch_v2.go) exactly, except jsonQuery is already built instead
// of being derived via generateCanonicalLogQuery — every downstream step
// (index defaulting, execution, error/format handling, workspace save,
// auto-diagnostics) is byte-identical to the shared path, so behavior after
// query generation is unchanged. Calls only shared, already-exported-within-
// package helpers (callTool, looksLikeFetchError, unwrapLokiInnerTimestamps,
// saveLogsToWorkspace, runAutoDiagnosticBundle, makeFetchResponse,
// executedLogQuery, mergeRefs, errorResponse, injectDefaultIndexIfMissing) —
// none of their definitions change.
func (a *FetchLogsAgentV2) executeCanonicalQueryDirect(ctx *security.RequestContext, request core.NBAgentRequest, provider services_server.ObservabilityProvider, jsonQuery string) (core.NBAgentResponse, string, error) {
	if provider.DefaultIndex != "" {
		jsonQuery = injectDefaultIndexIfMissing(jsonQuery, provider.DefaultIndex)
	}

	stepStart := time.Now()
	logs, toolRefs, err := callTool(ctx, a.accountId, request, tools.ToolLogsExecuteV2, jsonQuery)
	ctx.GetLogger().Info("fetch_logs_v3: step callTool(logs_execute_v2) done", "duration", time.Since(stepStart).String(), "has_error", err != nil, "response_bytes", len(logs))
	if err != nil {
		return errorResponse(a.GetName(), fmt.Errorf("logs_execute_v2: %w", err)), jsonQuery, nil
	}
	if matched, reason := looksLikeFetchError(provider.Provider, logs); matched {
		return errorResponse(a.GetName(), fmt.Errorf("%s fetch failed: %s", provider.Provider, reason)), jsonQuery, nil
	}
	if strings.EqualFold(provider.Provider, "loki") {
		stepStart = time.Now()
		logs = unwrapLokiInnerTimestamps(ctx, logs)
		ctx.GetLogger().Info("fetch_logs_v3: step unwrapLokiInnerTimestamps done", "duration", time.Since(stepStart).String())
	}

	stepStart = time.Now()
	fileRef, flattened, fileRefs := saveLogsToWorkspace(ctx, a.accountId, request.ConversationId, provider.Provider, logs)
	ctx.GetLogger().Info("fetch_logs_v3: step saveLogsToWorkspace done", "duration", time.Since(stepStart).String(), "file_ref", fileRef)

	stepStart = time.Now()
	bundleSignal, err := runAutoDiagnosticBundle(ctx, a.accountId, request, fileRef)
	ctx.GetLogger().Info("fetch_logs_v3: step runAutoDiagnosticBundle done", "duration", time.Since(stepStart).String(), "has_error", err != nil, "bundle_signal_empty", bundleSignal == "")
	if err != nil {
		return core.NBAgentResponse{}, jsonQuery, err
	}
	return makeFetchResponse(a.GetName(), executedLogQuery(logs, jsonQuery), logs, flattened, fileRef, bundleSignal, mergeRefs(toolRefs, fileRefs)), jsonQuery, nil
}

// buildFetchLogsV3ToolResponse shapes FetchLogsAgentV2.Execute's result into
// the NBToolResponse this tool returns. Split out from Call() so the mapping
// — in particular that the real terminal status is propagated instead of
// always reporting success — is directly unit-testable against hand-built
// resp/err values, without needing to run the real fetch pipeline (LLM calls,
// services-server, kubectl). See TestBuildFetchLogsV3ToolResponse.
//
// Status propagation fixes the A2/A3 bug class from the v1 bespoke wrapper
// (log_analysis_bugs notes: the old wrapper always returned
// NBToolResponseStatusSuccess even when the sub-run failed) — fixed by
// construction here since there is no separate wrapper to drift from this
// mapping.
func buildFetchLogsV3ToolResponse(resp core.NBAgentResponse, err error) (toolcore.NBToolResponse, error) {
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
