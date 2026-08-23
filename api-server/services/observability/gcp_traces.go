package observability

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nudgebee/services/cloud"
	"nudgebee/services/common"
	"nudgebee/services/query"
	"nudgebee/services/security"
)

// GcpTraceSource routes trace queries for GCP cloud accounts through the cloud package's
// Cloud Trace client (cloud.QueryTracesList), and adapts them to the unified TraceSource
// interface so GCP traces flow through the same actions (traces_list / traces_counts /
// traces_get_heatmap / traces_label_values) as every other provider — including the LLM
// traces agent. Cloud Trace has no rich query language, so filtering is limited to a time
// window and an optional Cloud Run service; grouping is not supported yet.
type GcpTraceSource struct{}

// buildCloudTracesRequest maps a unified TracesV3Request onto the cloud package's request:
// the time window (unix ms -> time.Time), the UI filters translated to a Cloud Trace
// filter string, a trace-id (routed to GetTrace), the sort, and a limit.
func (s *GcpTraceSource) buildCloudTracesRequest(req TracesV3Request) cloud.QueryTracesRequest {
	where := req.QueryRequest.Where
	// Only a pushable workload becomes ServiceName; QueryTracesList turns a non-empty
	// ServiceName into a `service.name:<v>` filter, which for a Cloud SQL instance id
	// matches nothing. Those are scoped in memory by QueryTraces instead.
	serviceName := ""
	if w := requestedWorkload(where); pushableAsServiceName(w) {
		serviceName = w
	}
	q := cloud.TracesQuery{
		ServiceName: serviceName,
		OrderBy:     gcpTraceOrderBy(req.QueryRequest.OrderBy),
		// "Search By Trace Id" — route to Cloud Trace GetTrace (single trace) instead of
		// a window scan; the collector honors Query.TraceId directly.
		TraceId: extractTraceStringFilter(where, "trace_id"),
		// Span name / HTTP status / resource filters, pushed to the source as a Cloud
		// Trace filter string (empty when none selected).
		Filter: gcpCloudTraceFilter(where),
	}
	if req.StartTime > 0 {
		t := time.UnixMilli(req.StartTime).UTC()
		q.StartTime = &t
	}
	if req.EndTime > 0 {
		t := time.UnixMilli(req.EndTime).UTC()
		q.EndTime = &t
	}
	if req.QueryRequest.Limit > 0 {
		l := int64(req.QueryRequest.Limit)
		q.Limit = &l
	}
	return cloud.QueryTracesRequest{AccountId: req.AccountId, Query: q}
}

func (s *GcpTraceSource) QueryTraces(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	spans, err := cloud.QueryTracesList(ctx, s.buildCloudTracesRequest(req))
	if err != nil {
		return nil, err
	}
	return mapCloudSpansToOTelTraces(scopeUnpushedWorkload(spans, req.QueryRequest.Where)), nil
}

// gcpTraceOrderBy maps the unified sort request to a Cloud Trace ListTraces
// order_by clause. Ordering is applied at the source (not in memory) so a limited
// query returns the correct page of traces — Cloud Trace's default order is by
// trace_id, so an in-memory sort would only reorder an arbitrary (oldest-by-id)
// page and never surface the most recent traces. Cloud Trace v1 supports the
// fields: trace_id, name, duration, start. Defaults to most-recent-first.
func gcpTraceOrderBy(orderBy []query.QueryOrderBy) string {
	col, dir := "start", "desc"
	if len(orderBy) > 0 && orderBy[0].Column != "" {
		if strings.EqualFold(string(orderBy[0].Order), "asc") {
			dir = "asc"
		}
		switch orderBy[0].Column {
		case "duration_ns", "duration":
			col = "duration"
		default: // timestamp
			col = "start"
		}
	}
	return col + " " + dir
}

// GetQuery returns the provider-native Cloud Trace filter that would be applied (empty when
// no service is selected). Surfaced by the traces_get_query action for transparency.
func (s *GcpTraceSource) GetQuery(_ *security.RequestContext, req TracesV3Request) (string, error) {
	svc := extractTraceStringFilter(req.QueryRequest.Where, "workload_name", "service_name")
	if svc == "" {
		return "", nil
	}
	return fmt.Sprintf(`service.name:%s`, svc), nil
}

func (s *GcpTraceSource) CountTraces(_ *security.RequestContext, _ TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	// Cloud Trace has no count API — a real total means paginating every page. A count
	// derived from the fetched page is only ever <= limit (misleading), and doing it
	// costs a second full ListTraces round-trip per tab load. Return the estimate
	// contract (Count = -1) the frontend already handles for Jaeger instead.
	return countTracesByTraceEstimate()
}

