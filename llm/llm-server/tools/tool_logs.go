package tools

import (
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools/core"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
)

const ToolLogsExecute = "logs_execute"

// logsUIRef builds the "#monitoring/logs" source reference for a logs_execute
// response. It carries the structured where-clause as the `filter` query
// param (URL-encoded JSON) — the exact shape the Logs tab's initializeQuery
// consumes — instead of a raw provider DSL (LogQL / ES JSON / Signoz filters)
// the UI cannot parse. When there is no where-clause, the bare tab link is
// emitted so the source still navigates correctly.
func logsUIRef(nbRequestContext core.NbToolContext, qb core.QueryBuilder) core.NBToolResponseReference {
	var params map[string]string
	if w := qb.Where; w.Binary != nil || len(w.And) > 0 || len(w.Or) > 0 || w.Not != nil {
		if b, err := common.MarshalJson(w); err == nil {
			params = map[string]string{"filter": string(b)}
		}
	}
	return core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"monitoring", "logs"}, "Query Logs", params, "")
}

func init() {
	core.RegisterNBToolFactory(ToolLogsExecute, func(accountId string) (core.NBTool, error) {
		return NewNBLogTool(accountId)
	})
}

func NewNBLogTool(accountId string) (*NBLogTool, error) {
	logProvider, err := GetLogProvider(accountId)
	if err != nil {
		slog.Error("logs: unable to get log provider", "error", err)
		return nil, err
	}
	return &NBLogTool{
		accountId:   accountId,
		logProvider: logProvider,
	}, nil
}

type NBLogTool struct {
	accountId   string
	logProvider services_server.ObservabilityProvider
	labels      []string
}

func (t *NBLogTool) Name() string {
	return ToolLogsExecute
}

func (t *NBLogTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (t *NBLogTool) Description() string {
	return "Executes a query and returns the result."
}

func (t *NBLogTool) QueryLabels() []string {
	if len(t.labels) > 0 {
		return t.labels
	}
	logLabels, err := executeFetchLogLabels(t.accountId, t.logProvider)
	if err != nil {
		return []string{}
	}
	labels := make([]string, 0, len(logLabels.Labels))
	for _, label := range logLabels.Labels {
		// skip some labels like k8s_** as they are only applicable for otel based system, causing consusion
		// on downside, it may skip some sceanrioswhere data is pushed from outside to loki using otel (TBD)
		if strings.EqualFold(t.logProvider.Provider, "loki") {
			if label.Label == "k8s_pod_name" || label.Label == "k8s_pod_namespace" || label.Label == "k8s_deployment_name" {
				continue
			}
		}
		if IsESLogProvider(t.logProvider.Provider) {
			if skipESLabel(label.Label) {
				continue
			}
		}
		labels = append(labels, label.Label)
	}
	if IsESLogProvider(t.logProvider.Provider) {
		labels = deduplicateESLabels(labels)
		labels = capESLabels(labels)
	}
	if len(labels) > 0 {
		t.labels = append(labels, "_body")
	}
	return t.labels
}

// maxESLabelsInPrompt is the cap on how many ES field names are injected into the
// LLM prompt. Real ES indices can return thousands of fields; dumping all of them
// wastes tokens and confuses the model. Priority fields are always included first;
// remaining slots are filled from the filtered/deduped list.
const maxESLabelsInPrompt = 50

// esLabelPriority lists the most useful ES fields for log querying in priority order.
// These are always included first (up to maxESLabelsInPrompt), regardless of what the
// index returns, as long as they exist in the actual label set.
var esLabelPriority = []string{
	"kubernetes.namespace_name.keyword",
	"kubernetes.pod_name.keyword",
	"kubernetes.container_name.keyword",
	"kubernetes.host.keyword",
	"kubernetes.labels.app_kubernetes_io/name.keyword",
	"kubernetes.labels.app_kubernetes_io/component.keyword",
	"kubernetes.labels.app_kubernetes_io/instance.keyword",
	"stream.keyword",
	"log.keyword",
	"message",
}

// IsESLogProvider returns true if the provider string identifies an Elasticsearch log provider.
func IsESLogProvider(provider string) bool {
	p := strings.ToLower(provider)
	return p == "es" || p == "elasticsearch"
}

// skipESLabel returns true for ES fields that are not useful for log querying:
// internal ES metadata fields and un-queryable parent container objects.
func skipESLabel(label string) bool {
	// Internal ES metadata: _id, _source, _version, _seq_no, _routing, etc.
	if strings.HasPrefix(label, "_") {
		return true
	}
	// Parent container objects — not directly filterable leaf fields.
	switch label {
	case "kubernetes", "kubernetes.labels", "kubernetes.annotations", "@timestamp", "time":
		return true
	}
	return false
}

// deduplicateESLabels removes text-field variants when the corresponding .keyword
// field is already present (e.g. keeps "kubernetes.pod_name.keyword", drops "kubernetes.pod_name").
func deduplicateESLabels(labels []string) []string {
	labelSet := make(map[string]struct{}, len(labels))
	for _, l := range labels {
		labelSet[l] = struct{}{}
	}
	result := make([]string, 0, len(labels))
	for _, l := range labels {
		if !strings.HasSuffix(l, ".keyword") {
			if _, hasKeyword := labelSet[l+".keyword"]; hasKeyword {
				continue // prefer the .keyword variant
			}
		}
		result = append(result, l)
	}
	return result
}

// capESLabels limits the label list to maxESLabelsInPrompt entries, always
// placing priority fields first so the most useful ones are never crowded out.
func capESLabels(labels []string) []string {
	if len(labels) <= maxESLabelsInPrompt {
		return labels
	}
	labelSet := make(map[string]struct{}, len(labels))
	for _, l := range labels {
		labelSet[l] = struct{}{}
	}

	result := make([]string, 0, maxESLabelsInPrompt)
	added := make(map[string]struct{}, maxESLabelsInPrompt)

	// Phase 1: priority fields that exist in the actual label set.
	for _, p := range esLabelPriority {
		if _, exists := labelSet[p]; exists {
			result = append(result, p)
			added[p] = struct{}{}
		}
	}

	// Phase 2: fill remaining slots from the filtered list (preserving original order).
	for _, l := range labels {
		if len(result) >= maxESLabelsInPrompt {
			break
		}
		if _, seen := added[l]; !seen {
			result = append(result, l)
		}
	}

	slog.Info("es labels capped for prompt", "total", len(labels), "capped", len(result))
	return result
}

func (t *NBLogTool) QueryLabelValues() []string {
	return []string{}
}

func (t *NBLogTool) GetOperators() []string {
	return []string{}
}

func (t *NBLogTool) InputSchema() core.ToolSchema {
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
				Description: "Elasticsearch index name or pattern to query (e.g., 'app-logs-*', 'nginx-access-*'). If not specified, the account's default index is used.",
			},
		},
		Required: []string{"command"},
	}
}

