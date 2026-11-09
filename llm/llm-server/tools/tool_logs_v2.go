package tools

import (
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools/core"
	"time"

	"github.com/pkg/errors"
)

// ToolLogsExecuteV2 is the canonical, provider-independent log executor used by
// FetchLogsAgent. It forwards the structured canonical where clause to
// services-server's FetchLogs, which resolves canonical→provider labels and
// builds the native query server-side — the same path the UI log query
// builder uses.
//
// It is registered so FetchLogsAgent can invoke it by name via callTool; it is
// NOT added to any agent's GetSupportedTools, so generic planners never see it as
// a separately-callable tool.
const ToolLogsExecuteV2 = "logs_execute_v2"

// NoLogsFoundPrefix is the leading phrase of the human-readable message
// logs_execute_v2 returns when a query succeeds but matches zero rows.
// Exported so FetchLogsAgent can detect "no results" (to trigger its
// kubectl fallback) without coupling to the full message text.
const NoLogsFoundPrefix = "No logs found"

func init() {
	core.RegisterNBToolFactory(ToolLogsExecuteV2, func(accountId string) (core.NBTool, error) {
		return NewNBLogToolV2(accountId)
	})
}

func NewNBLogToolV2(accountId string) (*NBLogToolV2, error) {
	logProvider, err := GetLogProvider(accountId)
	if err != nil {
		slog.Error("logs_v2: unable to get log provider", "error", err)
		return nil, err
	}
	return &NBLogToolV2{
		accountId:   accountId,
		logProvider: logProvider,
	}, nil
}

type NBLogToolV2 struct {
	accountId   string
	logProvider services_server.ObservabilityProvider
}

func (t *NBLogToolV2) Name() string { return ToolLogsExecuteV2 }

func (t *NBLogToolV2) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t *NBLogToolV2) Description() string {
	return "Executes a provider-independent canonical log query and returns the result."
}

// InputSchema — the agent emits a `{"where": {...}}` JSON `command`, plus
// optional time-window controls.
func (t *NBLogToolV2) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "JSON query to Execute",
			},
			"start_time": {
				Type:        core.ToolSchemaTypeString,
				Description: "Start Time for the query. Format: RFC3339 or Unix timestamp",
			},
			"end_time": {
				Type:        core.ToolSchemaTypeString,
				Description: "End Time for the query. Format: RFC3339 or Unix timestamp",
			},
			"range": {
				Type:        core.ToolSchemaTypeString,
				Description: "Time range for the query (e.g., '2d', '1w', '1h'). If provided, start_time is calculated relative to end_time.",
			},
			"index": {
				Type:        core.ToolSchemaTypeString,
				Description: "Elasticsearch index name or pattern to query (e.g., 'app-logs-*'). If not specified, the account's default index is used.",
			},
		},
		Required: []string{"command"},
	}
}