// GetLabelValues backs the trace filter dropdowns. The only meaningful facet for Cloud
// Trace today is the Cloud Run service, derived from a sample of in-window spans.
func (s *GcpTraceSource) GetLabelValues(ctx *security.RequestContext, req TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	// The only facet Cloud Trace can populate is the service (Cloud Run / OTel
	// service.name). Every other filter dropdown the K8s UI requests (namespace,
	// workload, http_status, span_name, ...) would each trigger a full ListTraces
	// scan for nothing — skip the fetch and return an empty facet for them.
	switch req.Label {
	case "service_name", "workload_name", "service":
	default:
		return common.OpenTelemetryTraceLabelValues{Label: req.Label}, nil
	}
	sample := TracesV3Request{AccountId: req.AccountId, StartTime: req.StartTime, EndTime: req.EndTime}
	spans, err := cloud.QueryTracesList(ctx, s.buildCloudTracesRequest(sample))
	if err != nil {
		// Non-fatal: an empty facet just means no dropdown options.
		return common.OpenTelemetryTraceLabelValues{Label: req.Label}, nil
	}
	seen := map[string]struct{}{}
	values := []string{}
	for _, sp := range spans {
		v := getSpanString(sp, "service_name")
		if v == "" {
			v = getSpanString(sp, "workload_name")
		}
		if v == "" {
			continue
		}
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
			values = append(values, v)
		}
	}
	return common.OpenTelemetryTraceLabelValues{Label: req.Label, Values: values}, nil
}

// QueryGroupedTraces / QueryGroupedTracesCount: trace grouping (by workload/resource) is
// not supported for Cloud Trace yet — return empty so the unified UI degrades gracefully.
func (s *GcpTraceSource) QueryGroupedTraces(_ *security.RequestContext, _ TracesV3Request) ([]TraceGroupingValues, error) {
	return []TraceGroupingValues{}, nil
}

func (s *GcpTraceSource) QueryGroupedTracesCount(_ *security.RequestContext, _ TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	return common.OpenTelemetryTraceGroupCount{Count: 0}, nil
}

// QueryRootSpansByTrace backs the "By Traces" listing view — one representative (root) span
// per trace. Pushed to the source: Cloud Trace's ListTraces View=ROOTSPAN returns only root
// spans, so the limit bounds traces (not spans) and there's no in-memory root reduction —
// unlike the generic queryRootSpansViaSpans path which over-fetches COMPLETE and picks roots.
func (s *GcpTraceSource) QueryRootSpansByTrace(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	cr := s.buildCloudTracesRequest(req)
	cr.Query.RootsOnly = true
	spans, err := cloud.QueryTracesList(ctx, cr)
	if err != nil {
		return nil, err
	}
	return mapCloudSpansToOTelTraces(scopeUnpushedWorkload(spans, req.QueryRequest.Where)), nil
}

// CountTracesByTrace returns the distinct-trace count for the "By Traces" view. Cloud Trace
// spans are already flattened per trace, so reuse CountTraces (distinct trace ids).
func (s *GcpTraceSource) CountTracesByTrace(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	return s.CountTraces(ctx, req)
}

// QueryLabels enumerates backend label keys. Cloud Trace exposes no label-key discovery API,
// so return empty and let FetchTraceLabels fall back to the canonical + mapping label set.
func (s *GcpTraceSource) QueryLabels(_ *security.RequestContext, _ FetchTraceLabelRequest) ([]OutputTraceLabel, error) {
	return []OutputTraceLabel{}, nil
}

// QueryTracesHeatmap fetches every span of a single trace (the gantt/heatmap drilldown) via
// Cloud Trace's GetTrace, threaded through as Query.TraceId.
func (s *GcpTraceSource) QueryTracesHeatmap(ctx *security.RequestContext, req TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error) {
	spans, err := cloud.QueryTracesList(ctx, cloud.QueryTracesRequest{
		AccountId: req.AccountId,
		Query:     cloud.TracesQuery{TraceId: req.TraceId},
	})
	if err != nil {
		return nil, err
	}
	return mapCloudSpansToOTelHeatmap(spans), nil
}

func (s *GcpTraceSource) GetLabelMapping() map[string]string { return map[string]string{} }

func (s *GcpTraceSource) GetSupportedOperators() []string { return []string{"_eq"} }

// ---- helpers ----