func (t *NBLogTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("logs: executing getLogs tool call", "query", input.Command)

	queryBuilder, err := core.BuildLogQueryBuilder(nbRequestContext, input.Command)
	if err != nil {
		return core.NBToolResponse{}, err
	}
	if queryBuilder.Limit == 0 {
		queryBuilder.Limit = 1000
	}

	switch strings.ToLower(t.logProvider.Provider) {
	case "loki":
		logsQuery, err := t.queryBuilderToLokiQuery(queryBuilder)
		if err != nil {
			return core.NBToolResponse{}, err
		}

		args := input.Arguments
		if args == nil {
			args = make(map[string]any)
		}
		if _, ok := args["range"]; !ok && queryBuilder.TimeRange != "" {
			args["range"] = queryBuilder.TimeRange
		}
		if _, ok := args["start_time"]; !ok && queryBuilder.StartTime != "" {
			args["start_time"] = queryBuilder.StartTime
		}
		if _, ok := args["end_time"]; !ok && queryBuilder.EndTime != "" {
			args["end_time"] = queryBuilder.EndTime
		}

		limit := queryBuilder.Limit

		start := time.Now().Add(-1 * time.Hour)
		end := time.Now()
		if t1, t2, err := ExtractStartEndtimeFromLabels(nbRequestContext, args); err == nil {
			start = t1
			end = t2
		}
		start, end = ExpandNarrowTimeWindow(nbRequestContext.Ctx.GetLogger(), start, end)

		// Format as Loki HTTP API query string: query=LOGQL&limit=N&start=NANOS&end=NANOS
		finalQuery := fmt.Sprintf("query=%s&limit=%d&start=%d&end=%d", logsQuery, limit, start.UnixNano(), end.UnixNano())

		response, err := executeFetchLogs(nbRequestContext, t.logProvider, finalQuery, map[string]any{
			"end_time":   end.UnixMilli(),
			"start_time": start.UnixMilli(),
			"limit":      limit,
		})
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("logs: unable to execute loki query", "query", logsQuery, "error", err.Error())
			return core.NBToolResponse{
				Data:   "",
				Status: core.NBToolResponseStatusError,
			}, errors.Wrap(core.ErrUnableToFetchData, err.Error())
		}
		if len(response.Logs) == 0 {
			noLogsMsg := fmt.Sprintf(
				"No logs found for Loki query: %s (time range: %s to %s, limit: %d). "+
					"The query executed successfully but returned no results. "+
					"Suggestions: check if label names/values are correct, try broader filters, or expand the time range.",
				logsQuery, start.Format(time.RFC3339), end.Format(time.RFC3339), limit,
			)
			return core.NBToolResponse{
				Data:   noLogsMsg,
				Status: core.NBToolResponseStatusSuccess,
			}, nil
		}
		lokiData, err := common.MarshalJson(response)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("logs: unable to serialize loki json", "error", err.Error())
			return core.NBToolResponse{
				Data:   "",
				Status: core.NBToolResponseStatusError,
			}, core.ErrUnableToFetchData
		}
		return core.NBToolResponse{
			Data:       string(lokiData),
			Type:       core.NBToolResponseTypeJson,
			Status:     core.NBToolResponseStatusSuccess,
			References: []core.NBToolResponseReference{logsUIRef(nbRequestContext, queryBuilder)},
		}, nil

	case "es", "elasticsearch":
		esDSL, err := t.queryBuilderToESQuery(queryBuilder)
		if err != nil {
			return core.NBToolResponse{}, err
		}

		args := input.Arguments
		if args == nil {
			args = make(map[string]any)
		}
		if _, ok := args["range"]; !ok && queryBuilder.TimeRange != "" {
			args["range"] = queryBuilder.TimeRange
		}
		if _, ok := args["start_time"]; !ok && queryBuilder.StartTime != "" {
			args["start_time"] = queryBuilder.StartTime
		}
		if _, ok := args["end_time"]; !ok && queryBuilder.EndTime != "" {
			args["end_time"] = queryBuilder.EndTime
		}

		limit := queryBuilder.Limit

		start := time.Now().Add(-1 * time.Hour)
		end := time.Now()
		if t1, t2, err := ExtractStartEndtimeFromLabels(nbRequestContext, args); err == nil {
			start = t1
			end = t2
		}
		start, end = ExpandNarrowTimeWindow(nbRequestContext.Ctx.GetLogger(), start, end)

		fetchConfigs := map[string]any{
			"end_time":   end.UnixMilli(),
			"start_time": start.UnixMilli(),
			"limit":      limit,
		}
		if queryBuilder.Index != "" {
			fetchConfigs["index"] = queryBuilder.Index
		}

		response, err := executeFetchLogs(nbRequestContext, t.logProvider, esDSL, fetchConfigs)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("logs: unable to execute es query", "query", esDSL, "error", err.Error())
			return core.NBToolResponse{
				Data:   "",
				Status: core.NBToolResponseStatusError,
			}, errors.Wrap(core.ErrUnableToFetchData, err.Error())
		}
		if len(response.Logs) == 0 {
			noLogsMsg := fmt.Sprintf(
				"No logs found for ES query (time range: %s to %s, limit: %d). "+
					"The query executed successfully but returned no results. "+
					"Suggestions: try broadening the query, check field names (e.g. kubernetes.pod_name.keyword), or expand the time range.",
				start.Format(time.RFC3339), end.Format(time.RFC3339), limit,
			)
			return core.NBToolResponse{
				Data:   noLogsMsg,
				Status: core.NBToolResponseStatusSuccess,
			}, nil
		}
		esData, err := common.MarshalJson(response)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("logs: unable to serialize es json", "error", err.Error())
			return core.NBToolResponse{
				Data:   "",
				Status: core.NBToolResponseStatusError,
			}, core.ErrUnableToFetchData
		}
		return core.NBToolResponse{
			Data:       string(esData),
			Type:       core.NBToolResponseTypeJson,
			Status:     core.NBToolResponseStatusSuccess,
			References: []core.NBToolResponseReference{logsUIRef(nbRequestContext, queryBuilder)},
		}, nil

	case "signoz":
		signozFilters := t.queryBuilderToSignozFilters(queryBuilder)
		filtersJSON, err := common.MarshalJson(signozFilters)
		if err != nil {
			return core.NBToolResponse{}, err
		}

		args := input.Arguments
		if args == nil {
			args = make(map[string]any)
		}
		if _, ok := args["range"]; !ok && queryBuilder.TimeRange != "" {
			args["range"] = queryBuilder.TimeRange
		}
		if _, ok := args["start_time"]; !ok && queryBuilder.StartTime != "" {
			args["start_time"] = queryBuilder.StartTime
		}
		if _, ok := args["end_time"]; !ok && queryBuilder.EndTime != "" {
			args["end_time"] = queryBuilder.EndTime
		}

		start := time.Now().Add(-1 * time.Hour)
		end := time.Now()
		if t1, t2, err := ExtractStartEndtimeFromLabels(nbRequestContext, args); err == nil {
			start = t1
			end = t2
		}

		response, err := executeFetchLogs(nbRequestContext, t.logProvider, string(filtersJSON), map[string]any{
			"end_time":   end.UnixMilli(),
			"start_time": start.UnixMilli(),
			"limit":      queryBuilder.Limit,
		})
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("logs: unable to execute signoz query", "error", err.Error())
			return core.NBToolResponse{
				Data:   fmt.Sprintf("Error executing Signoz query: %s", err.Error()),
				Status: core.NBToolResponseStatusError,
			}, errors.Wrap(core.ErrUnableToFetchData, err.Error())
		}
		if len(response.Logs) == 0 {
			noLogsMsg := fmt.Sprintf(
				"No logs found for Signoz query (time range: %s to %s, limit: %d). "+
					"The query executed successfully but returned no results. "+
					"Suggestions: check if label names/values are correct, try broader filters, or expand the time range.",
				start.Format(time.RFC3339), end.Format(time.RFC3339), queryBuilder.Limit,
			)
			return core.NBToolResponse{
				Data:   noLogsMsg,
				Status: core.NBToolResponseStatusSuccess,
			}, nil
		}
		signozData, err := common.MarshalJson(response)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("logs: unable to serialize signoz json", "error", err.Error())
			return core.NBToolResponse{
				Data:   "",
				Status: core.NBToolResponseStatusError,
			}, core.ErrUnableToFetchData
		}
		return core.NBToolResponse{
			Data:       string(signozData),
			Type:       core.NBToolResponseTypeJson,
			Status:     core.NBToolResponseStatusSuccess,
			References: []core.NBToolResponseReference{logsUIRef(nbRequestContext, queryBuilder)},
		}, nil

	case "loggly", "observ", "observe":
		var logQuery string
		var err error
		switch strings.ToLower(t.logProvider.Provider) {
		case "loggly":
			logQuery, err = t.queryBuilderToLogglyQuery(queryBuilder)
		case "observ", "observe":
			logQuery, err = t.queryBuilderToObservQuery(queryBuilder)
		}
		if err != nil {
			return core.NBToolResponse{}, err
		}

		start := time.Now().Add(-12 * time.Hour)
		end := time.Now()
		t1, t2, err := ExtractStartEndtimeFromLabels(nbRequestContext, input.Arguments)
		if err == nil {
			start = t1
			end = t2
		}
		start, end = ExpandNarrowTimeWindow(nbRequestContext.Ctx.GetLogger(), start, end)

		limit := 0
		if limitAny, ok := input.Arguments["limit"]; ok {
			switch limitValue := limitAny.(type) {
			case string:
				limit, _ = strconv.Atoi(limitValue)
			case float64:
				limit = int(limitValue)
			case int:
				limit = limitValue
			}
		}
		if limit == 0 && queryBuilder.StartTime == "" && queryBuilder.EndTime == "" {
			limit = 1000
		}
		response, err := executeFetchLogs(nbRequestContext, t.logProvider, logQuery, map[string]any{
			"end_time":   end.UnixMilli(),
			"start_time": start.UnixMilli(),
			"limit":      limit,
		})
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("logs: unable to execute query", "query", logQuery, "error", err.Error())
			return core.NBToolResponse{
				Data:   fmt.Sprintf("Error executing %s query: %s", t.logProvider.Provider, err.Error()),
				Status: core.NBToolResponseStatusError,
			}, errors.Wrap(core.ErrUnableToFetchData, err.Error())
		}
		if len(response.Logs) == 0 {
			noLogsMsg := fmt.Sprintf(
				"No logs found for %s query (time range: %s to %s, limit: %d). "+
					"The query executed successfully but returned no results. "+
					"Suggestions: try broader filters or expand the time range.",
				t.logProvider.Provider, start.Format(time.RFC3339), end.Format(time.RFC3339), limit,
			)
			return core.NBToolResponse{
				Data:   noLogsMsg,
				Status: core.NBToolResponseStatusSuccess,
			}, nil
		}
		data, err := common.MarshalJson(response)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("logs: unable to serialize json", "error", err.Error())
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

	nbRequestContext.Ctx.GetLogger().Error("logs: unsupported log provider", "provider", t.logProvider.Provider)
	return core.NBToolResponse{
		Data:   fmt.Sprintf("Unsupported log provider: %s", t.logProvider.Provider),
		Status: core.NBToolResponseStatusError,
	}, fmt.Errorf("unsupported log provider: %s", t.logProvider.Provider)
}

