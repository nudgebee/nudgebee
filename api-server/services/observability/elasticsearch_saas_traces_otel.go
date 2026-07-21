package observability

// ElasticOtelTraceSource reads traces written by the OpenTelemetry Collector
// `elasticsearch` exporter with `mapping.mode: otel` into the OTel-native
// traces-*.otel data streams (e.g. traces-generic.otel-default). This schema is
// incompatible with Data Prepper's otel-v1-apm-span-* schema (see
// ElasticSaasTraceSource), so it is a SEPARATE reader — additive, never modifying
// the Data Prepper reader's behavior.
//
// OTel-native _source shape (proven by ParseSourceMap / nestedMap in
// elasticsearch.go): `@timestamp` is an epoch-millis STRING; `resource.attributes`
// and `attributes` are nested OBJECTS whose KEYS are flat dotted strings
// (e.g. resource.attributes → {"k8s.namespace.name": "...", "service.name": "..."}).
// Leaf attributes are read via extractStringMap(src, "resource.attributes") /
// extractStringMap(src, "attributes") and indexed by the literal dotted key.
//
// The DSL field PATHS used below (resource.attributes.*, attributes.*, status.code,
// kind, duration, @timestamp, trace_id) are the OTel semantic-convention names and
// assume the traces-*.otel data stream maps attribute values as `keyword`. If
// aggregations/filters return empty against a real cluster, verify the actual field
// names with `GET traces-generic.otel-default/_mapping` and adjust the paths here —
// that mapping is the one thing not verifiable from code.

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/query"
	"nudgebee/services/security"
)

// ElasticOtelTraceSource implements TraceSource for user-managed Elasticsearch with
// OTel-native traces stored in the traces-*.otel data streams (OTel Collector
// `elasticsearch` exporter, mapping.mode: otel). Distinct from ElasticSaasTraceSource,
// which reads Data Prepper's otel-v1-apm-span-* schema.
type ElasticOtelTraceSource struct{}

// elasticOtelTraceLabelMapping maps frontend field names to OTel-native ES field paths.
var elasticOtelTraceLabelMapping = map[string]string{
	"timestamp":                      "@timestamp",
	"service_name":                   "resource.attributes.service.name",
	"span_name":                      "name",
	"trace_id":                       "trace_id",
	"span_id":                        "span_id",
	"parent_span_id":                 "parent_span_id",
	"status_code":                    "status.code",
	"duration_ns":                    "duration",
	"span_kind":                      "kind",
	"http_status_code":               "attributes.http.status_code",
	"workload_name":                  "resource.attributes.k8s.deployment.name",
	"workload_namespace":             "resource.attributes.k8s.namespace.name",
	"resource":                       "attributes.http.url",
	"destination_workload_name":      "attributes.net.peer.name",
	"destination_workload_namespace": "attributes.destination.namespace",
}

func (e *ElasticOtelTraceSource) GetLabelMapping() map[string]string {
	return elasticOtelTraceLabelMapping
}

func (e *ElasticOtelTraceSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_contains", "_like", "_nlike", "_gt", "_lt", "_is_null"}
}

// mapOtelSpanKind normalizes an OTel span kind — either the numeric enum
// (1=Internal, 2=Server, 3=Client, 4=Producer, 5=Consumer) or a string
// ("SPAN_KIND_SERVER"/"Server") — to the short form. Empty/unknown → "".
func mapOtelSpanKind(v any) string {
	switch k := v.(type) {
	case string:
		switch k {
		case "SPAN_KIND_INTERNAL", "Internal", "internal":
			return "Internal"
		case "SPAN_KIND_SERVER", "Server", "server":
			return "Server"
		case "SPAN_KIND_CLIENT", "Client", "client":
			return "Client"
		case "SPAN_KIND_PRODUCER", "Producer", "producer":
			return "Producer"
		case "SPAN_KIND_CONSUMER", "Consumer", "consumer":
			return "Consumer"
		default:
			return ""
		}
	case float64:
		return otelSpanKindFromInt(int(k))
	case int:
		return otelSpanKindFromInt(k)
	case int64:
		return otelSpanKindFromInt(int(k))
	case json.Number:
		i, _ := k.Int64()
		return otelSpanKindFromInt(int(i))
	default:
		return ""
	}
}

