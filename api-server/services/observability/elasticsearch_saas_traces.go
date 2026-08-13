package observability

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"strings"
)

// ElasticSaasTraceSource implements TraceSource for user-managed OpenSearch/Elasticsearch
// with trace data stored in Data Prepper's otel-v1-apm-span-* index format.
type ElasticSaasTraceSource struct{}

const esTraceIndex = "otel-v1-apm-span-*"

// esTraceIndexFor resolves the search target for trace queries in priority order:
// an explicit per-request override (the index picked in the Traces tab) → the
// integration's per-account trace_index (already resolved into cfg.TraceIndex by
// GetElasticsearchConfig) → Data Prepper's default. The field mappings in
// elasticTraceLabelMapping are Data Prepper's, so an override must point at an
// index carrying that schema (e.g. a renamed otel-v1-apm-span-*); an OTel trace
// data stream would resolve but match nothing.
func esTraceIndexFor(cfg *ElasticsearchConfig, override string) string {
	if override != "" {
		return override
	}
	if cfg.TraceIndex != "" {
		return cfg.TraceIndex
	}
	return esTraceIndex
}

// traceIndexOverride reads an explicit index from a trace request's free-form
// Request map (set by the Traces tab index picker). Returns "" when absent.
func traceIndexOverride(request map[string]any) string {
	if request == nil {
		return ""
	}
	idx, _ := request["index"].(string)
	return strings.TrimSpace(idx)
}

// elasticTraceLabelMapping maps frontend field names to Data Prepper OpenSearch field names.
var elasticTraceLabelMapping = map[string]string{
	"service_name":                   "serviceName",
	"span_name":                      "name",
	"trace_id":                       "traceId",
	"span_id":                        "spanId",
	"parent_span_id":                 "parentSpanId",
	"status_code":                    "status.code",
	"duration_ns":                    "durationInNanos",
	"span_kind":                      "kind",
	"http_status_code":               "span.attributes.http@status_code",
	"workload_name":                  "resource.attributes.k8s@deployment@name",
	"workload_namespace":             "resource.attributes.k8s@namespace@name",
	"resource":                       "span.attributes.http@url",
	"destination_workload_name":      "span.attributes.net@peer@name",
	"destination_workload_namespace": "span.attributes.destination@namespace",
}

func (e *ElasticSaasTraceSource) GetLabelMapping() map[string]string {
	return elasticTraceLabelMapping
}

func (e *ElasticSaasTraceSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_contains", "_like", "_nlike", "_gt", "_lt", "_is_null"}
}

// normalizeDataPrepperKey converts Data Prepper's @ separator back to dots for OTel convention.
func normalizeDataPrepperKey(key string) string {
	return strings.ReplaceAll(key, "@", ".")
}

// mapLabelToESField maps a frontend label name to the corresponding OpenSearch field name.
func mapLabelToESField(label string, mapping map[string]string) string {
	if mapped, ok := mapping[label]; ok {
		return mapped
	}
	return label
}

