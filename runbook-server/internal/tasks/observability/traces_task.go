package observability

import (
	"errors"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/service"
	"strconv"
	"time"

	"github.com/mitchellh/mapstructure"
)

// TracesTask defines a task for querying traces provider.
type TracesTask struct{}

func (t *TracesTask) GetName() string {
	return "observability.traces"
}

// GetDescription returns a brief description of the task.
func (t *TracesTask) GetDescription() string {
	return "Query Traces."
}

// GetDisplayName returns a human-readable name for the task.
func (t *TracesTask) GetDisplayName() string {
	return "Query Traces"
}

func (t *TracesTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	taskCtx.GetLogger().Debug("Executing TracesTask", "params", params)

	accountId := taskCtx.GetAccountID()

	endTime, startTime, err := parseTimeRange(params)
	if err != nil {
		return nil, err
	}

	limit := 100
	if params["limit"] != nil {
		limit, err = parseIntParam(params["limit"], "limit")
		if err != nil {
			return nil, err
		}
	}

	offset := 0
	if params["offset"] != nil {
		switch o := params["offset"].(type) {
		case int:
			offset = o
		case int64:
			offset = int(o)
		case float64:
			offset = int(o)
		}
	}

	// trace_provider / trace_provider_source are optional overrides. When
	// empty, services-server auto-resolves the provider from the selected
	// account's agent features via GetLogsMetricsTracesProvider.
	traceProvider, _ := params["trace_provider"].(string)
	traceProviderSource, _ := params["trace_provider_source"].(string)

	// Normalize query_mode up front. Don't infer mode from the presence of
	// `query_request` in params — the task runner may have applied the
	// schema Default (the JSON-editor sample payload) before Execute was
	// called, in which case the key is always present regardless of what
	// the user actually chose. Read the explicit mode, fall back to
	// "simple".
	queryMode := "simple"
	if v, ok := params["query_mode"].(string); ok && v != "" {
		queryMode = v
	}

	// Build the structured query_request off queryMode.
	//   - "structured" → decode params["query_request"] (JSON editor).
	//   - "text"       → empty builder; the free-form query string is
	//                    passed through separately via the `query` field.
	//   - "simple"     → compile from the simple-filter convenience params
	//                    (workload_name / status / span_name / etc.).
	var queryRequest service.TraceQueryBuilder
	switch queryMode {
	case "structured":
		if qr, ok := params["query_request"].(map[string]any); ok {
			if err := mapstructure.Decode(qr, &queryRequest); err != nil {
				return nil, errors.New("unable to parse query_request: " + err.Error())
			}
		}
	case "text":
		// intentionally empty — passthrough via `query`
	default:
		queryRequest = buildQueryRequestFromSimpleFilters(params)
	}

	// Apply defaults on the builder so a payload with only account_id (or
	// only account_provider_type) still produces a bounded, ordered result
	// rather than an unlimited full-table scan on the trace backend.
	if queryRequest.Limit == 0 {
		queryRequest.Limit = limit
	}
	if queryRequest.Offset == 0 {
		queryRequest.Offset = offset
	}
	if len(queryRequest.OrderBy) == 0 {
		if sortBy, ok := params["sort_by"].(string); ok && sortBy != "" {
			queryRequest.OrderBy = sortByToOrderBy(sortBy)
		}
		if len(queryRequest.OrderBy) == 0 {
			queryRequest.OrderBy = []service.QueryOrderBy{{Column: "timestamp", Order: "desc"}}
		}
	}

	// Optional legacy text query — passed through as-is to services-server.
	query, _ := params["query"].(string)

	requestContext := taskCtx.GetNewRequestContextForAccount(accountId)
	resp, err := service.QueryTraces(requestContext, service.ObservabilityTracesV3Request{
		AccountId:      accountId,
		Query:          query,
		EndTime:        endTime.UnixMilli(),
		StartTime:      startTime.UnixMilli(),
		Limit:          limit,
		Offset:         offset,
		ProviderType:   traceProvider,
		ProviderSource: traceProviderSource,
		QueryRequest:   queryRequest,
	})

	if err != nil {
		return nil, err
	}

	return map[string]any{"traces": resp.Traces, "metadata": resp.Metadata}, nil
}

