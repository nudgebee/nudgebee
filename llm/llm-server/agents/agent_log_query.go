package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strings"
	"time"
)

const LogQueryAgentName = "log_query"

// LogQueryAgent is the generic, provider-independent "generate one log query"
// chain behind the ai_generate_log_query action. Unlike the legacy per-provider
// chains it replaces (a LokiAgent/logql_query_generator/loki_execute trio and an
// ESLogAgent/elastic_search_query/elastic_search_execute trio, both removed once
// this action took over), it does not need a dedicated prompt/tool pair per
// backend: it reuses the exact canonical query-generation prompt
// FetchLogsAgentV2 uses for the full investigation flow (generateCanonicalLogQuery),
// then resolves the canonical where-clause to provider-native query TEXT via
// GetLogsQuery/logs_get_query — a preview-only resolution, not a real query
// execution. This endpoint's job is only to fill in the query bar; running the
// query for real (fetching actual log data) on every "generate" click, as an
// earlier version of this agent did via logs_execute_v2, wasted backend load
// and network bytes for data the caller always discarded. services-server owns
// canonical→native translation for every backend except Datadog (kept on its
// bespoke facet-syntax chain) and empty/k8s-only accounts (no query language to
// generate against).
type LogQueryAgent struct {
	accountId         string
	provider          services_server.ObservabilityProvider
	requestedProvider string
	requestedIndex    string
}

// NewLogQueryAgent resolves the account's log provider for this request.
// requestedProvider optionally pins resolution to a specific provider (the
// user's "Log Provider:" dropdown selection) instead of the account's
// default — e.g. an account configured with both Loki and Pinot integrations
// must generate against whichever one is selected, not silently fall back to
// whatever the account's default happens to be. Empty behaves exactly like
// the account-default resolution used elsewhere (FetchLogsAgentV2, etc).
//
// requestedIndex optionally pins generation to a specific Elasticsearch index
// (the logs tab's "Select an Index" dropdown). Elasticsearch field sets are
// per-index, so the selection has to reach label discovery, not just the final
// query: overwriting the resolved provider's DefaultIndex is what makes
// fetchLabelsAndIndices → QueryLogLabels scope its field lookup to the selected
// index, so the generator is shown the fields that actually exist there instead
// of the account default's. Ignored for backends with no index concept (Loki,
// Pinot, …), which have no DefaultIndex to speak of.
func NewLogQueryAgent(accountId, requestedProvider, requestedIndex string) *LogQueryAgent {
	provider, err := tools.GetLogProviderWithOverride(accountId, requestedProvider)
	// Empty provider (or unresolved) means no services-server log backend —
	// Execute rejects those accounts outright, same as FetchLogsAgentV2. An
	// override that failed to resolve is deliberately NOT normalized away here
	// — Execute uses requestedProvider to report a specific "not configured"
	// error instead of the generic "no log backend configured" message.
	if err == nil && strings.EqualFold(provider.Provider, "k8s") {
		provider = services_server.ObservabilityProvider{}
	}
	if err != nil {
		provider = services_server.ObservabilityProvider{}
	}
	if requestedIndex != "" && tools.IsESLogProvider(provider.Provider) {
		provider.DefaultIndex = requestedIndex
	}
	return &LogQueryAgent{accountId: accountId, provider: provider, requestedProvider: requestedProvider, requestedIndex: requestedIndex}
}

func (l *LogQueryAgent) GetName() string { return LogQueryAgentName }

func (l *LogQueryAgent) GetNameAliases() []string { return []string{"Log Query"} }

func (l *LogQueryAgent) GetDescription() string {
	return `Generates and validates a single log query from a natural-language question, for any configured log backend (Loki, Elasticsearch, Signoz, Loggly, Azure, Observe, Pinot, …).`
}

func (l *LogQueryAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	return core.NBAgentPrompt{
		Role: "an SRE expert that generates a single provider-native log query from natural language",
	}
}

func (l *LogQueryAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{}
}

func (l *LogQueryAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeCustom
}

// logQueryResult is the ai_generate_log_query response envelope. It is a typed
// struct, not a map[string]string — json.Marshal preserves field declaration
// order for a struct regardless of key name, whereas the old LokiAgent response
// relied on naming its field "logql_query" specifically so it would sort
// alphabetically before "logs" when marshaled from a map. Callers should read
// these fields by name rather than depend on encoding order.
type logQueryResult struct {
	Query    string `json:"query"`
	Provider string `json:"provider"`
	// Index is the Elasticsearch index the query was generated against — the
	// caller's own selection when it pinned one, otherwise whatever the
	// generator picked or the account default it fell back to. Lets the logs
	// tab show which index the query in the bar will actually run against.
	// Empty for backends with no index concept.
	Index string `json:"index,omitempty"`
}

