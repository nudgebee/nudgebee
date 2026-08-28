package observability

import (
	"fmt"
	"math"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SplunkEnterpriseTraceSource implements TraceSource over spans stored in a Splunk index.
//
// Splunk Enterprise has no native trace store. Spans are only present if an OpenTelemetry
// Collector was pointed at an index with the splunk_hec exporter, which writes each span
// as one HEC event whose JSON body Splunk auto-extracts into dotted fields
// (trace_id, span_id, parent_span_id, name, kind, start_time, end_time, status.code,
// attributes.*). That is the shape this source is written against.
//
// Every field is nevertheless resolved through a CANDIDATE LIST rather than a single
// hardcoded name. The exporter's spelling has changed across collector versions, Splunk
// Connect for Kubernetes and hand-rolled HEC pipelines use different names again, and a
// span field that resolves to nothing renders an empty column rather than an error — so a
// single wrong guess is silent. Reading several accepted spellings costs nothing at query
// time and is the difference between a populated table and a blank one.
//
// Not to be confused with SplunkTraceSource, which targets Splunk APM over the SignalFx API.
type SplunkEnterpriseTraceSource struct{}

// splunkEnterpriseTraceLabelMapping maps canonical filter fields onto the single Splunk
// field a WHERE clause targets. A filter can name only one field, so each entry points at
// the spelling that covers the most spans; display tolerates every spelling in
// splunkEnterpriseTraceFields below.
var splunkEnterpriseTraceLabelMapping = map[string]string{
	"trace_id":  "trace_id",
	"span_id":   "span_id",
	"parent_id": "parent_span_id",
	"span_name": "name",
	// The numeric/textual OTel status. The Ok/Error filter sends the OTel status code,
	// and the exporter writes the spelled-out STATUS_CODE_ERROR form, so both are matched
	// case-insensitively by the _contains operator the UI uses for this field.
	"status_code":        "status.code",
	"workload_name":      "service.name",
	"workload_namespace": "k8s.namespace.name",
	"http_status_code":   "attributes.http.status_code",
	"resource":           "attributes.http.target",
	// The callee of a client span. OTel writes this as net.peer.name / peer.service.
	"destination_workload_name": "attributes.net.peer.name",
}

// splunkEnterpriseTraceUnsupportedFields are canonical filter fields a span in Splunk
// cannot express. A span carries its own service's Kubernetes attributes plus a bare peer
// NAME for whatever it called — there is no peer namespace anywhere in the schema.
//
// Dropped rather than passed through for the reason OpenObserve and Datadog drop the same
// field: an unmapped name reaching the query as a bare field matches nothing, which takes
// the destination side of a caller-OR-callee search down with it and leaves the trace
// evidence empty. Dropping is confined to this named set, so a typo'd field still fails
// loudly instead of being silently ignored.
var splunkEnterpriseTraceUnsupportedFields = map[string]bool{
	"destination_workload_namespace": true,
}

// splunkEnterpriseTraceFields lists accepted field spellings per rendered column, in
// priority order; the first present and non-empty wins per span. See the type comment for
// why this is a list rather than a constant.
var splunkEnterpriseTraceFields = struct {
	TraceID        []string
	SpanID         []string
	ParentSpanID   []string
	SpanName       []string
	SpanKind       []string
	ServiceName    []string
	Namespace      []string
	PodName        []string
	StatusCode     []string
	StatusMessage  []string
	StartTime      []string
	EndTime        []string
	Duration       []string
	Resource       []string
	HTTPStatusCode []string
	Destination    []string
}{
	TraceID:      []string{"trace_id", "traceId", "traceID"},
	SpanID:       []string{"span_id", "spanId", "spanID"},
	ParentSpanID: []string{"parent_span_id", "parentSpanId", "parent_id", "references.parent_span_id"},
	SpanName:     []string{"name", "span_name", "operation_name"},
	SpanKind:     []string{"kind", "span_kind"},
	ServiceName:  []string{"service.name", "service_name", "resource.service.name"},
	Namespace:    []string{"k8s.namespace.name", "resource.k8s.namespace.name", "kubernetes.namespace_name"},
	PodName:      []string{"k8s.pod.name", "resource.k8s.pod.name", "kubernetes.pod_name"},
	// status.code is what the splunk_hec exporter writes; the flatter spellings cover
	// pipelines that promote it to a top-level field.
	StatusCode:    []string{"status.code", "status_code", "otel.status_code"},
	StatusMessage: []string{"status.message", "status_message", "otel.status_description"},
	StartTime:     []string{"start_time", "startTimeUnixNano", "start_time_unix_nano"},
	EndTime:       []string{"end_time", "endTimeUnixNano", "end_time_unix_nano"},
	// A pipeline that precomputes the duration saves the subtraction; nanoseconds is the
	// contract, matching OpenTelemetryTrace.DurationNs.
	Duration: []string{"duration_ns", "durationNanos"},
	// HTTP attributes were renamed between OTel semantic-convention generations
	// (http.target → url.full), and a real cluster runs SDKs from both eras side by side.
	Resource:       []string{"attributes.http.target", "attributes.url.full", "attributes.url.path", "attributes.http.route", "attributes.http.url"},
	HTTPStatusCode: []string{"attributes.http.status_code", "attributes.http.response.status_code", "attributes.rpc.grpc.status_code"},
	Destination:    []string{"attributes.peer.service", "attributes.net.peer.name", "attributes.server.address", "attributes.net.sock.peer.addr", "attributes.http.host"},
}

// splunkEnterpriseTraceDefaultLimit / MaxLimit bound one span search, mirroring the log
// source's ceilings for the same reason: Splunk would stream far more than the API server
// can decode.
const (
	splunkEnterpriseTraceDefaultLimit = 100
	splunkEnterpriseTraceMaxLimit     = 10000
)

// splunkEnterpriseTraceSearchTimeout bounds a single oneshot span search.
const splunkEnterpriseTraceSearchTimeout = 60 * time.Second

// splunkEnterpriseHeatmapSpanLimit caps the spans returned for one trace. A trace with
// more spans than this cannot be laid out legibly anyway, and the cap keeps a runaway
// trace from stalling the request.
const splunkEnterpriseHeatmapSpanLimit = 2000

// splunkEnterpriseHeatmapWindowPadding widens a caller-supplied window when fetching a
// single trace: the window comes from the listing row's timestamp, but a trace's earliest
// span can begin fractionally before it and its latest can end after, and clipping either
// truncates the waterfall. The trace_id filter keeps the wider scan cheap.
const splunkEnterpriseHeatmapWindowPadding = 5 * time.Minute

// splunkEnterpriseHeatmapDefaultLookback is how far back the heatmap searches when the
// caller supplies no window at all. The trace-detail view asks by trace_id alone, so the
// usual one-hour default would return zero spans for any older trace; the OpenObserve and
// New Relic heatmaps widen to the same order of magnitude for the same reason.
const splunkEnterpriseHeatmapDefaultLookback = 30 * 24 * time.Hour

// splunkEnterpriseTraceGroupLimit caps the rows the Trace Group view aggregates to.
const splunkEnterpriseTraceGroupLimit = 100

// The eval aliases used by the aggregate queries. Prefixed so they cannot collide with a
// field the exporter emitted.
const (
	splunkTraceServiceCol   = "nb_tr_service"
	splunkTraceNamespaceCol = "nb_tr_namespace"
	splunkTraceNameCol      = "nb_tr_name"
	splunkTraceResourceCol  = "nb_tr_resource"
	splunkTraceStatusCol    = "nb_tr_status"
	splunkTraceHTTPCodeCol  = "nb_tr_http_code"
	splunkTraceDestCol      = "nb_tr_destination"
	splunkTraceDurationCol  = "nb_tr_duration"
	splunkTraceCountCol     = "nb_tr_count"
	splunkTraceErrorsCol    = "nb_tr_errors"
	splunkTraceP99Col       = "nb_tr_p99"
	splunkTraceP95Col       = "nb_tr_p95"
	splunkTraceMaxCol       = "nb_tr_max"
	splunkTraceDistinctCol  = "nb_tr_distinct"
)

func (s *SplunkEnterpriseTraceSource) GetLabelMapping() map[string]string {
	return splunkEnterpriseTraceLabelMapping
}

func (s *SplunkEnterpriseTraceSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_contains", "_ilike"}
}

// splunkEnterpriseTraceContext resolves the account's config and its trace index,
// refusing early when no index is configured. The explicit error matters: without it a
// search would run against the empty string and report "no traces" for what is really
// "traces were never set up here".
func splunkEnterpriseTraceContext(
	ctx *security.RequestContext, accountId string,
) (integrations.SplunkEnterpriseConfig, string, error) {
	cfg, err := integrations.GetSplunkEnterpriseConfig(ctx, accountId)
	if err != nil {
		return cfg, "", fmt.Errorf("failed to get Splunk Enterprise config: %w", err)
	}
	if cfg.TraceIndex == "" {
		return cfg, "", fmt.Errorf(
			"splunk enterprise: no trace index configured for this account — set %q on the integration "+
				"to the index your OpenTelemetry Collector writes spans to",
			integrations.SplunkEnterpriseConfigTraceIndex)
	}
	if !integrations.IsSafeSplunkIndexName(cfg.TraceIndex) {
		return cfg, "", fmt.Errorf("invalid or unsafe trace index name: %q", cfg.TraceIndex)
	}
	return cfg, cfg.TraceIndex, nil
}

// stripSplunkEnterpriseUnsupportedTraceFields returns binary without the fields a Splunk
// span cannot express, plus how many were removed. The input map is never mutated — it
// belongs to the caller's request, which other providers in a fan-out may still read.
func stripSplunkEnterpriseUnsupportedTraceFields(binary query.BinaryWhereClause) (query.BinaryWhereClause, int) {
	dropped := 0
	for field := range binary {
		if splunkEnterpriseTraceUnsupportedFields[field] {
			dropped++
		}
	}
	if dropped == 0 {
		return binary, 0
	}
	kept := make(query.BinaryWhereClause, len(binary)-dropped)
	for field, ops := range binary {
		if !splunkEnterpriseTraceUnsupportedFields[field] {
			kept[field] = ops
		}
	}
	return kept, dropped
}

func buildSplunkEnterpriseTraceWhereClause(where query.QueryWhereClause) (string, error) {
	if len(where.Binary) > 0 {
		binary, dropped := stripSplunkEnterpriseUnsupportedTraceFields(where.Binary)
		if len(binary) == 0 {
			// Every predicate in this group was inexpressible. Returning "" would render
			// the group as no restriction at all: harmless inside an OR, but silently
			// matching everything inside an AND. A filter that cannot be honoured must
			// not widen the result set.
			return "", fmt.Errorf(
				"splunk enterprise: trace filter group has no field this span schema can express (dropped %d)", dropped)
		}
		return buildSplunkEnterpriseBinaryClause(binary, splunkEnterpriseTraceLabelMapping)
	}
	return buildSplunkEnterpriseWhereClauseWithMapping(where, splunkEnterpriseTraceLabelMapping)
}

// splunkEnterpriseTraceLimit clamps a requested page size into the supported range.
func splunkEnterpriseTraceLimit(requested int) int {
	if requested <= 0 {
		return splunkEnterpriseTraceDefaultLimit
	}
	if requested > splunkEnterpriseTraceMaxLimit {
		return splunkEnterpriseTraceMaxLimit
	}
	return requested
}

// buildSplunkEnterpriseTraceSPL renders the span search.
func buildSplunkEnterpriseTraceSPL(req TracesV3Request, index string) (string, error) {
	if !integrations.IsSafeSplunkIndexName(index) {
		return "", fmt.Errorf("invalid or unsafe index name: %q", index)
	}

	whereClause, err := buildSplunkEnterpriseTraceWhereClause(req.QueryRequest.Where)
	if err != nil {
		return "", err
	}

	spl := fmt.Sprintf(`search index="%s"`, index)
	if whereClause != "" {
		spl += " " + whereClause
	}
	spl += fmt.Sprintf(" | head %d", splunkEnterpriseTraceLimit(req.QueryRequest.Limit))
	// Same reason as the log source: Splunk only returns a field the search REFERENCES,
	// so without this every span comes back with the default fields only and every
	// mapped column renders blank.
	spl += " | fields *"
	return spl, nil
}

func (s *SplunkEnterpriseTraceSource) GetQuery(ctx *security.RequestContext, req TracesV3Request) (string, error) {
	_, index, err := splunkEnterpriseTraceContext(ctx, req.AccountId)
	if err != nil {
		return "", err
	}
	return buildSplunkEnterpriseTraceSPL(req, index)
}

// splunkInternalFields are the bookkeeping fields Splunk attaches to every event. They
// are not span data and must not be offered as filterable labels: selecting one builds a
// filter on an index-internal value (a bucket id, a byte offset) that matches nothing, so
// the query silently returns zero rows and reads as "no traces" rather than as a mistake.
var splunkInternalFields = map[string]bool{
	"eventtype": true, "linecount": true, "punct": true,
	"splunk_server": true, "splunk_server_group": true,
	"tag": true, "tag::eventtype": true,
	"index": true, "source": true, "sourcetype": true, "host": true,
}

// isSplunkInternalField reports whether a returned column is Splunk bookkeeping rather
// than data the span carried. Everything starting with '_' is internal by Splunk's own
// convention (_bkt, _cd, _si, _raw, …).
func isSplunkInternalField(name string) bool {
	return strings.HasPrefix(name, "_") || splunkInternalFields[name]
}

// firstSplunkValue returns the first candidate present and non-empty on the row.
func firstSplunkValue(row map[string]any, candidates []string) string {
	for _, c := range candidates {
		if v, ok := row[c]; ok && v != nil {
			if s := formatSplunkValue(v); s != "" {
				return s
			}
		}
	}
	return ""
}

// splunkEnterpriseSpanNanos parses a span timestamp into Unix nanoseconds.
//
// The splunk_hec exporter writes start_time/end_time as a Unix timestamp, but whether
// that lands as nanoseconds, or as the fractional seconds Splunk renders elsewhere,
// depends on the pipeline. Magnitude disambiguates: a seconds-scale value for any real
// span is ~1.8e9, so anything below the threshold is scaled up rather than being read as
// a span that started in 1970 — which would make every duration astronomically wrong.
func splunkEnterpriseSpanNanos(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}

	// Whole numbers are parsed as int64, NOT float64. A nanosecond epoch is 19 digits and
	// float64 carries about 15-16 significant ones, so ParseFloat silently rounds
	// 1787851349250000000 to 1787851349249999872 — a 128ns error on the timestamp that
	// then lands in full in every computed duration. This is the same hazard the search
	// decoder guards with json.Decoder.UseNumber.
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
		switch {
		case n >= 1e17: // already nanoseconds
			return n, true
		case n >= 1e14: // microseconds
			return n * 1e3, true
		case n >= 1e11: // milliseconds
			return n * 1e6, true
		case n <= math.MaxInt64/int64(time.Second): // seconds
			return n * int64(time.Second), true
		default:
			// Not a timestamp in any unit — scaling would overflow into a negative.
			return 0, false
		}
	}

	// Fractional seconds. Precision is not a concern at this magnitude: a seconds-scale
	// epoch is ~1.8e9, well inside float64's exact-integer range.
	if f, err := strconv.ParseFloat(raw, 64); err == nil && f > 0 {
		switch {
		case f >= 1e17:
			return int64(f), true
		case f >= 1e14:
			return int64(f * 1e3), true
		case f >= 1e11:
			return int64(f * 1e6), true
		default:
			return int64(f * 1e9), true
		}
	}
	// ISO 8601, which some collectors emit instead of a numeric timestamp.
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999-0700"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UnixNano(), true
		}
	}
	return 0, false
}

