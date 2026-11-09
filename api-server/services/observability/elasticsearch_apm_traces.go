package observability

// ElasticAPMTraceSource reads traces written by Elastic's own APM Server into the
// traces-apm* data streams. This is Elastic's APM schema (ECS + APM extensions) and
// shares no field names with either of the other two ES trace layouts — Data
// Prepper's otel-v1-apm-span-* (ElasticSaasTraceSource) or OTel-native traces-*.otel
// (ElasticOtelTraceSource) — so it is a SEPARATE reader, selected by the integration's
// declared trace_schema (see es_trace_schema.go).
//
// DOCUMENT SHAPE. traces-apm* holds two kinds of document, discriminated by
// processor.event, and they PARTITION the stream — nothing is stored twice. Verified
// against a live cluster (dev, APM Server 8.19.11, 166,065 docs):
//
//	                               transaction   span
//	  processor.event              105,618       60,447   (sums to the total)
//	  span.id                      present       present  (on a transaction it equals transaction.id)
//	  transaction.id               present       ABSENT
//	  parent.id                    root: absent  present
//	  transaction.name             present       ABSENT
//	  span.name                    ABSENT        present
//	  transaction.duration.us      present       ABSENT
//	  span.duration.us             ABSENT        present
//	  span.type / span.subtype     ABSENT        present
//	  span.destination.service.*   ABSENT        43% of spans
//	  event.outcome                present       present
//	  kubernetes.pod.name          present       present
//
// Two consequences the mapper depends on:
//
//   - Because the pairs above are mutually exclusive, the mapper COALESCES rather than
//     branching on processor.event, and no processor.event filter is applied. Filtering
//     to one event type would silently drop the other — 36% of the stream for spans.
//   - span.id is populated on every document, so it is the uniform span identifier.
//     transaction.id must NOT be used for that: on a span document it is absent here,
//     and where present it names the OWNING transaction, not the span.
//
// DURATION IS MICROSECONDS. transaction.duration.us / span.duration.us are µs, while
// the canonical OpenTelemetryTrace.DurationNs and both other ES readers are
// nanoseconds. apmDurationNs multiplies by 1000; a pass-through would under-report
// every latency by 1000x.
//
// _SOURCE IS NESTED, FIELD PATHS ARE FLAT. ECS documents nest in _source
// ({"trace":{"id":"…"}}), but the ES mapping exposes flat dotted field names
// ("trace.id"). So the DSL below uses dotted paths while reading a hit goes through
// apmString/apmInt64, which walk the nesting (and fall back to a literal dotted key,
// since some shippers flatten).

import (
	"encoding/json"
	"fmt"
	"strings"

	"nudgebee/services/common"
	"nudgebee/services/query"
	"nudgebee/services/security"
)

// esAPMTraceIndex is the default search target for the Elastic APM layout, used when
// neither a per-request override nor the integration's trace_index is set. APM
// Server's own default data stream is traces-apm-<namespace>.
const esAPMTraceIndex = "traces-apm*"

// ElasticAPMTraceSource implements TraceSource for user-managed Elasticsearch whose
// traces are written by Elastic APM Server (traces-apm*).
type ElasticAPMTraceSource struct{}

// elasticAPMTraceLabelMapping maps canonical frontend field names to Elastic APM field
// paths. Where a concept lives on a different field for transactions than for spans
// (name, duration), the mapping names the transaction field: it is the one used for
// filtering and sorting, which the Traces tab applies to entry spans. Reading a hit
// coalesces both — see mapAPMHitToTrace.
var elasticAPMTraceLabelMapping = map[string]string{
	"timestamp":          "@timestamp",
	"service_name":       "service.name",
	"span_name":          "transaction.name",
	"trace_id":           "trace.id",
	"span_id":            "span.id",
	"parent_span_id":     "parent.id",
	"status_code":        "event.outcome",
	"duration_ns":        "transaction.duration.us",
	"span_kind":          "processor.event",
	"http_status_code":   "http.response.status_code",
	"workload_name":      "service.name",
	"workload_namespace": "kubernetes.namespace",
	"resource":           "url.full",
	// The dependency target APM Server derives for an outgoing call ("mysql",
	// "product-catalog:8080"). Only spans carry it.
	"destination_workload_name": "span.destination.service.resource",
	// destination_workload_namespace is deliberately ABSENT: Elastic APM records no
	// namespace for the callee. Mapping it to kubernetes.namespace would make a
	// filter on "the destination's namespace" silently match the caller's instead.
	// An unmapped label passes through mapLabelToESField unchanged and matches
	// nothing, which is the honest outcome for a field this layout cannot answer.
}