// queryBuilderToSignozFilters converts the generic QueryBuilder where clause into
// Signoz's filter format: [{"key":{"key":"field"},"value":"val","op":"="}].
func (t *NBLogTool) queryBuilderToSignozFilters(queryBuilder core.QueryBuilder) []map[string]any {
	var filters []map[string]any

	appendFilters := func(clause core.BinaryWhereClause) {
		for field, ops := range clause {
			for opType, value := range ops {
				signozOp, skip := queryBuilderOpToSignozOp(opType)
				if skip {
					continue
				}
				filters = append(filters, map[string]any{
					"key":   map[string]string{"key": field},
					"value": value,
					"op":    signozOp,
				})
			}
		}
	}

	// Convert binary clauses
	appendFilters(queryBuilder.Where.Binary)

	// Convert _and clauses
	for _, andClause := range queryBuilder.Where.And {
		appendFilters(andClause.Binary)
	}

	// Note: _or clauses are intentionally skipped. Signoz filter API ANDs all items
	// together, so appending OR-intended filters would incorrectly narrow results.
	// The query_generator is guided via examples to avoid _or for Signoz queries.

	return filters
}

// queryBuilderOpToSignozOp maps QueryBuilder operators to Signoz filter operators.
// Returns the Signoz operator and whether the filter should be skipped.
func queryBuilderOpToSignozOp(op core.BinaryWhereClauseType) (string, bool) {
	switch op {
	case core.Eq, core.EqF:
		return "=", false
	case core.Nq, core.NqF:
		return "!=", false
	case core.Gt, core.GtF:
		return ">", false
	case core.Gte, core.GteF:
		return ">=", false
	case core.Lt, core.LtF:
		return "<", false
	case core.Lte, core.LteF:
		return "<=", false
	case core.Like, core.LikeF:
		return "like", false
	case core.ILike, core.ILikeF:
		return "contains", false
	case core.NLike:
		return "nlike", false
	case core.Contains:
		return "contains", false
	case core.In:
		return "in", false
	case core.NotIn:
		return "nin", false
	case core.IsNull:
		return "exists", true // Signoz supports exists/nexists but _is_null semantics don't map cleanly; skip
	default:
		return "=", false
	}
}

