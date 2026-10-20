package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	llmruntime "nudgebee/llm/llms/runtime"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/utils"
	"strings"
)

const ESMetricsQueryAgentName = "es_metrics_query_writer"

// ESMetricsQueryAgent generates ONE Elasticsearch metrics DSL query from a
// natural-language question and returns it as text. It never executes anything.
//
// It exists because the metrics tab's AI box needs a *query to put in the editor*,
// which neither existing path can supply on an ES account:
//
//   - ElasticSearchMetricsAgent is an executing ReAct agent. Its own output contract
//     forbids returning the DSL ("NEVER return the generated DSL as the final answer"),
//     and reaching an answer costs a full tool loop — one traced run spent 50s over six
//     es_metrics_query calls. Wrong shape, wrong cost.
//   - PrometheusAgent, which /v1/completions/prometheus-query used unconditionally
//     before this agent existed, emits PromQL. Handing a PromQL string to an
//     Elasticsearch DSL editor is not a degraded answer, it is a broken one.
//
// The design mirrors LogQueryAgent (the same job on the logs side): a Custom-planner
// agent with no tools and a single LLM call, returning a {query, provider, index}
// envelope. The account-specific field knowledge is NOT duplicated here — it comes
// from esSchemaGuidance, the same schema-detected guidance the executing agent uses,
// so the generator can never name a field the executor would not.
type ESMetricsQueryAgent struct {
	accountId string
	// requestedIndex is the metrics tab's index selection. Like LogQueryAgent's, it
	// is a hard pin rather than a hint: the dropdown is what the user sees, so the
	// generator must not silently resolve the query against a different index.
	requestedIndex string
}

// NewESMetricsQueryAgent builds the generator for one request. requestedIndex may be
// empty, in which case the account's configured metrics index config is used.
func NewESMetricsQueryAgent(accountId, requestedIndex string) *ESMetricsQueryAgent {
	return &ESMetricsQueryAgent{accountId: accountId, requestedIndex: strings.TrimSpace(requestedIndex)}
}

func (e *ESMetricsQueryAgent) GetName() string { return ESMetricsQueryAgentName }

func (e *ESMetricsQueryAgent) GetNameAliases() []string {
	return []string{"Elasticsearch Metrics Query Writer"}
}

func (e *ESMetricsQueryAgent) GetDescription() string {
	return `Generates a single Elasticsearch metrics DSL query from a natural-language question. Generation only — it does not execute the query.`
}

// GetSystemPrompt is unused: a Custom planner never consults it. The real prompt is
// built per request in Execute, because it depends on the account's detected schema.
func (e *ESMetricsQueryAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	return core.NBAgentPrompt{
		Role: "an SRE expert that writes one Elasticsearch metrics DSL query from natural language",
	}
}

func (e *ESMetricsQueryAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{}
}

func (e *ESMetricsQueryAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeCustom
}

// esMetricsQueryResult is the response envelope. It deliberately matches
// logQueryResult field-for-field so the frontend's generated-query handling is one
// shape across logs and metrics rather than two.
type esMetricsQueryResult struct {
	Query    string `json:"query"`
	Provider string `json:"provider"`
	// Index is the index pattern the query is meant to run against — the caller's
	// own selection when it pinned one, else what the generator chose, else the
	// account default. The metrics tab echoes it back into its index picker so Run
	// Query executes against the same index the query was written for.
	Index string `json:"index,omitempty"`
}

