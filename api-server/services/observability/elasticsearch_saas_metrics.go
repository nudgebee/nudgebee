package observability

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"sort"
	"strings"
	"time"
)

// ElasticSaasMetricSource implements MetricSource for user-managed OpenSearch/Elasticsearch.
type ElasticSaasMetricSource struct{}

func (e *ElasticSaasMetricSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_contains", "_in", "_not_in", "_like", "_nlike", "_gt", "_lt", "_is_null"}
}

func (e *ElasticSaasMetricSource) GetQuery(_ *security.RequestContext, req FetchMetricsRequest) (string, error) {
	for _, q := range req.Queries {
		return q, nil
	}
	return "", nil
}

func (e *ElasticSaasMetricSource) FetchMetricsQuery(ctx *security.RequestContext, req FetchMetricsRequest) (OutputMetricQuery, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return OutputMetricQuery{}, err
	}

	index := ""
	if req.Request != nil {
		index, _ = req.Request["metric_name"].(string)
	}
	if index == "" {
		return OutputMetricQuery{}, fmt.Errorf("index is required for Elasticsearch metrics query")
	}

	var results []QueryResult

	// Code Mode in the metrics query panel sends raw Elasticsearch DSL as the
	// query string and signals it via request.query_type = "dsl"; Builder Mode
	// sends the internal []QueryWhereClause format with no query_type. Keep
	// both paths working here so users can switch modes without the backend
	// caring about which generator produced the payload.
	queryType, _ := req.Request["query_type"].(string)

	for queryKey, queryDSL := range req.Queries {
		var (
			queryBody map[string]any
			buildErr  *string
		)

		if queryType == "dsl" {
			// Parse the raw ES body exactly as the user typed it, default the
			// `size` field if omitted, and if the caller supplied a time range
			// AND the body into a bool filter so scans are still bounded.
			var userBody map[string]any
			if err := json.Unmarshal([]byte(queryDSL), &userBody); err != nil {
				errStr := fmt.Sprintf("failed to parse DSL query body: %v", err)
				buildErr = &errStr
			} else if userBody == nil {
				// json.Unmarshal leaves userBody nil when the input is literal
				// "null" or empty. Reject up front — the follow-on map writes
				// would panic on nil, and a null body is not a valid _search.
				errStr := "DSL query body must be a JSON object, got null"
				buildErr = &errStr
			} else {
				if _, ok := userBody["size"]; !ok {
					userBody["size"] = 10000
				}
				if req.StartTime > 0 && req.EndTime > 0 {
					userQuery, ok := userBody["query"].(map[string]any)
					if !ok {
						userQuery = map[string]any{"match_all": map[string]any{}}
					}
					userBody["query"] = map[string]any{
						"bool": map[string]any{
							"filter": []any{userQuery, esMetricsTimeRangeClause(req.StartTime, req.EndTime)},
						},
					}
				}
				queryBody = userBody
			}
		} else {
			// Parse the internal filter format (array of QueryWhereClause) into flat ES filter clauses
			var whereClauses []query.QueryWhereClause
			if err := json.Unmarshal([]byte(queryDSL), &whereClauses); err != nil {
				errStr := fmt.Sprintf("failed to parse query filters: %v", err)
				buildErr = &errStr
			} else {
				var filters []any
				for _, wc := range whereClauses {
					clause, err := whereToBool(normalizeESMetricsWhere(wc))
					if err != nil {
						errStr := fmt.Sprintf("failed to build ES clause: %v", err)
						buildErr = &errStr
						break
					}
					filters = append(filters, clause)
				}
				// Add time range filter — try both "time" and "@timestamp" fields
				// since ES metric indices may use either depending on the ingestion pipeline.
				if buildErr == nil && req.StartTime > 0 && req.EndTime > 0 {
					filters = append(filters, esMetricsTimeRangeClause(req.StartTime, req.EndTime))
				}
				if buildErr == nil {
					queryBody = map[string]any{
						"size": 10000,
						"query": map[string]any{
							"bool": map[string]any{
								"filter": filters,
							},
						},
					}
				}
			}
		}

		if buildErr != nil {
			results = append(results, QueryResult{
				QueryKey: queryKey,
				Query:    queryDSL,
				Error:    buildErr,
			})
			continue
		}

		esURL := fmt.Sprintf("%s/%s/_search", cfg.Url, index)
		debugJSON, _ := json.Marshal(queryBody)
		slog.Info("ES metrics query debug", "url", esURL, "body", string(debugJSON))

		resp, err := esRequestJSON("POST", esURL, queryBody, cfg)
		if err != nil {
			errStr := fmt.Sprintf("failed to query metric: %v", err)
			results = append(results, QueryResult{
				QueryKey: queryKey,
				Query:    queryDSL,
				Error:    &errStr,
			})
			continue
		}

		bodyBytes, err := readResponse(resp, "metric query")
		if err != nil {
			errStr := err.Error()
			results = append(results, QueryResult{
				QueryKey: queryKey,
				Query:    queryDSL,
				Error:    &errStr,
			})
			continue
		}

		payload, err := parseESMetricsHits(bodyBytes)
		if err != nil {
			errStr := fmt.Sprintf("failed to parse ES metrics response: %v", err)
			results = append(results, QueryResult{
				QueryKey: queryKey,
				Query:    queryDSL,
				Error:    &errStr,
			})
			continue
		}

		results = append(results, QueryResult{
			QueryKey: queryKey,
			Query:    queryDSL,
			Payload:  payload,
		})
	}

	return OutputMetricQuery{Results: results}, nil
}