func (t *NBLogTool) queryBuilderToObservQuery(queryBuilder core.QueryBuilder) (string, error) {
	filterString, err := t.buildObservFilterString(queryBuilder.Where)
	if err != nil {
		return "", err
	}

	var queryParts []string
	if filterString != "" {
		queryParts = append(queryParts, "filter "+filterString)
	}

	// Append limit if specified. In OPAL, limit is part of the query string.
	if queryBuilder.Limit > 0 {
		queryParts = append(queryParts, fmt.Sprintf("limit %d", queryBuilder.Limit))
	}

	return strings.Join(queryParts, " | "), nil
}

func (t *NBLogTool) buildObservFilterString(clause core.QueryWhereClause) (string, error) {
	var parts []string

	// Helper to format non-string values (numbers, booleans)
	formatNonStringValue := func(v any) string {
		switch val := v.(type) {
		case string: // Should not happen if used correctly, but as a fallback
			return fmt.Sprintf("%q", val)
		case int, int32, int64, uint, uint32, uint64, float32, float64:
			return fmt.Sprintf("%v", val)
		case bool:
			return fmt.Sprintf("%t", val)
		default:
			return fmt.Sprintf("%v", v)
		}
	}

	// Helper to format string values that need to be quoted
	formatQuotedString := func(s string) string {
		return fmt.Sprintf("%q", s)
	}

	// Helper to format string values that should NOT be quoted (for `~` operator)

	formatUnquotedString := func(s string) string {

		// Convert SQL '%' wildcard to OPAL '*' wildcard

		s = strings.ReplaceAll(s, "%", "*")

		return s

	}

	// Helper to format regex literals (e.g., /pattern/)

	formatRegexLiteral := func(pattern string) string {

		return fmt.Sprintf("/%s/", pattern)

	}

	// 1. Process Binary conditions

	var binaryConditions []string

	for field, conditions := range clause.Binary {

		for op, value := range conditions {

			var part string

			stringValue := fmt.Sprintf("%v", value) // Convert value to string for easier handling

			switch op {

			case core.Eq:

				part = fmt.Sprintf("%s = %s", field, formatQuotedString(stringValue))

			case core.Nq:

				part = fmt.Sprintf("%s != %s", field, formatQuotedString(stringValue))

			case core.Gt:

				part = fmt.Sprintf("%s > %s", field, formatNonStringValue(value))

			case core.Gte:

				part = fmt.Sprintf("%s >= %s", field, formatNonStringValue(value))

			case core.Lt:

				part = fmt.Sprintf("%s < %s", field, formatNonStringValue(value))

			case core.Lte:

				part = fmt.Sprintf("%s <= %s", field, formatNonStringValue(value))

			case core.In:

				if values, ok := value.([]any); ok {

					var listItems []string

					for _, item := range values {

						switch v := item.(type) {

						case string:

							listItems = append(listItems, formatQuotedString(v))

						default:

							listItems = append(listItems, formatNonStringValue(v))

						}

					}

					part = fmt.Sprintf("%s in [%s]", field, strings.Join(listItems, ", "))

				}

			case core.NotIn:

				if values, ok := value.([]any); ok {

					var listItems []string

					for _, item := range values {

						switch v := item.(type) {

						case string:

							listItems = append(listItems, formatQuotedString(v))

						default:

							listItems = append(listItems, formatNonStringValue(v))

						}

					}

					part = fmt.Sprintf("not (%s in [%s])", field, strings.Join(listItems, ", "))

				}

			case core.Like:

				if strings.HasPrefix(stringValue, "/") && strings.HasSuffix(stringValue, "/") {

					// Explicit regex: filter log ~ /foo|bar/

					regex := strings.Trim(stringValue, "/")

					part = fmt.Sprintf("%s ~ %s", field, formatRegexLiteral(regex))

				} else {

					// Substring/Wildcard: filter log ~ "fig" or filter ip ~ "192.168.*.*"

					// The `~` operator is case-insensitive for simple strings.

					part = fmt.Sprintf("%s ~ %s", field, formatQuotedString(formatUnquotedString(stringValue)))

				}

			case core.ILike:

				if strings.HasPrefix(stringValue, "/") && strings.HasSuffix(stringValue, "/") {

					// Explicit regex: use match_re_i for case-insensitive regex

					regex := strings.Trim(stringValue, "/")

					part = fmt.Sprintf("match_re_i(%s, %s)", field, formatQuotedString(regex))

				} else {

					// Simple string or wildcard: use `~` operator directly, as it's case-insensitive

					part = fmt.Sprintf("%s ~ %s", field, formatQuotedString(formatUnquotedString(stringValue)))

				}

			case core.NLike:

				if strings.HasPrefix(stringValue, "/") && strings.HasSuffix(stringValue, "/") {

					// Not Regex: filter log !~ /foo|bar/

					regex := strings.Trim(stringValue, "/")

					part = fmt.Sprintf("%s !~ %s", field, formatRegexLiteral(regex))

				} else {

					// Not Substring/Wildcard: filter log !~ "fig"

					part = fmt.Sprintf("%s !~ %s", field, formatQuotedString(formatUnquotedString(stringValue)))

				}

			case core.IsNull:

				part = fmt.Sprintf("!is_defined(%s)", field)

			}

			if part != "" {

				binaryConditions = append(binaryConditions, part)

			}

		}

	}

	if len(binaryConditions) > 0 {

		parts = append(parts, strings.Join(binaryConditions, " and "))

	}

	// 2. Process AND clauses

	for _, andClause := range clause.And {

		subClauseStr, err := t.buildObservFilterString(andClause)

		if err != nil {

			return "", err

		}

		if subClauseStr != "" {

			parts = append(parts, subClauseStr)

		}

	}

	// 3. Process OR clauses

	var orGroup []string

	for _, orClause := range clause.Or {

		subClauseStr, err := t.buildObservFilterString(orClause)

		if err != nil {

			return "", err

		}

		if subClauseStr != "" {

			orGroup = append(orGroup, subClauseStr)

		}

	}

	if len(orGroup) > 0 {

		// OR clauses should be grouped with parentheses

		parts = append(parts, "("+strings.Join(orGroup, " or ")+")")

	}

	// 4. Process NOT clause

	if clause.Not != nil {

		notClauseStr, err := t.buildObservFilterString(*clause.Not)

		if err != nil {

			return "", err

		}

		if notClauseStr != "" {

			// NOT clause should be grouped with parentheses

			parts = append(parts, "not ("+notClauseStr+")")

		}

	}

	// Combine all parts with " and "

	if len(parts) == 0 {

		return "", nil

	}

	return strings.Join(parts, " and "), nil
}

