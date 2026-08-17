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
	return []string{"_eq", "_neq", "_contains", "_like", "_nlike", "_gt", "_lt", "_is_null"}
}

// GetQuery renders the first entry of req.Queries into the same compact
// ES _search body that FetchMetricsQuery would POST. The orchestrator at
// service.go:GetMetricsQuery always passes a single-entry Queries map, so
// "first" is well-defined in production. Return value is byte-identical
// to the Query field FetchMetricsQuery stores on each QueryResult — pinned
// by the parity tests.
//
// Note: GetMetricsQuery wraps GetQuery output with wrapPromQLAggregator
// (service.go:1063), which would corrupt this JSON. ES is not routed
// through GetMetricsQuery today (UI does not populate QueryItems for ES);
// if that changes, the wrap must be made provider-aware first.
func (e *ElasticSaasMetricSource) GetQuery(_ *security.RequestContext, req FetchMetricsRequest) (string, error) {
	queryType, _ := req.Request["query_type"].(string)
	for _, q := range req.Queries {
		body, err := buildESMetricsQueryBody(queryType, q, req.StartTime, req.EndTime)
		if err != nil {
			return "", err
		}
		out, err := json.Marshal(body)
		if err != nil {
			return "", err
		}
		return string(out), nil
	}
	return "", nil
}