// splunkEnterpriseSpanDurationNs resolves a span's duration in nanoseconds, preferring a
// precomputed field and falling back to end - start.
func splunkEnterpriseSpanDurationNs(row map[string]any) int64 {
	if d := firstSplunkValue(row, splunkEnterpriseTraceFields.Duration); d != "" {
		if n, err := strconv.ParseFloat(d, 64); err == nil && n >= 0 {
			return int64(n)
		}
	}
	start, okStart := splunkEnterpriseSpanNanos(firstSplunkValue(row, splunkEnterpriseTraceFields.StartTime))
	end, okEnd := splunkEnterpriseSpanNanos(firstSplunkValue(row, splunkEnterpriseTraceFields.EndTime))
	if okStart && okEnd && end >= start {
		return end - start
	}
	return 0
}

// convertSplunkEnterpriseSpans maps Splunk rows onto the shared OpenTelemetryTrace shape.
//
// The columns the trace list renders are not just the span identifiers — workload_name,
// workload_namespace, status_code, resource, http_status_code and the destination set are
// all displayed. Each is populated from the same field the label mapping declares for
// filtering, so a column that filters on service.name also displays from it; if the two
// disagreed, clicking a row would return nothing.
func convertSplunkEnterpriseSpans(rows []map[string]any) []common.OpenTelemetryTrace {
	spans := make([]common.OpenTelemetryTrace, 0, len(rows))

	for _, row := range rows {
		span := common.OpenTelemetryTrace{
			ResourceAttributes: make(map[string]string),
			SpanAttributes:     make(map[string]string),
			TraceSource:        "splunk_enterprise",
		}

		span.TraceID = firstSplunkValue(row, splunkEnterpriseTraceFields.TraceID)
		span.SpanID = firstSplunkValue(row, splunkEnterpriseTraceFields.SpanID)
		span.ParentSpanID = firstSplunkValue(row, splunkEnterpriseTraceFields.ParentSpanID)
		span.SpanName = firstSplunkValue(row, splunkEnterpriseTraceFields.SpanName)
		span.Operation = span.SpanName
		span.SpanKind = firstSplunkValue(row, splunkEnterpriseTraceFields.SpanKind)
		span.StatusCode = firstSplunkValue(row, splunkEnterpriseTraceFields.StatusCode)
		span.StatusMessage = firstSplunkValue(row, splunkEnterpriseTraceFields.StatusMessage)
		span.Resource = firstSplunkValue(row, splunkEnterpriseTraceFields.Resource)
		span.HTTPStatusCode = firstSplunkValue(row, splunkEnterpriseTraceFields.HTTPStatusCode)

		serviceName := firstSplunkValue(row, splunkEnterpriseTraceFields.ServiceName)
		span.ServiceName = serviceName
		span.Service = serviceName
		// The trace list groups by workload; for OTel spans the service IS the workload,
		// which is also what the label mapping filters on.
		span.WorkloadName = serviceName

		if ns := firstSplunkValue(row, splunkEnterpriseTraceFields.Namespace); ns != "" {
			span.WorkloadNamespace = ns
			span.ResourceAttributes["k8s.namespace.name"] = ns
		}
		if pod := firstSplunkValue(row, splunkEnterpriseTraceFields.PodName); pod != "" {
			span.ResourceAttributes["k8s.pod.name"] = pod
		}
		if dest := firstSplunkValue(row, splunkEnterpriseTraceFields.Destination); dest != "" {
			span.DestinationName = dest
			span.DestinationWorkload = dest
		}

		span.DurationNs = splunkEnterpriseSpanDurationNs(row)

		// The span's own start time is the timestamp the waterfall lays out against;
		// _time is the INGEST time and can differ by the collector's batch interval.
		if startNs, ok := splunkEnterpriseSpanNanos(firstSplunkValue(row, splunkEnterpriseTraceFields.StartTime)); ok {
			t := time.Unix(0, startNs).UTC()
			span.Timestamp = t.Format(time.RFC3339Nano)
			span.StartTime = span.Timestamp
			span.StartTimeUnixNano = strconv.FormatInt(startNs, 10)
			if span.DurationNs > 0 {
				end := t.Add(time.Duration(span.DurationNs))
				span.EndTime = end.Format(time.RFC3339Nano)
				span.EndTimeUnixNano = strconv.FormatInt(startNs+span.DurationNs, 10)
			}
		} else {
			span.Timestamp = splunkEnterpriseTimestamp(formatSplunkValue(row["_time"]))
		}

		// Everything else the exporter emitted stays reachable as a span attribute, so a
		// field this schema does not name is still visible in the span detail rather than
		// being dropped on the floor.
		for k, v := range row {
			if v == nil || strings.HasPrefix(k, "_") {
				continue
			}
			if strings.HasPrefix(k, "attributes.") {
				span.SpanAttributes[strings.TrimPrefix(k, "attributes.")] = formatSplunkValue(v)
				continue
			}
			if strings.HasPrefix(k, "resource.") {
				span.ResourceAttributes[strings.TrimPrefix(k, "resource.")] = formatSplunkValue(v)
			}
		}

		spans = append(spans, span)
	}

	return spans
}

