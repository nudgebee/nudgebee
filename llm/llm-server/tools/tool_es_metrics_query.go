package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools/core"
	"strconv"
	"time"

	"github.com/google/uuid"
)

const ToolESMetricsQuery = "es_metrics_query"

func init() {
	core.RegisterNBToolFactory(ToolESMetricsQuery, func(accountId string) (core.NBTool, error) {
		return ESMetricsQueryTool{}, nil
	})
}

type ESMetricsQueryTool struct{}

func (m ESMetricsQueryTool) Name() string {
	return ToolESMetricsQuery
}

func (m ESMetricsQueryTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m ESMetricsQueryTool) Description() string {
	return `Executes an Elasticsearch/Opensearch DSL query against metric indices via the metrics API.

	Usage:
	* Input: Provide a JSON object with:
	  - "index": (required) the Elasticsearch index pattern to query (e.g. "metrics-*", "metricbeat-*")
	  - "query": (required) the Elasticsearch DSL query body as a JSON object with "query" filters.
	* Output: Returns metric time-series results (timestamps and values) extracted from matching document hits.

	Important Notes:
	* Always include a range filter on @timestamp in your query (default: now-6h).
	* Do NOT use "aggs" or "size: 0" — the backend extracts time-series from document hits, not aggregation results.
	* The query is executed via the metrics_query API which handles Elasticsearch communication.`
}

func (m ESMetricsQueryTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"index": {
				Type:        core.ToolSchemaTypeString,
				Description: "The Elasticsearch index pattern to query (e.g. 'metrics-*', 'metricbeat-*').",
			},
			"query": {
				Type:        core.ToolSchemaTypeObject,
				Description: "The Elasticsearch DSL query body (JSON object with query, aggs, size, etc.).",
			},
		},
		Required: []string{"index", "query"},
	}
}