func otelSpanKindFromInt(k int) string {
	switch k {
	case 1:
		return "Internal"
	case 2:
		return "Server"
	case 3:
		return "Client"
	case 4:
		return "Producer"
	case 5:
		return "Consumer"
	default:
		return ""
	}
}

// mapOtelStatusCode normalizes an OTel status code — either the numeric enum
// (0=Unset, 1=Ok, 2=Error) or a string ("Unset"/"Ok"/"Error") — to the short
// form. Empty/unknown → "".
func mapOtelStatusCode(v any) string {
	switch c := v.(type) {
	case string:
		switch c {
		case "STATUS_CODE_UNSET", "Unset", "unset":
			return "Unset"
		case "STATUS_CODE_OK", "Ok", "OK", "ok":
			return "Ok"
		case "STATUS_CODE_ERROR", "Error", "ERROR", "error":
			return "Error"
		default:
			return ""
		}
	case float64:
		return otelStatusFromInt(int(c))
	case int:
		return otelStatusFromInt(c)
	case int64:
		return otelStatusFromInt(int(c))
	case json.Number:
		i, _ := c.Int64()
		return otelStatusFromInt(int(i))
	default:
		return ""
	}
}

func otelStatusFromInt(c int) string {
	switch c {
	case 0:
		return "Unset"
	case 1:
		return "Ok"
	case 2:
		return "Error"
	default:
		return ""
	}
}

// mapOtelHitToTrace maps an OTel-native _source document (traces-*.otel) to OpenTelemetryTrace.
func mapOtelHitToTrace(src map[string]any) common.OpenTelemetryTrace {
	resourceAttrs := extractStringMap(src, "resource.attributes")
	spanAttrs := extractStringMap(src, "attributes")

	statusCode := "Unset"
	statusMessage := ""
	if statusMap := getNestedMap(src, "status"); statusMap != nil {
		if mapped := mapOtelStatusCode(statusMap["code"]); mapped != "" {
			statusCode = mapped
		}
		statusMessage = getStringFromMap(statusMap, "message")
	}

	service := resourceAttrs["service.name"]

	workloadName := resourceAttrs["k8s.deployment.name"]
	if workloadName == "" {
		workloadName = service
	}
	workloadNamespace := resourceAttrs["k8s.namespace.name"]

	resource := spanAttrs["http.url"]
	if resource == "" {
		resource = spanAttrs["url.full"]
	}
	if resource == "" {
		resource = spanAttrs["http.route"]
	}
	if resource == "" {
		resource = spanAttrs["db.statement"]
	}

	destName := spanAttrs["net.peer.name"]
	if destName == "" {
		destName = spanAttrs["server.address"]
	}
	destWorkload := spanAttrs["peer.service"]
	if destWorkload == "" {
		destWorkload = destName
	}
	destNamespace := spanAttrs["destination.namespace"]

	httpStatusCode := spanAttrs["http.status_code"]
	if httpStatusCode == "" {
		httpStatusCode = spanAttrs["http.response.status_code"]
	}

	timestamp := normalizeESTimestamp(getStringFromMap(src, "@timestamp"))

	return common.OpenTelemetryTrace{
		TraceID:              getStringFromMap(src, "trace_id"),
		SpanID:               getStringFromMap(src, "span_id"),
		ParentSpanID:         getStringFromMap(src, "parent_span_id"),
		SpanName:             getStringFromMap(src, "name"),
		SpanKind:             mapOtelSpanKind(src["kind"]),
		ServiceName:          service,
		Timestamp:            timestamp,
		StartTime:            timestamp,
		DurationNs:           getInt64FromMap(src, "duration"),
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
		Service:              service,
	}
}

// mapOtelHitToHeatmap maps an OTel-native _source document to OpenTelemetryTraceHeatMap.
func mapOtelHitToHeatmap(src map[string]any) common.OpenTelemetryTraceHeatMap {
	resourceAttrs := extractStringMap(src, "resource.attributes")
	spanAttrs := extractStringMap(src, "attributes")

	statusCode := "Unset"
	if statusMap := getNestedMap(src, "status"); statusMap != nil {
		if mapped := mapOtelStatusCode(statusMap["code"]); mapped != "" {
			statusCode = mapped
		}
	}

	return common.OpenTelemetryTraceHeatMap{
		Timestamp:          normalizeESTimestamp(getStringFromMap(src, "@timestamp")),
		TraceID:            getStringFromMap(src, "trace_id"),
		SpanID:             getStringFromMap(src, "span_id"),
		SpanName:           getStringFromMap(src, "name"),
		ServiceName:        resourceAttrs["service.name"],
		DurationNs:         getInt64FromMap(src, "duration"),
		StatusCode:         statusCode,
		ResourceAttributes: resourceAttrs,
		SpanAttributes:     spanAttrs,
	}
}

