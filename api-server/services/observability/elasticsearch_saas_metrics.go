package observability

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/services/config"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"sort"
	"strings"
	"sync"
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

// esQueryFieldNames extracts the field names a DSL query filters on. Walks the parsed
// body looking for the leaf clauses that name a field — {"term":{"<field>":…}},
// {"exists":{"field":"<field>"}}, and friends — at any depth, so nested bool/must_not
// structures are covered.
func esQueryFieldNames(node any, out map[string]bool) {
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			switch k {
			case "term", "terms", "match", "match_phrase", "wildcard", "prefix", "regexp", "range", "fuzzy":
				// {"term": {"<field>": <value>}}
				if m, ok := child.(map[string]any); ok {
					for field := range m {
						out[field] = true
					}
				}
			case "exists":
				// {"exists": {"field": "<field>"}}
				if m, ok := child.(map[string]any); ok {
					if f, ok := m["field"].(string); ok {
						out[f] = true
					}
				}
			}
			esQueryFieldNames(child, out)
		}
	case []any:
		for _, child := range v {
			esQueryFieldNames(child, out)
		}
	}
}

// esUnknownQueryFields returns the fields a query references that the index does not
// have, ignoring any it cannot resolve.
//
// This exists because filtering on a field that is absent fails SILENTLY and in two
// opposite directions. In a `filter`/`must` an absent field matches nothing, which looks
// like "no data". In a `must_not` it excludes nothing, which is worse: the query
// degenerates to whatever remains — often just the time range — and returns a healthy
// row count that looks like the exclusion worked. One traced query excluded
// `__name__: kubernetes.proxy*` and `kubernetes.node*`, matched neither field, and
// returned 224 series that had been filtered by nothing but time.
//
// `__name__` is the common case: the backend synthesises it onto every series, so it
// appears in results and looks queryable, but no such field exists in the index.
func esUnknownQueryFields(fields []OutputMetricLabels, queryDSL string) []string {
	if len(fields) == 0 {
		// Cannot resolve the mapping — say nothing rather than warn wrongly.
		return nil
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(queryDSL), &body); err != nil {
		return nil
	}
	referenced := map[string]bool{}
	esQueryFieldNames(body, referenced)
	if len(referenced) == 0 {
		return nil
	}

	knownTypes := make(map[string]string, len(fields))
	for _, f := range fields {
		t, _ := f.Attributes["type"].(string)
		knownTypes[f.Label] = t
	}

	var unknown []string
	for f := range referenced {
		if _, ok := knownTypes[f]; ok {
			continue
		}
		// `.keyword` is only real when the parent is `text` — Elasticsearch adds that
		// subfield to text fields, not to fields already mapped as keyword. ECS maps
		// `kubernetes.namespace` and friends as plain keyword, so `<field>.keyword`
		// there resolves to nothing and matches nothing: the same silent miss that
		// motivated the unsuffixed fallback in #36408. Treating an unmapped `.keyword`
		// as valid would suppress the warning for exactly that case.
		if strings.HasSuffix(f, ".keyword") {
			if t, ok := knownTypes[strings.TrimSuffix(f, ".keyword")]; ok && t == "text" {
				continue
			}
		}
		unknown = append(unknown, f)
	}
	sort.Strings(unknown)
	return unknown
}

// resolveESMetricsIndex settles the index a metrics query runs against: the
// caller's explicit selection first, then the account's configured metrics index
// (per-account index_account_mapping override -> top-level metrics_index, both
// already folded into cfg.MetricsIndex by GetElasticsearchConfig). Returns "" when
// neither is set, which the caller reports as a required-index error. Mirrors the
// logs path in QueryLogs, which has always fallen back to cfg.LogIndex.
func resolveESMetricsIndex(requested string, cfg *ElasticsearchConfig) string {
	if trimmed := strings.TrimSpace(requested); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(cfg.MetricsIndex)
}

func (e *ElasticSaasMetricSource) FetchMetricsQuery(ctx *security.RequestContext, req FetchMetricsRequest) (OutputMetricQuery, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return OutputMetricQuery{}, err
	}

	requested := ""
	if req.Request != nil {
		requested, _ = req.Request["metric_name"].(string)
	}
	index := resolveESMetricsIndex(requested, cfg)
	if index == "" {
		return OutputMetricQuery{}, fmt.Errorf("index is required for Elasticsearch metrics query")
	}

	var results []QueryResult
	queryType, _ := req.Request["query_type"].(string)

	// Resolved at most once per request and shared by every query in it: the mapping
	// lookup is a network round trip, and req.Queries can hold several entries.
	var indexFields []OutputMetricLabels
	var fetchedIndexFields bool

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

		payload, stats, err := parseESMetricsHitsWithStats(bodyBytes, req.EndTime)
		if err != nil {
			errStr := fmt.Sprintf("failed to parse ES metrics response: %v", err)
			results = append(results, QueryResult{
				QueryKey: queryKey,
				Query:    renderedQuery,
				Error:    &errStr,
			})
			continue
		}

		// Report matched-vs-extracted separately. An empty payload has two causes that
		// look identical to the caller: the query matched nothing, or it matched
		// documents that carried no numeric value path the extractor recognised —
		// typically a `_source` projection listing only label fields such as
		// `__name__`, which excludes the very numeric paths the series are built from.
		// Measured on one customer conversation: the same query block with
		// `_source: true` yielded 6, 196 and 28 series, and with a label-only
		// projection yielded zero six times running. The agent read those zeros as
		// "this filter found nothing", discarded correct queries and reformulated.
		docsMatched := stats.DocsMatched
		qr := QueryResult{
			QueryKey:    queryKey,
			Query:       renderedQuery,
			Payload:     payload,
			DocsMatched: &docsMatched,
		}
		if len(payload) == 0 && stats.DocsMatched > 0 {
			qr.Note = esNoSeriesNote(stats)
		}
		// Flag clauses that name fields the index does not have. Absent fields fail
		// silently and in opposite directions depending on where they sit: nothing
		// matches inside a filter, nothing is excluded inside a must_not. The second
		// returns a plausible row count from a query that filtered on nothing at all,
		// so it is reported even when the payload is non-empty.
		if !fetchedIndexFields {
			indexFields, _ = esFetchMetricFields(cfg, index)
			fetchedIndexFields = true
		}
		if unknown := esUnknownQueryFields(indexFields, queryDSL); len(unknown) > 0 {
			if qr.Note != "" {
				qr.Note += " "
			}
			qr.Note += fmt.Sprintf(
				"The query references fields that do not exist in %s: %s. "+
					"Elasticsearch does not error on these — inside a filter they match nothing, and inside a "+
					"must_not they exclude nothing, so the result may look filtered when it was not. "+
					"Note that `__name__` is never a queryable field: it is this backend's label for a numeric "+
					"field path, so select that metric with {\"exists\":{\"field\":\"<path>\"}} instead.",
				index, strings.Join(unknown, ", "))
		}
		results = append(results, qr)
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

	requested := ""
	if req.Request != nil {
		requested, _ = req.Request["metric_name"].(string)
	}
	index := resolveESMetricsIndex(requested, cfg)
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

	index := resolveESMetricsIndex(req.MetricName, cfg)
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