func (e *ElasticAPMTraceSource) GetLabelMapping() map[string]string {
	return elasticAPMTraceLabelMapping
}

// GetSupportedOperators matches the other two ES trace readers. _regex is omitted
// because no ES trace DSL builder here handles `regexp`; advertising it would produce
// a query that silently matches nothing.
func (e *ElasticAPMTraceSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_contains", "_like", "_nlike", "_gt", "_lt", "_is_null"}
}

// esAPMTraceIndexFor mirrors esTraceIndexFor but defaults to the Elastic APM data
// stream rather than Data Prepper's index.
func esAPMTraceIndexFor(cfg *ElasticsearchConfig, override string) string {
	if override != "" {
		return override
	}
	if cfg.TraceIndex != "" {
		return cfg.TraceIndex
	}
	return esAPMTraceIndex
}

// apmString reads a dotted field path out of a nested ECS _source, e.g.
// apmString(src, "trace.id") for {"trace":{"id":"abc"}}. Falls back to the literal
// dotted key for shippers that flatten _source. Returns "" when absent.
func apmString(src map[string]any, path string) string {
	if idx := strings.LastIndexByte(path, '.'); idx >= 0 {
		if parent := getNestedMap(src, path[:idx]); parent != nil {
			if v := getStringFromMap(parent, path[idx+1:]); v != "" {
				return v
			}
		}
	}
	return getStringFromMap(src, path)
}

// apmInt64 is apmString for numeric leaves.
func apmInt64(src map[string]any, path string) int64 {
	if idx := strings.LastIndexByte(path, '.'); idx >= 0 {
		if parent := getNestedMap(src, path[:idx]); parent != nil {
			if v := getInt64FromMap(parent, path[idx+1:]); v != 0 {
				return v
			}
		}
	}
	return getInt64FromMap(src, path)
}

// apmFirstString returns the first non-empty value among the given paths, for the
// transaction-or-span pairs that never co-occur on one document.
func apmFirstString(src map[string]any, paths ...string) string {
	for _, p := range paths {
		if v := apmString(src, p); v != "" {
			return v
		}
	}
	return ""
}

// apmDurationNs converts APM's microsecond duration to the canonical nanoseconds.
func apmDurationNs(src map[string]any) int64 {
	for _, p := range []string{"transaction.duration.us", "span.duration.us"} {
		if us := apmInt64(src, p); us != 0 {
			return us * 1000
		}
	}
	return 0
}

// mapAPMOutcome converts ECS event.outcome to the canonical status vocabulary the
// other trace sources use. APM records request success/failure rather than OTel's
// status code, so "unknown" (and absent) map to Unset rather than being invented as Ok.
func mapAPMOutcome(outcome string) string {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "success":
		return "Ok"
	case "failure":
		return "Error"
	default:
		return "Unset"
	}
}

// mapAPMSpanKind derives a span kind from processor.event. APM Server does not store
// OTel's span kind: a transaction is a request this service served (Server) and a span
// is work it initiated (Client). That is the full extent of what the schema supports —
// internal spans are indistinguishable from outgoing ones here.
func mapAPMSpanKind(processorEvent string) string {
	switch processorEvent {
	case "transaction":
		return "Server"
	case "span":
		return "Client"
	default:
		return ""
	}
}

// apmAttributeMap collects the user-defined span attributes. APM keeps these in two
// dedicated namespaces rather than scattering them: labels.* (strings/bools) and
// numeric_labels.* (numbers).
func apmAttributeMap(src map[string]any) map[string]string {
	attrs := map[string]string{}
	for _, ns := range []string{"labels", "numeric_labels"} {
		for k, v := range extractStringMap(src, ns) {
			attrs[k] = v
		}
	}
	return attrs
}