// buildESBoolQuery converts a QueryWhereClause into an OpenSearch DSL bool query.
func buildESBoolQuery(where query.QueryWhereClause, labelMapping map[string]string) map[string]any {
	boolQuery := map[string]any{}

	// Process binary conditions
	if len(where.Binary) > 0 {
		var filters []map[string]any
		var mustNot []map[string]any

		for col, ops := range where.Binary {
			esField := mapLabelToESField(col, labelMapping)
			for op, val := range ops {
				switch op {
				case query.Eq:
					filters = append(filters, map[string]any{
						"term": map[string]any{esField: val},
					})
				case query.Nq:
					mustNot = append(mustNot, map[string]any{
						"term": map[string]any{esField: val},
					})
				case query.Gt:
					filters = append(filters, map[string]any{
						"range": map[string]any{esField: map[string]any{"gt": val}},
					})
				case query.Gte:
					filters = append(filters, map[string]any{
						"range": map[string]any{esField: map[string]any{"gte": val}},
					})
				case query.Lt:
					filters = append(filters, map[string]any{
						"range": map[string]any{esField: map[string]any{"lt": val}},
					})
				case query.Lte:
					filters = append(filters, map[string]any{
						"range": map[string]any{esField: map[string]any{"lte": val}},
					})
				case query.In:
					filters = append(filters, map[string]any{
						"terms": map[string]any{esField: val},
					})
				case query.NotIn:
					mustNot = append(mustNot, map[string]any{
						"terms": map[string]any{esField: val},
					})
				case query.Like:
					filters = append(filters, map[string]any{
						"wildcard": map[string]any{esField: map[string]any{"value": val}},
					})
				case query.ILike:
					filters = append(filters, map[string]any{
						"wildcard": map[string]any{esField: map[string]any{"value": val, "case_insensitive": true}},
					})
				case query.NLike:
					mustNot = append(mustNot, map[string]any{
						"wildcard": map[string]any{esField: map[string]any{"value": val}},
					})
				case query.Contains:
					filters = append(filters, map[string]any{
						"match_phrase": map[string]any{esField: val},
					})
				case query.IsNull:
					boolVal, _ := val.(bool)
					if boolVal {
						mustNot = append(mustNot, map[string]any{
							"exists": map[string]any{"field": esField},
						})
					} else {
						filters = append(filters, map[string]any{
							"exists": map[string]any{"field": esField},
						})
					}
				case query.Between:
					if betweenMap, ok := val.(map[string]any); ok {
						rangeFilter := map[string]any{}
						if gte, exists := betweenMap["_gte"]; exists {
							rangeFilter["gte"] = gte
						}
						if lte, exists := betweenMap["_lte"]; exists {
							rangeFilter["lte"] = lte
						}
						filters = append(filters, map[string]any{
							"range": map[string]any{esField: rangeFilter},
						})
					}
				}
			}
		}

		if len(filters) > 0 {
			boolQuery["filter"] = filters
		}
		if len(mustNot) > 0 {
			boolQuery["must_not"] = mustNot
		}
	}

	// Process AND conditions
	if len(where.And) > 0 {
		var must []map[string]any
		for _, andClause := range where.And {
			subBool := buildESBoolQuery(andClause, labelMapping)
			if len(subBool) > 0 {
				must = append(must, map[string]any{"bool": subBool})
			}
		}
		if len(must) > 0 {
			existing, _ := boolQuery["must"].([]map[string]any)
			boolQuery["must"] = append(existing, must...)
		}
	}

	// Process OR conditions
	if len(where.Or) > 0 {
		var should []map[string]any
		for _, orClause := range where.Or {
			subBool := buildESBoolQuery(orClause, labelMapping)
			if len(subBool) > 0 {
				should = append(should, map[string]any{"bool": subBool})
			}
		}
		if len(should) > 0 {
			boolQuery["should"] = should
			boolQuery["minimum_should_match"] = 1
		}
	}

	// Process NOT condition
	if where.Not != nil {
		subBool := buildESBoolQuery(*where.Not, labelMapping)
		if len(subBool) > 0 {
			existing, _ := boolQuery["must_not"].([]map[string]any)
			boolQuery["must_not"] = append(existing, map[string]any{"bool": subBool})
		}
	}

	return boolQuery
}

