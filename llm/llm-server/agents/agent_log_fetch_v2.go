package agents

import (
	"encoding/json"
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	"sort"
	"strings"
)

// FetchLogsAgentV2 is the canonical, provider-independent variant of the
// fetch_logs agent, gated by the deploy-time env var
// LLM_SERVER_LOG_AGENT_V2_ENABLED (config.Config.LogAgentV2Enabled), checked in
// Execute via canonicalEnabled. The shared factory in agent_log_fetch.go init()
// always constructs v2; when the gate is off v2 delegates to the embedded v1
// FetchLogsAgent so behaviour is identical to v1.
//
// It embeds *FetchLogsAgent and reuses every v1 helper. Routing (see Execute):
//   - any services-server-backed log provider (loki/es/signoz/loggly/azure/
//     observe/pinot/…) → a single canonical `{"where": {...}}` (canonical entity
//     names like `namespace`/`pod`/`service`) run through logs_execute_v2 →
//     services-server FetchLogs, which resolves canonical→provider labels and
//     builds the native query server-side. The provider type no longer matters
//     to the agent: query building + execution are entirely services-server's
//     job now, so there is no per-provider allowlist.
//   - datadog → the v1 facet-syntax path (its where→facet conversion stays on
//     the proven facet syntax; it does not use the canonical path).
//   - empty / k8s-only (no services-server backend) → kubectl directly.
//
// Resilience: when the services-server path errors OR returns no logs, v2 falls
// back to kubectl (kubectlFallback) so a misconfigured label mapping or a
// backend gap still yields logs when the workload is reachable in-cluster.
type FetchLogsAgentV2 struct {
	*FetchLogsAgent
}

func newFetchLogsAgentV2(accountId string) *FetchLogsAgentV2 {
	return &FetchLogsAgentV2{FetchLogsAgent: newFetchLogsAgent(accountId)}
}

// canonicalEnabled reports whether the canonical v2 path is enabled for this
// deploy, via the LLM_SERVER_LOG_AGENT_V2_ENABLED env var. It is a global
// per-environment toggle (default false); there is no per-account granularity.
func (a *FetchLogsAgentV2) canonicalEnabled(ctx *security.RequestContext) bool {
	return config.Config.LogAgentV2Enabled
}

// Execute routes the log fetch. When the v2 gate (LLM_SERVER_LOG_AGENT_V2_ENABLED)
// is off it delegates to the embedded v1 agent (identical to pre-v2 behaviour).
// When on, it sends a canonical where to services-server for any backed provider,
// keeps datadog on the proven facet path, and uses kubectl for empty/k8s. When
// the services-server path fails or returns no logs, it falls back to kubectl
// (kubectlFallback).
func (a *FetchLogsAgentV2) Execute(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	// Flag off → behave exactly as v1.
	if !a.canonicalEnabled(ctx) {
		return a.FetchLogsAgent.Execute(ctx, request)
	}

	provider := strings.ToLower(strings.TrimSpace(a.provider.Provider))

	// No services-server log backend (empty / k8s-only) → kubectl directly; there
	// is nothing to fall back from.
	if provider == "" {
		return a.generateKubeCtlLogQueryAndExecute(ctx, request)
	}

	// Primary: services-server. Canonical where for every backend except datadog,
	// which stays on its proven facet-syntax path (the v1 datadog executor).
	var resp core.NBAgentResponse
	var err error
	if provider == "datadog" {
		resp, err = a.generateDatadogLogQueryAndExecute(ctx, request)
	} else {
		resp, err = a.generateCanonicalLogQueryAndExecute(ctx, request)
	}
	if err != nil {
		return resp, err
	}

	// Fall back to kubectl when services-server errored or returned no logs.
	if resp.Status == core.ConversationStatusFailed || fetchResponseIsEmpty(resp) {
		return a.kubectlFallback(ctx, request, resp)
	}
	return resp, nil
}