func (s *SplunkEnterpriseTraceSource) QueryTraces(
	ctx *security.RequestContext, req TracesV3Request,
) ([]common.OpenTelemetryTrace, error) {
	cfg, index, err := splunkEnterpriseTraceContext(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}

	// A caller-supplied query wins, matching the log source and the Loki/Signoz sources:
	// validateSplunkEnterpriseQuery inside runSplunkEnterpriseSearch is what makes that safe.
	spl := strings.TrimSpace(req.Query)
	if spl == "" {
		spl, err = buildSplunkEnterpriseTraceSPL(req, index)
		if err != nil {
			return nil, err
		}
	}

	startTime, endTime := splunkEnterpriseTimeRangeSeconds(req.StartTime, req.EndTime, time.Now())

	ctx.GetLogger().Info("Splunk Enterprise Trace Query", "query", spl)

	rows, err := runSplunkEnterpriseSearch(cfg, spl, startTime, endTime,
		splunkEnterpriseTraceLimit(req.QueryRequest.Limit), splunkEnterpriseTraceSearchTimeout)
	if err != nil {
		return nil, err
	}

	return convertSplunkEnterpriseSpans(rows), nil
}

// buildSplunkEnterpriseTraceCountSPL counts matching spans, or distinct traces when
// distinct is set (the "By Traces" pagination count).
func buildSplunkEnterpriseTraceCountSPL(req TracesV3Request, index string, distinct bool) (string, error) {
	if !integrations.IsSafeSplunkIndexName(index) {
		return "", fmt.Errorf("invalid or unsafe index name: %q", index)
	}
	whereClause, err := buildSplunkEnterpriseTraceWhereClause(req.QueryRequest.Where)
	if err != nil {
		return "", err
	}

	spl := fmt.Sprintf(`search index="%s"`, index)
	if whereClause != "" {
		spl += " " + whereClause
	}
	if distinct {
		// dc() over trace_id is exact and cheap in Splunk, so the "By Traces" view gets a
		// real count rather than the -1 estimate providers without one have to return.
		return spl + fmt.Sprintf(` | stats dc(trace_id) AS %s`, splunkTraceDistinctCol), nil
	}
	return spl + fmt.Sprintf(` | stats count AS %s`, splunkTraceCountCol), nil
}