// esNoSeriesNote explains an empty payload over a non-empty match in terms of what
// the extractor actually observed.
//
// The text this replaces asserted one cause for every case — a label-only `_source`
// projection. On the RDS conversation of 2026-08-26 that cause was wrong (the
// documents were an unsupported shape), and the agent spent 15 minutes and 86 queries
// re-running the same query with and without `_source` on its advice, then abandoned
// Elasticsearch for CloudWatch and reported a different AWS account's databases. The
// drop counters that would have said so were computed and left in the logs.
func esNoSeriesNote(stats esParseStats) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The query matched %d document(s) but no metric series could be extracted from them. "+
		"This is NOT an absence of data.", stats.DocsMatched)

	// Independent, not a switch: one response can drop documents for both reasons, and
	// a note whose whole purpose is explaining an empty result must not report one and
	// hide the other.
	if stats.DroppedNoValue > 0 {
		fmt.Fprintf(&b, " %d document(s) carried no numeric value at any path the extractor recognises, "+
			"which means their shape is not supported yet — not that the filter was wrong.", stats.DroppedNoValue)
	}
	if stats.DroppedNoTimestamp > 0 {
		fmt.Fprintf(&b, " %d document(s) had no parseable timestamp; one must be an RFC3339 string at "+
			"`time` or `@timestamp`.", stats.DroppedNoTimestamp)
	}
	if len(stats.SampleSourceFields) > 0 {
		fmt.Fprintf(&b, " Top-level fields of the first matched document: %s.",
			strings.Join(stats.SampleSourceFields, ", "))
	}
	b.WriteString(" A `_source` projection that keeps only label fields also produces this — if one is set, " +
		"re-run without it. If not, do not reformulate the query: the field list above is what needs " +
		"extractor support, and repeating the search will return zero every time.")
	return b.String()
}

// esDataStreamTypes are the data-stream types Elastic writes. Only these names are
// treated as the "{type}" of a "{type}-{dataset}-{namespace}" index.
var esDataStreamTypes = map[string]bool{
	"metrics":    true,
	"logs":       true,
	"traces":     true,
	"synthetics": true,
	"profiling":  true,
}

// esIndexDataset extracts the data-stream dataset from a hit's `_index`.
//
// Elastic data streams are named "{type}-{dataset}-{namespace}", so the dataset
// ("aws.cloudwatch_metrics", "kubernetes.state_pod", ...) is a DECLARATION of the
// document's shape rather than something to infer from its body. It is the right
// dispatch key for one reason the in-document identity fields cannot match: the
// `_source` projection that is currently the only way to narrow a query to one
// metric strips `metricset.name` / `agent.type`, but never touches `_index`.
//
// Handles the cross-cluster prefix and the hidden backing-index form:
//
//	remote-cluster:.ds-metrics-aws.cloudwatch_metrics-prod-2026.08.16-000018
//	  -> "aws.cloudwatch_metrics"
//
// Returns "" for names that are not data-stream shaped (legacy concrete indices), so
// callers fall through to the body-shape detection below.
func esIndexDataset(index string) string {
	// Index names cannot contain ':', so a colon can only be the remote-cluster
	// prefix of a cross-cluster search result.
	if i := strings.LastIndex(index, ":"); i >= 0 {
		index = index[i+1:]
	}
	// A snapshot-restored backing index carries a restore prefix before ".ds-".
	if i := strings.Index(index, ".ds-"); i >= 0 {
		index = index[i+len(".ds-"):]
	}
	index = backingIndexSuffix.ReplaceAllString(index, "")

	// "{type}-{dataset}-{namespace}": a dataset never contains '-' (it uses '.' and
	// '_'), while a namespace routinely does, so cut at the first two separators.
	//
	// The type must be one Elastic actually uses for data streams. Without that check
	// any hyphenated legacy index parses as a data stream — "metricbeat-7.17.0-2026.08.16"
	// yields dataset "7.17.0" — and a name that happened to collide with a registered
	// dataset would be dispatched to the wrong parser.
	typeEnd := strings.Index(index, "-")
	if typeEnd < 0 || !esDataStreamTypes[index[:typeEnd]] {
		return ""
	}
	rest := index[typeEnd+1:]
	datasetEnd := strings.Index(rest, "-")
	if datasetEnd <= 0 {
		return ""
	}
	return rest[:datasetEnd]
}