// Call parses the LLM's canonical `{"where": {...}}` JSON into the shared
// QueryBuilder and forwards the structured where clause to services-server with
// an empty Query (executeFetchLogsCanonical). One path serves every provider;
// services-server owns the canonical→provider resolution and native query build.
func (t *NBLogToolV2) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	// A targeted child's call reads the target's account throughout (views,
	// provider, routing), exactly as inside the metrics/traces sub-agents.
	nbRequestContext = nbRequestContext.ForTarget()
	// Account-driven provider — resolved once at tool construction and shared
	// across requests via the 30-min tool cache.
	resolvedProvider := t.logProvider
	if nbRequestContext.AccountId != t.accountId {
		// The tool was built for the conversation's account; a target may use
		// a different log backend.
		provider, err := GetLogProvider(nbRequestContext.AccountId)
		if err != nil {
			return core.NBToolResponse{Status: core.NBToolResponseStatusError, Data: fmt.Sprintf("no log provider for the target account: %v", err)}, nil
		}
		resolvedProvider = provider
	}
	// Local, never written back to the receiver: this tool instance is cached and
	// shared across requests, so a write would pin one request's override for all.
	logProvider := EffectiveLogProvider(resolvedProvider, nbRequestContext.QueryConfig.LogProviderOverride)

	nbRequestContext.Ctx.GetLogger().Info("logs_v2: executing canonical getLogs tool call", "query", input.Command, "provider", logProvider.Provider)

	queryBuilder, err := core.BuildLogQueryBuilder(nbRequestContext, input.Command)
	if err != nil {
		return core.NBToolResponse{}, err
	}
	if queryBuilder.Limit == 0 {
		queryBuilder.Limit = 1000
	}

	args := input.Arguments
	if args == nil {
		args = make(map[string]any)
	}

	// The query's own window, then the event's when this is an event-triggered
	// conversation, then the last hour.
	now := time.Now()
	start, end := now.Add(-1*time.Hour), now
	if window, ok := ResolveLogQueryWindow(queryBuilder, args, now); ok {
		start, end = window.Start, window.End
	} else if t1, t2, ok := resolveWindowFromLabelsAt(nbRequestContext.QueryConfig.Labels, now.UTC()); ok {
		start, end = t1.UTC(), t2.UTC()
	}
	requestedStart, requestedEnd := start, end
	start, end = ExpandNarrowTimeWindow(nbRequestContext.Ctx.GetLogger(), start, end)
	widened := !start.Equal(requestedStart) || !end.Equal(requestedEnd)

	index := queryBuilder.Index
	if index == "" {
		index = logProvider.DefaultIndex
	}
	read := func(start, end time.Time) (core.ObservabilityLogResponse, error) {
		configs := map[string]any{
			"end_time":   end.UnixMilli(),
			"start_time": start.UnixMilli(),
			"limit":      queryBuilder.Limit,
		}
		if index != "" {
			configs["index"] = index
		}
		return executeFetchLogsCanonical(nbRequestContext, logProvider, queryBuilder.Where, configs)
	}

	response, err := read(start, end)
	if err == nil && widened && response.Coverage != nil {
		if coverageSpans(response.Coverage, requestedStart, requestedEnd) {
			// The widened read holds the whole requested window; say which
			// window was asked for and which one was read.
			response.Coverage.ReadStart = response.Coverage.RequestedStart
			response.Coverage.ReadEnd = response.Coverage.RequestedEnd
			response.Coverage.RequestedStart = requestedStart.UTC().Format(time.RFC3339)
			response.Coverage.RequestedEnd = requestedEnd.UTC().Format(time.RFC3339)
		} else {
			// The read stopped at its limit inside the widening: the backend
			// returns the newest lines first, so on a busy workload every line
			// can come from after the requested window. Read that window alone.
			nbRequestContext.Ctx.GetLogger().Info("logs_v2: widened read stopped short of the requested window, reading it alone",
				"requested_start", requestedStart.Format(time.RFC3339), "requested_end", requestedEnd.Format(time.RFC3339))
			start, end = requestedStart, requestedEnd
			response, err = read(start, end)
		}
	}
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("logs_v2: unable to execute canonical query", "provider", logProvider.Provider, "error", err.Error())
		return core.NBToolResponse{
			Data:   "",
			Status: core.NBToolResponseStatusError,
		}, errors.Wrap(core.ErrUnableToFetchData, err.Error())
	}

	if len(response.Logs) == 0 {
		// Fold the human-readable message (the ValidateRequest diagnosis suggestion,
		// or the generic "broaden your query" guidance) into Suggestion and still
		// serialize the full response — response.Metadata.Query/Provider already
		// carry the real backend query and provider services-server executed (even
		// on a zero-row result), and FetchLogsAgent's executedLogQuery/
		// providerFromLogs read them from here. Returning a bare string instead (as
		// this used to do) silently discarded them, so the UI fell back to showing
		// the LLM's canonical where-clause JSON instead of the real query.
		response.Suggestion = noLogsFoundMessage(response.Suggestion, logProvider.Provider, queryBuilder.Where, start, end, queryBuilder.Limit)
	}

	data, err := common.MarshalJson(response)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("logs_v2: unable to serialize json", "error", err.Error())
		return core.NBToolResponse{
			Data:   "",
			Status: core.NBToolResponseStatusError,
		}, core.ErrUnableToFetchData
	}
	return core.NBToolResponse{
		Data:       string(data),
		Type:       core.NBToolResponseTypeJson,
		Status:     core.NBToolResponseStatusSuccess,
		References: []core.NBToolResponseReference{logsUIRef(nbRequestContext, queryBuilder)},
	}, nil
}

// coverageSpans reports whether the lines of a read that stopped short reach
// from start to end, so every line of that window was read. Both ends are
// checked because the backend's read order is not known here: a newest-first
// read covers the window when its oldest line is at or before start, an
// oldest-first one when its newest line is at or after end. A newest-first
// read of a window that ends now therefore never passes (its newest line is
// before the widened end) and is read again; that costs a backend call, not
// correctness. False when the span of the lines is unknown.
//
// Oldest and Newest are cut to the second, so the oldest line can be up to a
// second later than Oldest says; it is only taken to reach start when that
// whole second is before start.
func coverageSpans(coverage *core.ObservabilityLogCoverage, start, end time.Time) bool {
	oldest, err := time.Parse(time.RFC3339, coverage.Oldest)
	if err != nil {
		return false
	}
	newest, err := time.Parse(time.RFC3339, coverage.Newest)
	if err != nil {
		return false
	}
	return !oldest.Add(time.Second).After(start) && !newest.Before(end)
}

// noLogsFoundMessage builds the tool response body for a canonical query that ran
// successfully but matched zero rows. suggestion is services-server's ValidateRequest
// diagnosis (unknown label name / unknown label value): it already names the specific
// offending label/value and the closest valid match(es), so it's strictly more
// actionable than the generic message and is preferred whenever present. Empty
// suggestion means the diagnosis found nothing actionable — a genuinely empty result —
// so fall back to the generic "broaden the query" guidance.
func noLogsFoundMessage(suggestion, provider string, where core.QueryWhereClause, start, end time.Time, limit int) string {
	if suggestion != "" {
		return suggestion
	}
	whereJSON, _ := common.MarshalJson(where)
	return fmt.Sprintf(
		NoLogsFoundPrefix+" for %s (canonical where: %s; time range: %s to %s, limit: %d). "+
			"The query executed successfully but returned no results. "+
			"Suggestions: check if label names/values are correct, try broader filters, or expand the time range.",
		provider, string(whereJSON), start.Format(time.RFC3339), end.Format(time.RFC3339), limit,
	)
}