// splunkEnterpriseCountFromRows reads a single-cell stats result.
func splunkEnterpriseCountFromRows(rows []map[string]any, column string) int {
	if len(rows) == 0 {
		return 0
	}
	n, ok := splunkEnterpriseNumber(rows[0][column])
	if !ok || n < 0 {
		return 0
	}
	maxInt := float64(int64(^uint(0) >> 1))
	if n > maxInt {
		return int(int64(maxInt))
	}
	return int(n)
}

func (s *SplunkEnterpriseTraceSource) CountTraces(
	ctx *security.RequestContext, req TracesV3Request,
) (common.OpenTelemetryTraceCount, error) {
	cfg, index, err := splunkEnterpriseTraceContext(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}
	spl, err := buildSplunkEnterpriseTraceCountSPL(req, index, false)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}

	startTime, endTime := splunkEnterpriseTimeRangeSeconds(req.StartTime, req.EndTime, time.Now())
	rows, err := runSplunkEnterpriseSearch(cfg, spl, startTime, endTime, 1, splunkEnterpriseTraceSearchTimeout)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}
	return common.OpenTelemetryTraceCount{Count: splunkEnterpriseCountFromRows(rows, splunkTraceCountCol)}, nil
}

// CountTracesByTrace returns the number of DISTINCT traces matching the filters.
func (s *SplunkEnterpriseTraceSource) CountTracesByTrace(
	ctx *security.RequestContext, req TracesV3Request,
) (common.OpenTelemetryTraceCount, error) {
	cfg, index, err := splunkEnterpriseTraceContext(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}
	spl, err := buildSplunkEnterpriseTraceCountSPL(req, index, true)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}

	startTime, endTime := splunkEnterpriseTimeRangeSeconds(req.StartTime, req.EndTime, time.Now())
	rows, err := runSplunkEnterpriseSearch(cfg, spl, startTime, endTime, 1, splunkEnterpriseTraceSearchTimeout)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}
	return common.OpenTelemetryTraceCount{Count: splunkEnterpriseCountFromRows(rows, splunkTraceDistinctCol)}, nil
}