// buildTraceQuery constructs the OpenSearch DSL query for trace requests.
func buildTraceQuery(req TracesV3Request, labelMapping map[string]string) map[string]any {
	boolFilters := []map[string]any{}

	// Time range filter
	if req.StartTime > 0 && req.EndTime > 0 {
		boolFilters = append(boolFilters, map[string]any{
			"range": map[string]any{
				"startTime": map[string]any{
					"gte":    fmt.Sprintf("%d", req.StartTime),
					"lte":    fmt.Sprintf("%d", req.EndTime),
					"format": "epoch_millis",
				},
			},
		})
	}

	// Build where clause
	whereBool := buildESBoolQuery(req.QueryRequest.Where, labelMapping)

	// Merge where clause filters into the main bool
	mainBool := map[string]any{}

	if whereFilters, ok := whereBool["filter"].([]map[string]any); ok {
		boolFilters = append(boolFilters, whereFilters...)
	}
	if len(boolFilters) > 0 {
		mainBool["filter"] = boolFilters
	}

	// Copy over must_not from where clause
	if mustNot, ok := whereBool["must_not"].([]map[string]any); ok {
		mainBool["must_not"] = mustNot
	}
	// Copy over must from where clause
	if must, ok := whereBool["must"].([]map[string]any); ok {
		mainBool["must"] = must
	}
	// Copy over should from where clause
	if should, ok := whereBool["should"].([]map[string]any); ok {
		mainBool["should"] = should
		mainBool["minimum_should_match"] = whereBool["minimum_should_match"]
	}

	dsl := map[string]any{
		"query": map[string]any{
			"bool": mainBool,
		},
	}

	// Limit and offset
	size := req.QueryRequest.Limit
	if size <= 0 {
		size = 100
	}
	dsl["size"] = size

	offset := req.QueryRequest.Offset
	if offset > 0 {
		dsl["from"] = offset
	}

	// Sort
	if len(req.QueryRequest.OrderBy) > 0 {
		var sortArr []map[string]any
		for _, ob := range req.QueryRequest.OrderBy {
			esField := mapLabelToESField(ob.Column, labelMapping)
			order := "desc"
			if ob.Order == query.Asc || ob.Order == query.AscNullsFirst || ob.Order == query.AscNullsLast {
				order = "asc"
			}
			sortArr = append(sortArr, map[string]any{esField: map[string]any{"order": order}})
		}
		dsl["sort"] = sortArr
	} else {
		dsl["sort"] = []map[string]any{{"startTime": map[string]any{"order": "desc"}}}
	}

	return dsl
}