func (t *NBLogTool) queryBuilderToLogglyQuery(queryBuilder core.QueryBuilder) (string, error) {
	var queryParts []string

	for field, conditions := range queryBuilder.Where.Binary {
		for op, value := range conditions {

			queryPart := ""

			switch op {
			case core.Eq:
				if field == "_body" {
					queryPart = fmt.Sprintf("%s", value)
				} else {
					queryPart = fmt.Sprintf("%s:%s", field, value)
				}
			case core.Nq:
				if field == "_body" {
					queryPart = fmt.Sprintf("NOT %s", value)
				} else {
					queryPart = fmt.Sprintf("NOT %s:%s", field, value)
				}
			case core.In:
				if values, ok := value.([]any); ok {
					var inParts []string
					for _, v := range values {
						inParts = append(inParts, fmt.Sprintf("%v", v))
					}
					if field == "_body" {
						queryPart = fmt.Sprintf("(%s)", strings.Join(inParts, " OR "))
					} else {
						queryPart = fmt.Sprintf("%s:(%s)", field, strings.Join(inParts, " OR "))
					}
				}
			case core.NotIn:
				if values, ok := value.([]any); ok {
					var notInParts []string
					for _, v := range values {
						notInParts = append(notInParts, fmt.Sprintf("%v", v))
					}
					if field == "_body" {
						queryPart = fmt.Sprintf("NOT (%s)", strings.Join(notInParts, " OR "))
					} else {
						queryPart = fmt.Sprintf("NOT %s:(%s)", field, strings.Join(notInParts, " OR "))
					}
				}
			case core.Like, core.ILike:
				if strValue, ok := value.(string); ok {
					if strings.HasPrefix(strValue, "/") && strings.HasSuffix(strValue, "/") {
						// Regex
						if field == "_body" {
							queryPart = strValue
						} else {
							queryPart = fmt.Sprintf("%s:%s", field, strValue)
						}
					} else {
						// Wildcard
						if field == "_body" {
							queryPart = fmt.Sprintf("*%s*", strValue)
						} else {
							queryPart = fmt.Sprintf("%s:*%s*", field, strValue)
						}
					}
				}
			case core.NLike:
				if strValue, ok := value.(string); ok {
					if strings.HasPrefix(strValue, "/") && strings.HasSuffix(strValue, "/") {
						// Regex
						if field == "_body" {
							queryPart = fmt.Sprintf("NOT %s", strValue)
						} else {
							queryPart = fmt.Sprintf("NOT %s:%s", field, strValue)
						}
					} else {
						// Wildcard
						if field == "_body" {
							queryPart = fmt.Sprintf("NOT *%s*", strValue)
						} else {
							queryPart = fmt.Sprintf("NOT %s:*%s*", field, strValue)
						}
					}
				}
			}
			if queryPart != "" {
				queryParts = append(queryParts, queryPart)
			}
		}
	}

	return strings.Join(queryParts, " AND "), nil
}