// esMetricsTimeRangeClause returns a bool/should clause that matches documents
// whose `time` OR `@timestamp` field falls inside [start, end] epoch_millis —
// ES metric indices use one or the other depending on the ingestion pipeline.
func esMetricsTimeRangeClause(startMillis, endMillis int64) map[string]any {
	timeRangeVal := map[string]any{
		"gte":    startMillis,
		"lte":    endMillis,
		"format": "epoch_millis",
	}
	return map[string]any{
		"bool": map[string]any{
			"should": []any{
				map[string]any{"range": map[string]any{"time": timeRangeVal}},
				map[string]any{"range": map[string]any{"@timestamp": timeRangeVal}},
			},
			"minimum_should_match": 1,
		},
	}
}

func (e *ElasticSaasMetricSource) FetchMetricList(ctx *security.RequestContext, req FetchMetricsListRequest) ([]OutputMetrics, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}

	resp, err := esRequest("GET", fmt.Sprintf("%s/_cat/indices?format=json", cfg.Url), "", cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to query metric list: %w", err)
	}

	bodyBytes, err := readResponse(resp, "metric list")
	if err != nil {
		return nil, err
	}

	var indices []map[string]any
	if err := json.Unmarshal(bodyBytes, &indices); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metric list response: %w", err)
	}

	var output []OutputMetrics
	for _, idx := range indices {
		if indexName, ok := idx["index"].(string); ok && indexName != "" {
			output = append(output, OutputMetrics{
				Metric:     indexName,
				Attributes: map[string]any{},
			})
		}
	}

	return output, nil
}

func (e *ElasticSaasMetricSource) FetchMetricLabelValues(ctx *security.RequestContext, req FetchMetricsLabelValueRequest) ([]OutputMetricsLabelValues, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}

	index := ""
	if req.Request != nil {
		index, _ = req.Request["metric_name"].(string)
	}
	if index == "" {
		return nil, fmt.Errorf("index is required for Elasticsearch metric label values query")
	}

	// Try .keyword suffix first for text fields, fall back to original field name.
	labelField := req.Label
	if !strings.HasSuffix(labelField, ".keyword") {
		labelField = labelField + ".keyword"
	}

	buildDSL := func(field string) map[string]any {
		return map[string]any{
			"size": 0,
			"aggs": map[string]any{
				"label_values": map[string]any{
					"terms": map[string]any{
						"field": field,
						"size":  1000,
					},
				},
			},
		}
	}

	resp, err := esRequestJSON("POST", fmt.Sprintf("%s/%s/_search", cfg.Url, index), buildDSL(labelField), cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to query metric label values: %w", err)
	}

	bodyBytes, err := readResponse(resp, "metric label values")
	if err != nil {
		// If .keyword field doesn't exist, retry with original field name
		if labelField != req.Label {
			resp, err = esRequestJSON("POST", fmt.Sprintf("%s/%s/_search", cfg.Url, index), buildDSL(req.Label), cfg)
			if err != nil {
				return nil, fmt.Errorf("failed to query metric label values: %w", err)
			}
			bodyBytes, err = readResponse(resp, "metric label values")
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	var searchResp esTraceSearchResponse
	if err := json.Unmarshal(bodyBytes, &searchResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metric label values response: %w", err)
	}

	var output []OutputMetricsLabelValues
	if raw, ok := searchResp.Aggregations["label_values"]; ok {
		var termsAgg struct {
			Buckets []struct {
				Key      string `json:"key"`
				DocCount int    `json:"doc_count"`
			} `json:"buckets"`
		}
		if err := json.Unmarshal(raw, &termsAgg); err == nil {
			for _, bucket := range termsAgg.Buckets {
				output = append(output, OutputMetricsLabelValues{
					Value:      bucket.Key,
					Attributes: map[string]any{},
				})
			}
		}
	}

	return output, nil
}

func (e *ElasticSaasMetricSource) FetchMetricsLabels(ctx *security.RequestContext, req FetchMetricLabelsRequest) ([]OutputMetricLabels, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}

	index := req.MetricName
	if index == "" {
		return nil, fmt.Errorf("index is required for Elasticsearch metrics labels query")
	}

	// Fetch field names from the index mapping.
	resp, err := esRequest("GET", fmt.Sprintf("%s/%s/_mapping", cfg.Url, index), "", cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to query metrics labels: %w", err)
	}

	bodyBytes, err := readResponse(resp, "metrics labels")
	if err != nil {
		return nil, err
	}

	var mappingResp map[string]any
	if err := json.Unmarshal(bodyBytes, &mappingResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metrics labels response: %w", err)
	}

	var output []OutputMetricLabels
	for _, indexData := range mappingResp {
		indexMap, ok := indexData.(map[string]any)
		if !ok {
			continue
		}
		mappings, ok := indexMap["mappings"].(map[string]any)
		if !ok {
			continue
		}
		properties, ok := mappings["properties"].(map[string]any)
		if !ok {
			continue
		}
		fields := extractFieldsFromProperties(properties, "")
		for _, f := range fields {
			output = append(output, OutputMetricLabels{
				Label:      f.Field,
				Attributes: map[string]any{},
			})
		}
		break
	}

	return output, nil
}