// QueryRootSpansByTrace backs the "By Traces" listing. Splunk returns spans, which the
// shared helper reduces to one representative root per trace.
func (s *SplunkEnterpriseTraceSource) QueryRootSpansByTrace(
	ctx *security.RequestContext, req TracesV3Request,
) ([]common.OpenTelemetryTrace, error) {
	return queryRootSpansViaSpans(ctx, s, req)
}

func (s *SplunkEnterpriseTraceSource) GetLabelValues(
	ctx *security.RequestContext, req TracesV3LabelValuesRequest,
) (common.OpenTelemetryTraceLabelValues, error) {
	cfg, index, err := splunkEnterpriseTraceContext(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, err
	}

	col := req.Label
	if mapped, ok := splunkEnterpriseTraceLabelMapping[col]; ok {
		col = mapped
	}
	if !isSafeSplunkFieldName(col) {
		return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("invalid or unsafe label name: %q", col)
	}

	// `top` ranks by frequency and caps server-side, so the most useful values surface
	// first even when the field has a long tail. The trailing `fields` drops top's count
	// and percent columns, which this caller has no use for.
	//
	// The field name is NOT quoted here, even though the OTel spellings carry dots. SPL
	// treats the two contexts differently: a dotted name must be single-quoted inside
	// eval/where/stats-BY (see the log-group query), but in a search term, `top` or
	// `fields` the quoted form matches nothing at all. Verified against Splunk 10.4.2 —
	// the single-quoted variant of this exact query returned 0 rows where the bare one
	// returned every service, which as a label dropdown reads as "this field has no
	// values" rather than as an error.
	spl := fmt.Sprintf(`search index="%s" %s=* | top limit=%d %s | fields %s`,
		index, col, splunkEnterpriseLabelValueLimit, col, col)

	startTime, endTime := splunkEnterpriseTimeRangeSeconds(req.StartTime, req.EndTime, time.Now())
	rows, err := runSplunkEnterpriseSearch(cfg, spl, startTime, endTime,
		splunkEnterpriseLabelValueLimit, 30*time.Second)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, err
	}

	values := make([]string, 0, len(rows))
	for _, row := range rows {
		if v := formatSplunkValue(row[col]); v != "" {
			values = append(values, v)
		}
	}
	return common.OpenTelemetryTraceLabelValues{Label: req.Label, Values: values}, nil
}