// Execute generates a canonical log query from the user's question and resolves
// it to provider-native query text via GetLogsQuery/logs_get_query — a preview
// resolution, not a real execution against the backend (see effectiveIndex for
// how the ES index is settled). Datadog and empty/k8s-only accounts are
// rejected outright — neither has a canonical query path today (Datadog stays
// on its bespoke facet-syntax chain; k8s-only accounts have no query language to
// generate against).
func (l *LogQueryAgent) Execute(ctx *security.RequestContext, request core.NBAgentRequest) (core.NBAgentResponse, error) {
	providerName := strings.ToLower(strings.TrimSpace(l.provider.Provider))

	if providerName == "" {
		if l.requestedProvider != "" {
			return errorResponse(l.GetName(), fmt.Errorf("AI query generation is not supported: the requested log provider %q is not configured for this account", l.requestedProvider)), nil
		}
		return errorResponse(l.GetName(), fmt.Errorf("AI query generation is not supported for this account: no log backend is configured")), nil
	}
	if providerName == "datadog" {
		return errorResponse(l.GetName(), fmt.Errorf("AI query generation is not supported for the datadog log provider yet")), nil
	}

	fields, indices := fetchLabelsAndIndices(l.accountId, l.provider)
	jsonQuery, err := generateCanonicalLogQuery(ctx, request, l.provider, fields, indices)
	if err != nil {
		return errorResponse(l.GetName(), fmt.Errorf("canonical query generation: %w", err)), nil
	}

	queryBuilder, configs, err := buildCanonicalTimeConfigs(ctx, request, jsonQuery)
	if err != nil {
		return errorResponse(l.GetName(), fmt.Errorf("parsing canonical query: %w", err)), nil
	}

	index := l.effectiveIndex(queryBuilder.Index)
	if index != "" {
		configs["index"] = index
	}

	query, err := tools.GetLogsQueryPreview(ctx, l.accountId, l.provider, queryBuilder.Where, configs)
	if err != nil {
		return errorResponse(l.GetName(), fmt.Errorf("logs_get_query: %w", err)), nil
	}
	if query == "" {
		// services-server returned no query text (e.g. a provider whose GetQuery
		// implementation has nothing to preview) — fall back to the canonical
		// JSON so the caller still sees something rather than an empty string.
		query = jsonQuery
	}

	result := logQueryResult{
		Query:    query,
		Provider: l.provider.Provider,
		Index:    index,
	}
	data, err := common.MarshalJson(result)
	if err != nil {
		return errorResponse(l.GetName(), fmt.Errorf("marshal log query result: %w", err)), nil
	}

	return core.NBAgentResponse{
		AgentName: l.GetName(),
		Response:  []string{string(data)},
		Status:    core.ConversationStatusCompleted,
	}, nil
}

// effectiveIndex resolves which Elasticsearch index the query is generated
// against, given the index the generator chose (if any). The caller's explicit
// selection is a hard pin, not a hint: the dropdown is what the user sees, so
// the generator must not be able to silently redirect the query to a different
// index than the one on screen. Falling back to the provider's DefaultIndex
// when neither is set mirrors executeFetchLogsCanonical — without it, a
// generated query that (correctly, per the prompt's own "omit index to use the
// account default" instruction) carries no index would resolve unscoped here
// while the real execution path resolved it to the account default.
func (l *LogQueryAgent) effectiveIndex(generatedIndex string) string {
	if l.requestedIndex != "" && tools.IsESLogProvider(l.provider.Provider) {
		return l.requestedIndex
	}
	if generatedIndex != "" {
		return generatedIndex
	}
	return l.provider.DefaultIndex
}

// buildCanonicalTimeConfigs parses the LLM-generated canonical query JSON into
// a QueryBuilder and resolves its time window exactly the way logs_execute_v2
// (NBLogToolV2.Call) does, so the previewed query text reflects the same
// limit/time-window the real execution path would have used. Note: unlike the
// execution path's tool call, this never touches toolcore.NBQueryConfig-driven
// per-tool config resolution — LogQueryAgent has no registered tool, just a
// canonical query and services-server's query-builder endpoint.
func buildCanonicalTimeConfigs(ctx *security.RequestContext, request core.NBAgentRequest, jsonQuery string) (toolcore.QueryBuilder, map[string]any, error) {
	toolCtx := toolcore.NewNbToolContext(
		ctx, nil, request.AccountId,
		request.UserId, request.ConversationId, request.MessageId, request.AgentId,
		jsonQuery, nil, request.QueryContext, request.QueryConfig, "",
	)
	queryBuilder, err := toolcore.BuildLogQueryBuilder(toolCtx, jsonQuery)
	if err != nil {
		return queryBuilder, nil, err
	}
	if queryBuilder.Limit == 0 {
		queryBuilder.Limit = 1000
	}

	args := map[string]any{}
	if queryBuilder.TimeRange != "" {
		args["range"] = queryBuilder.TimeRange
	}
	if queryBuilder.StartTime != "" {
		args["start_time"] = queryBuilder.StartTime
	}
	if queryBuilder.EndTime != "" {
		args["end_time"] = queryBuilder.EndTime
	}

	start := time.Now().Add(-1 * time.Hour)
	end := time.Now()
	if t1, t2, err := tools.ExtractStartEndtimeFromLabels(toolCtx, args); err == nil {
		start = t1
		end = t2
	}
	start, end = tools.ExpandNarrowTimeWindow(ctx.GetLogger(), start, end)

	configs := map[string]any{
		"end_time":   end.UnixMilli(),
		"start_time": start.UnixMilli(),
		"limit":      queryBuilder.Limit,
	}
	if queryBuilder.Index != "" {
		configs["index"] = queryBuilder.Index
	}
	return queryBuilder, configs, nil
}