// normalizeESMetricsWhere rewrites string equality field names to use the .keyword
// subfield. Metrics index fields are mapped as text (analyzed) with a .keyword subfield
// for exact match. Without this, term/terms queries on bare field names return 0 hits.
func normalizeESMetricsWhere(wc query.QueryWhereClause) query.QueryWhereClause {
	out := query.QueryWhereClause{}
	if wc.Binary != nil {
		out.Binary = query.BinaryWhereClause{}
		for field, ops := range wc.Binary {
			newField := field
			if !strings.HasSuffix(field, ".keyword") {
				for op, val := range ops {
					if op == query.Eq || op == query.Nq || op == query.In || op == query.NotIn {
						if _, isString := val.(string); isString {
							newField = field + ".keyword"
							break
						}
						if arr, isArr := val.([]any); isArr && len(arr) > 0 {
							if _, isString := arr[0].(string); isString {
								newField = field + ".keyword"
								break
							}
						}
					}
				}
			}
			out.Binary[newField] = ops
		}
	}
	for _, sub := range wc.And {
		out.And = append(out.And, normalizeESMetricsWhere(sub))
	}
	for _, sub := range wc.Or {
		out.Or = append(out.Or, normalizeESMetricsWhere(sub))
	}
	if wc.Not != nil {
		sub := normalizeESMetricsWhere(*wc.Not)
		out.Not = &sub
	}
	return out
}

// parseESMetricsHits parses an ES search response into []Result grouped by label set.
// Each unique combination of metric name + attributes becomes one Result with
// collected timestamps (epoch seconds) and values.
func parseESMetricsHits(bodyBytes []byte) ([]Result, error) {
	var esResp struct {
		Hits struct {
			Hits []struct {
				Source map[string]any `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(bodyBytes, &esResp); err != nil {
		return nil, err
	}

	type seriesData struct {
		metric     map[string]string
		timestamps []int64
		values     []float64
	}

	groups := make(map[string]*seriesData)
	var groupOrder []string

	for _, hit := range esResp.Hits.Hits {
		src := hit.Source

		name, _ := src["name"].(string)
		timeStr, _ := src["time"].(string)
		if timeStr == "" {
			timeStr, _ = src["@timestamp"].(string)
		}

		// Extract metric value: prefer value, then sum, then count
		var val float64
		if v, ok := src["value"].(float64); ok {
			val = v
		} else if v, ok := src["sum"].(float64); ok {
			val = v
		} else if v, ok := src["count"].(float64); ok {
			val = v
		}

		// Parse ISO timestamp to epoch seconds
		t, err := time.Parse(time.RFC3339Nano, timeStr)
		if err != nil {
			continue
		}
		ts := t.Unix()

		// Build label map from name + attributes
		labels := map[string]string{}
		if name != "" {
			labels["__name__"] = name
		}
		if attrs, ok := src["attributes"].(map[string]any); ok {
			for k, v := range attrs {
				labels[k] = fmt.Sprintf("%v", v)
			}
		}

		// Group by unique label set
		keyBytes, _ := json.Marshal(labels)
		key := string(keyBytes)

		if _, exists := groups[key]; !exists {
			groups[key] = &seriesData{metric: labels}
			groupOrder = append(groupOrder, key)
		}
		groups[key].timestamps = append(groups[key].timestamps, ts)
		groups[key].values = append(groups[key].values, val)
	}

	results := make([]Result, 0, len(groups))
	for _, key := range groupOrder {
		g := groups[key]
		// Co-sort timestamps and values together
		indices := make([]int, len(g.timestamps))
		for i := range indices {
			indices[i] = i
		}
		sort.Slice(indices, func(i, j int) bool {
			return g.timestamps[indices[i]] < g.timestamps[indices[j]]
		})
		sortedTs := make([]int64, len(indices))
		sortedVals := make([]float64, len(indices))
		for i, idx := range indices {
			sortedTs[i] = g.timestamps[idx]
			sortedVals[i] = g.values[idx]
		}
		results = append(results, Result{
			Metric:     g.metric,
			Timestamps: sortedTs,
			Values:     sortedVals,
		})
	}

	return results, nil
}