// QueryLabels reports the field names present on a sample of recent spans, the same way
// the log source does — Splunk has no schema to read, and `| fieldsummary` reports every
// search-time extraction, which is effectively unbounded on a raw sourcetype.
func (s *SplunkEnterpriseTraceSource) QueryLabels(
	ctx *security.RequestContext, req FetchTraceLabelRequest,
) ([]OutputTraceLabel, error) {
	cfg, index, err := splunkEnterpriseTraceContext(ctx, req.AccountId)
	if err != nil {
		// An account with no trace index has no labels to offer, but this is not an error
		// worth failing the labels call over: FetchTraceLabels falls back to the derived
		// canonical set, which is what a source with no discovery API returns anyway.
		ctx.GetLogger().Debug("splunk enterprise: trace labels unavailable", "error", err)
		return []OutputTraceLabel{}, nil
	}

	spl := fmt.Sprintf(`search index="%s" | head %d | fields *`, index, splunkEnterpriseTraceDefaultLimit)
	startTime, endTime := splunkEnterpriseTimeRangeSeconds(req.StartTime, req.EndTime, time.Now())

	rows, err := runSplunkEnterpriseSearch(cfg, spl, startTime, endTime,
		splunkEnterpriseTraceDefaultLimit, splunkEnterpriseTraceSearchTimeout)
	if err != nil {
		return nil, err
	}

	labelSet := map[string]struct{}{}
	for _, row := range rows {
		for name := range row {
			if isSplunkInternalField(name) {
				continue
			}
			labelSet[name] = struct{}{}
		}
	}
	names := make([]string, 0, len(labelSet))
	for name := range labelSet {
		names = append(names, name)
	}
	sort.Strings(names)

	labels := make([]OutputTraceLabel, 0, len(names))
	for _, name := range names {
		labels = append(labels, OutputTraceLabel{Label: name, Attributes: map[string]any{}})
	}
	return labels, nil
}