// apmResourceAttributeMap collects the identity of the emitting service and its host,
// flattened to dotted keys so consumers see the same shape as the OTel reader's
// resource attributes.
func apmResourceAttributeMap(src map[string]any) map[string]string {
	attrs := map[string]string{}
	for _, path := range []string{
		"service.name", "service.version", "service.environment", "service.language.name",
		"agent.name", "agent.version",
		"host.name", "host.hostname",
		"container.id",
		"kubernetes.pod.name", "kubernetes.pod.uid", "kubernetes.namespace",
		"cloud.region", "cloud.availability_zone", "cloud.provider",
	} {
		if v := apmString(src, path); v != "" {
			attrs[path] = v
		}
	}
	return attrs
}

// mapAPMHitToTrace maps a traces-apm* _source document to OpenTelemetryTrace. It
// coalesces the transaction and span field pairs rather than branching, because the
// two never co-occur on a single document (see the table in the file header).
func mapAPMHitToTrace(src map[string]any) common.OpenTelemetryTrace {
	resourceAttrs := apmResourceAttributeMap(src)
	spanAttrs := apmAttributeMap(src)

	processorEvent := apmString(src, "processor.event")
	service := apmString(src, "service.name")

	// Reported VERBATIM, including the ":port" suffix APM Server appends
	// ("product-catalog:8080"). Stripping it here would desynchronise four views of
	// the same thing: this field is also the filter target in
	// elasticAPMTraceLabelMapping, a group-by dimension, and what GetLabelValues
	// returns from a terms aggregation — all of which read the stored value. A user
	// filtering on the displayed name would then match nothing. Normalising to a
	// workload name belongs where that join happens, not in the reader.
	destResource := apmString(src, "span.destination.service.resource")

	// A transaction describes a served request, so its URL is the resource. A span
	// describes outgoing work, where the dependency target is the closest equivalent.
	// Left EMPTY when neither exists (an internal span, a non-HTTP transaction),
	// matching the OTel reader: resource names the thing acted upon, and the span's
	// own name already has its own column, so copying it here would just make the
	// two columns duplicate each other.
	//
	// Deliberately NOT http.request.method — "GET" is a verb, not a resource.
	resource := apmFirstString(src, "url.full", "url.original")
	if resource == "" {
		resource = destResource
	}

	timestamp := normalizeESTimestamp(apmString(src, "@timestamp"))

	return common.OpenTelemetryTrace{
		TraceID:      apmString(src, "trace.id"),
		SpanID:       apmString(src, "span.id"),
		ParentSpanID: apmString(src, "parent.id"),
		SpanName:     apmFirstString(src, "transaction.name", "span.name"),
		SpanKind:     mapAPMSpanKind(processorEvent),
		ServiceName:  service,
		Timestamp:    timestamp,
		StartTime:    timestamp,
		DurationNs:   apmDurationNs(src),
		StatusCode:   mapAPMOutcome(apmString(src, "event.outcome")),
		// APM has no per-span status message; the failure detail lives in a separate
		// logs-apm.error document, which this reader does not join.
		StatusMessage:      "",
		ResourceAttributes: resourceAttrs,
		SpanAttributes:     spanAttrs,
		// APM records the pod but not the workload that owns it, so the service name is
		// the best workload identity available from a trace document alone. The pod name
		// is in ResourceAttributes for consumers that can resolve it against inventory.
		WorkloadName:        service,
		WorkloadNamespace:   apmString(src, "kubernetes.namespace"),
		Resource:            resource,
		DestinationName:     destResource,
		DestinationWorkload: destResource,
		// Elastic APM records no namespace for the CALLEE — kubernetes.namespace on
		// these documents is the caller's own. Left empty rather than filled with
		// the caller's, which would read as a real destination namespace and is the
		// kind of plausible-but-wrong value nobody re-checks.
		DestinationNamespace: "",
		HTTPStatusCode:       apmFirstString(src, "http.response.status_code"),
		Service:              service,
	}
}