func (t *NBLogTool) escapeLokiValue(value string) string {
	// Escape double quotes and backslashes
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}

func (t *NBLogTool) sqlWildcardToRegex(pattern string) string {
	// SQL semantics: `%` → `.*`, `_` → `.`. All other metacharacters are
	// escaped so a literal `.` or `[ERROR]` in the user's pattern doesn't
	// become a regex wildcard / character class. NUL placeholders survive
	// QuoteMeta so the SQL wildcards can be restored after escaping.
	const pctMarker = "\x00P\x00"
	const undMarker = "\x00U\x00"
	pattern = strings.ReplaceAll(pattern, `%`, pctMarker)
	pattern = strings.ReplaceAll(pattern, `_`, undMarker)
	pattern = regexp.QuoteMeta(pattern)
	pattern = strings.ReplaceAll(pattern, pctMarker, `.*`)
	pattern = strings.ReplaceAll(pattern, undMarker, `.`)
	return pattern
}

func (t *NBLogTool) buildLokiQueryFromWhereClause(whereClause *core.QueryWhereClause) (string, error) {
	allBinaries := []map[string]map[core.BinaryWhereClauseType]any{}
	var orBodyRegex string
	orLabelSelectorsValues := make(map[string][]string)

	if whereClause != nil {
		queue := []*core.QueryWhereClause{whereClause}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]

			if len(current.Binary) > 0 {
				allBinaries = append(allBinaries, current.Binary)
			}
			for i := range current.And {
				queue = append(queue, &current.And[i])
			}
			if len(current.Or) > 0 {
				if orBodyRegex != "" || len(orLabelSelectorsValues) > 0 {
					return "", errors.New("multiple OR groups are not supported for Loki log queries")
				}

				var orField string
				var bodyRegexParts []string
				isBodyOr := false
				firstOrClause := true

				for _, orClause := range current.Or {
					if len(orClause.And) > 0 || len(orClause.Or) > 0 || orClause.Not != nil || len(orClause.Binary) != 1 {
						return "", errors.New("OR clauses are only supported for simple conditions on a single field in Loki log queries")
					}

					for field, conditions := range orClause.Binary {
						if firstOrClause {
							orField = field
							isBodyOr = field == "_body"
							firstOrClause = false
						} else if field != orField {
							// Different fields in OR, must use line filter.
							// If not already in line filter mode, we need to move existing parts to line filter.
							if !isBodyOr {
								isBodyOr = true
								// Move parts from label selector to body regex parts
								if values, ok := orLabelSelectorsValues[orField]; ok {
									for _, v := range values {
										// Reconstruct what would have been a regex part for both logfmt and JSON
										bodyRegexParts = append(bodyRegexParts, fmt.Sprintf(`(%s=\"%s\"|\"%s\":\"%s\")`, orField, v, orField, v))
									}
									delete(orLabelSelectorsValues, orField)
								}
							}
						}

						if len(conditions) != 1 {
							return "", errors.Errorf("OR clauses only support one condition per field, found multiple for '%s'", field)
						}

						for op, value := range conditions {
							stringValue := fmt.Sprintf("%v", value)
							var processedValue string
							switch op {
							case core.Eq:
								processedValue = t.escapeLokiValue(stringValue)
							case core.Like:
								processedValue = t.escapeLokiValue(t.sqlWildcardToRegex(stringValue))
							case core.ILike:
								// Wrap each ILike value in `(?i:...)` so case-insensitivity
								// scopes to this term when ORed with case-sensitive Like terms.
								processedValue = fmt.Sprintf("(?i:%s)", t.escapeLokiValue(t.sqlWildcardToRegex(stringValue)))
							default:
								return "", errors.Errorf("unsupported operator in OR for %s: %s. Only 'eq', 'like', 'ilike' are supported", field, op)
							}

							if isBodyOr {
								if field == "_body" {
									bodyRegexParts = append(bodyRegexParts, processedValue)
								} else {
									// Create a regex part that matches both logfmt (key="value") and JSON ("key":"value")
									bodyRegexParts = append(bodyRegexParts, fmt.Sprintf(`(%s=\"%s\"|\"%s\":\"%s\")`, field, processedValue, field, processedValue))
								}
							} else {
								orLabelSelectorsValues[orField] = append(orLabelSelectorsValues[orField], processedValue)
							}
						}
					}
				}

				if len(bodyRegexParts) > 0 {
					orBodyRegex = strings.Join(bodyRegexParts, "|")
				}
			} else if current.Not != nil {
				return "", errors.New("NOT clauses are not supported for Loki log queries. Use operators like '!=', '!~', or 'NotIn'.")
			}
		}
	}

	if len(allBinaries) == 0 && orBodyRegex == "" && len(orLabelSelectorsValues) == 0 {
		return `{stream="stdout"}`, nil
	}

	labelSelectorsMap := make(map[string]string)
	var lineFilters []string

	if orBodyRegex != "" {
		lineFilters = append(lineFilters, fmt.Sprintf(`|~ "%s"`, orBodyRegex))
	}

	for field, values := range orLabelSelectorsValues {
		selector := fmt.Sprintf(`%s=~"%s"`, field, strings.Join(values, "|"))
		if existing, found := labelSelectorsMap[field]; found && existing != selector {
			return "", errors.Errorf("conflicting AND/OR conditions for label '%s'", field)
		}
		labelSelectorsMap[field] = selector
	}

	for _, binary := range allBinaries {
		for field, conditions := range binary {
			for op, value := range conditions {
				stringValue := fmt.Sprintf("%v", value)
				if field == "_body" {
					switch op {
					case core.Eq:
						lineFilters = append(lineFilters, fmt.Sprintf(`|= "%s"`, t.escapeLokiValue(stringValue)))
					case core.Nq:
						lineFilters = append(lineFilters, fmt.Sprintf(`!= "%s"`, t.escapeLokiValue(stringValue)))
					case core.Like:
						lineFilters = append(lineFilters, fmt.Sprintf(`|~ "%s"`, t.escapeLokiValue(t.sqlWildcardToRegex(stringValue))))
					case core.ILike:
						// LogQL `|~` is case-sensitive by default. Prepend `(?i)` so ILike
						// matches the SQL semantics of case-insensitive substring/pattern.
						lineFilters = append(lineFilters, fmt.Sprintf(`|~ "(?i)%s"`, t.escapeLokiValue(t.sqlWildcardToRegex(stringValue))))
					case core.NLike:
						lineFilters = append(lineFilters, fmt.Sprintf(`!~ "%s"`, t.escapeLokiValue(t.sqlWildcardToRegex(stringValue))))
					case core.In:
						if values, ok := value.([]any); ok {
							var regexParts []string
							for _, v := range values {
								regexParts = append(regexParts, t.escapeLokiValue(fmt.Sprintf("%v", v)))
							}
							lineFilters = append(lineFilters, fmt.Sprintf(`|~ "%s"`, strings.Join(regexParts, "|")))
						} else {
							return "", errors.Errorf("invalid value type for _body In operator: %T", value)
						}
					case core.NotIn:
						if values, ok := value.([]any); ok {
							var regexParts []string
							for _, v := range values {
								regexParts = append(regexParts, t.escapeLokiValue(fmt.Sprintf("%v", v)))
							}
							lineFilters = append(lineFilters, fmt.Sprintf(`!~ "%s"`, strings.Join(regexParts, "|")))
						} else {
							return "", errors.Errorf("invalid value type for _body NotIn operator: %T", value)
						}
					case core.Lt, core.Gt, core.Lte, core.Gte:
						slog.Warn("Loki: Numeric comparisons on _body are not directly supported in this conversion. Treating as string match.")
						lineFilters = append(lineFilters, fmt.Sprintf(`|= "%s"`, t.escapeLokiValue(stringValue)))
					case core.IsNull:
						slog.Warn("Loki: IsNull operator on _body is not directly supported in this conversion.")
					default:
						return "", errors.Errorf("unsupported operator for _body: %s", op)
					}
				} else {
					var selector string
					switch op {
					case core.Eq:
						selector = fmt.Sprintf(`%s="%s"`, field, t.escapeLokiValue(stringValue))
					case core.Nq:
						selector = fmt.Sprintf(`%s!="%s"`, field, t.escapeLokiValue(stringValue))
					case core.Like:
						selector = fmt.Sprintf(`%s=~"%s"`, field, t.escapeLokiValue(t.sqlWildcardToRegex(stringValue)))
					case core.ILike:
						selector = fmt.Sprintf(`%s=~"(?i)%s"`, field, t.escapeLokiValue(t.sqlWildcardToRegex(stringValue)))
					case core.NLike:
						selector = fmt.Sprintf(`%s!~"%s"`, field, t.escapeLokiValue(t.sqlWildcardToRegex(stringValue)))
					case core.In:
						if values, ok := value.([]any); ok {
							var regexParts []string
							for _, v := range values {
								regexParts = append(regexParts, t.escapeLokiValue(fmt.Sprintf("%v", v)))
							}
							selector = fmt.Sprintf(`%s=~"%s"`, field, strings.Join(regexParts, "|"))
						} else {
							return "", errors.Errorf("invalid value type for In operator: %T", value)
						}
					case core.NotIn:
						if values, ok := value.([]any); ok {
							var regexParts []string
							for _, v := range values {
								regexParts = append(regexParts, t.escapeLokiValue(fmt.Sprintf("%v", v)))
							}
							selector = fmt.Sprintf(`%s!~"%s"`, field, strings.Join(regexParts, "|"))
						} else {
							return "", errors.Errorf("invalid value type for NotIn operator: %T", value)
						}
					case core.Lt, core.Gt, core.Lte, core.Gte:
						slog.Warn("Loki: Numeric comparisons on labels are not directly supported in this conversion. Treating as string match.")
						selector = fmt.Sprintf(`%s="%s"`, field, t.escapeLokiValue(stringValue))
					case core.IsNull:
						slog.Warn("Loki: IsNull operator on labels is not directly supported in this conversion.")
					default:
						return "", errors.Errorf("unsupported operator for label: %s", op)
					}

					if existing, found := labelSelectorsMap[field]; found && existing != selector {
						return "", errors.Errorf("conflicting AND conditions for label '%s'", field)
					}
					labelSelectorsMap[field] = selector
				}
			}
		}
	}

	var labelSelectors []string
	for _, selector := range labelSelectorsMap {
		labelSelectors = append(labelSelectors, selector)
	}
	sort.Strings(labelSelectors)

	query := fmt.Sprintf("{%s}", strings.Join(labelSelectors, ","))
	if len(lineFilters) > 0 {
		query += " " + strings.Join(lineFilters, " ")
	}
	return query, nil
}

