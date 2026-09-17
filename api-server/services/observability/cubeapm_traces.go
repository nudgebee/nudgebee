package observability

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	neturl "net/url"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CubeAPMTraceSource implements TraceSource for CubeAPM.
//
// Span queries — the listing, counts, grouping and label discovery — run against
// CubeAPM's LogsQL trace store, the same engine and query language as its logs.
// That store answers across every service in one request and applies filters,
// sorting and aggregation server-side.
//
// It replaced CubeAPM's documented search API (/api/traces/api/v1/search), which
// takes exactly one service per request: "show recent traces" had to fan out one
// request per service, so a cap on that fan-out silently hid whole services, and
// every filter had to be evaluated in Go over an over-fetched page. The search API
// is also not a complete view — on a live instance it returned 92 traces for a
// service that had 704 in the same window — so counts here intentionally do not
// match CubeAPM's own search screen.
//
// The trace waterfall (QueryTracesHeatmap) still uses the documented by-id
// endpoint, which speaks Jaeger's protobuf-JSON; the decoding for that shape lives
// at the bottom of this file.
type CubeAPMTraceSource struct{}

const (
	cubeAPMTracesQueryPath       = integrations.CubeAPMTracesQueryPath
	cubeAPMTracesFieldValuesPath = "/api/traces/select/logsql/field_values"
	cubeAPMTracesFieldNamesPath  = "/api/traces/select/logsql/field_names"
	cubeAPMTraceFetchPath        = "/api/traces/api/v1/traces/"
)

const (
	cubeAPMTraceQueryTimeout = 30 * time.Second
	cubeAPMDefaultTraceLimit = 100

	// cubeAPMMaxTraceLimit bounds one page. The response is streamed NDJSON with
	// no server-side cursor, so an unbounded limit asks for the whole window.
	cubeAPMMaxTraceLimit = 1000

	// cubeAPMTraceLabelValueLimit bounds a filter dropdown. field_values returns
	// the most frequent values first, so the cap drops only the long tail.
	cubeAPMTraceLabelValueLimit = 1000
)

// cubeAPMHeatmapDefaultLookback is how far back a by-id trace fetch searches when
// the caller supplies no window. The trace-detail view asks by trace_id alone, and
// an hour-long default silently returns nothing for any older trace — the same
// trap the OpenObserve and New Relic heatmaps document.
const cubeAPMHeatmapDefaultLookback = 30 * 24 * time.Hour

// cubeAPMTraceSpanSelector restricts every query to span records. The trace store
// also holds `span_event` records (exceptions and other OTel span events) as
// separate rows; without this they would be listed and counted as spans.
const cubeAPMTraceSpanSelector = `event.domain="span"`

// cubeAPMTraceLabelMapping maps canonical trace field names onto the LogsQL field
// that carries them. Unmapped names are used verbatim, so any raw field the user
// types — including `_resource.*` resource attributes — still works.
var cubeAPMTraceLabelMapping = map[string]string{
	"workload_name":             "service",
	"service_name":              "service",
	"service.name":              "service",
	"workload_namespace":        "_resource.k8s.namespace.name",
	"destination_workload_name": "peer.service",
	"span_name":                 "span_name",
	"span_kind":                 "span_kind",
	"http_status_code":          "http.response.status_code",
	"status_code":               "status_code",
	"trace_id":                  "trace_id",
	"span_id":                   "span_id",
	"parent_id":                 "parent_id",
	"parent_span_id":            "parent_id",
}

// cubeAPMTraceFieldAliases lists every LogsQL field a label may be backed by, for
// labels whose field depends on the SDK. A cluster runs several OTel
// semantic-convention generations at once (http.status_code was renamed to
// http.response.status_code), so a filter ORs across all of them rather than
// matching only the spans from one generation.
//
// Keyed by the PRIMARY LogsQL field — the value cubeAPMTraceLabelMapping maps a
// canonical label to — not by the canonical label. service.go rewrites a where
// clause through GetLabelMapping() before calling this source, so by the time a
// filter arrives here `workload_namespace` is already `_resource.k8s.namespace.name`;
// an alias table keyed by the canonical name would never match on that path.
// `resource` has no single-field mapping and so reaches the source unmapped.
var cubeAPMTraceFieldAliases = map[string][]string{
	"_resource.k8s.namespace.name": {"_resource.k8s.namespace.name", "_resource.service.namespace"},
	"http.response.status_code":    {"http.response.status_code", "http.status_code", "rpc.grpc.status_code"},
	"peer.service":                 {"peer.service", "server.address", "net.peer.name", "net.sock.peer.addr", "http.host"},
	"resource":                     {"http.route", "http.target", "url.path", "url.full", "http.url", "db.statement"},
}

// cubeAPMTraceFieldCandidates lists accepted attribute names per rendered column,
// in priority order, matched against span attributes and then resource attributes.
var cubeAPMTraceFieldCandidates = struct {
	Resource       []string
	HTTPStatusCode []string
	Destination    []string
	Namespace      []string
	StatusMessage  []string
}{
	Resource:       []string{"http.route", "http.target", "url.path", "url.full", "http.url", "db.statement"},
	HTTPStatusCode: []string{"http.response.status_code", "http.status_code", "rpc.grpc.status_code"},
	Destination:    []string{"peer.service", "server.address", "net.peer.name", "net.sock.peer.addr", "http.host"},
	Namespace:      []string{"k8s.namespace.name", "service.namespace"},
	StatusMessage:  []string{"status_message", "otel.status_description", "exception.message"},
}

// cubeAPMTraceRowFields are the LogsQL fields promoted onto dedicated span columns,
// so they are not repeated in SpanAttributes. Fields starting with "_" are engine
// metadata (_time, _stream, _msg) or resource attributes (_resource.*) and are
// handled separately.
var cubeAPMTraceRowFields = map[string]struct{}{
	"trace_id": {}, "span_id": {}, "parent_id": {}, "span_name": {}, "span_kind": {},
	"service": {}, "duration": {}, "status_code": {}, "env": {}, "event.domain": {},
}