// mapAPMHitToHeatmap maps a traces-apm* _source document to OpenTelemetryTraceHeatMap.
func mapAPMHitToHeatmap(src map[string]any) common.OpenTelemetryTraceHeatMap {
	return common.OpenTelemetryTraceHeatMap{
		Timestamp:          normalizeESTimestamp(apmString(src, "@timestamp")),
		TraceID:            apmString(src, "trace.id"),
		SpanID:             apmString(src, "span.id"),
		SpanName:           apmFirstString(src, "transaction.name", "span.name"),
		ServiceName:        apmString(src, "service.name"),
		DurationNs:         apmDurationNs(src),
		StatusCode:         mapAPMOutcome(apmString(src, "event.outcome")),
		ResourceAttributes: apmResourceAttributeMap(src),
		SpanAttributes:     apmAttributeMap(src),
	}
}

// apmTraceTimeRangeFilter ranges on @timestamp, which is a real ECS date field here
// (unlike the OTel-native layout's epoch-millis string). epoch_millis is accepted as
// an input format for a date field, so the request's millis need no conversion.
func apmTraceTimeRangeFilter(start, end int64) map[string]any {
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

// buildAPMTraceQuery constructs the Elastic APM DSL for trace requests. Mirrors
// buildOtelTraceQuery; no processor.event filter is applied, deliberately — see the
// file header.
func buildAPMTraceQuery(req TracesV3Request, labelMapping map[string]string) map[string]any {
	boolFilters := []map[string]any{}
	if req.StartTime > 0 && req.EndTime > 0 {
		boolFilters = append(boolFilters, apmTraceTimeRangeFilter(req.StartTime, req.EndTime))
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
		"query": map[string]any{"bool": mainBool},
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

func (e *ElasticAPMTraceSource) QueryTraces(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}

	dsl := buildAPMTraceQuery(req, elasticAPMTraceLabelMapping)

	resp, err := esRequestJSON("POST", esIndexURL(cfg.Url, esAPMTraceIndexFor(cfg, traceIndexOverride(req.Request)), "_search"), dsl, cfg) //nolint:bodyclose
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
		traces = append(traces, mapAPMHitToTrace(hit.Source))
	}
	return traces, nil
}

func (e *ElasticAPMTraceSource) GetQuery(_ *security.RequestContext, req TracesV3Request) (string, error) {
	dsl := buildAPMTraceQuery(req, elasticAPMTraceLabelMapping)
	data, err := json.MarshalIndent(dsl, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal trace query: %w", err)
	}
	return string(data), nil
}

func (e *ElasticAPMTraceSource) CountTraces(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}

	dsl := buildAPMTraceQuery(req, elasticAPMTraceLabelMapping)
	dsl["size"] = 0
	dsl["track_total_hits"] = true
	delete(dsl, "sort")
	delete(dsl, "from")

	resp, err := esRequestJSON("POST", esIndexURL(cfg.Url, esAPMTraceIndexFor(cfg, traceIndexOverride(req.Request)), "_search"), dsl, cfg) //nolint:bodyclose
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

// TraceLabelValuesAreComplete marks this source as safe for trace value validation —
// a terms aggregation with size 1000, at or above which the validator fails open.
func (e *ElasticAPMTraceSource) TraceLabelValuesAreComplete() {}

func (e *ElasticAPMTraceSource) GetLabelValues(ctx *security.RequestContext, req TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, err
	}

	esField := mapLabelToESField(req.Label, elasticAPMTraceLabelMapping)

	boolFilters := []map[string]any{}
	whereBool := buildESBoolQuery(req.QueryRequest.Where, elasticAPMTraceLabelMapping)
	if whereFilters, ok := whereBool["filter"].([]map[string]any); ok {
		boolFilters = append(boolFilters, whereFilters...)
	}
	if req.StartTime > 0 && req.EndTime > 0 {
		boolFilters = append(boolFilters, apmTraceTimeRangeFilter(req.StartTime, req.EndTime))
	}

	dsl := map[string]any{
		"size": 0,
		"aggs": map[string]any{
			"label_values": map[string]any{
				"terms": map[string]any{"field": esField, "size": 1000},
			},
		},
	}
	if len(boolFilters) > 0 {
		dsl["query"] = map[string]any{"bool": map[string]any{"filter": boolFilters}}
	}

	resp, err := esRequestJSON("POST", esIndexURL(cfg.Url, esAPMTraceIndexFor(cfg, traceIndexOverride(req.Request)), "_search"), dsl, cfg) //nolint:bodyclose
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

	values := []string{}
	if raw, ok := searchResp.Aggregations["label_values"]; ok {
		var termsAgg struct {
			Buckets []struct {
				Key      string `json:"key"`
				DocCount int    `json:"doc_count"`
			} `json:"buckets"`
		}
		if err := json.Unmarshal(raw, &termsAgg); err == nil {
			for _, b := range termsAgg.Buckets {
				values = append(values, b.Key)
			}
		}
	}

	return common.OpenTelemetryTraceLabelValues{Label: req.Label, Values: values}, nil
}