// otelTraceTimeRangeFilter builds the OTel-native time-range filter on @timestamp
// (epoch-millis string field) using the request's epoch-millis start/end.
func otelTraceTimeRangeFilter(start, end int64) map[string]any {
	return map[string]any{
		"range": map[string]any{
			"@timestamp": map[string]any{
				"gte":    fmt.Sprintf("%d", start),
				"lte":    fmt.Sprintf("%d", end),
				"format": "epoch_millis",
			},
		},
	}
}

// buildOtelTraceQuery constructs the OTel-native OpenSearch DSL query for trace requests.
// Mirrors buildTraceQuery but ranges/sorts on @timestamp instead of Data Prepper's startTime.
func buildOtelTraceQuery(req TracesV3Request, labelMapping map[string]string) map[string]any {
	boolFilters := []map[string]any{}

	if req.StartTime > 0 && req.EndTime > 0 {
		boolFilters = append(boolFilters, otelTraceTimeRangeFilter(req.StartTime, req.EndTime))
	}

	whereBool := buildESBoolQuery(req.QueryRequest.Where, labelMapping)

	mainBool := map[string]any{}
	if whereFilters, ok := whereBool["filter"].([]map[string]any); ok {
		boolFilters = append(boolFilters, whereFilters...)
	}
	if len(boolFilters) > 0 {
		mainBool["filter"] = boolFilters
	}
	if mustNot, ok := whereBool["must_not"].([]map[string]any); ok {
		mainBool["must_not"] = mustNot
	}
	if must, ok := whereBool["must"].([]map[string]any); ok {
		mainBool["must"] = must
	}
	if should, ok := whereBool["should"].([]map[string]any); ok {
		mainBool["should"] = should
		mainBool["minimum_should_match"] = whereBool["minimum_should_match"]
	}

	dsl := map[string]any{
		"query": map[string]any{
			"bool": mainBool,
		},
	}

	size := req.QueryRequest.Limit
	if size <= 0 {
		size = 100
	}
	dsl["size"] = size

	if offset := req.QueryRequest.Offset; offset > 0 {
		dsl["from"] = offset
	}

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
		dsl["sort"] = []map[string]any{{"@timestamp": map[string]any{"order": "desc"}}}
	}

	return dsl
}

func (e *ElasticOtelTraceSource) QueryTraces(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}

	dsl := buildOtelTraceQuery(req, elasticOtelTraceLabelMapping)

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
		traces = append(traces, mapOtelHitToTrace(hit.Source))
	}

	return traces, nil
}

func (e *ElasticOtelTraceSource) GetQuery(ctx *security.RequestContext, req TracesV3Request) (string, error) {
	dsl := buildOtelTraceQuery(req, elasticOtelTraceLabelMapping)
	data, err := json.MarshalIndent(dsl, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal trace query: %w", err)
	}
	return string(data), nil
}

func (e *ElasticOtelTraceSource) CountTraces(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}

	dsl := buildOtelTraceQuery(req, elasticOtelTraceLabelMapping)
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