// esUnsupportedAggError names aggregation responses the walker does not understand.
// It is an error rather than an empty result on purpose: an aggregation Elasticsearch
// executed and we could not read is a defect on our side, and reporting it as "no
// data" is what sent one investigation through 86 queries against data that was there.
type esUnsupportedAggError struct{ names []string }

func (e *esUnsupportedAggError) Error() string {
	return fmt.Sprintf("unsupported aggregation response shape for %s — "+
		"supported: terms, date_histogram, histogram, range, filters (bucketing) and "+
		"avg, max, min, sum, value_count, cardinality, stats (values). "+
		"Rewrite the aggregation using those, or query raw documents instead",
		strings.Join(e.names, ", "))
}

// esAggTimeBucketMinMs is the epoch-millis floor above which a numeric bucket key
// accompanied by key_as_string is read as a date_histogram bucket rather than a terms
// bucket over a numeric field. 1e12 ms is 2001; no plausible terms key is that large
// while also carrying a parseable date string.
const esAggTimeBucketMinMs = 1_000_000_000_000

// parseESAggregations walks an Elasticsearch aggregation response into series.
//
// Bucket aggregations contribute labels (the agg name is the label key, the bucket key
// its value); metric aggregations contribute values, named by the agg name. A
// date_histogram contributes timestamps instead of a label, so one nested agg becomes
// a proper time series.
//
//	"inst": {"buckets": [{"key": "db-1", "m": {"buckets": [
//	    {"key": "CPUUtilization", "avg_v": {"value": 53.5}}]}}]}
//	  -> {__name__: "avg_v", inst: "db-1", m: "CPUUtilization"} = 53.5
//
// Shapes it does not recognise produce an error naming them, never an empty result.
func parseESAggregations(aggs map[string]any, fallbackTs int64) ([]Result, error) {
	var out []Result
	var unsupported []string

	// walk reports whether it emitted anything beneath this node. A bucket that
	// produced no value from a sub-aggregation is a leaf, and its value is its
	// doc_count — see the fallback below.
	var walk func(node map[string]any, labels map[string]string, ts int64) bool
	walk = func(node map[string]any, labels map[string]string, ts int64) bool {
		emitted := false
		for name, raw := range node {
			// doc_count / key / key_as_string are bucket metadata, not sub-aggregations.
			if name == "doc_count" || name == "key" || name == "key_as_string" ||
				name == "doc_count_error_upper_bound" || name == "sum_other_doc_count" {
				continue
			}
			agg, ok := raw.(map[string]any)
			if !ok {
				continue
			}

			// Bucket aggregation: recurse, extending the label set (or the timestamp).
			if buckets, isBucket := esAggBuckets(agg); isBucket {
				for _, b := range buckets {
					childLabels := make(map[string]string, len(labels)+1)
					for k, v := range labels {
						childLabels[k] = v
					}
					childTs := ts
					if bts, isTime := esAggBucketTime(b); isTime {
						// date_histogram: the bucket IS the timestamp, not a label.
						childTs = bts
					} else {
						childLabels[name] = fmt.Sprintf("%v", b["key"])
					}
					if walk(b, childLabels, childTs) {
						emitted = true
						continue
					}
					// Leaf bucket: no sub-aggregation produced a value, so the
					// bucket's own doc_count is what it measures. A bare
					// `terms` aggregation — "which datasets exist", "which
					// instances exist" — is exactly this shape, and it is the
					// most common aggregation there is. Skipping doc_count as
					// bucket metadata made every one of them return nothing.
					count, ok := b["doc_count"].(float64)
					if !ok {
						continue
					}
					m := make(map[string]string, len(childLabels)+1)
					for k, v := range childLabels {
						m[k] = v
					}
					m["__name__"] = "doc_count"
					out = append(out, Result{Metric: m, Timestamps: []int64{childTs}, Values: []float64{count}})
					emitted = true
				}
				continue
			}

			// Metric aggregation: emit one series per value it carries.
			values, isMetric := esAggValues(agg)
			if !isMetric {
				unsupported = append(unsupported, name)
				continue
			}
			for statistic, v := range values {
				m := make(map[string]string, len(labels)+2)
				for k, val := range labels {
					m[k] = val
				}
				m["__name__"] = name
				if statistic != "" {
					// Same `statistic` label the CloudWatch parser uses, so a stats
					// aggregation and a raw-document read describe series alike.
					m["statistic"] = statistic
				}
				out = append(out, Result{Metric: m, Timestamps: []int64{ts}, Values: []float64{v}})
				emitted = true
			}
		}
		return emitted
	}

	if fallbackTs <= 0 {
		fallbackTs = time.Now().UnixMilli()
	}
	_ = walk(aggs, map[string]string{}, fallbackTs/1000)

	if len(unsupported) > 0 {
		sort.Strings(unsupported)
		return out, &esUnsupportedAggError{names: unsupported}
	}
	// Map iteration is random; sort so series order is stable across calls. Keys are
	// built once rather than inside the comparator, which would format every label
	// map on each of the O(n log n) comparisons.
	keys := make([]string, len(out))
	order := make([]int, len(out))
	for i, r := range out {
		keys[i] = fmt.Sprint(r.Metric)
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return keys[order[i]] < keys[order[j]] })
	sorted := make([]Result, len(out))
	for i, idx := range order {
		sorted[i] = out[idx]
	}
	return sorted, nil
}