// buildSplunkEnterpriseTraceGroupSPL aggregates spans into the Trace Group view.
//
// Every grouped field is coalesced to a non-null default for the same load-bearing reason
// as the log-group query: `stats ... BY f` drops every event where f is null, so one
// absent field would empty the whole view rather than degrading one column.
func buildSplunkEnterpriseTraceGroupSPL(req TracesV3Request, index string, limit int) (string, error) {
	if !integrations.IsSafeSplunkIndexName(index) {
		return "", fmt.Errorf("invalid or unsafe index name: %q", index)
	}
	if limit <= 0 || limit > splunkEnterpriseTraceGroupLimit {
		limit = splunkEnterpriseTraceGroupLimit
	}

	whereClause, err := buildSplunkEnterpriseTraceWhereClause(req.QueryRequest.Where)
	if err != nil {
		return "", err
	}

	evals := []struct {
		alias      string
		candidates []string
	}{
		{splunkTraceServiceCol, splunkEnterpriseTraceFields.ServiceName},
		{splunkTraceNamespaceCol, splunkEnterpriseTraceFields.Namespace},
		{splunkTraceNameCol, splunkEnterpriseTraceFields.SpanName},
		{splunkTraceResourceCol, splunkEnterpriseTraceFields.Resource},
		{splunkTraceStatusCol, splunkEnterpriseTraceFields.StatusCode},
		{splunkTraceHTTPCodeCol, splunkEnterpriseTraceFields.HTTPStatusCode},
		{splunkTraceDestCol, splunkEnterpriseTraceFields.Destination},
	}
	assignments := make([]string, 0, len(evals)+1)
	for _, e := range evals {
		expr, err := splunkEnterpriseCoalesce(e.candidates, `""`)
		if err != nil {
			return "", err
		}
		assignments = append(assignments, fmt.Sprintf("%s=%s", e.alias, expr))
	}

	// Duration in nanoseconds: a precomputed field if the pipeline emits one, otherwise
	// end - start. tonumber() is what keeps the percentiles arithmetic rather than
	// lexicographic when the exporter writes the timestamps as JSON strings.
	//
	// Note that tonumber() yields a double, and a nanosecond epoch (~1.8e18) is past
	// float64's exact-integer range, so this subtraction carries a rounding error —
	// measured at 128ns against Splunk 10.4.2, where a seeded 394ms span aggregated to
	// 393999872ns. That is 0.00006% of a millisecond-scale latency and invisible in the
	// percentiles, so it is left alone rather than worked around with an epoch offset.
	// The per-span duration the list and waterfall render does NOT go through this path:
	// splunkEnterpriseSpanDurationNs parses with int64 and is exact.
	durationParts := make([]string, 0, len(splunkEnterpriseTraceFields.Duration)+2)
	for _, name := range splunkEnterpriseTraceFields.Duration {
		if !isSafeSplunkFieldName(name) {
			return "", fmt.Errorf("invalid or unsafe field name: %q", name)
		}
		durationParts = append(durationParts, fmt.Sprintf("tonumber('%s')", name))
	}
	endExpr, err := splunkEnterpriseNumericCoalesce(splunkEnterpriseTraceFields.EndTime, "0")
	if err != nil {
		return "", err
	}
	startExpr, err := splunkEnterpriseNumericCoalesce(splunkEnterpriseTraceFields.StartTime, "0")
	if err != nil {
		return "", err
	}
	durationParts = append(durationParts, fmt.Sprintf("(%s - %s)", endExpr, startExpr), "0")
	assignments = append(assignments,
		fmt.Sprintf("%s=coalesce(%s)", splunkTraceDurationCol, strings.Join(durationParts, ", ")))

	spl := fmt.Sprintf(`search index="%s"`, index)
	if whereClause != "" {
		spl += " " + whereClause
	}
	spl += " | eval " + strings.Join(assignments, ", ")

	groupCols := strings.Join([]string{
		splunkTraceServiceCol, splunkTraceNamespaceCol, splunkTraceNameCol,
		splunkTraceResourceCol, splunkTraceDestCol, splunkTraceHTTPCodeCol,
	}, ", ")

	// The error count is a conditional count rather than a second search: OTel writes the
	// error status as STATUS_CODE_ERROR and some pipelines as the numeric 2, so both are
	// matched, case-insensitively.
	spl += fmt.Sprintf(
		` | stats count AS %s, count(eval(match(%s, "(?i)error") OR %s=2)) AS %s,`+
			` perc99(%s) AS %s, perc95(%s) AS %s, max(%s) AS %s BY %s | sort - %s | head %d`,
		splunkTraceCountCol,
		splunkTraceStatusCol, splunkTraceStatusCol, splunkTraceErrorsCol,
		splunkTraceDurationCol, splunkTraceP99Col,
		splunkTraceDurationCol, splunkTraceP95Col,
		splunkTraceDurationCol, splunkTraceMaxCol,
		groupCols,
		splunkTraceCountCol,
		limit,
	)
	return spl, nil
}