// kubectlFallback runs the kubectl log path after a services-server fetch errored
// or came back empty. If kubectl is itself unavailable (e.g. the account has no
// in-cluster access) and the services-server path had at least answered honestly
// (succeeded but empty), the original response is preferred so a real "no logs"
// answer isn't masked by a kubectl access error.
func (a *FetchLogsAgentV2) kubectlFallback(ctx *security.RequestContext, request core.NBAgentRequest, primary core.NBAgentResponse) (core.NBAgentResponse, error) {
	ctx.GetLogger().Info("fetch_logs v2: services-server returned error/empty — falling back to kubectl",
		"provider", a.provider.Provider, "primary_status", primary.Status)
	kresp, err := a.generateKubeCtlLogQueryAndExecute(ctx, request)
	if err != nil {
		return kresp, err
	}
	if kresp.Status == core.ConversationStatusFailed && primary.Status != core.ConversationStatusFailed {
		return primary, nil
	}
	return kresp, nil
}

// fetchResponseIsEmpty reports whether a makeFetchResponse envelope carries no
// log content — the signal (besides a Failed status) for the kubectl fallback.
// Treated as empty: a missing body, a blank `logs` field, the
// tools.NoLogsFoundPrefix sentinel (emitted by logs_execute / logs_execute_v2 on
// a zero-row result), or a `{"logs":[]}` envelope with zero entries. A kubectl
// `{"stdout":...}` body has no `logs` key and is never flagged.
func fetchResponseIsEmpty(resp core.NBAgentResponse) bool {
	if len(resp.Response) == 0 {
		return true
	}
	var env struct {
		Logs string `json:"logs"`
	}
	if err := json.Unmarshal([]byte(resp.Response[0]), &env); err != nil {
		return false
	}
	logs := strings.TrimSpace(env.Logs)
	if logs == "" || strings.HasPrefix(logs, tools.NoLogsFoundPrefix) {
		return true
	}
	var doc struct {
		Logs []json.RawMessage `json:"logs"`
	}
	if strings.Contains(logs, `"logs"`) && json.Unmarshal([]byte(logs), &doc) == nil && len(doc.Logs) == 0 {
		return true
	}
	return false
}

func (a *FetchLogsAgentV2) generateCanonicalLogQueryAndExecute(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	a.ensureLabelsAndIndices()
	jsonQuery, err := generateCanonicalLogQuery(ctx, request, a.provider, a.fields, a.indices)
	if err != nil {
		return errorResponse(a.GetName(), fmt.Errorf("canonical query extraction: %w", err)), nil
	}
	logs, toolRefs, err := callTool(ctx, a.accountId, request, tools.ToolLogsExecuteV2, jsonQuery)
	if err != nil {
		return errorResponse(a.GetName(), fmt.Errorf("logs_execute_v2: %w", err)), nil
	}
	if matched, reason := looksLikeFetchError(a.provider.Provider, logs); matched {
		return errorResponse(a.GetName(), fmt.Errorf("%s fetch failed: %s", a.provider.Provider, reason)), nil
	}
	if strings.EqualFold(a.provider.Provider, "loki") {
		logs = unwrapLokiInnerTimestamps(ctx, logs)
	}
	fileRef, fileRefs := saveLogsToWorkspace(ctx, a.accountId, request.ConversationId, a.provider.Provider, logs)
	return makeFetchResponse(a.GetName(), executedLogQuery(logs, jsonQuery), logs, fileRef, mergeRefs(toolRefs, fileRefs)), nil
}