func (e *ElasticOtelTraceSource) GetLabelValues(ctx *security.RequestContext, req TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, err
	}

	esField := mapLabelToESField(req.Label, elasticOtelTraceLabelMapping)

	boolFilters := []map[string]any{}
	whereBool := buildESBoolQuery(req.QueryRequest.Where, elasticOtelTraceLabelMapping)
	if whereFilters, ok := whereBool["filter"].([]map[string]any); ok {
		boolFilters = append(boolFilters, whereFilters...)
	}

	if req.StartTime > 0 && req.EndTime > 0 {
		boolFilters = append(boolFilters, otelTraceTimeRangeFilter(req.StartTime, req.EndTime))
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

// elasticOtelTraceGroupDims are the group-by dimensions for the OTel-native schema.
// Field-path precedence within each dimension mirrors mapOtelHitToTrace so a grouped
// row's identity matches the per-span mapping. http_status_code is intentionally
// excluded (see elasticOtelHTTPStatusFields / esGroupSubAggs) so each row aggregates
// all statuses and error_count is a real 4xx/5xx subset.
var elasticOtelTraceGroupDims = []esGroupDim{
	{Key: "workload_name", Fields: []string{"resource.attributes.k8s.deployment.name", "resource.attributes.service.name"}},
	{Key: "workload_namespace", Fields: []string{"resource.attributes.k8s.namespace.name"}},
	{Key: "destination_workload_name", Fields: []string{"attributes.peer.service", "attributes.net.peer.name", "attributes.server.address"}},
	{Key: "destination_workload_namespace", Fields: []string{"attributes.destination.namespace"}},
	{Key: "resource", Fields: []string{"attributes.http.url", "attributes.url.full", "attributes.http.route", "attributes.db.statement"}},
	{Key: "span_name", Fields: []string{"name"}},
}

// elasticOtelHTTPStatusFields are the candidate http-status fields used to compute
// the per-group error_count (4xx/5xx).
var elasticOtelHTTPStatusFields = []string{"attributes.http.status_code", "attributes.http.response.status_code"}

func (e *ElasticOtelTraceSource) QueryGroupedTraces(ctx *security.RequestContext, req TracesV3Request) ([]TraceGroupingValues, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}

	boolFilters := []map[string]any{}
	if req.StartTime > 0 && req.EndTime > 0 {
		boolFilters = append(boolFilters, otelTraceTimeRangeFilter(req.StartTime, req.EndTime))
	}
	whereBool := buildESBoolQuery(req.QueryRequest.Where, elasticOtelTraceLabelMapping)
	if whereFilters, ok := whereBool["filter"].([]map[string]any); ok {
		boolFilters = append(boolFilters, whereFilters...)
	}

	searchURL := fmt.Sprintf("%s/%s/_search", cfg.Url, esTraceIndexFor(cfg, traceIndexOverride(req.Request)))
	buckets, err := fetchAllCompositeTraceBuckets(
		ctx, cfg, searchURL, boolFilters,
		esCompositeSources(elasticOtelTraceGroupDims),
		esGroupSubAggs("duration", elasticOtelHTTPStatusFields),
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

func (e *ElasticOtelTraceSource) QueryGroupedTracesCount(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{}, err
	}

	boolFilters := []map[string]any{}
	if req.StartTime > 0 && req.EndTime > 0 {
		boolFilters = append(boolFilters, otelTraceTimeRangeFilter(req.StartTime, req.EndTime))
	}

	whereBool := buildESBoolQuery(req.QueryRequest.Where, elasticOtelTraceLabelMapping)
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
						"source": esGroupKeyCardinalityScript(elasticOtelTraceGroupDims),
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

func (e *ElasticOtelTraceSource) QueryTracesHeatmap(ctx *security.RequestContext, req TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}

	dsl := map[string]any{
		"size": 2000,
		"query": map[string]any{
			"term": map[string]any{
				"trace_id": req.TraceId,
			},
		},
		"sort": []map[string]any{
			{"@timestamp": map[string]any{"order": "asc"}},
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
		heatmap = append(heatmap, mapOtelHitToHeatmap(hit.Source))
	}

	return heatmap, nil
}

// QueryRootSpansByTrace returns one root span per trace for the "By Traces" view via the
// shared queryRootSpansViaSpans reducer (same contract as ElasticSaasTraceSource).
func (e *ElasticOtelTraceSource) QueryRootSpansByTrace(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	return queryRootSpansViaSpans(ctx, e, req)
}

// CountTracesByTrace returns -1 (estimate) for the "By Traces" view; distinct-trace counting is
// not available cheaply for this provider, so the frontend estimates pagination.
func (e *ElasticOtelTraceSource) CountTracesByTrace(_ *security.RequestContext, _ TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	return countTracesByTraceEstimate()
}

// QueryLabels returns an empty list: ElasticOtelTraceSource has no generic label-key discovery
// API, so FetchTraceLabels falls back to the derived canonical + mapping label set.
func (e *ElasticOtelTraceSource) QueryLabels(_ *security.RequestContext, _ FetchTraceLabelRequest) ([]OutputTraceLabel, error) {
	return []OutputTraceLabel{}, nil
}