// convertSplunkEnterpriseTraceGroups maps aggregated rows onto the Trace Group contract.
func convertSplunkEnterpriseTraceGroups(rows []map[string]any) []TraceGroupingValues {
	groups := make([]TraceGroupingValues, 0, len(rows))
	readInt64 := func(v any) int64 {
		if n, ok := splunkEnterpriseNumber(v); ok && n > 0 {
			return int64(n)
		}
		return 0
	}

	for _, row := range rows {
		count, ok := splunkEnterpriseNumber(row[splunkTraceCountCol])
		if !ok {
			continue
		}
		p99 := readInt64(row[splunkTraceP99Col])
		groups = append(groups, TraceGroupingValues{
			Count:                   int(count),
			ErrorCount:              int(readInt64(row[splunkTraceErrorsCol])),
			P99Latency:              p99,
			P95Latency:              readInt64(row[splunkTraceP95Col]),
			MaxLatency:              readInt64(row[splunkTraceMaxCol]),
			WorkloadName:            formatSplunkValue(row[splunkTraceServiceCol]),
			WorkloadNamespace:       formatSplunkValue(row[splunkTraceNamespaceCol]),
			DestinationWorkloadName: formatSplunkValue(row[splunkTraceDestCol]),
			Resource:                formatSplunkValue(row[splunkTraceResourceCol]),
			SpanName:                formatSplunkValue(row[splunkTraceNameCol]),
			HTTPStatusCode:          formatSplunkValue(row[splunkTraceHTTPCodeCol]),
			// The row's representative latency. p99 rather than the mean: the group view
			// exists to surface tail latency, and a mean hides exactly what it is for.
			DurationNS: p99,
		})
	}
	return groups
}

func (s *SplunkEnterpriseTraceSource) QueryGroupedTraces(
	ctx *security.RequestContext, req TracesV3Request,
) ([]TraceGroupingValues, error) {
	cfg, index, err := splunkEnterpriseTraceContext(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}
	spl, err := buildSplunkEnterpriseTraceGroupSPL(req, index, req.QueryRequest.Limit)
	if err != nil {
		return nil, err
	}

	startTime, endTime := splunkEnterpriseTimeRangeSeconds(req.StartTime, req.EndTime, time.Now())
	ctx.GetLogger().Info("Splunk Enterprise Trace Group Query", "query", spl)

	rows, err := runSplunkEnterpriseSearch(cfg, spl, startTime, endTime,
		splunkEnterpriseTraceGroupLimit, splunkEnterpriseTraceSearchTimeout)
	if err != nil {
		return nil, err
	}
	return convertSplunkEnterpriseTraceGroups(rows), nil
}

// QueryGroupedTracesCount reports how many groups the grouping query produced. It runs the
// same aggregation and counts the rows rather than issuing a cheaper distinct-count,
// because the group key is a coalesce over several fields that only exists inside that
// pipeline — counting anything else would disagree with the list the user sees.
func (s *SplunkEnterpriseTraceSource) QueryGroupedTracesCount(
	ctx *security.RequestContext, req TracesV3Request,
) (common.OpenTelemetryTraceGroupCount, error) {
	groups, err := s.QueryGroupedTraces(ctx, req)
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{}, err
	}
	return common.OpenTelemetryTraceGroupCount{Count: len(groups)}, nil
}

// splunkEnterpriseHeatmapWindow resolves the search window for one trace, in milliseconds.
func splunkEnterpriseHeatmapWindow(startMs, endMs int64, now time.Time) (int64, int64) {
	if startMs > 0 && endMs > 0 {
		pad := splunkEnterpriseHeatmapWindowPadding.Milliseconds()
		return startMs - pad, endMs + pad
	}
	return now.Add(-splunkEnterpriseHeatmapDefaultLookback).UnixMilli(), now.UnixMilli()
}

// QueryTracesHeatmap returns every span of one trace, which the UI lays out as the trace
// waterfall.
func (s *SplunkEnterpriseTraceSource) QueryTracesHeatmap(
	ctx *security.RequestContext, req TracesHeatMapRequest,
) ([]common.OpenTelemetryTraceHeatMap, error) {
	if req.TraceId == "" {
		return nil, fmt.Errorf("trace_id is required for the trace heatmap")
	}

	cfg, index, err := splunkEnterpriseTraceContext(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}

	spl := fmt.Sprintf(`search index="%s" trace_id="%s" | head %d | fields *`,
		index, escapeSplunkString(req.TraceId), splunkEnterpriseHeatmapSpanLimit)

	startMs, endMs := splunkEnterpriseHeatmapWindow(req.StartTime, req.EndTime, time.Now())
	startTime, endTime := splunkEnterpriseTimeRangeSeconds(startMs, endMs, time.Now())

	ctx.GetLogger().Info("Splunk Enterprise Trace Heatmap Query", "query", spl)

	rows, err := runSplunkEnterpriseSearch(cfg, spl, startTime, endTime,
		splunkEnterpriseHeatmapSpanLimit, splunkEnterpriseTraceSearchTimeout)
	if err != nil {
		return nil, err
	}

	spans := convertSplunkEnterpriseSpans(rows)
	// Execution order, not ingest order: the collector batches, so _time ordering can
	// interleave spans that ran sequentially and the waterfall would render out of order.
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].Timestamp < spans[j].Timestamp })

	out := make([]common.OpenTelemetryTraceHeatMap, 0, len(spans))
	for _, span := range spans {
		out = append(out, common.OpenTelemetryTraceHeatMap{
			Timestamp:          span.Timestamp,
			ResourceAttributes: span.ResourceAttributes,
			SpanAttributes:     span.SpanAttributes,
			SpanName:           span.SpanName,
			StatusCode:         span.StatusCode,
			DurationNs:         span.DurationNs,
			TraceID:            span.TraceID,
			SpanID:             span.SpanID,
			ServiceName:        span.ServiceName,
		})
	}
	return out, nil
}