// queryBuilderToESQuery converts a generic QueryBuilder into an Elasticsearch DSL JSON string.
// The special field "_body" is aliased to "message" (the standard ES log field).
func (t *NBLogTool) queryBuilderToESQuery(queryBuilder core.QueryBuilder) (string, error) {
	must, mustNot := t.whereClauseToESClauses(queryBuilder.Where)

	boolQuery := map[string]any{}
	if len(must) > 0 {
		boolQuery["filter"] = must
	}
	if len(mustNot) > 0 {
		boolQuery["must_not"] = mustNot
	}

	query := map[string]any{
		"query": map[string]any{
			"bool": boolQuery,
		},
	}
	if queryBuilder.Limit > 0 {
		query["size"] = queryBuilder.Limit
	}

	data, err := common.MarshalJson(query)
	return string(data), err
}

// whereClauseToESClauses recursively converts a QueryWhereClause into ES bool query must/must_not slices.
func (t *NBLogTool) whereClauseToESClauses(clause core.QueryWhereClause) (must []map[string]any, mustNot []map[string]any) {
	// matchField strips .keyword suffix for match_phrase queries — match_phrase works on
	// text fields and behaves unexpectedly on keyword sub-fields in some ES versions.
	matchField := func(f string) string {
		if f == "_body" {
			return "message"
		}
		return strings.TrimSuffix(f, ".keyword")
	}
	// esField normalizes fields for wildcard/terms/range queries.
	// kubernetes.labels.* fields are text type — .keyword sub-field is not mapped.
	esField := func(f string) string {
		if f == "_body" {
			return "message"
		}
		if strings.HasPrefix(f, "kubernetes.labels.") && strings.HasSuffix(f, ".keyword") {
			return strings.TrimSuffix(f, ".keyword")
		}
		return f
	}
	sqlWildcardToES := func(s string) string {
		return strings.ReplaceAll(s, "%", "*")
	}

	for field, conditions := range clause.Binary {
		ef := esField(field)
		mf := matchField(field)
		for op, value := range conditions {
			switch op {
			case core.Eq:
				must = append(must, map[string]any{
					"match_phrase": map[string]any{mf: value},
				})
			case core.Nq:
				mustNot = append(mustNot, map[string]any{
					"match_phrase": map[string]any{mf: value},
				})
			case core.Like:
				esVal := sqlWildcardToES(fmt.Sprintf("%v", value))
				if !strings.Contains(esVal, "*") && !strings.Contains(esVal, "?") {
					must = append(must, map[string]any{"match_phrase": map[string]any{mf: value}})
				} else {
					must = append(must, map[string]any{"wildcard": map[string]any{ef: map[string]any{"value": esVal}}})
				}
			case core.ILike:
				esVal := sqlWildcardToES(fmt.Sprintf("%v", value))
				if !strings.Contains(esVal, "*") && !strings.Contains(esVal, "?") {
					must = append(must, map[string]any{"match_phrase": map[string]any{mf: value}})
				} else {
					must = append(must, map[string]any{"wildcard": map[string]any{ef: map[string]any{"value": esVal, "case_insensitive": true}}})
				}
			case core.NLike:
				esVal := sqlWildcardToES(fmt.Sprintf("%v", value))
				if !strings.Contains(esVal, "*") && !strings.Contains(esVal, "?") {
					mustNot = append(mustNot, map[string]any{"match_phrase": map[string]any{mf: value}})
				} else {
					mustNot = append(mustNot, map[string]any{"wildcard": map[string]any{ef: map[string]any{"value": esVal}}})
				}
			case core.In:
				if vals, ok := value.([]any); ok {
					must = append(must, map[string]any{"terms": map[string]any{ef: vals}})
				}
			case core.NotIn:
				if vals, ok := value.([]any); ok {
					mustNot = append(mustNot, map[string]any{"terms": map[string]any{ef: vals}})
				}
			case core.Gt:
				must = append(must, map[string]any{"range": map[string]any{ef: map[string]any{"gt": value}}})
			case core.Gte:
				must = append(must, map[string]any{"range": map[string]any{ef: map[string]any{"gte": value}}})
			case core.Lt:
				must = append(must, map[string]any{"range": map[string]any{ef: map[string]any{"lt": value}}})
			case core.Lte:
				must = append(must, map[string]any{"range": map[string]any{ef: map[string]any{"lte": value}}})
			case core.IsNull:
				mustNot = append(mustNot, map[string]any{"exists": map[string]any{"field": ef}})
			}
		}
	}

	// AND: recurse and merge into the same must/must_not lists
	for _, andClause := range clause.And {
		subMust, subMustNot := t.whereClauseToESClauses(andClause)
		must = append(must, subMust...)
		mustNot = append(mustNot, subMustNot...)
	}

	// OR: each branch is a self-contained bool query to preserve internal AND grouping.
	// e.g. (A AND B) OR C must NOT flatten to A OR B OR C.
	// Exception: a branch with exactly one must clause and no must_not is emitted as-is
	// (no bool wrapper needed), which keeps the DSL idiomatic and matches test expectations.
	if len(clause.Or) > 0 {
		var shouldClauses []map[string]any
		for _, orClause := range clause.Or {
			subMust, subMustNot := t.whereClauseToESClauses(orClause)
			if len(subMust) == 1 && len(subMustNot) == 0 {
				// Single positive clause — emit directly, no bool wrapper needed.
				shouldClauses = append(shouldClauses, subMust[0])
				continue
			}
			branchQuery := make(map[string]any)
			if len(subMust) > 0 {
				branchQuery["filter"] = subMust
			}
			if len(subMustNot) > 0 {
				branchQuery["must_not"] = subMustNot
			}
			if len(branchQuery) > 0 {
				shouldClauses = append(shouldClauses, map[string]any{"bool": branchQuery})
			}
		}
		if len(shouldClauses) > 0 {
			must = append(must, map[string]any{
				"bool": map[string]any{"should": shouldClauses, "minimum_should_match": 1},
			})
		}
	}

	// NOT: invert — sub-must becomes must_not and vice versa
	if clause.Not != nil {
		subMust, subMustNot := t.whereClauseToESClauses(*clause.Not)
		mustNot = append(mustNot, subMust...)
		must = append(must, subMustNot...)
	}

	return must, mustNot
}

func (t *NBLogTool) queryBuilderToLokiQuery(queryBuilder core.QueryBuilder) (string, error) {
	query, err := t.buildLokiQueryFromWhereClause(&queryBuilder.Where)
	if err != nil {
		return "", err
	}

	// if there is no filter, then by default use stdout
	if strings.HasPrefix(query, "{}") {
		query = strings.Replace(query, "{}", `{stream="stdout"}`, 1)
	}

	return query, nil
}