func (m ESMetricsQueryTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("es_metrics_query: executing metrics query", "input", input.Command)

	// Parse the input to extract index and query.
	var inputObj map[string]any
	if err := common.UnmarshalJson([]byte(input.Command), &inputObj); err != nil {
		return core.NBToolResponse{
			Data:   "Invalid input format. Provide a JSON object with 'index' and 'query' fields.",
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("es_metrics_query: invalid input JSON: %w", err)
	}

	indexPattern, _ := inputObj["index"].(string)
	if indexPattern == "" {
		return core.NBToolResponse{
			Data:   "Missing required 'index' field. Provide an Elasticsearch index pattern (e.g. 'metrics-*').",
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("es_metrics_query: missing index field")
	}

	queryObj, ok := inputObj["query"]
	if !ok {
		return core.NBToolResponse{
			Data:   "Missing required 'query' field. Provide the Elasticsearch DSL query body.",
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("es_metrics_query: missing query field")
	}

	// Serialize the query object to a JSON string for the queries map.
	queryBytes, err := common.MarshalJson(queryObj)
	if err != nil {
		return core.NBToolResponse{
			Data:   "Failed to serialize query object.",
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("es_metrics_query: failed to marshal query: %w", err)
	}

	// Determine API-level time range. The api-server uses StartTime/EndTime to select
	// date-partitioned indices, so this must be at least as wide as the DSL's @timestamp filter.
	// Parse the DSL query's @timestamp range; fall back to 7 days if not found.
	endTime := time.Now()
	startTime := endTime.Add(-7 * 24 * time.Hour)
	if dslStart := extractTimestampFromDSL(queryObj); !dslStart.IsZero() {
		startTime = dslStart
	}
	t1, t2, err := ExtractStartEndtimeFromLabels(nbRequestContext, input.Arguments)
	if err == nil {
		startTime = t1
		endTime = t2
	}
	startTime, endTime = ExpandNarrowTimeWindow(nbRequestContext.Ctx.GetLogger(), startTime, endTime)

	queryKey := uuid.New().String()
	req := core.ObservabilityMetricsQueryRequest{
		AccountId:      nbRequestContext.AccountId,
		MetricProvider: "ES",
		Queries: map[string]string{
			queryKey: string(queryBytes),
		},
		StartTime: startTime.UnixMilli(),
		EndTime:   endTime.UnixMilli(),
		Request: map[string]any{
			"query_type":  "dsl",
			"metric_name": indexPattern,
		},
	}

	response, err := services_server.QueryMetrics(*nbRequestContext.Ctx, req)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("es_metrics_query: API call failed", "error", err.Error())
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Metrics query failed: %s", err.Error()),
			Status: core.NBToolResponseStatusError,
		}, err
	}

	// Compute stats and cap series to manage token usage, mirroring the Prometheus tool pattern.
	summarized := summarizeESMetricsResponse(response)

	data, err := common.MarshalJson(summarized)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("es_metrics_query: unable to serialize response", "error", err.Error())
		return core.NBToolResponse{
			Data:   "",
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("es_metrics_query: failed to marshal response: %w", err)
	}

	return core.NBToolResponse{
		Data:   string(data),
		Type:   core.NBToolResponseTypeJson,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

// maxESMetricsSeries is the maximum number of series to return per query result.
// Beyond this, only stats are useful — raw values would blow the context window.
const maxESMetricsSeries = 10

// maxESMetricsDataPoints caps values/timestamps per series. When exceeded, values
// are sub-sampled and stats are appended for accuracy.
const maxESMetricsDataPoints = 50

// summarizeESMetricsResponse computes per-series stats (min/max/avg), caps the number
// of series, and sub-samples large value arrays. This happens at the tool level so the
// LLM always receives a manageable response regardless of query breadth.
func summarizeESMetricsResponse(resp core.ObservabilityMetricsQueryResponse) map[string]any {
	type seriesSummary struct {
		Metric     map[string]string `json:"metric"`
		Timestamps []int64           `json:"timestamps"`
		Values     []float64         `json:"values"`
		Stats      map[string]any    `json:"stats"`
		NumPoints  int               `json:"num_points"`
	}

	type resultSummary struct {
		QueryKey     string          `json:"query_key"`
		Query        string          `json:"query"`
		Payload      []seriesSummary `json:"payload"`
		TotalSeries  int             `json:"total_series"`
		Error        *string         `json:"error,omitempty"`
		CappedNotice string          `json:"capped_notice,omitempty"`
	}

	out := map[string]any{}
	results := make([]resultSummary, 0, len(resp.Results))

	for _, r := range resp.Results {
		rs := resultSummary{
			QueryKey:    r.QueryKey,
			Query:       r.Query,
			TotalSeries: len(r.Payload),
			Error:       r.Error,
		}

		series := r.Payload
		if len(series) > maxESMetricsSeries {
			rs.CappedNotice = fmt.Sprintf("Showing %d of %d series. Use more specific filters to narrow results.", maxESMetricsSeries, len(series))
			series = series[:maxESMetricsSeries]
		}

		payload := make([]seriesSummary, 0, len(series))
		for _, s := range series {
			ss := seriesSummary{
				Metric:    s.Metric,
				NumPoints: len(s.Values),
			}

			// Compute stats from values.
			if len(s.Values) > 0 {
				minVal, maxVal, sum := s.Values[0], s.Values[0], 0.0
				for _, v := range s.Values {
					if v < minVal {
						minVal = v
					}
					if v > maxVal {
						maxVal = v
					}
					sum += v
				}
				ss.Stats = map[string]any{
					"min": minVal,
					"max": maxVal,
					"avg": sum / float64(len(s.Values)),
				}
			}

			// Sub-sample if too many data points.
			if len(s.Values) <= maxESMetricsDataPoints {
				ss.Values = s.Values
				ss.Timestamps = s.Timestamps
			} else {
				step := max(len(s.Values)/maxESMetricsDataPoints, 1)
				sampledValues := make([]float64, 0, maxESMetricsDataPoints+1)
				sampledTimestamps := make([]int64, 0, maxESMetricsDataPoints+1)
				for i := 0; i < len(s.Values); i += step {
					sampledValues = append(sampledValues, s.Values[i])
					if i < len(s.Timestamps) {
						sampledTimestamps = append(sampledTimestamps, s.Timestamps[i])
					}
				}
				// Ensure last point is included.
				if (len(s.Values)-1)%step != 0 {
					sampledValues = append(sampledValues, s.Values[len(s.Values)-1])
					if len(s.Timestamps) > 0 {
						sampledTimestamps = append(sampledTimestamps, s.Timestamps[len(s.Timestamps)-1])
					}
				}
				ss.Values = sampledValues
				ss.Timestamps = sampledTimestamps
			}

			payload = append(payload, ss)
		}
		rs.Payload = payload
		results = append(results, rs)
	}

	out["results"] = results
	return out
}

// extractTimestampFromDSL parses the DSL query object to find the @timestamp range filter's
// "gte" value (e.g. "now-6h", "now-7d", or an ISO timestamp). Returns the resolved time
// or zero time if not found. This ensures the API-level StartTime covers the DSL's time range.
func extractTimestampFromDSL(queryObj any) time.Time {
	q, ok := queryObj.(map[string]any)
	if !ok {
		return time.Time{}
	}

	// Traverse: query.bool.filter[] looking for range.@timestamp.gte
	queryInner, _ := q["query"].(map[string]any)
	if queryInner == nil {
		return time.Time{}
	}
	boolQuery, _ := queryInner["bool"].(map[string]any)
	if boolQuery == nil {
		return time.Time{}
	}
	filters, _ := boolQuery["filter"].([]any)
	if filters == nil {
		return time.Time{}
	}

	for _, f := range filters {
		fm, ok := f.(map[string]any)
		if !ok {
			continue
		}
		rangeObj, ok := fm["range"].(map[string]any)
		if !ok {
			continue
		}
		// Check both "time" and "@timestamp" — different ES schemas use different field names.
		tsObj, ok := rangeObj["time"].(map[string]any)
		if !ok {
			tsObj, ok = rangeObj["@timestamp"].(map[string]any)
		}
		if !ok {
			continue
		}
		gteVal, ok := tsObj["gte"].(string)
		if !ok || gteVal == "" {
			continue
		}
		return resolveESTimeExpression(gteVal)
	}
	return time.Time{}
}

// resolveESTimeExpression converts ES time expressions like "now-6h", "now-3d" to absolute time.
func resolveESTimeExpression(expr string) time.Time {
	now := time.Now()

	// Try ISO format first.
	if t, err := time.Parse(time.RFC3339, expr); err == nil {
		return t
	}

	// Parse "now-Xunit" format.
	if len(expr) < 5 || expr[:4] != "now-" {
		return time.Time{}
	}
	rest := expr[4:]
	if len(rest) < 2 {
		return time.Time{}
	}

	unit := rest[len(rest)-1]
	numStr := rest[:len(rest)-1]
	num, err := strconv.Atoi(numStr)
	if err != nil || num == 0 {
		return time.Time{}
	}

	switch unit {
	case 's':
		return now.Add(-time.Duration(num) * time.Second)
	case 'm':
		return now.Add(-time.Duration(num) * time.Minute)
	case 'h':
		return now.Add(-time.Duration(num) * time.Hour)
	case 'd':
		return now.Add(-time.Duration(num) * 24 * time.Hour)
	case 'w':
		return now.Add(-time.Duration(num) * 7 * 24 * time.Hour)
	default:
		return time.Time{}
	}
}