// gcpCloudTraceFilter translates the UI filter predicates into a Cloud Trace v1
// filter string (space-separated terms are AND-ed at the source). Uses OTel
// semantic-convention labels (service.name / http.response.status_code / url.path),
// which are what GCP OTel-instrumented services emit and which we verified narrow
// correctly; Cloud Trace's filter grammar has no OR, so agent-only "/http/*" spans
// aren't dual-matched. Trace-id and Ok/Error are handled elsewhere (GetTrace and
// not expressible, respectively).
func gcpCloudTraceFilter(where query.QueryWhereClause) string {
	var terms []string
	if v := extractTraceStringFilter(where, "service_name", "workload_name", "workload_name_1"); pushableAsServiceName(v) {
		terms = append(terms, "service.name:"+v)
	}
	if v := extractTraceStringFilter(where, "span_name"); v != "" {
		terms = append(terms, "span:"+v)
	}
	if v := extractTraceStringFilter(where, "http_status_code"); v != "" {
		terms = append(terms, "http.response.status_code:"+v)
	}
	if v := extractTraceStringFilter(where, "resource"); v != "" {
		terms = append(terms, "url.path:"+v)
	}
	return strings.Join(terms, " ")
}

// extractTraceStringFilter pulls the first scalar value for any of the given keys out of a
// where clause, in key order. Keys are searched across the whole clause tree, not just its
// top level — callers compose predicates under And/Or (the event trace enricher nests every
// predicate, leaving Where.Binary empty), and a top-level-only lookup reported "no filter"
// for those, so the query ran unscoped over the whole project.
func extractTraceStringFilter(where query.QueryWhereClause, keys ...string) string {
	for _, key := range keys {
		if v := findTraceFilterValue(where, key); v != "" {
			return v
		}
	}
	return ""
}

// findTraceFilterValue walks Binary + nested And/Or for the first scalar bound to key.
// A negated (Not) branch is skipped deliberately: "workload is not X" names a workload to
// exclude, and treating it as the scope would filter to exactly what the caller ruled out.
func findTraceFilterValue(where query.QueryWhereClause, key string) string {
	if v := traceFilterScalar(where.Binary[key]); v != "" {
		return v
	}
	for _, clause := range where.And {
		if v := findTraceFilterValue(clause, key); v != "" {
			return v
		}
	}
	for _, clause := range where.Or {
		if v := findTraceFilterValue(clause, key); v != "" {
			return v
		}
	}
	return ""
}

// traceFilterScalar reads a single string out of one key's operators (handles _eq, _in, and
// _like — the search boxes send _like with % wildcards, which Cloud Trace matches as a
// substring, so the wildcards are stripped).
func traceFilterScalar(ops map[query.BinaryWhereClauseType]any) string {
	if len(ops) == 0 {
		return ""
	}
	if v, ok := ops[query.Eq]; ok {
		if str, ok := v.(string); ok && str != "" {
			return str
		}
	}
	if v, ok := ops[query.Like]; ok {
		if str, ok := v.(string); ok {
			if trimmed := strings.Trim(str, "%"); trimmed != "" {
				return trimmed
			}
		}
	}
	if v, ok := ops[query.In]; ok {
		switch arr := v.(type) {
		case []any:
			for _, e := range arr {
				if str, ok := e.(string); ok && str != "" {
					return str
				}
			}
		case []string:
			if len(arr) > 0 {
				return arr[0]
			}
		}
	}
	return ""
}

// requestedWorkload returns the workload the caller asked to scope to, if any.
func requestedWorkload(where query.QueryWhereClause) string {
	return extractTraceStringFilter(where, "workload_name", "service_name", "workload_name_1")
}

// pushableAsServiceName reports whether a workload can be expressed as a Cloud Trace
// `service.name:<v>` filter term. A Cloud SQL workload is the instance id `project:instance`,
// whose colon breaks the LABEL:VALUE grammar — and it would never match anyway, because
// Cloud SQL Query Insights spans carry the *database* as service.name and the instance only
// in their `instance` label. Those are scoped in memory instead of pushed down.
func pushableAsServiceName(workload string) bool {
	return workload != "" && !strings.Contains(workload, ":")
}