// esAggBuckets returns the buckets of a bucket aggregation. Elasticsearch renders them
// as an array (terms, date_histogram, histogram, range) or as a keyed object (filters,
// keyed ranges); both are accepted.
func esAggBuckets(agg map[string]any) ([]map[string]any, bool) {
	raw, ok := agg["buckets"]
	if !ok {
		return nil, false
	}
	switch b := raw.(type) {
	case []any:
		out := make([]map[string]any, 0, len(b))
		for _, item := range b {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out, true
	case map[string]any:
		out := make([]map[string]any, 0, len(b))
		for key, item := range b {
			if m, ok := item.(map[string]any); ok {
				// Keyed buckets carry their name as the map key, not a "key" field.
				if _, has := m["key"]; !has {
					m["key"] = key
				}
				out = append(out, m)
			}
		}
		return out, true
	}
	return nil, false
}

// esAggBucketTime reports whether a bucket is a date_histogram bucket, and its epoch
// seconds. A numeric key large enough to be epoch millis AND carrying key_as_string is
// a time bucket; a terms aggregation over a numeric field has no key_as_string.
func esAggBucketTime(b map[string]any) (int64, bool) {
	if _, hasStr := b["key_as_string"].(string); !hasStr {
		return 0, false
	}
	k, ok := b["key"].(float64)
	if !ok || k < esAggTimeBucketMinMs {
		return 0, false
	}
	return int64(k) / 1000, true
}

// esAggValues extracts the numeric results of a metric aggregation, keyed by statistic
// name ("" for single-value aggregations like avg/max/sum/value_count/cardinality).
// Returns false for anything else, so the caller can report it rather than guess.
func esAggValues(agg map[string]any) (map[string]float64, bool) {
	if v, ok := agg["value"]; ok {
		if v == nil {
			// A null value is a real answer: the aggregation matched no documents.
			// nil rather than an empty map — this is the common path on a filter that
			// excludes everything, and it allocates nothing.
			return nil, true
		}
		f, isNum := v.(float64)
		if !isNum {
			// Present, non-null and not a number: a shape we do not understand.
			// Reporting it as "no documents" would be this change's own defect,
			// one level up.
			return nil, false
		}
		return map[string]float64{"": f}, true
	}
	// stats / extended_stats: several numbers under one name.
	out := map[string]float64{}
	for _, k := range []string{"count", "min", "max", "avg", "sum"} {
		if f, ok := agg[k].(float64); ok {
			out[k] = f
		}
	}
	if len(out) > 0 {
		return out, true
	}
	return nil, false
}

// esSeriesPoint is one (labels, value) pair extracted from a single document.
type esSeriesPoint struct {
	Labels map[string]string
	Value  float64
}

// esDatasetParsers maps a data-stream dataset to the extractor for its shape.
//
// Adding a source is an entry here plus its parser — not another shape guess
// appended to the if-chain in parseESMetricsHitsWithStats. A dataset absent from
// this map falls through to that chain unchanged, so existing callers are
// unaffected. A parser returns ok=false when the document does not match the
// shape its dataset implies, which also falls through rather than dropping.
var esDatasetParsers = map[string]func(src map[string]any) (points []esSeriesPoint, ts int64, ok bool){
	"aws.cloudwatch_metrics": awsCloudwatchMetricsetSeries,
}

// awsCloudwatchMetricsetSeries reads the Elastic AWS "cloudwatch_metrics" shape:
//
//	metricset: {metric_name, dimensions{...}, unit, timestamp,
//	            value{count, max, min, sum}}
//
// Two details that a scalar-value assumption gets wrong:
//
// CloudWatch metric streams deliver a StatisticSet per period, not a single reading,
// so one document yields three series — avg (sum/count), max and min — separated by a
// `statistic` label. Collapsing to one loses the peak, which is the number that
// matters on saturation metrics like CPUUtilization.
//
// `metricset.timestamp` is when the metric was observed; `@timestamp` is when it was
// ingested, ~3 minutes later on sampled documents. Charting on `@timestamp` buckets
// every metric of one ingest batch onto the same instant.
//
// The same metric arrives at several dimension granularities (DBInstanceIdentifier,
// DBClusterIdentifier+Role, EngineName, DatabaseClass). Each dimension set is its own
// series; callers must filter by dimension or they will double count.
func awsCloudwatchMetricsetSeries(src map[string]any) ([]esSeriesPoint, int64, bool) {
	ms, ok := src["metricset"].(map[string]any)
	if !ok {
		return nil, 0, false
	}
	name, _ := ms["metric_name"].(string)
	if name == "" {
		return nil, 0, false
	}

	base := map[string]string{"__name__": name}
	// "None" is CloudWatch's placeholder for dimensionless metrics; carrying it as a
	// label would just add a constant that distinguishes no two series.
	if unit, _ := ms["unit"].(string); unit != "" && unit != "None" {
		base["unit"] = unit
	}
	if dims, ok := ms["dimensions"].(map[string]any); ok {
		for k, v := range dims {
			// Dimensions are part of a series' identity, so a non-string value is
			// still rendered rather than dropped — dropping one would merge two
			// distinct series and misreport both. A null dimension is absent,
			// though, and must not become the literal string "<nil>".
			switch dv := v.(type) {
			case string:
				base[k] = dv
			case nil:
			default:
				base[k] = fmt.Sprintf("%v", dv)
			}
		}
	}

	var ts int64
	if tsStr, _ := ms["timestamp"].(string); tsStr != "" {
		if t, err := time.Parse(time.RFC3339Nano, tsStr); err == nil {
			ts = t.Unix()
		}
	}

	point := func(statistic string, val float64) esSeriesPoint {
		labels := make(map[string]string, len(base)+1)
		for k, v := range base {
			labels[k] = v
		}
		if statistic != "" {
			labels["statistic"] = statistic
		}
		return esSeriesPoint{Labels: labels, Value: val}
	}

	// Scalar form: some streams emit a plain number rather than a StatisticSet.
	if v, ok := ms["value"].(float64); ok {
		return []esSeriesPoint{point("", v)}, ts, true
	}

	stat, ok := ms["value"].(map[string]any)
	if !ok {
		return nil, 0, false
	}
	sum, hasSum := stat["sum"].(float64)
	count, hasCount := stat["count"].(float64)
	maxVal, hasMax := stat["max"].(float64)
	minVal, hasMin := stat["min"].(float64)

	points := make([]esSeriesPoint, 0, 4)
	// count is the number of raw samples folded into the period; a zero count is a
	// period with no observation, not an average of zero.
	if hasSum && hasCount && count > 0 {
		points = append(points, point("avg", sum/count))
	}
	if hasMax {
		points = append(points, point("max", maxVal))
	}
	if hasMin {
		points = append(points, point("min", minVal))
	}
	// sum is the meaningful statistic for Count-unit metrics — Deadlocks,
	// Aurora_pq_request_attempted, AuroraNumOomRecoveryTriggered are all in one
	// customer's live sample. Emitting only avg/max/min made "how many deadlocks in
	// the last hour" unanswerable, and worse, answerable wrongly: {count:2, sum:5}
	// renders as avg 2.5, a plausible number that means nothing.
	if hasSum {
		points = append(points, point("sum", sum))
	}
	if len(points) == 0 {
		return nil, 0, false
	}
	return points, ts, true
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

// genericMetricSkip are top-level branches that describe a document rather than
// measure anything. Walking them would emit series for log offsets, event durations
// and agent metadata alongside the real metrics.
var genericMetricSkip = map[string]bool{
	"@timestamp":    true,
	"@version":      true,
	"time":          true,
	"timestamp":     true,
	"datetime":      true,
	"tags":          true,
	"ecs":           true,
	"agent":         true,
	"elastic_agent": true,
	"event":         true,
	"log":           true,
	"data_stream":   true,
	"stream":        true,
	"input":         true,
	"error":         true,
}

// genericLabelSkip are path fragments whose leaves are per-resource constants. They
// are labels at best and repeated on every document, so they bloat every series'
// label set without distinguishing any two — the same reason beatsLabelSkip exists.
var genericLabelSkip = []string{"labels", "annotations", "namespace_labels"}

// genericNumericLeafSeries is the last resort before a document is dropped: walk the
// whole `_source` and treat every numeric leaf as a metric keyed by its dotted path,
// every string leaf as a label.
//
// It exists so an unrecognised shape degrades to "here is what we found" instead of
// "0", which is indistinguishable from an idle database and is what sent one
// investigation to the wrong AWS account. It is deliberately the LAST branch: a
// registered dataset parser produces better names, correct statistics and the right
// timestamp, and this one produces none of that. A non-zero fallback count in the
// logs is the signal that a shape has earned a real parser.
func genericNumericLeafSeries(src map[string]any) (labels map[string]string, values map[string]float64) {
	labels = map[string]string{}
	values = map[string]float64{}
	walkGenericLeaves(src, "", labels, values)
	return labels, values
}

// walkGenericLeaves is the recursive half of genericNumericLeafSeries, at package
// level rather than a closure inside it: a self-referencing closure cannot be stack
// allocated, and this runs once per document — 10,000 allocations on a full response
// for a context that never varies.
//
// An empty path marks the document root, where the branches that describe the document
// rather than measure anything are dropped.
func walkGenericLeaves(node map[string]any, path string, labels map[string]string, values map[string]float64) {
	atRoot := path == ""
	for k, v := range node {
		if atRoot && genericMetricSkip[k] {
			continue
		}
		p := k
		if !atRoot {
			p = path + "." + k
		}
		switch tv := v.(type) {
		case map[string]any:
			// An Elasticsearch histogram is an object, not a leaf: {values, counts}.
			// Recognising it here rather than recursing keeps the paired arrays
			// together, which is the only way the numbers mean anything.
			if hist, ok := esHistogramValues(tv); ok {
				for stat, n := range hist {
					values[p+"."+stat] = n
				}
				continue
			}
			walkGenericLeaves(tv, p, labels, values)
		case float64:
			values[p] = tv
		case []any:
			for stat, n := range esNumericArrayStats(tv) {
				values[p+"."+stat] = n
			}
		case string:
			if !containsLabelSkip(p, genericLabelSkip) {
				labels[p] = tv
			}
		}
	}
}

// esHistogramValues reduces an Elasticsearch `histogram` field to scalars.
//
// The type is {"values": [...bucket centres...], "counts": [...frequencies...]} — two
// parallel arrays that only mean something together. It is a datatype, not a vendor
// schema: APM stores transaction latency this way, and so does anything else using a
// histogram field. Skipping it, as the walk did, silently discarded the single most
// useful metric an APM deployment produces.
//
// Reduced to a count, a sum and a weighted average rather than expanded per bucket:
// a bucket-per-series would multiply cardinality by the bucket count for a shape we
// have no name for, and an average is what a caller asking "how slow is it" wants.
// Returns false for anything that is not the paired-array form.
func esHistogramValues(node map[string]any) (map[string]float64, bool) {
	rawValues, hasValues := node["values"].([]any)
	rawCounts, hasCounts := node["counts"].([]any)
	if !hasValues || !hasCounts || len(rawValues) != len(rawCounts) || len(rawValues) == 0 {
		return nil, false
	}

	var totalCount, weighted float64
	for i := range rawValues {
		centre, okV := rawValues[i].(float64)
		count, okC := rawCounts[i].(float64)
		if !okV || !okC {
			return nil, false
		}
		totalCount += count
		weighted += centre * count
	}
	if totalCount == 0 {
		// A histogram with no observations. Real, and not an average of zero.
		return map[string]float64{"count": 0}, true
	}
	return map[string]float64{
		"count": totalCount,
		"sum":   weighted,
		"avg":   weighted / totalCount,
	}, true
}

// esNumericArrayStats reduces an array of numbers to scalars.
//
// Arrays were skipped entirely, so any metric stored as a list produced nothing at
// all. One series per element is not an option — the index carries no meaning and the
// element count varies per document — so the array is summarised. Non-numeric arrays
// (tags, ip addresses) yield nothing, which is correct: they are not measurements.
func esNumericArrayStats(arr []any) map[string]float64 {
	var sum, minV, maxV float64
	var count int
	for _, item := range arr {
		f, ok := item.(float64)
		if !ok {
			continue
		}
		if count == 0 {
			minV, maxV = f, f
		} else if f < minV {
			minV = f
		} else if f > maxV {
			maxV = f
		}
		sum += f
		count++
	}
	if count == 0 {
		// Not a numeric array — tags, ip addresses. nil rather than an empty map:
		// this is the common case on a document full of string arrays, and it
		// should cost nothing.
		return nil
	}
	return map[string]float64{
		"count": float64(count),
		"sum":   sum,
		"min":   minV,
		"max":   maxV,
		"avg":   sum / float64(count),
	}
}

// containsLabelSkip reports whether any skip name appears in p as a whole path
// segment.
//
// It walks p segment by segment rather than testing decorated substrings. Two
// reasons, in order:
//
// Correctness. Substring tests over "."+skip / skip+"." misfire on names that
// merely contain a skip word — `strings.Contains("my_labels.value", "labels.")`
// is true, so that path would be dropped as a constant when it is a real label.
// The form this replaced had the mirror-image bug: it matched only ".labels.",
// so a root `labels` map ("labels.app", no leading dot) was never skipped and
// every top-level label became a series label.
//
// Allocation. Comparing segments needs no concatenation, so this allocates
// nothing per call — it runs once per string leaf per document, and a 10k-hit
// response is millions of calls.
func containsLabelSkip(p string, skips []string) bool {
	for len(p) > 0 {
		segment := p
		if i := strings.IndexByte(p, '.'); i >= 0 {
			segment, p = p[:i], p[i+1:]
		} else {
			p = ""
		}
		for _, skip := range skips {
			if segment == skip {
				return true
			}
		}
	}
	return false
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
// esParseStats is what the extractor observed, so callers can tell "nothing matched"
// from "documents matched but nothing was extractable from them". These numbers were
// already computed for the log lines below; they were simply never returned.
type esParseStats struct {
	DocsMatched  int64
	HitsReturned int
	SeriesParsed int
	// Drop counters and the first document's field list. These were already computed
	// for the log lines below but never returned, so a caller seeing zero series had
	// to guess why — and the fixed note it got guessed wrong (see esNoSeriesNote).
	DroppedNoTimestamp int
	DroppedNoValue     int
	SampleSourceFields []string
}

func parseESMetricsHits(bodyBytes []byte) ([]Result, error) {
	res, _, err := parseESMetricsHitsWithStats(bodyBytes, 0)
	return res, err
}

// parseESMetricsHitsWithStats decodes an ES _search response into series.
//
// queryEndMs stamps aggregation results that carry no time bucket of their own
// (a bare `terms` agg is a single reading over the query window). Pass 0 when
// there is no window; the current time is used.
func parseESMetricsHitsWithStats(bodyBytes []byte, queryEndMs int64) ([]Result, esParseStats, error) {
	var esResp struct {
		Hits struct {
			Hits []struct {
				// Index is the data-stream backing index the hit came from. It is
				// decoded because the index NAME declares the document's shape
				// (see esIndexDataset) and, unlike the in-document identity fields,
				// no `_source` projection can strip it.
				Index  string         `json:"_index"`
				Source map[string]any `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
		// Aggregations was absent from this struct, so encoding/json dropped it as an
		// unknown key: Elasticsearch computed the aggregation, returned it, and the
		// caller was told `total_series: 0`. An agent that reached for the only
		// strategy that scales — one terms aggregation instead of 10k raw documents —
		// got nothing back and fell into fetch-and-truncate.
		Aggregations map[string]any `json:"aggregations"`
	}
	if err := json.Unmarshal(bodyBytes, &esResp); err != nil {
		return nil, esParseStats{}, err
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
	// fallbackHits counts documents only the generic numeric-leaf walk could read.
	// A non-zero count names a shape that deserves an esDatasetParsers entry.
	var fallbackHits int
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

		// Dataset-declared shape first: the index name states what this document is
		// (see esIndexDataset), so no body sniffing is needed and a `_source`
		// projection cannot hide it. Datasets with no registered parser — and
		// documents that do not match the shape their dataset implies — fall through
		// to the detection chain below unchanged.
		if parse, known := esDatasetParsers[esIndexDataset(hit.Index)]; known {
			if points, datasetTs, matched := parse(src); matched {
				// The shape carries its own observation time; the generic `ts` above
				// is the ingest time for these documents.
				if datasetTs > 0 {
					ts = datasetTs
				}
				for _, pt := range points {
					add(pt.Labels, ts, pt.Value)
				}
				continue
			}
		}

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
				// Catch-all: no known shape matched, so walk the document for numeric
				// leaves rather than dropping it. The logs path has done this since
				// sourceAsMessage ("render the whole _source ... instead of silently
				// returning nothing"); metrics did not, and every shape we had not
				// met returned zero — 84,478 documents discarded in 19 minutes on
				// 2026-08-26, read by the agent as "this database has no data".
				//
				// Generic names ("system.cpu.total.pct") are worse than a purpose-built
				// parser's, which is why esDatasetParsers is still the first choice.
				// They are enormously better than nothing: the caller can see what
				// exists and say so.
				if !config.Config.FeatureESMetricsGenericFallbackEnabled {
					droppedNonNumericValue++
					continue
				}
				labels, values = genericNumericLeafSeries(src)
				if len(values) == 0 {
					// Genuinely no number anywhere. Falling through would append a
					// datapoint of 0 — indistinguishable from a real zero reading — so
					// an unknown shape rendered as a flat zero line with no error
					// anywhere, and dropped_non_numeric_value stayed 0 while the log
					// said "parsed". A value key that *is* present and holds 0 keeps
					// haveVal=true and never reaches here.
					droppedNonNumericValue++
					continue
				}
				fallbackHits++
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
			"sample_index", esResp.Hits.Hits[0].Index,
			"sample_dataset", esIndexDataset(esResp.Hits.Hits[0].Index),
			"hint", "timestamps must be an RFC3339 string at `time` or `@timestamp`; values at metrics.<name>, name+value|sum|count, numeric leaves under kubernetes.* (Metricbeat), or a dataset registered in esDatasetParsers")
	default:
		slog.Info("ES metrics query: parsed hits",
			"matched", esHitsTotal(string(bodyBytes)), "returned", returned, "series", len(groups),
			"dropped_unparseable_timestamp", droppedNoTimestamp,
			"dropped_non_numeric_value", droppedNonNumericValue,
			"generic_fallback_hits", fallbackHits)
	}
	// A shape only the generic walk could read is one esDatasetParsers should own:
	// the caller gets dotted-path names and no statistics until it does. Logged at
	// WARN with the field list so the shape is identifiable from the log alone.
	if fallbackHits > 0 && returned > 0 {
		slog.Warn("ES metrics query: shape read by generic fallback",
			"hits", fallbackHits, "returned", returned,
			"sample_index", esResp.Hits.Hits[0].Index,
			"sample_dataset", esIndexDataset(esResp.Hits.Hits[0].Index),
			"sample_source_fields", esSourceFieldNames(esResp.Hits.Hits[0].Source),
			"hint", "add a parser for this dataset to esDatasetParsers for real metric names, statistics and observation timestamps")
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

	// Aggregations, if any. A size:0 aggregation query has no hits by design, so
	// without this the response looks identical to "matched nothing".
	if len(esResp.Aggregations) > 0 {
		aggResults, aggErr := parseESAggregations(esResp.Aggregations, queryEndMs)
		results = append(aggResults, results...)
		if aggErr != nil {
			// Surface it: an aggregation we executed and could not read is our defect,
			// and returning an empty result instead would repeat the failure this
			// whole path exists to end. Any series we DID read are still returned.
			return results, esParseStats{
				DocsMatched:  esHitsTotal(string(bodyBytes)),
				HitsReturned: returned,
				SeriesParsed: len(results),
			}, aggErr
		}
	}

	var sampleFields []string
	if returned > 0 {
		sampleFields = esSourceFieldNames(esResp.Hits.Hits[0].Source)
	}
	return results, esParseStats{
		DocsMatched:        esHitsTotal(string(bodyBytes)),
		HitsReturned:       returned,
		SeriesParsed:       len(results),
		DroppedNoTimestamp: droppedNoTimestamp,
		DroppedNoValue:     droppedNonNumericValue,
		SampleSourceFields: sampleFields,
	}, nil
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

	scope := esUtilisationScope(meta)
	stepSec := esUtilStepSeconds(meta, req.StartTime, req.EndTime)

	// One request per metric key, run concurrently. They were sequential, and on a
	// real cluster each takes ~250ms, so the fourteen keys the utilisation panel asks
	// for cost ~3.5s of round-trips before the page can draw anything. The queries are
	// independent — separate keys, separate aggregations — so the only reason they
	// were serial was the loop.
	//
	// Bounded so a wide request cannot open an unbounded fan-out against a customer's
	// cluster; the panel asks for fourteen, and esUtilisationConcurrency keeps that to
	// a couple of waves.
	results := make([]QueryResult, len(meta.RequestedMetrics))
	var wg sync.WaitGroup
	sem := make(chan struct{}, esUtilisationConcurrency)

	// Keys that resolve to the same candidate sources produce byte-identical search
	// bodies and differ only in which reduction they read back — cpu_real, p50_cpu,
	// p90_cpu and max_usage_cpu are one query, not four. Group them so each distinct
	// search is issued once. On the panel's fourteen keys this is six searches.
	groups := map[string]*esUtilQueryGroup{}
	var groupOrder []string
	plans := make([]esUtilMetric, len(meta.RequestedMetrics))

	// Every plan is resolved before any body is rendered, because the pod join below has
	// to run first: it fills meta.NodePods, which changes the filters those bodies carry.
	// Rendering first and joining second would silently produce the pre-join queries.
	resolved := make([]bool, len(meta.RequestedMetrics))
	for idx, metricKey := range meta.RequestedMetrics {
		plan, ok := esUtilisationMetric(metricKey, scope, meta.ContainerName != "")
		if !ok {
			// No equivalent in any Elasticsearch layout. Say so: the old code returned
			// an empty payload here, which the caller could not tell apart from a
			// cluster that simply has no data for a metric we do support.
			results[idx] = QueryResult{
				QueryKey: metricKey,
				Payload:  []Result{},
				Note:     esUtilUnsupportedNote(metricKey, scope),
			}
			continue
		}
		plans[idx] = plan
		resolved[idx] = true
	}

	// One join for the whole request: node-scoped keys whose source carries no node
	// field (requests and limits, from state_container) are narrowed by the pods on the
	// node instead. Done once here rather than per key, and only when a candidate needs it.
	if esScopeNeedsPodJoin(plans, meta, scope) {
		pods, truncated, joinErr := esPodsOnNode(ctx, cfg, index, meta, req.StartTime, req.EndTime)
		switch {
		case joinErr != nil:
			// NodePods stays empty, which leaves those sources unusable at this scope —
			// the affected lines stay absent rather than showing the cluster total.
			ctx.GetLogger().Warn("ES utilisation: node pod join failed; node-less sources omitted",
				"node", meta.NodeName, "err", joinErr)
		case truncated:
			// A short pod list under-counts the node without looking like it did.
			ctx.GetLogger().Warn("ES utilisation: node pod list truncated by terms cap",
				"node", meta.NodeName, "cap", esUtilTermsSize, "pods", len(pods))
			meta.NodePods = pods
		default:
			meta.NodePods = pods
		}
	}

	for idx, metricKey := range meta.RequestedMetrics {
		if !resolved[idx] {
			continue
		}
		plan := plans[idx]

		// The grouping key is the rendered query itself, not a summary of the fields
		// that went into it. A hand-listed signature has to be updated every time
		// esMetricSource grows a field, and forgetting one groups two keys that need
		// different searches — they would then share a single response, and one of
		// them would be answered by the other's query. Keying on the bytes that will
		// actually be sent makes that impossible to get wrong. json.Marshal sorts map
		// keys, so equal bodies render to equal strings.
		body := buildESUtilisationAggQuery(plan, meta, scope, req.StartTime, req.EndTime, stepSec)
		rendered, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			errStr := fmt.Sprintf("failed to marshal query for metric %s: %v", metricKey, marshalErr)
			results[idx] = QueryResult{QueryKey: metricKey, Error: &errStr}
			continue
		}
		sig := string(rendered)
		g, seen := groups[sig]
		if !seen {
			g = &esUtilQueryGroup{body: body, rendered: sig}
			groups[sig] = g
			groupOrder = append(groupOrder, sig)
		}
		g.members = append(g.members, idx)
	}

	for _, sig := range groupOrder {
		group := groups[sig]
		members := group.members
		// Every member rendered this identical body, so any one of them names it.
		plan := plans[members[0]]
		metricKey := meta.RequestedMetrics[members[0]]

		wg.Add(1)
		go func(group *esUtilQueryGroup, members []int, metricKey string, plan esUtilMetric) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			queryBody, renderedQuery := group.body, group.rendered

			esURL := fmt.Sprintf("%s/%s/_search", cfg.Url, index)
			slog.Info("ES utilisation query", "index", index, "url", esURL,
				"metric", metricKey, "scope", scope, "sources", len(plan.Sources), "step_sec", stepSec,
				"start_ms", req.StartTime, "end_ms", req.EndTime)

			resp, reqErr := esRequestJSON("POST", esURL, queryBody, cfg) //nolint:bodyclose
			if reqErr != nil {
				errStr := fmt.Sprintf("failed to query metric %s: %v", metricKey, reqErr)
				esUtilAssignError(results, meta.RequestedMetrics, members, renderedQuery, errStr)
				return
			}

			bodyBytes, readErr := readResponse(resp, "utilisation query")
			if readErr != nil {
				errStr := readErr.Error()
				esUtilAssignError(results, meta.RequestedMetrics, members, renderedQuery, errStr)
				return
			}

			// Decode the envelope once; every member then reads it with its own
			// reduction rather than re-unmarshalling the same bytes per key.
			var aggResp esUtilAggResponse
			if unmarshalErr := json.Unmarshal(bodyBytes, &aggResp); unmarshalErr != nil {
				errStr := fmt.Sprintf("failed to parse ES response: %v", unmarshalErr)
				esUtilAssignError(results, meta.RequestedMetrics, members, renderedQuery, errStr)
				return
			}

			// One response, read once per member with that member's own reduction.
			for _, m := range members {
				memberPlan := plans[m]
				memberKey := meta.RequestedMetrics[m]
				out, parseErr := parseESUtilisationResponse(&aggResp, memberPlan, meta)
				if parseErr != nil {
					errStr := fmt.Sprintf("failed to parse ES response for metric %s: %v", memberKey, parseErr)
					results[m] = QueryResult{QueryKey: memberKey, Query: renderedQuery, Error: &errStr}
					continue
				}
				matched := out.DocsMatched
				qr := QueryResult{QueryKey: memberKey, Query: renderedQuery, Payload: out.Series, DocsMatched: &matched}
				switch {
				case len(out.Series) == 0:
					qr.Payload = []Result{}
					qr.Note = esUtilNoDataNote(memberPlan, matched)
					slog.Info("ES utilisation: no series", "metric", memberKey, "scope", scope, "docs_matched", matched)
				case out.TruncatedDocs > 0:
					// The cap dropped series from the sum, so the number reads low.
					// Say so on the result: logging it alone leaves a wrong figure
					// looking authoritative in the UI.
					field := memberPlan.Sources[out.SourceIdx].Field
					qr.Note = esUtilTruncatedNote(field, out.TruncatedDocs)
					slog.Warn("ES utilisation: series truncated by terms cap",
						"metric", memberKey, "scope", scope, "field", field,
						"cap", esUtilTermsSize, "dropped_docs", out.TruncatedDocs)
				default:
					slog.Info("ES utilisation: series resolved", "metric", memberKey, "scope", scope,
						"source", memberPlan.Sources[out.SourceIdx].Field, "points", len(out.Series[0].Values))
				}
				results[m] = qr
			}
		}(group, members, metricKey, plan)
	}
	wg.Wait()

	return OutputMetricQuery{Results: results}, nil
}