func (e *ESMetricsQueryAgent) Execute(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	indexCfg := e.resolveIndexConfig(ctx)
	schemas, availableIndices, timestampField, targets := detectMetricSchemas(indexCfg, newESFieldProbe(ctx, e.accountId))

	fallbackIndex := "metrics-*"
	if len(availableIndices) > 0 {
		fallbackIndex = availableIndices[0]
	}

	prompt := buildESMetricsQueryPrompt(schemas, availableIndices, timestampField, targets, fallbackIndex)
	messages := buildLogIntentMessages(prompt, request)

	summaryCtx := security.NewRequestContext(
		context.WithValue(ctx.GetContext(), llmruntime.ContextKeyModelTier, llmruntime.ModelTierSummary),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)
	res, err := llmruntime.GenerateAndTrackLLMContent(summaryCtx, request.UserId, request.AccountId, request.ConversationId, request.MessageId, request.AgentId, false, messages, true)
	if err != nil {
		return errorResponse(e.GetName(), fmt.Errorf("es metrics query generation: %w", err)), nil
	}
	if res == nil || len(res.Choices) == 0 {
		return errorResponse(e.GetName(), fmt.Errorf("es metrics query generation: empty LLM response")), nil
	}

	dsl, generatedIndex, err := parseESMetricsQueryResponse(res.Choices[0].Content)
	if err != nil {
		return errorResponse(e.GetName(), err), nil
	}

	result := esMetricsQueryResult{
		Query:    dsl,
		Provider: "ES",
		Index:    e.effectiveIndex(generatedIndex, fallbackIndex),
	}
	data, err := common.MarshalJson(result)
	if err != nil {
		return errorResponse(e.GetName(), fmt.Errorf("marshal es metrics query result: %w", err)), nil
	}

	return core.NBAgentResponse{
		AgentName: e.GetName(),
		Response:  []string{string(data)},
		Status:    core.ConversationStatusCompleted,
	}, nil
}

// resolveIndexConfig mirrors ElasticSearchMetricsAgent.GetSystemPrompt so the
// generator probes exactly the indices the executor would. A pinned index wins
// outright: schema detection then describes the index the user is looking at.
func (e *ESMetricsQueryAgent) resolveIndexConfig(ctx *security.RequestContext) utils.ESIndexConfig {
	var indexCfg utils.ESIndexConfig
	if e.requestedIndex != "" {
		indexCfg.DefaultIndex = e.requestedIndex
		return indexCfg
	}
	if ctx != nil {
		if provider, err := services_server.GetObservabilityProvider(*ctx, e.accountId, "metrics", ""); err == nil && provider.DefaultIndex != "" {
			indexCfg.DefaultIndex = provider.DefaultIndex
		}
	}
	if indexCfg.DefaultIndex == "" || indexCfg.DefaultIndex == "*" {
		indexCfg = utils.GetESAccountIndexConfig(e.accountId, "metrics")
	}
	return indexCfg
}

func (e *ESMetricsQueryAgent) effectiveIndex(generatedIndex, fallbackIndex string) string {
	if e.requestedIndex != "" {
		return e.requestedIndex
	}
	if strings.TrimSpace(generatedIndex) != "" {
		return generatedIndex
	}
	return fallbackIndex
}