// cubeAPMTraceInternalFields are engine fields no user filters on, hidden from the
// label picker.
var cubeAPMTraceInternalFields = map[string]struct{}{
	"_time": {}, "_stream": {}, "_stream_id": {}, "_msg": {}, "event.domain": {},
}

// cubeAPMTraceResourcePrefix marks a resource attribute in a LogsQL span record.
const cubeAPMTraceResourcePrefix = "_resource."

func (s *CubeAPMTraceSource) GetLabelMapping() map[string]string {
	return cubeAPMTraceLabelMapping
}

func (s *CubeAPMTraceSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_contains"}
}

// cubeAPMTraceFieldsFor returns the LogsQL fields a filter label resolves to. It
// accepts both a canonical label and an already-mapped field (see
// cubeAPMTraceFieldAliases for why both arrive here).
func cubeAPMTraceFieldsFor(label string) []string {
	if mapped, ok := cubeAPMTraceLabelMapping[label]; ok {
		label = mapped
	}
	if aliases, ok := cubeAPMTraceFieldAliases[label]; ok {
		return aliases
	}
	return []string{label}
}

// cubeAPMNormalizeTraceValue adapts a filter value to how CubeAPM stores it.
// LogsQL equality is case-sensitive and CubeAPM writes the bare upper-case OTel
// status (ERROR / OK / UNSET), while the Traces page sends the ClickHouse spelling
// STATUS_CODE_ERROR — so without this the status filter never matches.
func cubeAPMNormalizeTraceValue(field, value string) string {
	if field == "status_code" {
		return strings.TrimPrefix(strings.ToUpper(value), "STATUS_CODE_")
	}
	return value
}

// cubeAPMFilterValues normalizes a filter operand into the list of strings to
// compare against, so the scalar and list operators share one code path.
func cubeAPMFilterValues(val any) []string {
	switch v := val.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, fmt.Sprintf("%v", item))
		}
		return out
	default:
		return []string{fmt.Sprintf("%v", val)}
	}
}

// cubeAPMTraceWhere rewrites a canonical where-clause into one over LogsQL field
// names, ready for buildCubeAPMConditions.
//
// Each predicate becomes its own AND term, which is what lets a label with several
// backing fields expand to an OR, and keeps two labels that map to the same field
// (workload_name and service_name) from overwriting each other in one binary map.
// A negative operator wraps the whole OR in NOT: distributing it (NOT a OR NOT b)
// is true whenever the fields differ, which for an alias pair is every span.
// `_in` / `_nin` expand to OR-ed equalities because the LogsQL builder renders
// only scalar operators.
func cubeAPMTraceWhere(where query.QueryWhereClause) (query.QueryWhereClause, error) {
	var out query.QueryWhereClause

	labels := make([]string, 0, len(where.Binary))
	for label := range where.Binary {
		labels = append(labels, label)
	}
	sort.Strings(labels)

	for _, label := range labels {
		fields := cubeAPMTraceFieldsFor(label)

		ops := make([]string, 0, len(where.Binary[label]))
		for op := range where.Binary[label] {
			ops = append(ops, string(op))
		}
		sort.Strings(ops)

		for _, opName := range ops {
			op := query.BinaryWhereClauseType(opName)
			values := cubeAPMFilterValues(where.Binary[label][op])

			var matchOp query.BinaryWhereClauseType
			negate := false
			switch op {
			case query.Eq, query.In:
				matchOp = query.Eq
			case query.Nq, query.NotIn:
				matchOp, negate = query.Eq, true
			case query.Contains, query.ILike, query.Regex:
				matchOp = op
			default:
				return query.QueryWhereClause{}, fmt.Errorf("unsupported operator %q for CubeAPM traces (field %q)", op, label)
			}

			var terms []query.QueryWhereClause
			for _, field := range fields {
				for _, v := range values {
					terms = append(terms, query.QueryWhereClause{Binary: query.BinaryWhereClause{
						field: {matchOp: cubeAPMNormalizeTraceValue(field, v)},
					}})
				}
			}
			if len(terms) == 0 {
				continue
			}

			term := terms[0]
			if len(terms) > 1 {
				term = query.QueryWhereClause{Or: terms}
			}
			if negate {
				inner := term
				term = query.QueryWhereClause{Not: &inner}
			}
			out.And = append(out.And, term)
		}
	}

	for _, sub := range where.And {
		rewritten, err := cubeAPMTraceWhere(sub)
		if err != nil {
			return query.QueryWhereClause{}, err
		}
		out.And = append(out.And, rewritten)
	}

	if len(where.Or) > 0 {
		var ors []query.QueryWhereClause
		for _, sub := range where.Or {
			rewritten, err := cubeAPMTraceWhere(sub)
			if err != nil {
				return query.QueryWhereClause{}, err
			}
			ors = append(ors, rewritten)
		}
		out.And = append(out.And, query.QueryWhereClause{Or: ors})
	}

	if where.Not != nil {
		rewritten, err := cubeAPMTraceWhere(*where.Not)
		if err != nil {
			return query.QueryWhereClause{}, err
		}
		out.And = append(out.And, query.QueryWhereClause{Not: &rewritten})
	}

	return out, nil
}