// elasticAPMTraceGroupDims are the group-by dimensions for the Elastic APM schema.
// Field precedence within each dimension mirrors mapAPMHitToTrace so a grouped row's
// identity matches the per-span mapping. http status is intentionally excluded (see
// elasticAPMHTTPStatusFields) so each row aggregates all statuses and error_count is a
// real 4xx/5xx subset.
// Every Fields list must match mapAPMHitToTrace's precedence for the same concept, so
// a grouped row's identity is the same string the per-span rows show and the same
// string a filter on that label compares against.
//
// destination_workload_namespace is absent on purpose: Elastic APM records no callee
// namespace, and grouping on kubernetes.namespace would silently group by the CALLER's.
var elasticAPMTraceGroupDims = []esGroupDim{
	{Key: "workload_name", Fields: []string{"service.name"}},
	{Key: "workload_namespace", Fields: []string{"kubernetes.namespace"}},
	{Key: "destination_workload_name", Fields: []string{"span.destination.service.resource"}},
	// No span.name fallback: mapAPMHitToTrace leaves Resource empty when there is
	// neither a URL nor a destination, so grouping on the name here would file
	// internal spans under a resource the detail view shows as blank.
	{Key: "resource", Fields: []string{"url.full", "url.original", "span.destination.service.resource"}},
	{Key: "span_name", Fields: []string{"transaction.name", "span.name"}},
}

// elasticAPMHTTPStatusFields are the candidate http-status fields used to compute the
// per-group error_count (4xx/5xx).
var elasticAPMHTTPStatusFields = []string{"http.response.status_code"}

// apmGroupDurationField is the duration field the grouping aggregations average over.
// It is transaction-only on purpose: a grouped row represents a served operation, and
// mixing in span durations would average a request against its own sub-calls.
const apmGroupDurationField = "transaction.duration.us"

// scaleAPMGroupLatenciesToNs converts the latency aggregates from the microseconds this
// layout stores into the nanoseconds TraceGroupingValues declares.
//
// This is NOT cosmetic. parseTraceGroupBucket writes the raw aggregation values into
// DurationNS / P95Latency / P99Latency / MaxLatency, and the other two ES readers
// aggregate nanosecond fields (durationInNanos, duration). Leaving these unscaled makes
// every grouped latency read 1000x too small — and 1.5ms rendering as 1.5µs looks like
// a fast service, not like a bug, so nothing downstream would flag it.
func scaleAPMGroupLatenciesToNs(rows []TraceGroupingValues) []TraceGroupingValues {
	const usToNs = 1000
	for i := range rows {
		rows[i].DurationNS *= usToNs
		rows[i].P95Latency *= usToNs
		rows[i].P99Latency *= usToNs
		rows[i].MaxLatency *= usToNs
	}
	return rows
}