// buildESMetricsQueryPrompt assembles the generator's system prompt. Split out from
// Execute so the output contract and the workload-name rule can be asserted without
// an LLM round trip — see TestBuildESMetricsQueryPrompt.
func buildESMetricsQueryPrompt(
	schemas map[esMetricSchema]bool,
	availableIndices []string,
	timestampField string,
	targets map[esMetricSchema]schemaTarget,
	fallbackIndex string,
) string {
	var b strings.Builder

	b.WriteString("You write ONE Elasticsearch metrics query for a Nudgebee user. You do NOT execute it.\n\n")
	b.WriteString("**OUTPUT CONTRACT (ALWAYS):** Return exactly one JSON object as your entire response, with no prose, no explanation and no markdown fence:\n")
	b.WriteString("  {\"index\": \"<index pattern>\", \"query\": {\"query\": {\"bool\": {\"filter\": [ ... ]}}}}\n")
	b.WriteString("`query` MUST be the Elasticsearch _search body as a JSON object, not a string.\n\n")

	if len(availableIndices) > 0 {
		fmt.Fprintf(&b, "**Target Indices:** Use one of these metric indices: '%s'. NEVER use a log index (e.g. 'fluentk8-*', 'logs-*').\n\n", strings.Join(availableIndices, ", "))
	} else {
		fmt.Fprintf(&b, "**Target Index:** Use '%s'. NEVER use a log index (e.g. 'fluentk8-*', 'logs-*').\n\n", fallbackIndex)
	}

	// The account's real schema, field catalogue and index/timestamp mapping — the
	// same text the executing agent is given, so the two cannot disagree about how
	// this account spells a field.
	for _, line := range esSchemaGuidance(schemas, targets, timestampField, fallbackIndex) {
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n")

	b.WriteString("**Time Range:** " + buildTimestampRule(schemas, timestampField) + " Default to `now-1h` if the user does not specify one.\n\n")

	// The single most common way a generated metrics query returns nothing. A
	// Kubernetes pod name carries a generated suffix, so an exact `term` on the name
	// the user typed matches zero documents and reads to the user as "no data" — a
	// traced run burned three of its six queries rediscovering the full pod name.
	// The generator gets one shot with no retry loop, so this rule has to land here.
	b.WriteString("**Workload names are NOT pod names:** a Deployment/StatefulSet named `app-dev` runs pods named `app-dev-<replicaset-hash>-<suffix>`. When the user names a workload, NEVER filter it with `term` — use `{\"prefix\":{\"<pod name field>\":\"app-dev-\"}}` (or `wildcard` with `*app-dev*`). Use `term` on a pod field ONLY when the user gave a full pod name including its suffix. The same applies to container and node names the user abbreviates.\n\n")

	b.WriteString("**Rules:**\n")
	b.WriteString("  - Select the metric the question is about, and ONLY that metric: filter it by name (`name.keyword`) or by `exists` on its value path, per the schema above. A query with no metric filter returns every field in the index and is never the right answer.\n")
	b.WriteString("  - Copy names the user gave (namespace, workload, container, node) literally. Do not correct spelling, expand abbreviations, or substitute a similar name you know.\n")
	b.WriteString("  - Use `bool`/`filter` queries for time series. For an average, sum, count or top-N over the window, use an aggregation with `size: 0` (metric aggs inside `terms` or `date_histogram`); the backend turns aggregation buckets into series too.\n")
	b.WriteString("  - If the question cannot be answered from the fields above, still return your best query rather than prose. The user can edit it.\n")

	return b.String()
}

// parseESMetricsQueryResponse extracts the DSL body and index from the model's reply.
//
// It is strict about one thing only — the body must be valid JSON — because the
// query goes straight into the user's editor and an unparseable one wastes the whole
// interaction. Everything else is repaired rather than rejected: common's extractor
// recovers the object from a fenced or prose-wrapped reply, and a bare filter clause
// is wrapped into a full _search body the same way the execution path
// (esWrapBareQueryClause, api-server) would wrap it, so what the editor shows is what
// will actually run.
func parseESMetricsQueryResponse(raw string) (dsl string, index string, err error) {
	var envelope struct {
		Index string          `json:"index"`
		Query json.RawMessage `json:"query"`
	}
	if jsonErr := common.ExtractAndUnmarshalJSON([]byte(strings.TrimSpace(raw)), &envelope); jsonErr != nil {
		return "", "", fmt.Errorf("es metrics query generation: model did not return JSON (%v)", jsonErr)
	}
	if len(envelope.Query) == 0 {
		return "", "", fmt.Errorf("es metrics query generation: response carried no `query` object")
	}

	var body map[string]any
	if jsonErr := json.Unmarshal(envelope.Query, &body); jsonErr != nil {
		return "", "", fmt.Errorf("es metrics query generation: `query` was not a JSON object (%v)", jsonErr)
	}
	if body == nil {
		return "", "", fmt.Errorf("es metrics query generation: `query` was null")
	}
	if _, hasQuery := body["query"]; !hasQuery {
		body = map[string]any{"query": body}
	}

	pretty, marshalErr := json.MarshalIndent(body, "", "  ")
	if marshalErr != nil {
		return "", "", fmt.Errorf("es metrics query generation: could not render query (%v)", marshalErr)
	}
	return string(pretty), envelope.Index, nil
}

// stripQueryCodeFence removes a surrounding ```…``` fence when the model adds one despite
// the output contract. Returns the input unchanged when there is no fence.
//
// This is for query text that is NOT JSON — a bare Datadog query, say — where
// common.ExtractAndUnmarshalJSON has nothing to unmarshal and so cannot help. JSON
// replies go through that helper instead.
func stripQueryCodeFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if idx := strings.Index(s, "\n"); idx != -1 {
		s = s[idx+1:]
	}
	if idx := strings.LastIndex(s, "```"); idx != -1 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}