// cubeAPMTraceBaseQuery renders the filter half of every span query: the span
// stream selector plus the request's conditions. A raw query typed in Code mode is
// passed through untouched, matching the logs source — rewriting it would fight
// the user.
func cubeAPMTraceBaseQuery(rawQuery string, where query.QueryWhereClause, env string) (string, error) {
	if raw := strings.TrimSpace(rawQuery); raw != "" {
		return raw, nil
	}

	selector := "{" + cubeAPMTraceSpanSelector + "}"
	if env != "" {
		selector = fmt.Sprintf("{env=%s, %s}", cubeAPMQuote(env), cubeAPMTraceSpanSelector)
	}

	rewritten, err := cubeAPMTraceWhere(where)
	if err != nil {
		return "", err
	}
	// The mapping is already applied by cubeAPMTraceWhere, so the builder gets
	// LogsQL field names and must not remap them.
	conditions, err := buildCubeAPMConditions(rewritten, nil)
	if err != nil {
		return "", err
	}
	if conditions == "" {
		return selector, nil
	}
	return selector + " " + conditions, nil
}

func cubeAPMTraceLimit(requested int) int {
	if requested <= 0 {
		return cubeAPMDefaultTraceLimit
	}
	if requested > cubeAPMMaxTraceLimit {
		return cubeAPMMaxTraceLimit
	}
	return requested
}

// cubeAPMTracePage appends the offset and limit pipes for one page.
func cubeAPMTracePage(offset, limit int) string {
	page := ""
	if offset > 0 {
		page += fmt.Sprintf(" | offset %d", offset)
	}
	return page + fmt.Sprintf(" | limit %d", cubeAPMTraceLimit(limit))
}

// cubeAPMTraceSort renders the sort pipe. Only time and duration are sortable
// span columns; anything else falls back to newest first. duration is stored as a
// numeric string, which LogsQL sorts numerically.
func cubeAPMTraceSort(orderBy []query.QueryOrderBy) string {
	field, dir := "_time", "desc"
	if len(orderBy) > 0 {
		if orderBy[0].Column == "duration_ns" || orderBy[0].Column == "duration" {
			field = "duration"
		}
		if strings.HasPrefix(strings.ToLower(string(orderBy[0].Order)), "asc") {
			dir = "asc"
		}
	}
	return fmt.Sprintf(" | sort by (%s %s)", field, dir)
}

// buildCubeAPMTraceListQuery renders the span listing query. A raw Code-mode query
// is sent as typed; the page size still applies through the request's limit.
func buildCubeAPMTraceListQuery(req TracesV3Request, env string) (string, error) {
	base, err := cubeAPMTraceBaseQuery(req.Query, req.QueryRequest.Where, env)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(req.Query) != "" {
		return base, nil
	}
	return base + cubeAPMTraceSort(req.QueryRequest.OrderBy) +
		cubeAPMTracePage(req.QueryRequest.Offset, req.QueryRequest.Limit), nil
}

// cubeAPMRawQueryHasPipes reports whether a Code-mode query is already a pipeline
// rather than a bare filter. Only a bare filter can have count or grouping pipes
// appended to it. A `|` inside a quoted value also counts, which errs towards the
// safe answer (an estimate) rather than a wrong total.
func cubeAPMRawQueryHasPipes(raw string) bool {
	return strings.Contains(raw, "|")
}

// cubeAPMTraceGroupFields are the dimensions of the grouped view.
const cubeAPMTraceGroupFields = "service, span_name, _resource.k8s.namespace.name"

// buildCubeAPMTraceGroupQuery renders the grouped view: one row per (service,
// operation, namespace) with call count, error count and latency percentiles,
// computed by CubeAPM over every matching span rather than over one page.
func buildCubeAPMTraceGroupQuery(req TracesV3Request, env string) (string, error) {
	if cubeAPMRawQueryHasPipes(req.Query) {
		return "", fmt.Errorf("the grouped trace view needs a filter-only LogsQL query; " +
			"remove the pipes (|) from the query to group its spans")
	}
	base, err := cubeAPMTraceBaseQuery(req.Query, req.QueryRequest.Where, env)
	if err != nil {
		return "", err
	}
	return base + " | stats by (" + cubeAPMTraceGroupFields + ") " +
		"count() count, " +
		"count() if (status_code:=ERROR) error_count, " +
		"quantile(0.95, duration) p95, " +
		"quantile(0.99, duration) p99, " +
		"max(duration) max_duration, " +
		"sum(duration) total_duration" +
		" | sort by (count desc)" +
		cubeAPMTracePage(req.QueryRequest.Offset, req.QueryRequest.Limit), nil
}

// isCubeAPMMissingTraceEndpoint reports whether a request failed because this
// CubeAPM does not serve the LogsQL trace API, as opposed to a bad query or an
// unreachable server. CubeAPM answers an unknown path with a 400 "unsupported path
// requested"; a reverse proxy in front of it answers 404.
func isCubeAPMMissingTraceEndpoint(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "unsupported path requested") || strings.Contains(msg, "HTTP 404")
}

// cubeAPMTracePost runs one LogsQL trace request. A missing endpoint is reported
// as such instead of surfacing as an opaque 400 or, worse, an empty result that
// reads as "no traces".
func cubeAPMTracePost(cfg integrations.CubeAPMConfig, path string, form neturl.Values, startMs, endMs int64) ([]byte, error) {
	startMs, endMs = cubeAPMTimeRangeMillis(startMs, endMs, time.Now())
	form.Set("start", strconv.FormatInt(startMs/1000, 10))
	form.Set("end", strconv.FormatInt(endMs/1000, 10))

	body, err := cubeAPMPostForm(cfg, cfg.URL+path, form, cubeAPMTraceQueryTimeout)
	if err != nil {
		if isCubeAPMMissingTraceEndpoint(err) {
			return nil, fmt.Errorf("CubeAPM at %s does not serve the trace query API (%s); "+
				"traces need a CubeAPM version that supports LogsQL trace search: %w", cfg.URL, path, err)
		}
		return nil, err
	}
	return body, nil
}