// esTraceSearchResponse is the parsed response from an OpenSearch search query on trace data.
type esTraceSearchResponse struct {
	Hits struct {
		Total struct {
			Value int `json:"value"`
		} `json:"total"`
		Hits []struct {
			Source map[string]any `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
	Aggregations map[string]json.RawMessage `json:"aggregations"`
}

func getStringFromMap(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func getInt64FromMap(m map[string]any, key string) int64 {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int64(n)
		case int64:
			return n
		case json.Number:
			i, _ := n.Int64()
			return i
		}
	}
	return 0
}

// getNestedMap retrieves a nested map by key. Supports dotted paths (e.g., "resource.attributes")
// to navigate through nested JSON objects.
func getNestedMap(m map[string]any, key string) map[string]any {
	parts := strings.Split(key, ".")
	current := m
	for _, part := range parts {
		if v, ok := current[part]; ok {
			if nested, ok := v.(map[string]any); ok {
				current = nested
			} else {
				return nil
			}
		} else {
			return nil
		}
	}
	return current
}

// extractStringMap extracts a map[string]any sub-map and converts all values to strings,
// normalizing Data Prepper @ keys to dots.
func extractStringMap(m map[string]any, key string) map[string]string {
	result := map[string]string{}
	nested := getNestedMap(m, key)
	if nested == nil {
		return result
	}
	for k, v := range nested {
		normalizedKey := normalizeDataPrepperKey(k)
		if s, ok := v.(string); ok {
			result[normalizedKey] = s
		} else if v != nil {
			result[normalizedKey] = fmt.Sprintf("%v", v)
		}
	}
	return result
}

// mapDataPrepperStatusCode converts Data Prepper status.code int to string.
func mapDataPrepperStatusCode(v any) string {
	var code int
	switch n := v.(type) {
	case float64:
		code = int(n)
	case int:
		code = n
	case json.Number:
		i, _ := n.Int64()
		code = int(i)
	default:
		return "Unset"
	}
	switch code {
	case 1:
		return "Ok"
	case 2:
		return "Error"
	default:
		return "Unset"
	}
}

// mapDataPrepperSpanKind converts Data Prepper kind string to short form.
func mapDataPrepperSpanKind(kind string) string {
	switch kind {
	case "SPAN_KIND_SERVER":
		return "Server"
	case "SPAN_KIND_CLIENT":
		return "Client"
	case "SPAN_KIND_PRODUCER":
		return "Producer"
	case "SPAN_KIND_CONSUMER":
		return "Consumer"
	case "SPAN_KIND_INTERNAL":
		return "Internal"
	default:
		return kind
	}
}

// mapESHitToTrace maps an OpenSearch _source document from otel-v1-apm-span-* to OpenTelemetryTrace.
func mapESHitToTrace(src map[string]any) common.OpenTelemetryTrace {
	resourceAttrs := extractStringMap(src, "resource.attributes")
	spanAttrs := extractStringMap(src, "span.attributes")

	statusMap := getNestedMap(src, "status")
	statusCode := "Unset"
	statusMessage := ""
	if statusMap != nil {
		statusCode = mapDataPrepperStatusCode(statusMap["code"])
		statusMessage = getStringFromMap(statusMap, "message")
	}

	// Determine workload info from resource attributes
	workloadName := resourceAttrs["k8s.deployment.name"]
	if workloadName == "" {
		workloadName = getStringFromMap(src, "serviceName")
	}
	workloadNamespace := resourceAttrs["k8s.namespace.name"]

	// Determine resource (URL or DB statement)
	resource := spanAttrs["http.url"]
	if resource == "" {
		resource = spanAttrs["http.route"]
	}
	if resource == "" {
		resource = spanAttrs["db.statement"]
	}

	// Determine destination
	destName := spanAttrs["net.peer.name"]
	destWorkload := spanAttrs["peer.service"]
	if destWorkload == "" {
		destWorkload = destName
	}
	destNamespace := spanAttrs["destination.namespace"]

	httpStatusCode := spanAttrs["http.status_code"]

	trace := common.OpenTelemetryTrace{
		TraceID:              getStringFromMap(src, "traceId"),
		SpanID:               getStringFromMap(src, "spanId"),
		ParentSpanID:         getStringFromMap(src, "parentSpanId"),
		SpanName:             getStringFromMap(src, "name"),
		SpanKind:             mapDataPrepperSpanKind(getStringFromMap(src, "kind")),
		ServiceName:          getStringFromMap(src, "serviceName"),
		Timestamp:            getStringFromMap(src, "startTime"),
		StartTime:            getStringFromMap(src, "startTime"),
		EndTime:              getStringFromMap(src, "endTime"),
		DurationNs:           getInt64FromMap(src, "durationInNanos"),
		StatusCode:           statusCode,
		StatusMessage:        statusMessage,
		ResourceAttributes:   resourceAttrs,
		SpanAttributes:       spanAttrs,
		WorkloadName:         workloadName,
		WorkloadNamespace:    workloadNamespace,
		Resource:             resource,
		DestinationName:      destName,
		DestinationWorkload:  destWorkload,
		DestinationNamespace: destNamespace,
		HTTPStatusCode:       httpStatusCode,
		Service:              getStringFromMap(src, "serviceName"),
	}

	return trace
}

// mapESHitToHeatmap maps an OpenSearch _source document to OpenTelemetryTraceHeatMap.
func mapESHitToHeatmap(src map[string]any) common.OpenTelemetryTraceHeatMap {
	resourceAttrs := extractStringMap(src, "resource.attributes")
	spanAttrs := extractStringMap(src, "span.attributes")

	statusMap := getNestedMap(src, "status")
	statusCode := "Unset"
	if statusMap != nil {
		statusCode = mapDataPrepperStatusCode(statusMap["code"])
	}

	return common.OpenTelemetryTraceHeatMap{
		Timestamp:          getStringFromMap(src, "startTime"),
		TraceID:            getStringFromMap(src, "traceId"),
		SpanID:             getStringFromMap(src, "spanId"),
		SpanName:           getStringFromMap(src, "name"),
		ServiceName:        getStringFromMap(src, "serviceName"),
		DurationNs:         getInt64FromMap(src, "durationInNanos"),
		StatusCode:         statusCode,
		ResourceAttributes: resourceAttrs,
		SpanAttributes:     spanAttrs,
	}
}

func (e *ElasticSaasTraceSource) QueryTraces(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}

	dsl := buildTraceQuery(req, elasticTraceLabelMapping)

	resp, err := esRequestJSON("POST", fmt.Sprintf("%s/%s/_search", cfg.Url, esTraceIndexFor(cfg, traceIndexOverride(req.Request))), dsl, cfg) //nolint:bodyclose
	if err != nil {
		return nil, fmt.Errorf("failed to query traces: %w", err)
	}

	bodyBytes, err := readResponse(resp, "trace query")
	if err != nil {
		return nil, err
	}

	var searchResp esTraceSearchResponse
	if err := json.Unmarshal(bodyBytes, &searchResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal trace response: %w", err)
	}

	traces := make([]common.OpenTelemetryTrace, 0, len(searchResp.Hits.Hits))
	for _, hit := range searchResp.Hits.Hits {
		traces = append(traces, mapESHitToTrace(hit.Source))
	}

	return traces, nil
}

func (e *ElasticSaasTraceSource) GetQuery(ctx *security.RequestContext, req TracesV3Request) (string, error) {
	dsl := buildTraceQuery(req, elasticTraceLabelMapping)
	data, err := json.MarshalIndent(dsl, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal trace query: %w", err)
	}
	return string(data), nil
}

func (e *ElasticSaasTraceSource) CountTraces(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}

	dsl := buildTraceQuery(req, elasticTraceLabelMapping)
	dsl["size"] = 0
	dsl["track_total_hits"] = true
	delete(dsl, "sort")
	delete(dsl, "from")

	resp, err := esRequestJSON("POST", fmt.Sprintf("%s/%s/_search", cfg.Url, esTraceIndexFor(cfg, traceIndexOverride(req.Request))), dsl, cfg) //nolint:bodyclose
	if err != nil {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("failed to count traces: %w", err)
	}

	bodyBytes, err := readResponse(resp, "trace count")
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}

	var searchResp esTraceSearchResponse
	if err := json.Unmarshal(bodyBytes, &searchResp); err != nil {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("failed to unmarshal trace count response: %w", err)
	}

	return common.OpenTelemetryTraceCount{Count: searchResp.Hits.Total.Value}, nil
}

func (e *ElasticSaasTraceSource) GetLabelValues(ctx *security.RequestContext, req TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, err
	}

	esField := mapLabelToESField(req.Label, elasticTraceLabelMapping)

	// Build filters from where clause
	boolFilters := []map[string]any{}
	whereBool := buildESBoolQuery(req.QueryRequest.Where, elasticTraceLabelMapping)
	if whereFilters, ok := whereBool["filter"].([]map[string]any); ok {
		boolFilters = append(boolFilters, whereFilters...)
	}

	// Add time range filter directly (same as buildTraceQuery)
	if req.StartTime > 0 && req.EndTime > 0 {
		boolFilters = append(boolFilters, map[string]any{
			"range": map[string]any{
				"startTime": map[string]any{
					"gte":    fmt.Sprintf("%d", req.StartTime),
					"lte":    fmt.Sprintf("%d", req.EndTime),
					"format": "epoch_millis",
				},
			},
		})
	}

	dsl := map[string]any{
		"size": 0,
		"aggs": map[string]any{
			"label_values": map[string]any{
				"terms": map[string]any{
					"field": esField,
					"size":  1000,
				},
			},
		},
	}

	if len(boolFilters) > 0 {
		dsl["query"] = map[string]any{
			"bool": map[string]any{"filter": boolFilters},
		}
	}

	resp, err := esRequestJSON("POST", fmt.Sprintf("%s/%s/_search", cfg.Url, esTraceIndexFor(cfg, traceIndexOverride(req.Request))), dsl, cfg) //nolint:bodyclose
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("failed to query label values: %w", err)
	}

	bodyBytes, err := readResponse(resp, "trace label values")
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, err
	}

	var searchResp esTraceSearchResponse
	if err := json.Unmarshal(bodyBytes, &searchResp); err != nil {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("failed to unmarshal label values response: %w", err)
	}

	var buckets []struct {
		Key      string `json:"key"`
		DocCount int    `json:"doc_count"`
	}
	if raw, ok := searchResp.Aggregations["label_values"]; ok {
		var termsAgg struct {
			Buckets []struct {
				Key      string `json:"key"`
				DocCount int    `json:"doc_count"`
			} `json:"buckets"`
		}
		if err := json.Unmarshal(raw, &termsAgg); err == nil {
			buckets = termsAgg.Buckets
		}
	}

	values := make([]string, 0, len(buckets))
	for _, b := range buckets {
		values = append(values, b.Key)
	}

	return common.OpenTelemetryTraceLabelValues{
		Label:  req.Label,
		Values: values,
	}, nil
}

// elasticTraceGroupDims are the group-by dimensions for the Data Prepper
// otel-v1-apm-span-* schema. Field-path precedence within each dimension mirrors the
// per-span mapping so a grouped row's identity matches a single span.
// http_status_code is intentionally excluded (see elasticTraceHTTPStatusFields /
// esGroupSubAggs) so each row aggregates all statuses and error_count is a real
// 4xx/5xx subset.
var elasticTraceGroupDims = []esGroupDim{
	{Key: "workload_name", Fields: []string{"resource.attributes.k8s@deployment@name", "serviceName"}},
	{Key: "workload_namespace", Fields: []string{"resource.attributes.k8s@namespace@name"}},
	{Key: "destination_workload_name", Fields: []string{"span.attributes.net@peer@name"}},
	{Key: "destination_workload_namespace", Fields: []string{"span.attributes.destination@namespace"}},
	{Key: "resource", Fields: []string{"span.attributes.http@url", "span.attributes.db@statement"}},
	{Key: "span_name", Fields: []string{"name"}},
}

// elasticTraceHTTPStatusFields are the candidate http-status fields used to compute
// the per-group error_count (4xx/5xx).
var elasticTraceHTTPStatusFields = []string{"span.attributes.http@status_code"}

func (e *ElasticSaasTraceSource) QueryGroupedTraces(ctx *security.RequestContext, req TracesV3Request) ([]TraceGroupingValues, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}

	boolFilters := []map[string]any{}
	if req.StartTime > 0 && req.EndTime > 0 {
		boolFilters = append(boolFilters, map[string]any{
			"range": map[string]any{
				"startTime": map[string]any{
					"gte":    fmt.Sprintf("%d", req.StartTime),
					"lte":    fmt.Sprintf("%d", req.EndTime),
					"format": "epoch_millis",
				},
			},
		})
	}
	whereBool := buildESBoolQuery(req.QueryRequest.Where, elasticTraceLabelMapping)
	if whereFilters, ok := whereBool["filter"].([]map[string]any); ok {
		boolFilters = append(boolFilters, whereFilters...)
	}

	searchURL := fmt.Sprintf("%s/%s/_search", cfg.Url, esTraceIndexFor(cfg, traceIndexOverride(req.Request)))
	buckets, err := fetchAllCompositeTraceBuckets(
		ctx, cfg, searchURL, boolFilters,
		esCompositeSources(elasticTraceGroupDims),
		esGroupSubAggs("durationInNanos", elasticTraceHTTPStatusFields),
	)
	if err != nil {
		return nil, err
	}

	results := make([]TraceGroupingValues, 0, len(buckets))
	for _, b := range buckets {
		if v, ok := parseTraceGroupBucket(b); ok {
			results = append(results, v)
		}
	}
	return sortAndPaginateTraceGroups(results, req), nil
}

func (e *ElasticSaasTraceSource) QueryGroupedTracesCount(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{}, err
	}

	// Build base filters
	boolFilters := []map[string]any{}
	if req.StartTime > 0 && req.EndTime > 0 {
		boolFilters = append(boolFilters, map[string]any{
			"range": map[string]any{
				"startTime": map[string]any{
					"gte":    fmt.Sprintf("%d", req.StartTime),
					"lte":    fmt.Sprintf("%d", req.EndTime),
					"format": "epoch_millis",
				},
			},
		})
	}

	whereBool := buildESBoolQuery(req.QueryRequest.Where, elasticTraceLabelMapping)
	if whereFilters, ok := whereBool["filter"].([]map[string]any); ok {
		boolFilters = append(boolFilters, whereFilters...)
	}

	dsl := map[string]any{
		"size": 0,
		"query": map[string]any{
			"bool": map[string]any{"filter": boolFilters},
		},
		"aggs": map[string]any{
			"group_count": map[string]any{
				"cardinality": map[string]any{
					"script": map[string]any{
						// Same dimension list as QueryGroupedTraces so the group
						// count matches the number of grouped rows produced.
						"source": esGroupKeyCardinalityScript(elasticTraceGroupDims),
						"lang":   "painless",
					},
				},
			},
		},
	}

	resp, err := esRequestJSON("POST", fmt.Sprintf("%s/%s/_search", cfg.Url, esTraceIndexFor(cfg, traceIndexOverride(req.Request))), dsl, cfg) //nolint:bodyclose
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{}, fmt.Errorf("failed to query grouped traces count: %w", err)
	}

	bodyBytes, err := readResponse(resp, "grouped traces count")
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{}, err
	}

	var searchResp esTraceSearchResponse
	if err := json.Unmarshal(bodyBytes, &searchResp); err != nil {
		return common.OpenTelemetryTraceGroupCount{}, fmt.Errorf("failed to unmarshal grouped traces count response: %w", err)
	}

	count := 0
	if raw, ok := searchResp.Aggregations["group_count"]; ok {
		var cardAgg struct {
			Value int `json:"value"`
		}
		if err := json.Unmarshal(raw, &cardAgg); err == nil {
			count = cardAgg.Value
		}
	}

	return common.OpenTelemetryTraceGroupCount{Count: count}, nil
}

func (e *ElasticSaasTraceSource) QueryTracesHeatmap(ctx *security.RequestContext, req TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}

	dsl := map[string]any{
		"size": 2000,
		"query": map[string]any{
			"term": map[string]any{
				"traceId": req.TraceId,
			},
		},
		"sort": []map[string]any{
			{"startTime": map[string]any{"order": "asc"}},
		},
	}

	resp, err := esRequestJSON("POST", fmt.Sprintf("%s/%s/_search", cfg.Url, esTraceIndexFor(cfg, traceIndexOverride(req.Request))), dsl, cfg) //nolint:bodyclose
	if err != nil {
		return nil, fmt.Errorf("failed to query traces heatmap: %w", err)
	}

	bodyBytes, err := readResponse(resp, "traces heatmap")
	if err != nil {
		return nil, err
	}

	var searchResp esTraceSearchResponse
	if err := json.Unmarshal(bodyBytes, &searchResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal traces heatmap response: %w", err)
	}

	heatmap := make([]common.OpenTelemetryTraceHeatMap, 0, len(searchResp.Hits.Hits))
	for _, hit := range searchResp.Hits.Hits {
		heatmap = append(heatmap, mapESHitToHeatmap(hit.Source))
	}

	return heatmap, nil
}

// QueryRootSpansByTrace returns one root span per trace for the "By Traces" view (see
// queryRootSpansViaSpans): spans are fetched via QueryTraces and reduced to one root per trace.
func (e *ElasticSaasTraceSource) QueryRootSpansByTrace(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	return queryRootSpansViaSpans(ctx, e, req)
}

// CountTracesByTrace returns -1 (estimate) for the "By Traces" view; distinct-trace counting is
// not available cheaply for this provider, so the frontend estimates pagination.
func (e *ElasticSaasTraceSource) CountTracesByTrace(_ *security.RequestContext, _ TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	return countTracesByTraceEstimate()
}

// QueryLabels returns backend-discovered trace label keys. ElasticSaasTraceSource has no generic
// label-key discovery API, so it returns an empty list; FetchTraceLabels falls back to
// the derived canonical + mapping label set. Implements TraceSource.QueryLabels.
func (e *ElasticSaasTraceSource) QueryLabels(_ *security.RequestContext, _ FetchTraceLabelRequest) ([]OutputTraceLabel, error) {
	return []OutputTraceLabel{}, nil
}