// buildESMetricsQueryBody renders one query entry into the ES _search body
// that FetchMetricsQuery would POST. Pure: no IO, no config lookups — safe
// to call from GetQuery for query rendering as well as from FetchMetricsQuery
// before execution.
//
// queryType "dsl" — Code Mode: parse queryDSL as a raw ES body, default the
// `size` field if omitted, and AND-merge the time range into a bool filter
// so scans are still bounded.
//
// any other queryType — Builder Mode: treat queryDSL as a JSON-encoded
// []QueryWhereClause and render bool/filter via whereToBool +
// normalizeESMetricsWhere, appending the time range.
func buildESMetricsQueryBody(queryType, queryDSL string, startMillis, endMillis int64) (map[string]any, error) {
	if queryType == "dsl" {
		var userBody map[string]any
		if err := json.Unmarshal([]byte(queryDSL), &userBody); err != nil {
			return nil, fmt.Errorf("failed to parse DSL query body: %v", err)
		}
		if userBody == nil {
			// json.Unmarshal leaves userBody nil when the input is literal
			// "null" or empty. Reject up front — follow-on map writes would
			// panic on nil, and a null body is not a valid _search.
			return nil, fmt.Errorf("DSL query body must be a JSON object, got null")
		}
		if _, ok := userBody["size"]; !ok {
			userBody["size"] = 10000
		}
		if startMillis > 0 && endMillis > 0 {
			userQuery, ok := userBody["query"].(map[string]any)
			if !ok {
				userQuery = map[string]any{"match_all": map[string]any{}}
			}
			userBody["query"] = map[string]any{
				"bool": map[string]any{
					"filter": []any{userQuery, esMetricsTimeRangeClause(startMillis, endMillis)},
				},
			}
		}
		return userBody, nil
	}

	var whereClauses []query.QueryWhereClause
	if err := json.Unmarshal([]byte(queryDSL), &whereClauses); err != nil {
		return nil, fmt.Errorf("failed to parse query filters: %v", err)
	}
	var filters []any
	for _, wc := range whereClauses {
		clause, err := whereToBool(normalizeESMetricsWhere(wc))
		if err != nil {
			return nil, fmt.Errorf("failed to build ES clause: %v", err)
		}
		filters = append(filters, clause)
	}
	if startMillis > 0 && endMillis > 0 {
		filters = append(filters, esMetricsTimeRangeClause(startMillis, endMillis))
	}
	return map[string]any{
		"size": 10000,
		"query": map[string]any{
			"bool": map[string]any{
				"filter": filters,
			},
		},
	}, nil
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
	queryType, _ := req.Request["query_type"].(string)

	for queryKey, queryDSL := range req.Queries {
		queryBody, buildErr := buildESMetricsQueryBody(queryType, queryDSL, req.StartTime, req.EndTime)
		if buildErr != nil {
			// No body to render — echo raw input back so the user sees what
			// they submitted alongside the error.
			errStr := buildErr.Error()
			results = append(results, QueryResult{
				QueryKey: queryKey,
				Query:    queryDSL,
				Error:    &errStr,
			})
			continue
		}

		renderedJSON, _ := json.Marshal(queryBody)
		renderedQuery := string(renderedJSON)

		// Mirrors the "ES log query" line so both paths are greppable the same way:
		// resolved index, full URL, the window, and the rendered body — enough to
		// replay the exact request by hand against the cluster.
		esURL := fmt.Sprintf("%s/%s/_search", cfg.Url, index)
		slog.Info("ES metrics query", "index", index, "url", esURL,
			"query_key", queryKey, "query_type", queryType,
			"start_ms", req.StartTime, "end_ms", req.EndTime,
			"body", renderedQuery)

		resp, err := esRequestJSON("POST", esURL, queryBody, cfg) //nolint:bodyclose
		if err != nil {
			errStr := fmt.Sprintf("failed to query metric: %v", err)
			results = append(results, QueryResult{
				QueryKey: queryKey,
				Query:    renderedQuery,
				Error:    &errStr,
			})
			continue
		}

		bodyBytes, err := readResponse(resp, "metric query")
		if err != nil {
			errStr := err.Error()
			results = append(results, QueryResult{
				QueryKey: queryKey,
				Query:    renderedQuery,
				Error:    &errStr,
			})
			continue
		}

		payload, err := parseESMetricsHits(bodyBytes)
		if err != nil {
			errStr := fmt.Sprintf("failed to parse ES metrics response: %v", err)
			results = append(results, QueryResult{
				QueryKey: queryKey,
				Query:    renderedQuery,
				Error:    &errStr,
			})
			continue
		}

		results = append(results, QueryResult{
			QueryKey: queryKey,
			Query:    renderedQuery,
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

	// List stable data-stream names, not the rolled-over ".ds-*" backing indices
	// that _cat/indices exposes. No type-prefix filter: client clusters don't
	// necessarily name metric streams "metrics-*", so every queryable target is
	// offered. See ListAllESIndexTargets.
	indexNames, err := ListAllESIndexTargets(cfg)
	if err != nil {
		return nil, err
	}

	output := make([]OutputMetrics, 0, len(indexNames))
	for _, indexName := range indexNames {
		output = append(output, OutputMetrics{
			Metric:     indexName,
			Attributes: map[string]any{},
		})
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

	return esFetchLabelValues(cfg, index, req.Label)
}

// esFetchLabelValues aggregates the distinct values of a label field in an index.
// Split from FetchMetricLabelValues so the .keyword fallback can be tested against a
// stub Elasticsearch without a database-backed integration config.
// esLabelValuesWindow bounds label enumeration to actively-written data. Introspection
// does not need history, and an unbounded agg over a snapshot-backed pattern is
// expensive; see buildDSL below.
const (
	esLabelValuesWindow    = "now-24h"
	esLabelValuesTimeField = "@timestamp"
)

func esFetchLabelValues(cfg *ElasticsearchConfig, index, reqLabel string) ([]OutputMetricsLabelValues, error) {
	req := FetchMetricsLabelValueRequest{Label: reqLabel}

	// OTel-native keyword fields (resource.attributes.*, scope.*, metrics.*) are
	// already aggregatable keywords; appending .keyword targets a subfield that
	// does not exist. Append it only for other (legacy text) fields.
	labelField := req.Label
	if !strings.HasSuffix(labelField, ".keyword") && !isOTelKeywordField(labelField) {
		labelField = labelField + ".keyword"
	}

	// Bound the aggregation to recent data.
	//
	// Without a time filter this terms agg runs over every index the pattern matches.
	// On one customer estate that is 276 indices, 211 of them searchable snapshots on
	// object storage — an unbounded frozen-tier scan on every label lookup, paid for in
	// S3 retrieval. With a range filter Elasticsearch can_match-prunes the cold shards:
	// measured on that cluster, a filtered query skipped 287 of 292 shards and returned
	// in 138ms.
	//
	// The window only needs to be long enough to enumerate labels that are in active
	// use; a label absent for this long is not one the caller should be steered toward.
	buildDSL := func(field string) map[string]any {
		return map[string]any{
			"size": 0,
			"query": map[string]any{
				"bool": map[string]any{
					"filter": []any{
						map[string]any{"range": map[string]any{
							esLabelValuesTimeField: map[string]any{"gte": esLabelValuesWindow},
						}},
					},
				},
			},
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

	// Aggregating a field that does not exist is NOT an error in Elasticsearch — it
	// returns zero buckets. So a `.keyword` suffix guessed onto a field that is already
	// a plain keyword silently yields "no values" instead of failing, and the
	// retry-without-suffix below never fires.
	//
	// That is the ECS case, and it is not rare: Elastic Agent / Metricbeat map
	// `kubernetes.namespace`, `metricset.name`, `host.name` and friends as plain
	// `keyword`, so `<field>.keyword` does not exist. Every label-values lookup on such
	// an index came back empty while the same field aggregated fine unsuffixed, which
	// read to callers as "this environment has no such label".
	//
	// The suffix is still tried FIRST because the opposite case is a hard error: OTel
	// `name` is `text`, aggregating it unsuffixed fails outright, and only
	// `name.keyword` works. So: try the suffixed field, then fall back on either an
	// error OR an empty result.
	queryLabelValues := func(field string) ([]OutputMetricsLabelValues, error) {
		resp, err := esRequestJSON("POST", fmt.Sprintf("%s/%s/_search", cfg.Url, index), buildDSL(field), cfg) //nolint:bodyclose
		if err != nil {
			return nil, fmt.Errorf("failed to query metric label values: %w", err)
		}
		bodyBytes, err := readResponse(resp, "metric label values")
		if err != nil {
			return nil, err
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

	output, err := queryLabelValues(labelField)
	if labelField != req.Label && (err != nil || len(output) == 0) {
		fallback, ferr := queryLabelValues(req.Label)
		if ferr == nil && len(fallback) > 0 {
			slog.Info("ES metric label values: resolved unsuffixed field after empty .keyword lookup",
				"index", index, "field", req.Label, "num_values", len(fallback))
			return fallback, nil
		}
		// Keep the original error when neither spelling worked, so a genuine backend
		// failure is not masked by an empty fallback.
		if err != nil {
			if ferr != nil {
				return nil, err
			}
			return nil, err
		}
	}
	if err != nil {
		return nil, err
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

	return esFetchMetricFields(cfg, index)
}

// esFetchMetricFields returns every field the index pattern exposes, merged across all
// matching indices and carrying each field's type. Split from FetchMetricsLabels so the
// merge can be tested against a stub Elasticsearch without a database-backed config.
func esFetchMetricFields(cfg *ElasticsearchConfig, index string) ([]OutputMetricLabels, error) {
	// Fetch field names from the index mapping.
	resp, err := esRequest("GET", fmt.Sprintf("%s/%s/_mapping", cfg.Url, index), "", cfg) //nolint:bodyclose
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

	// Merge every index the pattern matched, and keep each field's TYPE.
	//
	// This used to `break` after the first index in the response and drop the type.
	// Both were consequential. A metrics index pattern routinely fans out over
	// per-dataset data streams — one customer estate has 64 datasets across 276
	// indices — and the order Elasticsearch returns them in is arbitrary: not
	// alphabetical, not by recency, and it can be an index holding zero documents.
	// So the caller was handed one dataset's fields chosen essentially at random and
	// told that was the index. In the traced incident that was `system.socket`, which
	// carries no `kubernetes.*` fields at all, so the agent never learned that
	// `kubernetes.pod.cpu.usage.nanocores` existed and spent 56 tool calls guessing.
	//
	// The type matters just as much: numeric fields are the metric value paths
	// (select with `exists`), keyword fields are the dimensions (select with `term`).
	// Without it the caller has to guess which is which, and guessing is what the
	// canned field catalogues were papering over.
	seen := make(map[string]bool)
	output := make([]OutputMetricLabels, 0)
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
		for _, f := range extractFieldsFromProperties(properties, "") {
			if seen[f.Field] {
				continue
			}
			seen[f.Field] = true
			attrs := f.Attributes
			if attrs == nil {
				attrs = map[string]any{}
			}
			output = append(output, OutputMetricLabels{Label: f.Field, Attributes: attrs})
		}
	}

	// Stable order, so the same estate always yields the same field list regardless of
	// the order Elasticsearch happened to enumerate its indices in.
	sort.Slice(output, func(i, j int) bool { return output[i].Label < output[j].Label })

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
			if !strings.HasSuffix(field, ".keyword") && !isOTelKeywordField(field) {
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

// isOTelKeywordField reports whether a field path is already a keyword in the
// OTel-native ES mapping (mapping.mode: otel) — resource attributes, scope
// fields and metric dimensions are all keyword at the base level, so appending
// a ".keyword" subfield (needed for legacy text fields) would target a field
// that does not exist and silently match nothing.
func isOTelKeywordField(field string) bool {
	return strings.HasPrefix(field, "resource.attributes.") ||
		strings.HasPrefix(field, "scope.") ||
		strings.HasPrefix(field, "metrics.")
}

// otelMetricLabels flattens the dimensions of an OTel-native metric doc into a
// label map: resource.attributes.* (k8s.namespace.name, k8s.pod.name, …) plus
// any per-datapoint attributes.*.
func otelMetricLabels(src map[string]any) map[string]string {
	ra, _ := nestedMap(src, "resource", "attributes")
	a, _ := src["attributes"].(map[string]any)
	labels := make(map[string]string, len(ra)+len(a))
	for k, v := range ra {
		if v != nil {
			labels[k] = fmt.Sprintf("%v", v)
		}
	}
	for k, v := range a {
		if v != nil {
			labels[k] = fmt.Sprintf("%v", v)
		}
	}
	return labels
}

// beatsLabelSkip are `kubernetes.*` subtrees that are constant per pod, node or
// namespace and repeated on every single document. They would bloat every
// series' label set without distinguishing any two series; the log pipeline
// drops the same ones for the same reason.
var beatsLabelSkip = []string{
	"kubernetes.labels.",
	"kubernetes.annotations.",
	"kubernetes.namespace_labels.",
	"kubernetes.node.labels.",
}

// beatsMetricSeries splits a Metricbeat document's `kubernetes` subtree into
// numeric leaves (one metric apiece, keyed by full dotted path) and string
// leaves (labels).
//
// Metricbeat encodes a metric's identity in its field path and carries no
// name/value pair: `kubernetes.pod.cpu.usage.nanocores` IS the metric name, and
// one document holds ~16 of them. All are emitted; callers narrow to a single
// metric with `_source` filtering on the query, which is the only selector the
// request contract offers today (`metric_name` is consumed as the ES index).
func beatsMetricSeries(src map[string]any) (labels map[string]string, values map[string]float64) {
	labels = map[string]string{}
	values = map[string]float64{}

	root, ok := src["kubernetes"].(map[string]any)
	if !ok {
		return labels, values
	}

	var walk func(node map[string]any, path string)
	walk = func(node map[string]any, path string) {
		for k, v := range node {
			p := path + "." + k
			switch tv := v.(type) {
			case map[string]any:
				walk(tv, p)
			case float64:
				values[p] = tv
			case string:
				if !hasAnyPrefix(p, beatsLabelSkip) {
					labels[p] = tv
				}
			}
		}
	}
	walk(root, "kubernetes")

	return labels, values
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
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

	// add appends one datapoint to the series identified by labels.
	add := func(labels map[string]string, ts int64, val float64) {
		keyBytes, _ := json.Marshal(labels)
		key := string(keyBytes)
		if _, exists := groups[key]; !exists {
			groups[key] = &seriesData{metric: labels}
			groupOrder = append(groupOrder, key)
		}
		groups[key].timestamps = append(groups[key].timestamps, ts)
		groups[key].values = append(groups[key].values, val)
	}

	// Drop counters — an empty metrics result carries no error, so without these
	// "matched nothing" and "matched but every hit was discarded" are the same
	// observation. Tracked per reason so the log names which shape assumption failed.
	var droppedNoTimestamp, droppedNonNumericValue int
	sampleTimestamp := ""

	for _, hit := range esResp.Hits.Hits {
		src := hit.Source

		// Parse ISO timestamp to epoch seconds (time, else @timestamp).
		timeStr, _ := src["time"].(string)
		if timeStr == "" {
			timeStr, _ = src["@timestamp"].(string)
		}
		t, err := time.Parse(time.RFC3339Nano, timeStr)
		if err != nil {
			// Record the raw value and its Go type once: a JSON number, or the
			// epoch-millis string OTel-native indices use, fails the .(string)
			// assertion or RFC3339Nano and lands here for every hit in the response.
			if sampleTimestamp == "" {
				raw, exists := src["time"]
				if !exists {
					raw = src["@timestamp"]
				}
				sampleTimestamp = fmt.Sprintf("%T(%v)", raw, raw)
			}
			droppedNoTimestamp++
			continue
		}
		ts := t.Unix()

		// OTel-native mapping (mapping.mode: otel): each doc keys its metrics by
		// name under "metrics" and carries dimensions under resource.attributes.
		// One doc can hold several metrics sharing the same labels+timestamp, so
		// emit a series per metric name.
		if metricsMap, ok := src["metrics"].(map[string]any); ok && len(metricsMap) > 0 {
			base := otelMetricLabels(src)
			for name, raw := range metricsMap {
				val, ok := raw.(float64)
				if !ok {
					droppedNonNumericValue++
					continue
				}
				labels := make(map[string]string, len(base)+1)
				for k, v := range base {
					labels[k] = v
				}
				labels["__name__"] = name
				add(labels, ts, val)
			}
			continue
		}

		// Legacy flat shape: {name, value|sum|count, attributes}.
		name, _ := src["name"].(string)
		var val float64
		var haveVal bool
		if v, ok := src["value"].(float64); ok {
			val, haveVal = v, true
		} else if v, ok := src["sum"].(float64); ok {
			val, haveVal = v, true
		} else if v, ok := src["count"].(float64); ok {
			val, haveVal = v, true
		}

		if !haveVal {
			// Beats (Metricbeat) shape: no name/value pair anywhere — every value
			// is a numeric leaf nested under `kubernetes.*`, and the metric's
			// identity IS its field path. Emit one series per leaf; narrow to a
			// single metric with `_source` filtering on the query.
			//
			// Dispatch is by shape, not by shipper. Keying off `metricset.name` or
			// `agent.type` looks tempting but breaks twice: `_source` filtering
			// strips those fields (so the documented way to select one metric would
			// disable detection), and a Beat emitting a reshaped name/value document
			// would be stolen from the flat branch below.
			labels, values := beatsMetricSeries(src)
			if len(values) == 0 {
				// Nothing recognisable. Falling through would append a datapoint of
				// 0 — indistinguishable from a real zero reading — so an unknown
				// shape rendered as a flat zero line with no error anywhere, and
				// dropped_non_numeric_value stayed 0 while the log said "parsed".
				// A value key that *is* present and holds 0 keeps haveVal=true and
				// never reaches here.
				droppedNonNumericValue++
				continue
			}
			// Map iteration order is random; sort so series order is stable.
			names := make([]string, 0, len(values))
			for n := range values {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				m := make(map[string]string, len(labels)+1)
				for k, v := range labels {
					m[k] = v
				}
				m["__name__"] = n
				add(m, ts, values[n])
			}
			continue
		}

		labels := map[string]string{}
		if name != "" {
			labels["__name__"] = name
		}
		if attrs, ok := src["attributes"].(map[string]any); ok {
			for k, v := range attrs {
				labels[k] = fmt.Sprintf("%v", v)
			}
		}
		add(labels, ts, val)
	}

	returned := len(esResp.Hits.Hits)
	switch {
	case returned == 0:
		slog.Info("ES metrics query: matched no documents",
			"matched", esHitsTotal(string(bodyBytes)), "returned", 0,
			"hint", "index or the time filter (`time` OR `@timestamp`) excludes everything; label-values aggregate with no time bound, which is why they still return data")
	case len(groups) == 0:
		slog.Warn("ES metrics query: no series parsed from hits",
			"matched", esHitsTotal(string(bodyBytes)), "returned", returned,
			"dropped_unparseable_timestamp", droppedNoTimestamp,
			"dropped_non_numeric_value", droppedNonNumericValue,
			"sample_timestamp", sampleTimestamp,
			"sample_source_fields", esSourceFieldNames(esResp.Hits.Hits[0].Source),
			"hint", "timestamps must be an RFC3339 string at `time` or `@timestamp`; values at metrics.<name>, name+value|sum|count, or numeric leaves under kubernetes.* (Metricbeat)")
	default:
		slog.Info("ES metrics query: parsed hits",
			"matched", esHitsTotal(string(bodyBytes)), "returned", returned, "series", len(groups),
			"dropped_unparseable_timestamp", droppedNoTimestamp,
			"dropped_non_numeric_value", droppedNonNumericValue)
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

// esOtlpMetricName maps abstract utilisation keys to OTLP metric names
// available in the Elasticsearch OTLP metrics index.
//
// Only metrics currently present in the ES pipeline are mapped.
// Resource request/limit and node capacity metrics are not available
// because k8sclusterreceiver metrics are not currently exported into ES.
//
// Returns ("", false) when no equivalent OTLP metric exists.
func esOtlpMetricName(key string, isNode bool) (otlpName string, found bool) {
	if isNode {
		switch key {
		case "cpu_real":
			// Available from hostmetrics/kubeletstats receiver.
			return "system.cpu.utilization", true

		case "mem_real":
			// Represents current memory usage on the node.
			return "system.memory.usage", true

			// Not currently available in ES metrics pipeline:
			// - mem_total (no node memory capacity metric)
			// - cpu_total (no node CPU capacity metric)
			// - requests/limits (requires k8sclusterreceiver)
		}
		return "", false
	}

	switch key {
	case "cpu_real":
		// Container CPU usage metric from kubeletstatsreceiver.
		return "container.cpu.usage", true

	case "mem_real":
		// Recommended container working set metric.
		return "container.memory.working_set", true

		// Not currently available in ES metrics pipeline:
		// - mem_total (container memory capacity/limit metric missing)
		// - cpu_total (k8s.container.cpu_limit not present)
		// - cpu_request
		// - cpu_limit
		// - memory_request
		// - memory_limit
		// These require k8sclusterreceiver metrics to be enabled and exported.
	}

	return "", false
}

// fetchESMetricUtilisation executes utilisation queries against Elasticsearch.
// Documents follow the OTLP/Data-Prepper schema produced by kubeletstatsreceiver:
//
//	{name, time|@timestamp, value, attributes:{metric:{attributes:{namespace,pod,container,...}},
//	 resource:{attributes:{k8s@namespace@name, k8s@pod@name, k8s@node@name, ...}}}}
//
// Abstract metric keys (cpu_real, mem_real, …) are mapped to OTLP names via esOtlpMetricName.
// Index is resolved: req.Request["metric_index"] → cfg.MetricsIndex → "metrics-*".
func fetchESMetricUtilisation(ctx *security.RequestContext, req GetUtilisationTrendRequest, meta RequestMetadata) (OutputMetricQuery, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return OutputMetricQuery{}, err
	}

	index := "metrics-*"
	if idx, ok := req.Request["metric_index"].(string); ok && idx != "" {
		index = idx
	} else if cfg.MetricsIndex != "" {
		index = cfg.MetricsIndex
	}

	isNode := meta.Kind == "node" || (meta.Namespace == "" && meta.Name == "" && meta.NodeName != "")

	var results []QueryResult
	for _, metricKey := range meta.RequestedMetrics {
		otlpName, ok := esOtlpMetricName(metricKey, isNode)
		if !ok {
			// Not available in this ES schema (e.g. K8s resource specs from kube-state-metrics).
			// Return empty payload with no error — same as a query that matches zero documents.
			results = append(results, QueryResult{QueryKey: metricKey, Payload: []Result{}})
			continue
		}

		queryBody := buildESUtilisationQuery(meta, otlpName, req.StartTime, req.EndTime)
		renderedJSON, marshalErr := json.Marshal(queryBody)
		if marshalErr != nil {
			slog.Warn("ES utilisation: failed to marshal query body", "metric", metricKey, "err", marshalErr)
		}
		renderedQuery := string(renderedJSON)

		esURL := fmt.Sprintf("%s/%s/_search", cfg.Url, index)
		slog.Info("ES utilisation query", "index", index, "url", esURL,
			"metric", metricKey, "otlp", otlpName,
			"start_ms", req.StartTime, "end_ms", req.EndTime,
			"body", renderedQuery)

		resp, reqErr := esRequestJSON("POST", esURL, queryBody, cfg) //nolint:bodyclose
		if reqErr != nil {
			errStr := fmt.Sprintf("failed to query metric %s: %v", metricKey, reqErr)
			results = append(results, QueryResult{QueryKey: metricKey, Query: renderedQuery, Error: &errStr})
			continue
		}

		bodyBytes, readErr := readResponse(resp, "utilisation query")
		if readErr != nil {
			errStr := readErr.Error()
			results = append(results, QueryResult{QueryKey: metricKey, Query: renderedQuery, Error: &errStr})
			continue
		}

		payload, parseErr := parseOtlpMetricsHits(bodyBytes)
		if parseErr != nil {
			errStr := fmt.Sprintf("failed to parse ES response for metric %s: %v", metricKey, parseErr)
			results = append(results, QueryResult{QueryKey: metricKey, Query: renderedQuery, Error: &errStr})
			continue
		}

		results = append(results, QueryResult{QueryKey: metricKey, Query: renderedQuery, Payload: payload})
	}

	return OutputMetricQuery{Results: results}, nil
}

// buildESUtilisationQuery builds an ES DSL query for a single OTLP metric name within the time
// range, filtered by the OTLP attribute paths produced by kubeletstatsreceiver / Data Prepper:
//
//	Namespace → attributes.metric.attributes.namespace
//	Workload  → attributes.metric.attributes.pod (wildcard prefix: "name-*")
//	Node      → attributes.resource.attributes.k8s@node@name
func buildESUtilisationQuery(meta RequestMetadata, otlpName string, startMs, endMs int64) map[string]any {
	filters := []any{
		map[string]any{"term": map[string]any{"name.keyword": otlpName}},
		esMetricsTimeRangeClause(startMs, endMs),
	}
	if meta.Namespace != "" {
		filters = append(filters, map[string]any{"term": map[string]any{
			"attributes.metric.attributes.namespace.keyword": meta.Namespace,
		}})
	}
	if meta.Name != "" {
		// Pod names follow {workload}-{rs-hash}-{pod-hash}; prefix wildcard covers all pods.
		filters = append(filters, map[string]any{"wildcard": map[string]any{
			"attributes.metric.attributes.pod.keyword": escapeESWildcard(meta.Name) + "-*",
		}})
	}
	if meta.NodeName != "" {
		filters = append(filters, map[string]any{"term": map[string]any{
			"attributes.resource.attributes.k8s@node@name.keyword": meta.NodeName,
		}})
	}
	return map[string]any{
		"size": 10000,
		"query": map[string]any{
			"bool": map[string]any{
				"filter": filters,
			},
		},
	}
}

// parseOtlpMetricsHits parses an ES response whose documents follow the OTLP/Data-Prepper schema.
// Attributes are nested as attributes.metric.attributes.* and attributes.resource.attributes.*.
// Each unique combination of (name + metric attrs) becomes one time-series Result.
func parseOtlpMetricsHits(bodyBytes []byte) ([]Result, error) {
	var esResp struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
			Hits []struct {
				Source map[string]any `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(bodyBytes, &esResp); err != nil {
		return nil, err
	}

	slog.Info("ES OTLP metrics hits", "total", esResp.Hits.Total.Value, "returned", len(esResp.Hits.Hits))

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
		timeStr, ok := src["time"].(string)
		if !ok || timeStr == "" {
			timeStr, _ = src["@timestamp"].(string)
		}
		// Log the first document's raw fields to diagnose field names/formats
		if len(groups) == 0 && len(groupOrder) == 0 {
			slog.Info("ES OTLP sample doc", "name", name, "time_field", src["time"], "timestamp_field", src["@timestamp"], "value_field", src["value"])
		}

		var val float64
		if v, ok := src["value"].(float64); ok {
			val = v
		} else if v, ok := src["sum"].(float64); ok {
			val = v
		} else if v, ok := src["count"].(float64); ok {
			val = v
		}

		t, err := time.Parse(time.RFC3339Nano, timeStr)
		if err != nil {
			slog.Warn("ES OTLP: skipping hit with unparseable timestamp", "timeStr", timeStr, "err", err)
			continue
		}
		ts := t.Unix()

		labels := map[string]string{}
		if name != "" {
			labels["__name__"] = name
		}

		// attributes in _source may be a flat map with dotted string keys or a nested
		// map (Data Prepper OTLP schema: metric.attributes.* / resource.attributes.*).
		// Handle both: if a top-level value is itself a map, descend one level into
		// its "attributes" child to avoid stringified map values in labels.
		if topAttrs, ok := src["attributes"].(map[string]any); ok {
			for section, sectionVal := range topAttrs {
				if sectionMap, ok := sectionVal.(map[string]any); ok {
					if innerAttrs, ok := sectionMap["attributes"].(map[string]any); ok {
						for k, v := range innerAttrs {
							labels[k] = fmt.Sprintf("%v", v)
						}
					}
				} else {
					labels[section] = fmt.Sprintf("%v", sectionVal)
				}
			}
		}

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