// scopeUnpushedWorkload narrows project-wide Cloud Trace spans to the requested workload in
// the one case the filter could not be pushed to Cloud Trace. Paths that do push down are
// left untouched — the source already scoped those, so their results are unchanged.
//
// Strict by design: an unmatched workload yields no spans rather than the whole project
// window. Returning everything is what attached one Cloud SQL instance's slow queries to
// another instance's incident, while the evidence still reported itself as scoped.
func scopeUnpushedWorkload(spans []map[string]any, where query.QueryWhereClause) []map[string]any {
	workload := requestedWorkload(where)
	if workload == "" || pushableAsServiceName(workload) {
		return spans
	}
	// Keep matched traces whole: a Cloud SQL query's plan-node children (Seq Scan,
	// Aggregate, ...) carry no instance label of their own, so matching span-by-span
	// would strip every child off its `Cloud SQL Query` root and leave a bare timing.
	matched := map[string]bool{}
	for _, sp := range spans {
		if spanMatchesWorkload(sp, workload) {
			matched[getSpanString(sp, "trace_id")] = true
		}
	}
	out := make([]map[string]any, 0, len(spans))
	for _, sp := range spans {
		if matched[getSpanString(sp, "trace_id")] {
			out = append(out, sp)
		}
	}
	return out
}

// spanMatchesWorkload reports whether a mapped Cloud Trace span belongs to the workload.
// Checks the projected identity columns plus the Cloud SQL labels, where `instance` — not
// service.name — carries the instance identity.
func spanMatchesWorkload(sp map[string]any, workload string) bool {
	for _, key := range []string{"service_name", "workload_name"} {
		if strings.EqualFold(getSpanString(sp, key), workload) {
			return true
		}
	}
	attrs := map[string]string{}
	if raw := getSpanString(sp, "span_attributes"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &attrs)
	}
	for _, key := range []string{"instance", "database", "service.name"} {
		if strings.EqualFold(attrs[key], workload) {
			return true
		}
	}
	return false
}

// spanAttrsWithContext parses the cloud span's span_attributes JSON string and augments it
// with the GCP context fields (project, region, service_name, parent_span_id) the frontend
// drilldowns (Cloud Logs filter, gantt) read out of span_attributes.
func spanAttrsWithContext(sp map[string]any) map[string]string {
	attrs := map[string]string{}
	if raw := getSpanString(sp, "span_attributes"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &attrs)
	}
	for _, k := range []string{"project", "region", "service_name", "parent_span_id"} {
		if v := getSpanString(sp, k); v != "" {
			attrs[k] = v
		}
	}
	return attrs
}

func mapCloudSpansToOTelTraces(spans []map[string]any) []common.OpenTelemetryTrace {
	out := make([]common.OpenTelemetryTrace, len(spans))
	for i, sp := range spans {
		svc := getSpanString(sp, "service_name")
		if svc == "" {
			svc = getSpanString(sp, "workload_name")
		}
		ts := getSpanString(sp, "timestamp")
		out[i] = common.OpenTelemetryTrace{
			Timestamp:          ts,
			StartTime:          ts,
			TraceID:            getSpanString(sp, "trace_id"),
			SpanID:             getSpanString(sp, "span_id"),
			ParentSpanID:       getSpanString(sp, "parent_span_id"),
			SpanName:           getSpanString(sp, "span_name"),
			SpanKind:           getSpanString(sp, "span_kind"),
			ServiceName:        svc,
			WorkloadName:       svc,
			Service:            svc,
			Operation:          getSpanString(sp, "span_name"),
			DurationNs:         getSpanInt64(sp, "duration_ns"),
			StatusCode:         getSpanString(sp, "status_code"),
			HTTPStatusCode:     getSpanString(sp, "http_status_code"),
			Resource:           getSpanString(sp, "resource"),
			TraceSource:        "gcp",
			ResourceAttributes: map[string]string{},
			SpanAttributes:     spanAttrsWithContext(sp),
		}
	}
	return out
}

func mapCloudSpansToOTelHeatmap(spans []map[string]any) []common.OpenTelemetryTraceHeatMap {
	out := make([]common.OpenTelemetryTraceHeatMap, len(spans))
	for i, sp := range spans {
		svc := getSpanString(sp, "service_name")
		if svc == "" {
			svc = getSpanString(sp, "workload_name")
		}
		out[i] = common.OpenTelemetryTraceHeatMap{
			Timestamp:          getSpanString(sp, "timestamp"),
			ResourceAttributes: map[string]string{},
			SpanName:           getSpanString(sp, "span_name"),
			StatusCode:         getSpanString(sp, "status_code"),
			DurationNs:         getSpanInt64(sp, "duration_ns"),
			SpanAttributes:     spanAttrsWithContext(sp),
			TraceID:            getSpanString(sp, "trace_id"),
			SpanID:             getSpanString(sp, "span_id"),
			ServiceName:        svc,
		}
	}
	return out
}

func getSpanString(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getSpanInt64(m map[string]any, key string) int64 {
	switch v := m[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}