// executedLogQuery pulls the provider query the backend actually ran out of the
// logs_execute_v2 tool result (ObservabilityLogResponse.metadata.query) so the
// fetch envelope reports the real executed query (e.g. LogQL / ES DSL, or the
// canonical where JSON for native-where providers) rather than the canonical
// JSON the LLM produced. Falls back to the canonical JSON when the result
// carries no metadata (e.g. the zero-rows "no logs found" message).
func executedLogQuery(logs, fallback string) string {
	var doc struct {
		Metadata struct {
			Query string `json:"query"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(logs), &doc); err == nil && doc.Metadata.Query != "" {
		return doc.Metadata.Query
	}
	return fallback
}

// providerFromLogs extracts the provider the backend reported in the
// logs_execute_v2 tool result (ObservabilityLogResponse.metadata.provider) so
// the fetch envelope can show it alongside the query in the UI. Empty when
// absent (e.g. the kubectl/datadog paths or the zero-rows "no logs" message).
func providerFromLogs(logs string) string {
	var doc struct {
		Metadata struct {
			Provider string `json:"provider"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(logs), &doc); err == nil {
		return doc.Metadata.Provider
	}
	return ""
}

// generateCanonicalLogQuery produces the same JSON-where envelope logs_execute_v2
// consumes, but steers the LLM toward provider-independent canonical entity
// names (the keys of labelMappings) that services-server resolves. When
// labelMappings is empty (backend not yet enriched for this provider/account),
// it falls back to advertising the provider-native fields — identical to v1 —
// so the canonical path keeps working before enrichment lands.
func generateCanonicalLogQuery(ctx *security.RequestContext, request core.NBAgentRequest, provider services_server.ObservabilityProvider, fields []string, indices map[string]string) (string, error) {
	prompt := buildCanonicalLogQueryPrompt(provider, fields, indices)
	messages := buildLogIntentMessages(prompt, request)

	res, err := core.GenerateAndTrackLLMContent(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, false, messages, true)
	if err != nil {
		return "", err
	}
	if res == nil || len(res.Choices) == 0 {
		return "", fmt.Errorf("empty LLM response")
	}
	return strings.TrimSpace(res.Choices[0].Content), nil
}

// buildCanonicalLogQueryPrompt assembles the byte-stable system prompt for the
// canonical log-query generator. It is extracted from generateCanonicalLogQuery
// so the prompt's anti-regression rules — literal-value copying (no
// substitution/dropping of the user's names), explicit-window honouring, and the
// canonical-field / no-`service_name` constraints — can be asserted in a unit
// test without an LLM round-trip. See TestBuildCanonicalLogQueryPrompt.
func buildCanonicalLogQueryPrompt(provider services_server.ObservabilityProvider, fields []string, indices map[string]string) string {
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
		// Show the FULL `canonical_name → backend_field` mapping, not just the
		// keys. The backend_field carries the semantics the bare key lacks (e.g.
		// `app → deployment.keyword` tells the model `app` is the workload field),
		// so the model can map the user's wording onto THIS account's exact
		// canonical vocabulary instead of guessing the generic service/pod/message
		// names — which are only correct when they literally appear as keys.
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
		}
		b.WriteString("**HARD RULE — field names:** Emit ONLY a `canonical_name` from the list above (or, if none fits, a backend label from the line above). NEVER invent a field name. In particular do NOT emit `service_name`, `service.name`, `service`, `pod`, `message`, `namespace`, or `container` unless that exact word is listed above as a canonical_name — if the word is not listed, use the listed canonical_name that maps to the same concept (e.g. for a workload/service name use the canonical_name resolving to the app/deployment field, which is often `app`).\n")
	} else {
		// No canonical mapping for this account/provider — advertise the
		// provider-native labels from the backend's label list (get_labels).
		nativeFields := fields
		if len(nativeFields) == 0 {
			nativeFields = []string{"_body", "namespace", "pod"}
		}
		b.WriteString("AVAILABLE FIELDS for query building\n")
		fmt.Fprintf(&b, "  - **Fields**: %s\n", strings.Join(nativeFields, ", "))
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
	examples := canonicalQueryExamples()
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

// defaultLogQueryOperators is the comparison-operator set advertised to the
// LLM when get_default_provider omits capabilities.supported_operators (older
// backends, fetch failure). Combinators are added separately by
// resolveQueryOperators.
var defaultLogQueryOperators = []string{"_eq", "_neq", "_gt", "_gte", "_lt", "_lte", "_in", "_not_in", "_like", "_ilike", "_nlike", "_is_null"}

// logQueryCombinators are the structural JSON combinators the where-schema
// relies on. They are not provider comparison operators, so the backend's
// per-provider supported_operators list never includes them — resolveQueryOperators
// always unions them in regardless of provider.
var logQueryCombinators = []string{"_or", "_and"}

// resolveQueryOperators picks the operator set named in the query-generator
// prompt. When the backend supplies provider-specific operators
// (capabilities.supported_operators from get_default_provider), use them so the
// model never emits an operator the backend can't execute (Signoz lacks
// `_ilike`, Pinot lacks `_in`, etc.); otherwise fall back to the static default.
// `_or`/`_and` are always appended because the where-schema needs them for any
// provider. The result is de-duplicated, preserving backend order then
// combinators.
func resolveQueryOperators(providerOperators []string) []string {
	base := providerOperators
	if len(base) == 0 {
		base = defaultLogQueryOperators
	}
	ops := make([]string, 0, len(base)+len(logQueryCombinators))
	seen := make(map[string]struct{}, len(base)+len(logQueryCombinators))
	for _, op := range append(append([]string{}, base...), logQueryCombinators...) {
		op = strings.TrimSpace(op)
		if op == "" {
			continue
		}
		if _, dup := seen[op]; dup {
			continue
		}
		seen[op] = struct{}{}
		ops = append(ops, op)
	}
	return ops
}

// canonicalQueryExamples are STRUCTURE-only few-shots. Field names are
// placeholders (<WORKLOAD_FIELD>, <LOG_TEXT_FIELD>, <NAMESPACE_FIELD>) the model
// MUST replace with a canonical_name from the account's advertised
// `canonical_name → backend_field` list — they intentionally do NOT hardcode
// service/pod/message, because that generic vocabulary only resolves when it
// literally appears as a key in the account's label_mappings (often it does not,
// e.g. a backend whose canonical keys are `app`/`content`). The examples teach
// query shape (operators, _or, time_range/limit, when to add an error filter);
// the field list above teaches which name to substitute.
func canonicalQueryExamples() []core.NBAgentPromptExample {
	return []core.NBAgentPromptExample{
		{
			Question:    "show me recent logs for the checkout workload",
			Answer:      `{"where": {"<WORKLOAD_FIELD>": {"_eq": "checkout"}}, "time_range": "1h", "limit": 1000}`,
			Explanation: "Replace <WORKLOAD_FIELD> with the canonical_name that resolves to the workload/app/service field. Routine view → 1h / 1000, NO error filter.",
		},
		{
			Question:    "errors in the checkout workload in the last hour",
			Answer:      `{"where": {"<WORKLOAD_FIELD>": {"_eq": "checkout"}, "<LOG_TEXT_FIELD>": {"_ilike": "%error%"}}, "time_range": "1h", "limit": 5000}`,
			Explanation: "Replace <LOG_TEXT_FIELD> with the canonical_name for the log body. The question says 'last hour' → set time_range to \"1h\" EXACTLY; never widen a window the user gave (use the 24h default ONLY when the question gives no window). 'checkout' is illustrative — substitute the real workload name from the question.",
		},
		{
			Question:    "warn or error logs for checkout",
			Answer:      `{"where": {"<WORKLOAD_FIELD>": {"_eq": "checkout"}, "_or": [{"<LOG_TEXT_FIELD>": {"_ilike": "%warn%"}}, {"<LOG_TEXT_FIELD>": {"_ilike": "%error%"}}]}, "time_range": "24h", "limit": 5000}`,
			Explanation: "Multiple values for the same concept → _or over the log-body canonical_name.",
		},
		{
			Question:    "logs for pod checkout-7f8b9c-x2k in namespace prod",
			Answer:      `{"where": {"<POD_FIELD>": {"_eq": "checkout-7f8b9c-x2k"}, "<NAMESPACE_FIELD>": {"_eq": "prod"}}, "time_range": "1h", "limit": 1000}`,
			Explanation: "Replace <POD_FIELD>/<NAMESPACE_FIELD> with the canonical_names that resolve to the pod and namespace fields. An exact pod name → _eq.",
		},
		{
			Question:    "all logs for checkout in namespace prod last 6h limit 2000",
			Answer:      `{"where": {"<WORKLOAD_FIELD>": {"_eq": "checkout"}, "<NAMESPACE_FIELD>": {"_eq": "prod"}}, "time_range": "6h", "limit": 2000}`,
			Explanation: "\"all logs\" → NO log-body filter; explicit window and limit honoured verbatim.",
		},
	}
}