// parseTimeRange parses start_time, end_time, and duration from params.
// duration takes precedence: if set, end_time=now and start_time=now-duration.
// Otherwise falls back to explicit start_time/end_time (default: last 1 hour).
func parseTimeRange(params map[string]any) (endTime, startTime time.Time, err error) {
	endTime = time.Now()
	startTime = endTime.Add(-1 * time.Hour)

	// duration overrides start_time/end_time
	if d, ok := params["duration"].(string); ok && d != "" {
		dur, parseErr := time.ParseDuration(d)
		if parseErr != nil {
			return endTime, startTime, errors.New("unable to parse duration - " + d + " (use Go duration format: 5m, 1h, 24h)")
		}
		startTime = endTime.Add(-dur)
		return endTime, startTime, nil
	}

	if params["end_time"] != nil && params["end_time"] != "" {
		switch v := params["end_time"].(type) {
		case time.Time:
			endTime = v
		case string:
			parsed, parseErr := time.Parse(time.RFC3339, v)
			if parseErr != nil {
				parsed, parseErr = time.Parse(time.RFC3339Nano, v)
				if parseErr != nil {
					return endTime, startTime, errors.New("unable to parse end_time - " + v)
				}
			}
			endTime = parsed
		}
		startTime = endTime.Add(-1 * time.Hour)
	}

	if params["start_time"] != nil && params["start_time"] != "" {
		switch v := params["start_time"].(type) {
		case time.Time:
			startTime = v
		case string:
			parsed, parseErr := time.Parse(time.RFC3339, v)
			if parseErr != nil {
				parsed, parseErr = time.Parse(time.RFC3339Nano, v)
				if parseErr != nil {
					return endTime, startTime, errors.New("unable to parse start_time - " + v)
				}
			}
			startTime = parsed
		}
	}

	return endTime, startTime, nil
}

// parseIntParam parses a numeric parameter that can be int, int64, float32, float64, or string.
func parseIntParam(val any, name string) (int, error) {
	switch l := val.(type) {
	case int:
		return l, nil
	case int64:
		return int(l), nil
	case float32:
		return int(l), nil
	case float64:
		return int(l), nil
	case string:
		parsed, err := strconv.Atoi(l)
		if err != nil {
			return 0, errors.New("unable to parse " + name + " - " + l)
		}
		return parsed, nil
	}
	return 0, nil
}