// cubeAPMTraceRows runs a LogsQL query and decodes the NDJSON rows.
func cubeAPMTraceRows(cfg integrations.CubeAPMConfig, logsQL string, startMs, endMs int64, limit int) ([]map[string]any, error) {
	form := neturl.Values{}
	form.Set("query", logsQL)
	if limit > 0 {
		form.Set("limit", strconv.Itoa(limit))
	}
	body, err := cubeAPMTracePost(cfg, cubeAPMTracesQueryPath, form, startMs, endMs)
	if err != nil {
		return nil, err
	}
	return decodeCubeAPMNDJSON(bytes.NewReader(body))
}

// cubeAPMTraceValues decodes a field_names / field_values response.
func cubeAPMTraceValues(body []byte) ([]string, error) {
	var decoded struct {
		Values []struct {
			Value string `json:"value"`
		} `json:"values"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("failed to parse CubeAPM trace field response: %w", err)
	}
	out := make([]string, 0, len(decoded.Values))
	for _, v := range decoded.Values {
		out = append(out, v.Value)
	}
	return out, nil
}

// cubeAPMInt reads an integer stats column. Quantiles can come back fractional.
func cubeAPMInt(v any) int64 {
	s := cubeAPMString(v)
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(f)
	}
	return 0
}

// cubeAPMCount reads a count column into an int. A count is never negative, and it
// saturates rather than wrapping when the platform int cannot hold the value.
func cubeAPMCount(v any) int {
	n := cubeAPMInt(v)
	if n < 0 {
		return 0
	}
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	return int(n)
}

// cubeAPMTraceRowToSpan projects one LogsQL span record onto the shared span model.
func cubeAPMTraceRowToSpan(row map[string]any) common.OpenTelemetryTrace {
	spanAttrs := map[string]string{}
	resourceAttrs := map[string]string{}
	for key, value := range row {
		switch {
		case strings.HasPrefix(key, cubeAPMTraceResourcePrefix):
			resourceAttrs[strings.TrimPrefix(key, cubeAPMTraceResourcePrefix)] = cubeAPMString(value)
		case strings.HasPrefix(key, "_"):
			continue
		default:
			if _, promoted := cubeAPMTraceRowFields[key]; !promoted {
				spanAttrs[key] = cubeAPMString(value)
			}
		}
	}

	serviceName := cubeAPMString(row["service"])
	if serviceName != "" {
		resourceAttrs["service.name"] = serviceName
	}

	startTime := cubeAPMString(row["_time"])
	durationNs := cubeAPMInt(row["duration"])
	spanName := cubeAPMString(row["span_name"])

	statusCode := strings.ToUpper(cubeAPMString(row["status_code"]))
	if statusCode == "" && strings.EqualFold(spanAttrs["error"], "true") {
		statusCode = "ERROR"
	}

	out := common.OpenTelemetryTrace{
		Timestamp:          startTime,
		StartTime:          startTime,
		EndTime:            cubeAPMEndTime(startTime, durationNs),
		TraceID:            cubeAPMString(row["trace_id"]),
		SpanID:             cubeAPMString(row["span_id"]),
		ParentSpanID:       cubeAPMString(row["parent_id"]),
		SpanName:           spanName,
		Operation:          spanName,
		SpanKind:           cubeAPMString(row["span_kind"]),
		ServiceName:        serviceName,
		Service:            serviceName,
		WorkloadName:       serviceName,
		WorkloadNamespace:  cubeAPMFirstAttr(spanAttrs, resourceAttrs, cubeAPMTraceFieldCandidates.Namespace),
		ResourceAttributes: resourceAttrs,
		SpanAttributes:     spanAttrs,
		DurationNs:         durationNs,
		Resource:           cubeAPMFirstAttr(spanAttrs, resourceAttrs, cubeAPMTraceFieldCandidates.Resource),
		HTTPStatusCode:     cubeAPMFirstAttr(spanAttrs, resourceAttrs, cubeAPMTraceFieldCandidates.HTTPStatusCode),
		DestinationName:    cubeAPMFirstAttr(spanAttrs, resourceAttrs, cubeAPMTraceFieldCandidates.Destination),
		StatusCode:         statusCode,
		StatusMessage:      cubeAPMFirstAttr(spanAttrs, resourceAttrs, cubeAPMTraceFieldCandidates.StatusMessage),
		TraceSource:        "cubeapm",
	}
	out.DestinationWorkload = out.DestinationName
	return out
}

// cubeAPMTraceGroupRowToValues projects one grouped-view stats row.
func cubeAPMTraceGroupRowToValues(row map[string]any) TraceGroupingValues {
	return TraceGroupingValues{
		Count:             cubeAPMCount(row["count"]),
		ErrorCount:        cubeAPMCount(row["error_count"]),
		P95Latency:        cubeAPMInt(row["p95"]),
		P99Latency:        cubeAPMInt(row["p99"]),
		MaxLatency:        cubeAPMInt(row["max_duration"]),
		WorkloadName:      cubeAPMString(row["service"]),
		WorkloadNamespace: cubeAPMString(row["_resource.k8s.namespace.name"]),
		SpanName:          cubeAPMString(row["span_name"]),
		DurationNS:        cubeAPMInt(row["total_duration"]),
	}
}

// cubeAPMTraceCount runs `<base> | stats <expr> count` and reads the number. A
// Code-mode query that already carries pipes returns -1, the contract's "estimate"
// signal: appending stats after the user's own `| limit` would count only that
// page and present it as the total.
func cubeAPMTraceCount(cfg integrations.CubeAPMConfig, req TracesV3Request, expr string) (int, error) {
	if cubeAPMRawQueryHasPipes(req.Query) {
		return -1, nil
	}
	base, err := cubeAPMTraceBaseQuery(req.Query, req.QueryRequest.Where, cfg.Env)
	if err != nil {
		return 0, err
	}
	rows, err := cubeAPMTraceRows(cfg, base+" | stats "+expr+" count", req.StartTime, req.EndTime, 0)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return cubeAPMCount(rows[0]["count"]), nil
}

func (s *CubeAPMTraceSource) GetQuery(ctx *security.RequestContext, req TracesV3Request) (string, error) {
	cfg, err := integrations.GetCubeAPMConfigs(ctx, req.AccountId)
	if err != nil {
		return "", fmt.Errorf("failed to get CubeAPM configs: %w", err)
	}
	return buildCubeAPMTraceListQuery(req, cfg.Env)
}

func (s *CubeAPMTraceSource) QueryTraces(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	cfg, err := integrations.GetCubeAPMConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get CubeAPM configs: %w", err)
	}
	logsQL, err := buildCubeAPMTraceListQuery(req, cfg.Env)
	if err != nil {
		return nil, err
	}
	ctx.GetLogger().Info("CubeAPM Trace Query", "query", logsQL)
	return queryCubeAPMTraces(cfg, logsQL, req)
}

// queryCubeAPMTraces runs a rendered listing query. Split from QueryTraces so it
// can be exercised against a test server without integration config lookup.
func queryCubeAPMTraces(cfg integrations.CubeAPMConfig, logsQL string, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	rows, err := cubeAPMTraceRows(cfg, logsQL, req.StartTime, req.EndTime, cubeAPMTraceLimit(req.QueryRequest.Limit))
	if err != nil {
		return nil, err
	}
	spans := make([]common.OpenTelemetryTrace, 0, len(rows))
	for _, row := range rows {
		spans = append(spans, cubeAPMTraceRowToSpan(row))
	}
	return spans, nil
}

func (s *CubeAPMTraceSource) CountTraces(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	cfg, err := integrations.GetCubeAPMConfigs(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("failed to get CubeAPM configs: %w", err)
	}
	n, err := cubeAPMTraceCount(cfg, req, "count()")
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}
	return common.OpenTelemetryTraceCount{Count: n}, nil
}

func (s *CubeAPMTraceSource) CountTracesByTrace(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	cfg, err := integrations.GetCubeAPMConfigs(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceCount{}, fmt.Errorf("failed to get CubeAPM configs: %w", err)
	}
	n, err := cubeAPMTraceCount(cfg, req, "count_uniq(trace_id)")
	if err != nil {
		return common.OpenTelemetryTraceCount{}, err
	}
	return common.OpenTelemetryTraceCount{Count: n}, nil
}

// QueryRootSpansByTrace backs the "By Traces" listing, reducing the span result to
// one representative root per trace via the shared helper.
func (s *CubeAPMTraceSource) QueryRootSpansByTrace(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	return queryRootSpansViaSpans(ctx, s, req)
}

// GetLabelValues answers a filter dropdown from field_values, which reads the
// distinct values across the whole window rather than a sample of spans. A label
// backed by several fields unions their values.
func (s *CubeAPMTraceSource) GetLabelValues(ctx *security.RequestContext, req TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	empty := common.OpenTelemetryTraceLabelValues{Label: req.Label, Values: []string{}}
	if !ctx.GetSecurityContext().CanReadAccountData(req.AccountId, "traces") {
		return empty, fmt.Errorf("access denied for account: %s", req.AccountId)
	}

	cfg, err := integrations.GetCubeAPMConfigs(ctx, req.AccountId)
	if err != nil {
		return empty, fmt.Errorf("failed to get CubeAPM configs: %w", err)
	}

	values, err := queryCubeAPMTraceLabelValues(cfg, req)
	if err != nil {
		return empty, err
	}
	return common.OpenTelemetryTraceLabelValues{Label: req.Label, Values: values}, nil
}

func queryCubeAPMTraceLabelValues(cfg integrations.CubeAPMConfig, req TracesV3LabelValuesRequest) ([]string, error) {
	base, err := cubeAPMTraceBaseQuery("", req.QueryRequest.Where, cfg.Env)
	if err != nil {
		return nil, err
	}

	seen := map[string]struct{}{}
	values := []string{}
	for _, field := range cubeAPMTraceFieldsFor(req.Label) {
		if !isSafeCubeAPMField(field) {
			return nil, fmt.Errorf("invalid or unsafe field name: %q", field)
		}
		form := neturl.Values{}
		form.Set("query", base)
		form.Set("field", field)
		form.Set("limit", strconv.Itoa(cubeAPMTraceLabelValueLimit))

		body, err := cubeAPMTracePost(cfg, cubeAPMTracesFieldValuesPath, form, req.StartTime, req.EndTime)
		if err != nil {
			return nil, err
		}
		fieldValues, err := cubeAPMTraceValues(body)
		if err != nil {
			return nil, err
		}
		for _, v := range fieldValues {
			if v == "" {
				continue
			}
			if _, dup := seen[v]; dup {
				continue
			}
			seen[v] = struct{}{}
			values = append(values, v)
		}
	}
	sort.Strings(values)
	return values, nil
}

// QueryLabels lists the span field names present in the window. Names are returned
// as LogsQL fields — resource attributes keep their `_resource.` prefix — because
// a label picked here is sent back verbatim as a filter field.
func (s *CubeAPMTraceSource) QueryLabels(ctx *security.RequestContext, req FetchTraceLabelRequest) ([]OutputTraceLabel, error) {
	cfg, err := integrations.GetCubeAPMConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get CubeAPM configs: %w", err)
	}
	labels, err := queryCubeAPMTraceLabels(cfg, req)
	if err != nil {
		// An empty list is the documented "no backend discovery" answer, and
		// FetchTraceLabels falls back to the canonical set — a better outcome than
		// failing the whole label request.
		ctx.GetLogger().Warn("CubeAPMTraceSource.QueryLabels: field_names query failed", "error", err)
		return []OutputTraceLabel{}, nil
	}
	return labels, nil
}

func queryCubeAPMTraceLabels(cfg integrations.CubeAPMConfig, req FetchTraceLabelRequest) ([]OutputTraceLabel, error) {
	base, err := cubeAPMTraceBaseQuery("", query.QueryWhereClause{}, cfg.Env)
	if err != nil {
		return nil, err
	}
	form := neturl.Values{}
	form.Set("query", base)

	body, err := cubeAPMTracePost(cfg, cubeAPMTracesFieldNamesPath, form, req.StartTime, req.EndTime)
	if err != nil {
		return nil, err
	}
	names, err := cubeAPMTraceValues(body)
	if err != nil {
		return nil, err
	}

	sort.Strings(names)
	labels := make([]OutputTraceLabel, 0, len(names))
	for _, name := range names {
		if _, internal := cubeAPMTraceInternalFields[name]; internal {
			continue
		}
		labels = append(labels, OutputTraceLabel{Label: name, Attributes: map[string]any{"type": "string"}})
	}
	return labels, nil
}

func (s *CubeAPMTraceSource) QueryGroupedTraces(ctx *security.RequestContext, req TracesV3Request) ([]TraceGroupingValues, error) {
	cfg, err := integrations.GetCubeAPMConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get CubeAPM configs: %w", err)
	}
	logsQL, err := buildCubeAPMTraceGroupQuery(req, cfg.Env)
	if err != nil {
		return nil, err
	}
	ctx.GetLogger().Info("CubeAPM Trace Group Query", "query", logsQL)

	rows, err := cubeAPMTraceRows(cfg, logsQL, req.StartTime, req.EndTime, 0)
	if err != nil {
		return nil, err
	}
	groups := make([]TraceGroupingValues, 0, len(rows))
	for _, row := range rows {
		groups = append(groups, cubeAPMTraceGroupRowToValues(row))
	}
	return groups, nil
}

func (s *CubeAPMTraceSource) QueryGroupedTracesCount(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	cfg, err := integrations.GetCubeAPMConfigs(ctx, req.AccountId)
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{}, fmt.Errorf("failed to get CubeAPM configs: %w", err)
	}
	n, err := cubeAPMTraceCount(cfg, req, "count_uniq("+cubeAPMTraceGroupFields+")")
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{}, err
	}
	return common.OpenTelemetryTraceGroupCount{Count: n}, nil
}

// ---------------------------------------------------------------------------
// Trace waterfall: CubeAPM's by-id endpoint, in Jaeger protobuf-JSON.
//
// Ids arrive as base64-encoded byte arrays rather than hex strings, tag values are
// carried in a typed union (v_str / v_int64 / v_float64 / v_bool), and durations
// are bare nanosecond counts. Everything below that looks like ceremony is that
// translation.
// ---------------------------------------------------------------------------

// cubeAPMTag is one Jaeger protobuf-JSON tag. Exactly one v_* field is populated,
// selected by v_type — which is itself omitted for the string case, since 0 is
// the zero value and protobuf-JSON drops zero-valued fields.
type cubeAPMTag struct {
	Key     string       `json:"key"`
	VType   int          `json:"v_type"`
	VStr    string       `json:"v_str"`
	VBool   bool         `json:"v_bool"`
	VInt64  json.Number  `json:"v_int64"`
	VFloat  json.Number  `json:"v_float64"`
	VBinary string       `json:"v_binary"`
	Value   *json.Number `json:"value,omitempty"`
}

// String renders a tag value as the display/filter string.
func (t cubeAPMTag) String() string {
	switch {
	case t.VStr != "":
		return t.VStr
	case t.VInt64 != "":
		return t.VInt64.String()
	case t.VFloat != "":
		return t.VFloat.String()
	case t.VBinary != "":
		return t.VBinary
	case t.VBool:
		return "true"
	case t.VType == 1:
		// v_type 1 is BOOL; a false value is omitted from the JSON, so the type
		// tag is the only evidence the tag was present and false.
		return "false"
	default:
		return ""
	}
}

type cubeAPMSpanRef struct {
	TraceID string `json:"trace_id"`
	SpanID  string `json:"span_id"`
}

type cubeAPMProcess struct {
	ServiceName string       `json:"service_name"`
	Tags        []cubeAPMTag `json:"tags"`
}

type cubeAPMSpanLog struct {
	Timestamp string       `json:"timestamp"`
	Fields    []cubeAPMTag `json:"fields"`
}

type cubeAPMSpan struct {
	TraceID       string           `json:"trace_id"`
	SpanID        string           `json:"span_id"`
	OperationName string           `json:"operation_name"`
	References    []cubeAPMSpanRef `json:"references"`
	Flags         int              `json:"flags"`
	StartTime     string           `json:"start_time"`
	Duration      json.RawMessage  `json:"duration"`
	Tags          []cubeAPMTag     `json:"tags"`
	Logs          []cubeAPMSpanLog `json:"logs"`
	Process       *cubeAPMProcess  `json:"process"`
}

// cubeAPMTraceFetch is the by-id fetch response — a bare span list, with no
// enclosing trace object.
type cubeAPMTraceFetch struct {
	Spans []cubeAPMSpan `json:"spans"`
}

// cubeAPMDecodeID converts a base64 id to the lowercase hex every other provider
// here reports. Ids that already look like hex are passed through, so a build that
// switches to the hex encoding does not silently produce garbage ids.
func cubeAPMDecodeID(encoded string) string {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return ""
	}
	if isHexID(encoded) {
		return strings.ToLower(encoded)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		// Jaeger emits standard base64, but url-safe is a cheap second guess and
		// costs nothing to try before giving up on the id entirely.
		raw, err = base64.URLEncoding.DecodeString(encoded)
		if err != nil {
			return encoded
		}
	}
	return hex.EncodeToString(raw)
}

// isHexID reports whether a string is already a hex-encoded id of trace or span
// length (16 or 32 characters).
func isHexID(s string) bool {
	if len(s) != 16 && len(s) != 32 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

// cubeAPMDurationNanos reads a span duration in nanoseconds.
//
// CubeAPM documents the field as a bare nanosecond count and that is what the API
// returns, but protobuf's canonical JSON encoding for a Duration is a string with
// a unit suffix ("0.040965161s"). Accepting both means a build that switches
// encodings produces correct waterfalls rather than zero-length spans.
func cubeAPMDurationNanos(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}

	var asNumber json.Number
	if err := json.Unmarshal(raw, &asNumber); err == nil {
		if n, err := asNumber.Int64(); err == nil {
			return n
		}
		if f, err := asNumber.Float64(); err == nil {
			return int64(f)
		}
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err != nil {
		return 0
	}
	asString = strings.TrimSpace(asString)
	if asString == "" {
		return 0
	}
	// Longest suffix first: "us"/"ms"/"ns" must be tested before the bare "s".
	for _, unit := range []struct {
		suffix string
		scale  float64
	}{
		{"ns", 1},
		{"us", 1e3},
		{"ms", 1e6},
		{"s", 1e9},
	} {
		if !strings.HasSuffix(asString, unit.suffix) {
			continue
		}
		n, err := strconv.ParseFloat(strings.TrimSuffix(asString, unit.suffix), 64)
		if err != nil {
			return 0
		}
		return int64(n * unit.scale)
	}
	n, err := strconv.ParseFloat(asString, 64)
	if err != nil {
		return 0
	}
	return int64(n)
}

// cubeAPMSpanToTrace projects one Jaeger-shaped span onto the shared span model.
func cubeAPMSpanToTrace(span cubeAPMSpan) common.OpenTelemetryTrace {
	spanAttrs := make(map[string]string, len(span.Tags))
	for _, tag := range span.Tags {
		if tag.Key != "" {
			spanAttrs[tag.Key] = tag.String()
		}
	}

	resourceAttrs := map[string]string{}
	serviceName := ""
	if span.Process != nil {
		serviceName = span.Process.ServiceName
		for _, tag := range span.Process.Tags {
			if tag.Key != "" {
				resourceAttrs[tag.Key] = tag.String()
			}
		}
	}
	if serviceName != "" {
		resourceAttrs["service.name"] = serviceName
	}

	parentSpanID := ""
	if len(span.References) > 0 {
		parentSpanID = cubeAPMDecodeID(span.References[0].SpanID)
	}

	durationNs := cubeAPMDurationNanos(span.Duration)
	startTime := strings.TrimSpace(span.StartTime)

	out := common.OpenTelemetryTrace{
		Timestamp:          startTime,
		StartTime:          startTime,
		TraceID:            cubeAPMDecodeID(span.TraceID),
		SpanID:             cubeAPMDecodeID(span.SpanID),
		ParentSpanID:       parentSpanID,
		SpanName:           span.OperationName,
		Operation:          span.OperationName,
		SpanKind:           spanAttrs["span.kind"],
		ServiceName:        serviceName,
		Service:            serviceName,
		WorkloadName:       serviceName,
		WorkloadNamespace:  cubeAPMFirstAttr(spanAttrs, resourceAttrs, cubeAPMTraceFieldCandidates.Namespace),
		ResourceAttributes: resourceAttrs,
		SpanAttributes:     spanAttrs,
		DurationNs:         durationNs,
		Resource:           cubeAPMFirstAttr(spanAttrs, resourceAttrs, cubeAPMTraceFieldCandidates.Resource),
		HTTPStatusCode:     cubeAPMFirstAttr(spanAttrs, resourceAttrs, cubeAPMTraceFieldCandidates.HTTPStatusCode),
		DestinationName:    cubeAPMFirstAttr(spanAttrs, resourceAttrs, cubeAPMTraceFieldCandidates.Destination),
		StatusCode:         cubeAPMStatusCode(spanAttrs),
		StatusMessage:      spanAttrs["otel.status_description"],
		TraceSource:        "cubeapm",
	}
	out.DestinationWorkload = out.DestinationName

	if endNs := cubeAPMEndTime(startTime, durationNs); endNs != "" {
		out.EndTime = endNs
	}

	// Span logs are OTel span events; the exception event is the one the UI
	// surfaces, so carrying them through is what makes error detail visible.
	for _, log := range span.Logs {
		attrs := make(map[string]string, len(log.Fields))
		name := "log"
		for _, f := range log.Fields {
			attrs[f.Key] = f.String()
			if f.Key == "event" && f.String() != "" {
				name = f.String()
			}
		}
		out.EventsTimestamp = append(out.EventsTimestamp, log.Timestamp)
		out.EventsName = append(out.EventsName, name)
		out.EventsAttributes = append(out.EventsAttributes, attrs)
	}

	return out
}

// cubeAPMFirstAttr returns the first candidate present on the span or its resource.
func cubeAPMFirstAttr(spanAttrs, resourceAttrs map[string]string, candidates []string) string {
	for _, c := range candidates {
		if v := spanAttrs[c]; v != "" {
			return v
		}
		if v := resourceAttrs[c]; v != "" {
			return v
		}
	}
	return ""
}

// cubeAPMStatusCode normalizes the several ways a span records failure. `error=true`
// is the Jaeger convention and outlives the OTel status on spans exported through a
// Jaeger-compatible path, so it is checked alongside otel.status_code rather than
// instead of it.
func cubeAPMStatusCode(spanAttrs map[string]string) string {
	if v := spanAttrs["otel.status_code"]; v != "" {
		return strings.ToUpper(v)
	}
	if strings.EqualFold(spanAttrs["error"], "true") {
		return "ERROR"
	}
	if v := spanAttrs["status.code"]; v != "" {
		return strings.ToUpper(v)
	}
	return ""
}

// cubeAPMEndTime derives a span's end from its start and duration. Returns "" when
// the start is unparseable, so a bad timestamp leaves the field empty rather than
// inventing an epoch-anchored end.
func cubeAPMEndTime(startTime string, durationNs int64) string {
	if startTime == "" || durationNs <= 0 {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339Nano, startTime)
	if err != nil {
		return ""
	}
	return parsed.Add(time.Duration(durationNs)).UTC().Format(time.RFC3339Nano)
}

// cubeAPMSpanStartNanos parses a span's start timestamp into a comparable value.
//
// Ordering the RFC3339 strings directly is wrong, and wrong in a way that looks
// right in most fixtures: the format trims trailing zeros from the fractional
// second, so 100ms and 120ms render as "…04.1Z" and "…04.12Z". Those compare on
// their first differing byte — 'Z' (0x5A) against '2' (0x32) — which puts the
// LATER span first. It only shows up when two spans differ in fractional-second
// digit width, which is exactly the sub-millisecond spacing a trace waterfall is
// made of.
//
// An unparseable timestamp sorts as 0 rather than failing the query; a span with
// a malformed start is still worth showing.
func cubeAPMSpanStartNanos(span common.OpenTelemetryTrace) int64 {
	ts := span.Timestamp
	if ts == "" {
		ts = span.StartTime
	}
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(ts))
	if err != nil {
		return 0
	}
	return parsed.UnixNano()
}

// sortCubeAPMSpans orders spans by the request's primary sort column. The search
// API returns traces in its own order and the span list within a trace is
// execution order, so without this the table's sort header does nothing.
//
// Sort keys are computed once up front rather than inside the comparator: a
// comparator that parses timestamps would do O(n log n) parses and would have to
// swallow errors mid-sort.
func sortCubeAPMSpans(spans []common.OpenTelemetryTrace, orderBy []query.QueryOrderBy) {
	column, desc := "timestamp", true
	if len(orderBy) > 0 && orderBy[0].Column != "" {
		column = orderBy[0].Column
		desc = strings.HasPrefix(string(orderBy[0].Order), "desc")
	}

	type keyed struct {
		span common.OpenTelemetryTrace
		key  int64
	}

	rows := make([]keyed, len(spans))
	for i, span := range spans {
		key := cubeAPMSpanStartNanos(span)
		if column == "duration_ns" {
			key = span.DurationNs
		}
		rows[i] = keyed{span: span, key: key}
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if desc {
			return rows[j].key < rows[i].key
		}
		return rows[i].key < rows[j].key
	})

	for i := range rows {
		spans[i] = rows[i].span
	}
}

// QueryTracesHeatmap returns every span of one trace, which the UI lays out as the
// trace waterfall. This is the one place CubeAPM's by-id fetch endpoint is used —
// it returns the complete trace regardless of how many spans the search page held.
func (s *CubeAPMTraceSource) QueryTracesHeatmap(ctx *security.RequestContext, req TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error) {
	if req.TraceId == "" {
		return nil, fmt.Errorf("trace_id is required for the trace heatmap")
	}

	cfg, err := integrations.GetCubeAPMConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get CubeAPM configs: %w", err)
	}

	startSec, endSec := cubeAPMHeatmapWindowSeconds(req.StartTime, req.EndTime, time.Now())
	params := neturl.Values{}
	params.Set("start", strconv.FormatInt(startSec, 10))
	params.Set("end", strconv.FormatInt(endSec, 10))

	endpoint := cfg.URL + cubeAPMTraceFetchPath + neturl.PathEscape(req.TraceId) + "?" + params.Encode()
	ctx.GetLogger().Info("CubeAPM Trace Heatmap Query", "endpoint", endpoint)

	body, err := cubeAPMGet(cfg, endpoint, cubeAPMTraceQueryTimeout)
	if err != nil {
		return nil, err
	}

	var fetched cubeAPMTraceFetch
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&fetched); err != nil {
		return nil, fmt.Errorf("failed to decode CubeAPM trace: %w", err)
	}

	spans := make([]common.OpenTelemetryTrace, 0, len(fetched.Spans))
	for _, span := range fetched.Spans {
		spans = append(spans, cubeAPMSpanToTrace(span))
	}
	// Execution order, not ingest order — the waterfall reads wrong otherwise.
	// Ordered through the shared ascending sort so it uses parsed timestamps
	// rather than raw strings (see cubeAPMSpanStartNanos).
	sortCubeAPMSpans(spans, []query.QueryOrderBy{{Column: "timestamp", Order: query.Asc}})

	return cubeAPMTracesToHeatmap(spans), nil
}

// cubeAPMHeatmapWindowSeconds resolves the window for a by-id trace fetch. A
// caller-supplied window is padded because a trace's earliest span can begin
// fractionally before the row's timestamp and its latest can end after it;
// clipping either truncates the waterfall.
func cubeAPMHeatmapWindowSeconds(startMs, endMs int64, now time.Time) (int64, int64) {
	if startMs > 0 && endMs > 0 {
		pad := int64(5 * time.Minute / time.Second)
		return startMs/1000 - pad, endMs/1000 + pad
	}
	return now.Add(-cubeAPMHeatmapDefaultLookback).Unix(), now.Unix()
}

// cubeAPMTracesToHeatmap projects parsed spans onto the heatmap shape.
func cubeAPMTracesToHeatmap(traces []common.OpenTelemetryTrace) []common.OpenTelemetryTraceHeatMap {
	out := make([]common.OpenTelemetryTraceHeatMap, 0, len(traces))
	for _, t := range traces {
		out = append(out, common.OpenTelemetryTraceHeatMap{
			Timestamp:          t.Timestamp,
			ResourceAttributes: t.ResourceAttributes,
			SpanName:           t.SpanName,
			StatusCode:         t.StatusCode,
			DurationNs:         t.DurationNs,
			SpanAttributes:     t.SpanAttributes,
			TraceID:            t.TraceID,
			SpanID:             t.SpanID,
			ServiceName:        t.ServiceName,
			EventsName:         t.EventsName,
			EventsAttributes:   t.EventsAttributes,
		})
	}
	return out
}