func (e *ElasticAPMTraceSource) QueryGroupedTraces(ctx *security.RequestContext, req TracesV3Request) ([]TraceGroupingValues, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}

	boolFilters := []map[string]any{}
	if req.StartTime > 0 && req.EndTime > 0 {
		boolFilters = append(boolFilters, apmTraceTimeRangeFilter(req.StartTime, req.EndTime))
	}
	whereBool := buildESBoolQuery(req.QueryRequest.Where, elasticAPMTraceLabelMapping)
	if whereFilters, ok := whereBool["filter"].([]map[string]any); ok {
		boolFilters = append(boolFilters, whereFilters...)
	}

	searchURL := esIndexURL(cfg.Url, esAPMTraceIndexFor(cfg, traceIndexOverride(req.Request)), "_search")
	buckets, err := fetchAllCompositeTraceBuckets(
		ctx, cfg, searchURL, boolFilters,
		esCompositeSources(elasticAPMTraceGroupDims),
		esGroupSubAggs(apmGroupDurationField, elasticAPMHTTPStatusFields),
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
	// Scale BEFORE sorting/paginating: a latency sort must order by the same unit the
	// row reports.
	return sortAndPaginateTraceGroups(scaleAPMGroupLatenciesToNs(results), req), nil
}

func (e *ElasticAPMTraceSource) QueryGroupedTracesCount(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{}, err
	}

	boolFilters := []map[string]any{}
	if req.StartTime > 0 && req.EndTime > 0 {
		boolFilters = append(boolFilters, apmTraceTimeRangeFilter(req.StartTime, req.EndTime))
	}
	whereBool := buildESBoolQuery(req.QueryRequest.Where, elasticAPMTraceLabelMapping)
	if whereFilters, ok := whereBool["filter"].([]map[string]any); ok {
		boolFilters = append(boolFilters, whereFilters...)
	}

	dsl := map[string]any{
		"size":  0,
		"query": map[string]any{"bool": map[string]any{"filter": boolFilters}},
		"aggs": map[string]any{
			"group_count": map[string]any{
				"cardinality": map[string]any{
					"script": map[string]any{
						// Same dimension list as QueryGroupedTraces so the group count
						// matches the number of grouped rows produced.
						"source": esGroupKeyCardinalityScript(elasticAPMTraceGroupDims),
						"lang":   "painless",
					},
				},
			},
		},
	}

	resp, err := esRequestJSON("POST", esIndexURL(cfg.Url, esAPMTraceIndexFor(cfg, traceIndexOverride(req.Request)), "_search"), dsl, cfg) //nolint:bodyclose
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

func (e *ElasticAPMTraceSource) QueryTracesHeatmap(ctx *security.RequestContext, req TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error) {
	cfg, err := GetElasticsearchConfig(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}

	dsl := map[string]any{
		"size":  2000,
		"query": map[string]any{"term": map[string]any{"trace.id": req.TraceId}},
		"sort":  []map[string]any{{"@timestamp": map[string]any{"order": "asc"}}},
	}

	resp, err := esRequestJSON("POST", esIndexURL(cfg.Url, esAPMTraceIndexFor(cfg, traceIndexOverride(req.Request)), "_search"), dsl, cfg) //nolint:bodyclose
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
		heatmap = append(heatmap, mapAPMHitToHeatmap(hit.Source))
	}
	return heatmap, nil
}

// QueryRootSpansByTrace returns one root span per trace for the "By Traces" view via
// the shared queryRootSpansViaSpans reducer (same contract as the other ES readers).
func (e *ElasticAPMTraceSource) QueryRootSpansByTrace(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	return queryRootSpansViaSpans(ctx, e, req)
}

// CountTracesByTrace returns -1 (estimate) for the "By Traces" view; distinct-trace
// counting is not available cheaply here, so the frontend estimates pagination.
func (e *ElasticAPMTraceSource) CountTracesByTrace(_ *security.RequestContext, _ TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	return countTracesByTraceEstimate()
}

// QueryLabels returns an empty list: there is no generic label-key discovery API for
// this layout, so FetchTraceLabels falls back to the derived canonical + mapping set.
func (e *ElasticAPMTraceSource) QueryLabels(_ *security.RequestContext, _ FetchTraceLabelRequest) ([]OutputTraceLabel, error) {
	return []OutputTraceLabel{}, nil
}