// parseNumericParam extracts a numeric value from an any param, returns 0 if not numeric.
func parseNumericParam(val any) int64 {
	if val == nil {
		return 0
	}
	switch v := val.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

// buildQueryRequestFromSimpleFilters constructs a TraceQueryBuilder where clause
// from the simple convenience params (workload_name, status, span_name,
// min_duration_ms). Column names match TracesOutputResponse / the
// ClickhouseTraceTableDefinition in api-server — snake_case, not the
// TitleCase aliases the legacy in-app trace viewer used. `workload_name`
// is preferred over `service_name` because the k8s-agent-captured view
// falls back to service.name, so workload_name is populated for both
// OTel and k8s data and matches the sample graphql payload
// (`workload_name: {_in: ["llm-server"]}`).
func buildQueryRequestFromSimpleFilters(params map[string]any) service.TraceQueryBuilder {
	qr := service.TraceQueryBuilder{
		Where: service.QueryWhereClause{
			Binary: service.BinaryWhereClause{},
		},
	}

	// String-equality filters — UI param name == clickhouse column name.
	// Keep this list in sync with the simple-mode fields declared in
	// InputSchema below.
	stringEqFilters := []string{
		"workload_name",
		"workload_namespace",
		"destination_workload_name",
		"destination_workload_namespace",
		"span_kind",
		"trace_id",
		"trace_source",
		"http_status_code",
	}
	for _, col := range stringEqFilters {
		if v, ok := params[col].(string); ok && v != "" {
			qr.Where.Binary[col] = map[service.BinaryWhereClauseType]any{
				service.Eq: v,
			}
		}
	}

	// Pattern-match filters — span_name and resource commonly hold long
	// strings (HTTP URL / SQL statement), so _like is the natural default.
	stringLikeFilters := []string{"span_name", "resource"}
	for _, col := range stringLikeFilters {
		if v, ok := params[col].(string); ok && v != "" {
			qr.Where.Binary[col] = map[service.BinaryWhereClauseType]any{
				service.Like: v,
			}
		}
	}

	// Status code: UI offers short aliases (error / ok / unset) that expand
	// to the STATUS_CODE_* constants clickhouse stores. Any other string
	// falls through as-is for forward-compat.
	if status, ok := params["status"].(string); ok && status != "" {
		statusCode := ""
		switch status {
		case "error":
			statusCode = "STATUS_CODE_ERROR"
		case "ok":
			statusCode = "STATUS_CODE_OK"
		case "unset":
			statusCode = "STATUS_CODE_UNSET"
		default:
			statusCode = status
		}
		qr.Where.Binary["status_code"] = map[service.BinaryWhereClauseType]any{
			service.Eq: statusCode,
		}
	}

	// Duration: user-facing milliseconds, backend column is nanoseconds.
	minDurationMs := parseNumericParam(params["min_duration_ms"])
	if minDurationMs > 0 {
		minDurationNs := minDurationMs * 1_000_000
		qr.Where.Binary["duration_ns"] = map[service.BinaryWhereClauseType]any{
			service.Gte: minDurationNs,
		}
	}

	return qr
}

// sortByToOrderBy converts a sort_by option string to QueryOrderBy slice.
// Column names match the ClickhouseTraceTableDefinition (duration_ns, not
// duration).
func sortByToOrderBy(sortBy string) []service.QueryOrderBy {
	switch sortBy {
	case "timestamp_desc":
		return []service.QueryOrderBy{{Column: "timestamp", Order: "desc"}}
	case "timestamp_asc":
		return []service.QueryOrderBy{{Column: "timestamp", Order: "asc"}}
	case "duration_desc":
		return []service.QueryOrderBy{{Column: "duration_ns", Order: "desc"}}
	case "duration_asc":
		return []service.QueryOrderBy{{Column: "duration_ns", Order: "asc"}}
	default:
		return nil
	}
}

func (t *TracesTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			// --- Provider config (top of form) ---
			"account_id": {
				Type:        types.PropertyTypeAccount,
				Description: "NB Account Id.",
				Required:    false,
				Title:       "Account",
				Order:       1,
			},
			// Synthetic hidden field. Not consumed by Execute — traces_task
			// delegates provider resolution to services-server — but its
			// presence in the schema activates the "Default trace provider"
			// chip effect in ActionDetailsSidebar (gated on
			// !!inputSchema.account_provider_type), matching the logs and
			// metrics forms.
			"account_provider_type": {
				Type:        types.PropertyTypeString,
				Description: "Internal: derived cloud provider type for the selected account.",
				Required:    false,
				Hidden:      true,
				Order:       2,
			},
			// Optional overrides. Free-text so callers can target any
			// provider value services-server understands — the fixed list of
			// known providers drifts over time. Leave blank to let
			// GetLogsMetricsTracesProvider auto-resolve from the selected
			// account's agent features.
			"trace_provider": {
				Type:        types.PropertyTypeString,
				Description: "Optional: trace provider override (e.g. signoz, jaeger, tempo, datadog, newrelic). Auto-detected from account if empty.",
				Required:    false,
				Title:       "Trace Provider",
				Order:       3,
			},
			"trace_provider_source": {
				Type:        types.PropertyTypeString,
				Description: "Optional: trace provider source override (e.g. agent, user). Auto-detected from account if empty.",
				Required:    false,
				Title:       "Provider Source",
				Order:       4,
			},

			// --- Query mode selector ---
			// Visible in the UI — users pick simple / text / structured to
			// switch between the simple-filter fields, a free-text query, or
			// the structured query_request JSON editor.
			"query_mode": {
				Type:        types.PropertyTypeString,
				Description: "How to specify the trace query.",
				Required:    false,
				Default:     "simple",
				Title:       "Query Mode",
				Options:     []string{"simple", "text", "structured"},
				Order:       5,
			},

			// --- Simple mode filters ---
			// Column names on the TraceQueryBuilder.Where side use the
			// snake_case fields from TracesOutputResponse / the clickhouse
			// table definition in api-server (workload_name, status_code,
			// span_name, duration_ns). workload_name is preferred over
			// service_name because the k8s-agent view falls back to
			// service.name when workload labels are absent, so
			// workload_name is populated for both k8s and OTel-only data.
			"workload_name": {
				Type:        types.PropertyTypeString,
				Description: "Filter traces by workload name (k8s deployment / OTel service.name fallback).",
				Required:    false,
				Title:       "Workload Name",
				Order:       6,
				DependsOn:   []string{"query_mode"},
				VisibleWhen: &types.VisibleWhen{Field: "query_mode", Value: []string{"simple"}},
			},
			"workload_namespace": {
				Type:        types.PropertyTypeString,
				Description: "Filter traces by workload namespace (k8s namespace).",
				Required:    false,
				Title:       "Workload Namespace",
				Order:       7,
				DependsOn:   []string{"query_mode"},
				VisibleWhen: &types.VisibleWhen{Field: "query_mode", Value: []string{"simple"}},
			},
			"status": {
				Type:        types.PropertyTypeString,
				Description: "Filter by span status.",
				Required:    false,
				Title:       "Status",
				Options:     []string{"error", "ok"},
				Order:       8,
				DependsOn:   []string{"query_mode"},
				VisibleWhen: &types.VisibleWhen{Field: "query_mode", Value: []string{"simple"}},
			},
			"min_duration_ms": {
				Type:        types.PropertyTypeNumber,
				Description: "Minimum span duration in milliseconds. Useful for finding slow traces.",
				Required:    false,
				Title:       "Min Duration (ms)",
				Order:       9,
				DependsOn:   []string{"query_mode"},
				VisibleWhen: &types.VisibleWhen{Field: "query_mode", Value: []string{"simple"}},
			},
			"span_name": {
				Type:        types.PropertyTypeString,
				Description: "Filter by span/operation name (pattern matching via _like).",
				Required:    false,
				Title:       "Span Name",
				Order:       10,
				DependsOn:   []string{"query_mode"},
				VisibleWhen: &types.VisibleWhen{Field: "query_mode", Value: []string{"simple"}},
			},
			"span_kind": {
				Type:        types.PropertyTypeString,
				Description: "Filter by OTel span kind (e.g. SERVER, CLIENT, INTERNAL, PRODUCER, CONSUMER).",
				Required:    false,
				Title:       "Span Kind",
				Order:       11,
				DependsOn:   []string{"query_mode"},
				VisibleWhen: &types.VisibleWhen{Field: "query_mode", Value: []string{"simple"}},
			},
			"resource": {
				Type:        types.PropertyTypeString,
				Description: "Filter by span resource (SQL statement / HTTP URL). Pattern matching via _like.",
				Required:    false,
				Title:       "Resource",
				Order:       12,
				DependsOn:   []string{"query_mode"},
				VisibleWhen: &types.VisibleWhen{Field: "query_mode", Value: []string{"simple"}},
			},
			"destination_workload_name": {
				Type:        types.PropertyTypeString,
				Description: "Filter by downstream workload name (callee of the span).",
				Required:    false,
				Title:       "Destination Workload Name",
				Order:       13,
				DependsOn:   []string{"query_mode"},
				VisibleWhen: &types.VisibleWhen{Field: "query_mode", Value: []string{"simple"}},
			},
			"destination_workload_namespace": {
				Type:        types.PropertyTypeString,
				Description: "Filter by downstream workload namespace.",
				Required:    false,
				Title:       "Destination Workload Namespace",
				Order:       14,
				DependsOn:   []string{"query_mode"},
				VisibleWhen: &types.VisibleWhen{Field: "query_mode", Value: []string{"simple"}},
			},
			"http_status_code": {
				Type:        types.PropertyTypeString,
				Description: "Filter by HTTP status code (stored as string — e.g. \"200\", \"500\").",
				Required:    false,
				Title:       "HTTP Status Code",
				Order:       15,
				DependsOn:   []string{"query_mode"},
				VisibleWhen: &types.VisibleWhen{Field: "query_mode", Value: []string{"simple"}},
			},
			"trace_id": {
				Type:        types.PropertyTypeString,
				Description: "Look up a specific trace by its trace ID.",
				Required:    false,
				Title:       "Trace ID",
				Order:       16,
				DependsOn:   []string{"query_mode"},
				VisibleWhen: &types.VisibleWhen{Field: "query_mode", Value: []string{"simple"}},
			},
			"trace_source": {
				Type:        types.PropertyTypeString,
				Description: "Filter by trace source — `otel` (instrumented SDK) or `ebpf` (nudgebee-node-agent).",
				Required:    false,
				Title:       "Trace Source",
				Options:     []string{"otel", "ebpf"},
				Order:       17,
				DependsOn:   []string{"query_mode"},
				VisibleWhen: &types.VisibleWhen{Field: "query_mode", Value: []string{"simple"}},
			},

			// --- Text mode ---
			"query": {
				Type:        types.PropertyTypeString,
				Description: "Traces text query (e.g. service.name = \"my-service\").",
				Required:    false,
				Title:       "Query",
				Order:       18,
				DependsOn:   []string{"query_mode"},
				VisibleWhen: &types.VisibleWhen{Field: "query_mode", Value: []string{"text"}},
			},

			// --- Structured mode ---
			// Default seeds the JSON editor with a realistic sample when the
			// user flips into structured mode. It is SAFE to set here only
			// because Execute() branches on query_mode (not on the presence
			// of params["query_request"]) — so query_mode == "simple" ignores
			// whatever the JSON editor holds. See the Execute comment above
			// the query_mode discriminator.
			"query_request": {
				Type:        types.PropertyTypeObject,
				Description: "Structured query builder with where, order_by, limit, offset.",
				Help: "`where.binary` keys are clickhouse column names from the traces table.\n\n" +
					"**Operators**\n\n" +
					"`_eq`, `_neq`, `_in`, `_nin`, `_like`, `_ilike`, `_gt`, `_gte`, `_lt`, `_lte`\n\n" +
					"**String columns**\n\n" +
					"- `workload_name`, `workload_namespace`\n" +
					"- `destination_workload_name`, `destination_workload_namespace`\n" +
					"- `service_name`\n" +
					"- `span_name`, `span_kind` — `SERVER` / `CLIENT` / `INTERNAL` / `PRODUCER` / `CONSUMER`\n" +
					"- `resource` — SQL statement / HTTP URL\n" +
					"- `status_code` — `STATUS_CODE_OK` / `STATUS_CODE_ERROR` / `STATUS_CODE_UNSET`\n" +
					"- `http_status_code` — stored as string (e.g. `\"200\"`, `\"500\"`)\n" +
					"- `trace_id`, `span_id`\n" +
					"- `trace_source` — `otel` (instrumented SDK) / `ebpf` (node-agent)\n\n" +
					"**Numeric columns**\n\n" +
					"- `duration_ns` — nanoseconds (1 ms = 1_000_000)\n" +
					"- `timestamp`\n\n" +
					"**`order_by`**\n\n" +
					"Any column above with `order`: `asc` / `desc`.\n\n" +
					"**Example**\n\n" +
					"```json\n" +
					"{\n" +
					"  \"where\": {\n" +
					"    \"binary\": {\n" +
					"      \"workload_name\": {\"_eq\": \"llm-server\"},\n" +
					"      \"status_code\": {\"_eq\": \"STATUS_CODE_ERROR\"},\n" +
					"      \"duration_ns\": {\"_gte\": 100000000}\n" +
					"    }\n" +
					"  },\n" +
					"  \"order_by\": [{\"column\": \"timestamp\", \"order\": \"desc\"}],\n" +
					"  \"limit\": 100,\n" +
					"  \"offset\": 0\n" +
					"}\n" +
					"```",
				Required: false,
				Title:    "Query Request",
				SubType:  "json",
				Default: map[string]any{
					"where": map[string]any{
						"binary": map[string]any{
							"workload_name": map[string]any{"_eq": "llm-server"},
							"status_code":   map[string]any{"_eq": "STATUS_CODE_ERROR"},
							"duration_ns":   map[string]any{"_gte": 100000000},
						},
					},
					"order_by": []map[string]any{
						{"column": "timestamp", "order": "desc"},
					},
					"limit":  100,
					"offset": 0,
				},
				Order:       19,
				DependsOn:   []string{"query_mode"},
				VisibleWhen: &types.VisibleWhen{Field: "query_mode", Value: []string{"structured"}},
			},

			// --- Time range ---
			"duration": {
				Type:        types.PropertyTypeString,
				Description: "Relative lookback window. Overrides start_time/end_time when set.",
				Required:    false,
				Title:       "Relative Range",
				Default:     "1h",
				Options:     []string{"5m", "15m", "30m", "1h", "3h", "6h", "12h", "24h"},
				Order:       20,
			},
			"start_time": {
				Type:        types.PropertyTypeTimestamp,
				Description: "Absolute start time. Ignored if duration is set.",
				Required:    false,
				Title:       "Start Time",
				Order:       21,
			},
			"end_time": {
				Type:        types.PropertyTypeTimestamp,
				Description: "Absolute end time. Ignored if duration is set.",
				Required:    false,
				Title:       "End Time",
				Order:       22,
			},

			// --- Pagination & sorting ---
			"sort_by": {
				Type:        types.PropertyTypeString,
				Description: "Sort order for results.",
				Required:    false,
				Title:       "Sort By",
				Default:     "timestamp_desc",
				Options:     []string{"timestamp_desc", "timestamp_asc", "duration_desc", "duration_asc"},
				Order:       23,
			},
			"limit": {
				Type:        types.PropertyTypeNumber,
				Description: "Maximum number of traces to return.",
				Required:    false,
				Default:     100,
				Title:       "Limit",
				Order:       24,
			},
			"offset": {
				Type:        types.PropertyTypeNumber,
				Description: "Offset for pagination.",
				Required:    false,
				Default:     0,
				Title:       "Offset",
				Order:       25,
			},
		},
	}
}

func (t *TracesTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"traces": {
				Type:        types.PropertyTypeArray,
				Description: "The output of Traces Query.",
				Required:    true,
			},
			"metadata": {
				Type:        types.PropertyTypeObject,
				Description: "Metadata for Traces Query.",
				Required:    true,
			},
		},
	}
}
