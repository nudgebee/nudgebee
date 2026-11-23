package observability

import (
	"errors"
	"fmt"
	"math"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"slices"
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
	// The trace UI filters the callee by destination_name; a span has only the one peer
	// name, so both canonical spellings resolve to it.
	"destination_name": "attributes.net.peer.name",
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
	return splunkEnterpriseFilterOperators
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

// prepareSplunkEnterpriseTraceGroup is the trace schema's per-group rewrite. It runs on
// every binary group in the tree, not only the top-level one: a caller-OR-callee search
// nests the unsupported field inside an _or, where a bare field matching nothing would
// take the whole branch down with it.
func prepareSplunkEnterpriseTraceGroup(binary query.BinaryWhereClause) (query.BinaryWhereClause, int, error) {
	kept, dropped := stripSplunkEnterpriseUnsupportedTraceFields(binary)

	if _, ok := kept["duration_ns"]; ok {
		// Duration is end_time - start_time, computed after the search; the search
		// clause has no field to compare, and rendering it bare would match no span.
		// buildSplunkEnterpriseTraceFilter lifts every duration predicate it can honour
		// into a post-search stage before this runs, so reaching here means a caller
		// skipped it.
		return nil, 0, fmt.Errorf("splunk enterprise: duration_ns is not supported as a search term")
	}
	return kept, dropped, nil
}

var splunkEnterpriseTraceFilterSchema = splunkEnterpriseFilterSchema{
	mapping: splunkEnterpriseTraceLabelMapping,
	prepare: prepareSplunkEnterpriseTraceGroup,
}

func buildSplunkEnterpriseTraceWhereClause(where query.QueryWhereClause) (string, error) {
	clause, err := buildSplunkEnterpriseFilter(where, splunkEnterpriseTraceFilterSchema)
	if errors.Is(err, errSplunkEnterpriseGroupInexpressible) {
		// Nothing left to render would mean no restriction at all. A filter that cannot
		// be honoured must not widen the result set.
		return "", fmt.Errorf("splunk enterprise: trace %w", err)
	}
	return clause, err
}

// splunkEnterpriseTraceFilter is a trace where clause split into the two places SPL can
// honour it. Every trace SPL builder consumes BOTH halves, and places the post-stage
// before its own head/sort/stats/top — so the list, the counts, the grouping and the facet
// values always agree on which spans matched.
type splunkEnterpriseTraceFilter struct {
	// search is the part rendered into the search command; "" means no restriction.
	search string
	// postStage filters on what the search cannot express: " | eval ... | where ...",
	// or "" when there is nothing to filter after the search.
	postStage string
}

// render emits `search index="<index>" <terms...> <search>` followed by the post-stage.
// extraTerms are ANDed into the search command ahead of the filter.
func (f splunkEnterpriseTraceFilter) render(index string, extraTerms ...string) string {
	parts := append([]string{fmt.Sprintf(`search index="%s"`, index)}, extraTerms...)
	if f.search != "" {
		parts = append(parts, f.search)
	}
	return strings.Join(parts, " ") + f.postStage
}

// splunkEnterpriseDurationPredicate is one duration_ns comparison lifted out of the tree.
type splunkEnterpriseDurationPredicate struct {
	op    query.BinaryWhereClauseType
	value any
}

// buildSplunkEnterpriseTraceFilter splits a trace filter into its search clause and its
// post-search stage.
//
// duration_ns is the one canonical field a span does not carry: it is end_time -
// start_time, which only an eval can compute, and a `where` on that eval cannot sit
// inside the search expression. So a duration predicate is lifted into the post-stage,
// which is only equivalent while it is ANDed with the rest of the filter. It may appear
// at the top level or under _and members, recursively — the path
// ApplyDefaultTraceFilters produces when it wraps the request as {_and: [request,
// defaults]} — but under an _or or a _not, lifting it out would change what matches, so
// that is an explicit error rather than a silently different answer.
func buildSplunkEnterpriseTraceFilter(where query.QueryWhereClause) (splunkEnterpriseTraceFilter, error) {
	var durations []splunkEnterpriseDurationPredicate
	rest, err := rewriteSplunkEnterpriseTraceWhere(where, true, &durations)
	if err != nil {
		return splunkEnterpriseTraceFilter{}, err
	}
	search, err := buildSplunkEnterpriseTraceWhereClause(rest)
	if err != nil {
		return splunkEnterpriseTraceFilter{}, err
	}
	postStage, err := renderSplunkEnterpriseDurationStage(durations)
	if err != nil {
		return splunkEnterpriseTraceFilter{}, err
	}
	return splunkEnterpriseTraceFilter{search: search, postStage: postStage}, nil
}

// rewriteSplunkEnterpriseTraceWhere returns a copy of where with every duration predicate
// moved into durations and every status_code value widened to its equivalence class. The
// caller's clause is never mutated: other providers in a fan-out may still read it.
// andReachable is false once the walk has passed through an _or or a _not.
func rewriteSplunkEnterpriseTraceWhere(
	where query.QueryWhereClause, andReachable bool, durations *[]splunkEnterpriseDurationPredicate,
) (query.QueryWhereClause, error) {
	var out query.QueryWhereClause

	if len(where.Binary) > 0 {
		out.Binary = make(query.BinaryWhereClause, len(where.Binary))
		fields := make([]string, 0, len(where.Binary))
		for field := range where.Binary {
			fields = append(fields, field)
		}
		// Sorted so the rendered post-stage is stable across runs.
		sort.Strings(fields)
		for _, field := range fields {
			ops := where.Binary[field]
			switch {
			case field == "duration_ns":
				if !andReachable {
					return query.QueryWhereClause{}, fmt.Errorf(
						"splunk enterprise: duration_ns can only be filtered when it is ANDed with the rest of the " +
							"filter - Splunk computes a span's duration after the search, so it cannot be combined " +
							"under _or or _not")
				}
				opNames := make([]string, 0, len(ops))
				for op := range ops {
					opNames = append(opNames, string(op))
				}
				sort.Strings(opNames)
				for _, op := range opNames {
					*durations = append(*durations, splunkEnterpriseDurationPredicate{
						op: query.BinaryWhereClauseType(op), value: ops[query.BinaryWhereClauseType(op)],
					})
				}
			case isSplunkEnterpriseTraceStatusField(field):
				kept, extra := canonicaliseSplunkEnterpriseTraceStatus(field, ops)
				out.Binary[field] = kept
				out.And = append(out.And, extra...)
			default:
				out.Binary[field] = ops
			}
		}
	}

	for _, c := range where.And {
		child, err := rewriteSplunkEnterpriseTraceWhere(c, andReachable, durations)
		if err != nil {
			return query.QueryWhereClause{}, err
		}
		out.And = append(out.And, child)
	}
	for _, c := range where.Or {
		child, err := rewriteSplunkEnterpriseTraceWhere(c, false, durations)
		if err != nil {
			return query.QueryWhereClause{}, err
		}
		out.Or = append(out.Or, child)
	}
	if where.Not != nil {
		child, err := rewriteSplunkEnterpriseTraceWhere(*where.Not, false, durations)
		if err != nil {
			return query.QueryWhereClause{}, err
		}
		out.Not = &child
	}
	return out, nil
}

// splunkEnterpriseTraceDurationSymbols are the comparisons a duration predicate accepts.
// Equality is deliberately absent: the eval computes the duration in floating point (see
// splunkEnterpriseTraceDurationExpr), so an exact match on nanoseconds is not reliable.
var splunkEnterpriseTraceDurationSymbols = map[query.BinaryWhereClauseType]string{
	query.Gt: ">", query.Gte: ">=", query.Lt: "<", query.Lte: "<=",
}

// renderSplunkEnterpriseDurationStage renders the lifted duration predicates as the
// post-search stage, or "" when there are none.
func renderSplunkEnterpriseDurationStage(preds []splunkEnterpriseDurationPredicate) (string, error) {
	if len(preds) == 0 {
		return "", nil
	}
	conds := make([]string, 0, len(preds))
	for _, p := range preds {
		symbol, ok := splunkEnterpriseTraceDurationSymbols[p.op]
		if !ok {
			return "", fmt.Errorf("splunk enterprise: duration_ns supports only _gt, _gte, _lt and _lte, got %s", p.op)
		}
		n, err := splunkEnterpriseFilterNumber(p.op, p.value)
		if err != nil {
			return "", fmt.Errorf("splunk enterprise: duration_ns %w", err)
		}
		conds = append(conds, splunkTraceDurationCol+symbol+n)
	}
	expr, err := splunkEnterpriseTraceDurationExpr()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(" | eval %s=%s | where %s", splunkTraceDurationCol, expr, strings.Join(conds, " AND ")), nil
}

// splunkEnterpriseTraceStatusClasses are the spellings of one OTel span status. The
// splunk_hec exporter writes the STATUS_CODE_* form, but a caller sends whatever it has:
// the UI the value it displays, the event enrichers "Error", OTLP-derived code the enum
// number. Splunk term matching is case-insensitive, so one spelling per case-fold covers
// "error", "Error" and "ERROR".
var splunkEnterpriseTraceStatusClasses = [][]string{
	{"STATUS_CODE_ERROR", "Error", "2"},
	{"STATUS_CODE_OK", "Ok", "1"},
	{"STATUS_CODE_UNSET", "Unset", "0"},
}

// isSplunkEnterpriseTraceStatusField reports whether a filter field is the span status,
// under its canonical name or any Splunk spelling it may already have been mapped to.
func isSplunkEnterpriseTraceStatusField(field string) bool {
	return field == "status_code" || slices.Contains(splunkEnterpriseTraceFields.StatusCode, field)
}

// splunkEnterpriseTraceStatusClass returns every spelling of a status value, or nil when
// the value is not a known status (it then passes through unchanged).
func splunkEnterpriseTraceStatusClass(v any) []string {
	s := strings.TrimSpace(fmt.Sprintf("%v", v))
	for _, class := range splunkEnterpriseTraceStatusClasses {
		for _, spelling := range class {
			if strings.EqualFold(s, spelling) {
				return class
			}
		}
	}
	return nil
}

// canonicaliseSplunkEnterpriseTraceStatus widens status predicates to OR over the
// equivalence class of the value sent, rather than rewriting it to one spelling: an index
// fed by a pipeline that writes the enum number must still match. _eq/_neq become
// _in/_not_in over the class. A predicate whose target operator is already taken in the
// group is returned as its own group for the caller to AND in — merging two _in sets
// would turn their AND into an OR.
func canonicaliseSplunkEnterpriseTraceStatus(
	field string, ops map[query.BinaryWhereClauseType]any,
) (map[query.BinaryWhereClauseType]any, []query.QueryWhereClause) {
	kept := make(map[query.BinaryWhereClauseType]any, len(ops))
	var extra []query.QueryWhereClause
	place := func(op query.BinaryWhereClauseType, values []string) {
		if _, taken := kept[op]; taken {
			extra = append(extra, query.QueryWhereClause{Binary: query.BinaryWhereClause{field: {op: values}}})
			return
		}
		kept[op] = values
	}

	opNames := make([]string, 0, len(ops))
	for op := range ops {
		opNames = append(opNames, string(op))
	}
	sort.Strings(opNames)
	for _, name := range opNames {
		op := query.BinaryWhereClauseType(name)
		v := ops[op]
		switch op {
		case query.Eq, query.Nq:
			class := splunkEnterpriseTraceStatusClass(v)
			if class == nil {
				kept[op] = v
				continue
			}
			if op == query.Eq {
				place(query.In, class)
			} else {
				place(query.NotIn, class)
			}
		case query.In, query.NotIn:
			values, err := splunkEnterpriseListValues(op, v)
			if err != nil {
				// Left as sent: the builder reports the malformed operand.
				kept[op] = v
				continue
			}
			var widened []string
			for _, value := range values {
				spellings := []string{value}
				if class := splunkEnterpriseTraceStatusClass(value); class != nil {
					spellings = class
				}
				for _, s := range spellings {
					if !slices.Contains(widened, s) {
						widened = append(widened, s)
					}
				}
			}
			place(op, widened)
		default:
			kept[op] = v
		}
	}
	return kept, extra
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

// splunkEnterpriseTracePage resolves a request's page into (offset, limit), refusing a
// window deeper than one search may return: the page is cut from the first offset+limit
// rows, so that is what Splunk has to produce.
func splunkEnterpriseTracePage(req TracesQueryBuilderRequest) (int, int, error) {
	limit := splunkEnterpriseTraceLimit(req.Limit)
	offset := max(req.Offset, 0)
	// Compared by subtraction: limit is already clamped to the ceiling, so this cannot
	// overflow the way offset+limit can for an offset near MaxInt.
	if offset > splunkEnterpriseTraceMaxLimit-limit {
		return 0, 0, fmt.Errorf("splunk enterprise: offset %d + limit %d is past the %d-row search ceiling - narrow the time range or filters",
			offset, limit, splunkEnterpriseTraceMaxLimit)
	}
	return offset, limit, nil
}

// splunkEnterpriseOffsetStage drops the first offset rows of a stream already cut to
// offset+limit. A running row number rather than `head N | tail M | reverse`: tail takes
// the last M rows of whatever is there, so on a short last page (25 rows, offset 20,
// limit 10) it returns rows 16-25 — five of them repeated from the previous page.
func splunkEnterpriseOffsetStage(offset int) string {
	if offset <= 0 {
		return ""
	}
	return fmt.Sprintf(" | streamstats count AS nb_tr_row | where nb_tr_row>%d", offset)
}

// splunkEnterpriseSortAscending reads one order_by direction. An empty direction is
// ascending, as in SQL and in pickRootSpans.
func splunkEnterpriseSortAscending(order query.QuerySortOrder) (bool, error) {
	switch order {
	case "", query.Asc, query.AscNullsFirst, query.AscNullsLast:
		return true, nil
	case query.Desc, query.DescNullsFirst, query.DescNullsLast:
		return false, nil
	}
	return false, fmt.Errorf("splunk enterprise: unknown sort order %q", order)
}

// isSplunkEnterpriseNaturalTraceOrder reports whether order_by asks for what the index
// already returns: newest first. Splunk streams events in descending _time, so that order
// needs no sort at all — which keeps the default listing a `head`, and never a buffered
// sort of the whole window.
func isSplunkEnterpriseNaturalTraceOrder(orderBy []query.QueryOrderBy) bool {
	if len(orderBy) == 0 {
		return true
	}
	if len(orderBy) > 1 || orderBy[0].Column != "timestamp" {
		return false
	}
	asc, err := splunkEnterpriseSortAscending(orderBy[0].Order)
	return err == nil && !asc
}

// splunkEnterpriseTraceSortFields maps the span columns a trace listing may be ordered by
// onto the Splunk field that orders them. timestamp is _time (the span's start, which is
// what the exporter stamps as the event time); duration_ns is the post-search eval;
// trace_id is what the event enrichers order a trace-tree fetch by.
var splunkEnterpriseTraceSortFields = map[string]string{
	"timestamp":   "_time",
	"duration_ns": splunkTraceDurationCol,
	"trace_id":    "trace_id",
}

// splunkEnterpriseTraceSortClause renders a non-natural order_by as `sort` keys, and
// reports whether they read the duration eval. Any other column is an error: ignoring it
// is what made `duration_ns desc` come back in ingest order.
func splunkEnterpriseTraceSortClause(orderBy []query.QueryOrderBy) (string, bool, error) {
	keys := make([]string, 0, len(orderBy)+1)
	needsDuration, hasTime := false, false
	for _, ob := range orderBy {
		field, ok := splunkEnterpriseTraceSortFields[ob.Column]
		if !ok {
			return "", false, fmt.Errorf("splunk enterprise: traces can only be ordered by timestamp, duration_ns or trace_id, got %q", ob.Column)
		}
		asc, err := splunkEnterpriseSortAscending(ob.Order)
		if err != nil {
			return "", false, err
		}
		sign := "-"
		if asc {
			sign = "+"
		}
		keys = append(keys, sign+field)
		needsDuration = needsDuration || field == splunkTraceDurationCol
		hasTime = hasTime || field == "_time"
	}
	// A tie on the requested keys (synthetic spans often share a round duration) is broken
	// by recency, so the same rows land on the same page every time the page is asked for.
	if !hasTime {
		keys = append(keys, "-_time")
	}
	return strings.Join(keys, ", "), needsDuration, nil
}

// buildSplunkEnterpriseTraceSPL renders the span search, one page of it.
//
// The default order (none, or timestamp desc) is the index's own and costs a `head`. Any
// other order is a `sort` bounded to offset+limit rows, which Splunk keeps as a running
// top-N — but it still reads every matching span in the window on the search head, so it
// is only rendered when asked for.
func buildSplunkEnterpriseTraceSPL(req TracesV3Request, index string) (string, error) {
	if !integrations.IsSafeSplunkIndexName(index) {
		return "", fmt.Errorf("invalid or unsafe index name: %q", index)
	}

	filter, err := buildSplunkEnterpriseTraceFilter(req.QueryRequest.Where)
	if err != nil {
		return "", err
	}
	offset, limit, err := splunkEnterpriseTracePage(req.QueryRequest)
	if err != nil {
		return "", err
	}

	spl := filter.render(index)
	if isSplunkEnterpriseNaturalTraceOrder(req.QueryRequest.OrderBy) {
		spl += fmt.Sprintf(" | head %d", offset+limit)
	} else {
		keys, needsDuration, err := splunkEnterpriseTraceSortClause(req.QueryRequest.OrderBy)
		if err != nil {
			return "", err
		}
		// The duration filter's post-stage already evaluated the column.
		if needsDuration && filter.postStage == "" {
			expr, err := splunkEnterpriseTraceDurationExpr()
			if err != nil {
				return "", err
			}
			spl += fmt.Sprintf(" | eval %s=%s", splunkTraceDurationCol, expr)
		}
		spl += fmt.Sprintf(" | sort limit=%d %s", offset+limit, keys)
	}
	spl += splunkEnterpriseOffsetStage(offset)
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

	// A caller-supplied query wins, matching the log source and the Loki/Signoz sources.
	// Two guards make that safe: validateSplunkEnterpriseQuery inside
	// runSplunkEnterpriseSearch keeps it read-only, and the index scope check keeps it on
	// this account's trace index — without it `search index=_internal` was accepted here.
	spl := strings.TrimSpace(req.Query)
	if spl != "" {
		if err := validateSplunkEnterpriseTraceQueryScope(spl, index); err != nil {
			return nil, err
		}
	} else {
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
	filter, err := buildSplunkEnterpriseTraceFilter(req.QueryRequest.Where)
	if err != nil {
		return "", err
	}

	spl := filter.render(index)
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

// splunkEnterpriseTraceRootExpr is true for a span with no parent under any accepted
// spelling — the same test pickRootSpans applies to ParentSpanID. nullif() because the
// exporter writes a root's parent_span_id as the empty string, not as an absent field.
const splunkEnterpriseTraceRootExpr = `isnull(coalesce(nullif('parent_span_id', ""), nullif('parentSpanId', ""), ` +
	`nullif('parent_id', ""), nullif('references.parent_span_id', "")))`

// splunkEnterpriseTraceSpansPerTrace bounds how many non-root spans each trace on a page
// contributes, so the page's spans fit in the one search's row ceiling and no trace is cut
// off by another trace's size. A trace's root is always kept on top of that (the exporter
// stamps _time from the span start, so newest-first puts the root LAST, where a plain cap
// would drop it first). Only past limit > ceiling/2 can the ceiling still bind.
// splunkEnterpriseTraceByTraceMaxLimit is the largest By Traces page whose worst case -
// every trace at one span plus its root - fits the row ceiling.
const splunkEnterpriseTraceByTraceMaxLimit = splunkEnterpriseTraceMaxLimit / 2

func splunkEnterpriseTraceSpansPerTrace(limit int) int {
	return max(1, splunkEnterpriseTraceMaxLimit/limit-1)
}

// splunkEnterpriseByTraceNaturalOrderReason explains the one order the By Traces view can
// be listed in. buildSplunkEnterpriseTraceByTraceSPL refuses with it and the capability
// advertises it, so the two cannot describe different rules.
const splunkEnterpriseByTraceNaturalOrderReason = "splunk enterprise: the By Traces view is ordered newest first"

// ByTraceNaturalOrderOnlyReason implements byTraceNaturalOrderOnlySource: this reader ranks
// By Traces pages by each trace's newest span while streaming, so the rank that decides
// page membership is the order, and no other can be served.
func (s *SplunkEnterpriseTraceSource) ByTraceNaturalOrderOnlyReason() string {
	return splunkEnterpriseByTraceNaturalOrderReason
}

// buildSplunkEnterpriseTraceByTraceSPL renders one page of the "By Traces" view: every
// matching span of exactly the requested traces, which pickRootSpans then reduces to one
// row per trace.
//
// The page is cut on TRACES, not spans — a head over spans (what this view used to run)
// returned fewer traces than asked, and a non-root span as the "root". A trace is ranked by
// its FIRST, i.e. newest, matching span in the newest-first stream: the running distinct
// count gives that span its trace's rank, and a running min per trace carries the rank to
// the trace's older spans, so a later span of an early trace cannot leak onto the next
// page. Both are streaming state, O(distinct traces) — no eventstats buffering on the
// search head — but they do read the whole window, as the dc() count query already does.
//
// The representative contract is unchanged: a trace is listed when ANY of its spans match
// the filter, shown by its root if the root matched, else by its earliest matching span.
// (A root-only query would list almost nothing for a downstream workload's filter — a
// service that is never a root — while the count still reported every trace it is in.)
func buildSplunkEnterpriseTraceByTraceSPL(req TracesV3Request, index string) (string, error) {
	if !integrations.IsSafeSplunkIndexName(index) {
		return "", fmt.Errorf("invalid or unsafe index name: %q", index)
	}
	// Page membership is by each trace's newest span; re-ranking by duration or oldest
	// first would need every trace's spans before any could be ranked. pickRootSpans still
	// orders the page it is handed, so a sort here would reorder one page, not the list.
	// The reason is the one the capability advertises, so a client told the view is
	// natural-order-only and a caller that asked anyway read the same sentence.
	if !isSplunkEnterpriseNaturalTraceOrder(req.QueryRequest.OrderBy) {
		return "", fmt.Errorf("%s; order_by %v is not supported there",
			splunkEnterpriseByTraceNaturalOrderReason, req.QueryRequest.OrderBy)
	}
	filter, err := buildSplunkEnterpriseTraceFilter(req.QueryRequest.Where)
	if err != nil {
		return "", err
	}
	offset, limit, err := splunkEnterpriseTracePage(req.QueryRequest)
	if err != nil {
		return "", err
	}
	if limit > splunkEnterpriseTraceByTraceMaxLimit {
		// Past this every trace is down to its root plus one span and the page can still
		// outgrow the row ceiling, where Splunk truncates silently - roots first.
		return "", fmt.Errorf("splunk enterprise: the By Traces view returns at most %d traces per page", splunkEnterpriseTraceByTraceMaxLimit)
	}

	spl := filter.render(index)
	spl += " | streamstats dc(trace_id) AS nb_tr_n"
	spl += " | streamstats min(nb_tr_n) AS nb_tr_rank BY trace_id"
	spl += fmt.Sprintf(" | where nb_tr_rank>%d AND nb_tr_rank<=%d", offset, offset+limit)
	spl += " | streamstats count AS nb_tr_span BY trace_id"
	spl += fmt.Sprintf(" | where nb_tr_span<=%d OR %s", splunkEnterpriseTraceSpansPerTrace(limit), splunkEnterpriseTraceRootExpr)
	spl += " | fields *"
	return spl, nil
}

// splunkEnterpriseTruncationMarkers are the phrases that make a Splunk WARN mean "these
// results may be incomplete" (memory ceilings, truncation, an auto-finalized search). A
// bare "limit" is deliberately absent: Splunk warns "The limit has been reached for log
// messages in info.csv" on healthy searches, and failing the page on that would be wrong.
var splunkEnterpriseTruncationMarkers = []string{"memory", "max_mem", "truncat", "exceed", "auto-finalized"}

func isSplunkEnterpriseTruncationWarning(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range splunkEnterpriseTruncationMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// QueryRootSpansByTrace backs the "By Traces" listing: one page of traces, reduced to one
// representative span each.
func (s *SplunkEnterpriseTraceSource) QueryRootSpansByTrace(
	ctx *security.RequestContext, req TracesV3Request,
) ([]common.OpenTelemetryTrace, error) {
	// A hand-written query already decides its own rows; it keeps the span-level contract
	// (and QueryTraces' index scope check).
	if strings.TrimSpace(req.Query) != "" {
		return queryRootSpansViaSpans(ctx, s, req)
	}

	cfg, index, err := splunkEnterpriseTraceContext(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}
	spl, err := buildSplunkEnterpriseTraceByTraceSPL(req, index)
	if err != nil {
		return nil, err
	}
	if err := validateSplunkEnterpriseQuery(spl); err != nil {
		return nil, err
	}

	startTime, endTime := splunkEnterpriseTimeRangeSeconds(req.StartTime, req.EndTime, time.Now())
	ctx.GetLogger().Info("Splunk Enterprise Trace By-Trace Query", "query", spl)

	// The row ceiling, not the page size: limit counts traces here, and each one brings
	// every matching span (bounded by splunkEnterpriseTraceSpansPerTrace).
	rows, warnings, err := execSplunkEnterpriseSearchWithWarnings(cfg, spl, startTime, endTime,
		splunkEnterpriseTraceMaxLimit, splunkEnterpriseTraceSearchTimeout)
	if err != nil {
		return nil, err
	}
	// The ranking lives in streamstats state. If Splunk ran short of memory for it, the
	// ranks are no longer the ones the next page will see, so the page would silently
	// overlap or skip traces — a wrong page, reported as an error rather than served.
	for _, w := range warnings {
		ctx.GetLogger().Warn("Splunk Enterprise By-Trace search warning", "warning", w)
		if isSplunkEnterpriseTruncationWarning(w) {
			return nil, fmt.Errorf("splunk enterprise: the By Traces page may be incomplete, Splunk reported: %s", w)
		}
	}
	// Splunk's row cap truncates without a message. The span cap keeps a page under the
	// ceiling by construction; a result AT the ceiling means that did not hold, and the
	// rows cut would be the roots (they come last newest-first).
	if len(rows) >= splunkEnterpriseTraceMaxLimit {
		return nil, fmt.Errorf("splunk enterprise: the By Traces page reached the %d-span ceiling and may be incomplete - narrow the filter or lower the page size", splunkEnterpriseTraceMaxLimit)
	}
	return pickRootSpans(convertSplunkEnterpriseSpans(rows), req.QueryRequest.OrderBy), nil
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
	// The facet dropdowns are narrowed by the filters already applied (namespace=demo
	// offers only demo's workloads), so the values go through the same filter as the list.
	filter, err := buildSplunkEnterpriseTraceFilter(req.QueryRequest.Where)
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{}, err
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
	spl := buildSplunkEnterpriseTraceLabelValuesSPL(filter, index, col)

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

// buildSplunkEnterpriseTraceLabelValuesSPL renders the facet query for one (already
// mapped and validated) field. col=* sits in the search command beside the filter; the
// filter's post-stage runs before `top` counts anything.
func buildSplunkEnterpriseTraceLabelValuesSPL(filter splunkEnterpriseTraceFilter, index, col string) string {
	return fmt.Sprintf(`%s | top limit=%d %s | fields %s`,
		filter.render(index, col+"=*"), splunkEnterpriseLabelValueLimit, col, col)
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

// splunkEnterpriseTraceDurationExpr renders a span's duration in nanoseconds as an eval
// expression: a precomputed field if the pipeline emits one, otherwise end - start. The
// group percentiles and the duration filter/sort share it, so a span counted as 300ms in
// one is 300ms in the other. tonumber() is what keeps the arithmetic numeric rather than
// lexicographic when the exporter writes the timestamps as JSON strings.
//
// Note that tonumber() yields a double, and a nanosecond epoch (~1.8e18) is past
// float64's exact-integer range, so this subtraction carries a rounding error —
// measured at 128ns against Splunk 10.4.2, where a seeded 394ms span aggregated to
// 393999872ns. That is 0.00006% of a millisecond-scale latency and invisible in the
// percentiles, so it is left alone rather than worked around with an epoch offset; for a
// duration filter it means a span within ~256ns of the threshold can fall on either side.
// The per-span duration the list and waterfall render does NOT go through this path:
// splunkEnterpriseSpanDurationNs parses with int64 and is exact.
func splunkEnterpriseTraceDurationExpr() (string, error) {
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
	return fmt.Sprintf("coalesce(%s)", strings.Join(durationParts, ", ")), nil
}

// splunkEnterpriseTraceGroupSortColumns maps the Trace Group view's sort columns onto the
// stats aliases. duration_ns is the row's representative latency, which
// convertSplunkEnterpriseTraceGroups sets to the p99.
var splunkEnterpriseTraceGroupSortColumns = map[string]string{
	"count":       splunkTraceCountCol,
	"error_count": splunkTraceErrorsCol,
	"p99_latency": splunkTraceP99Col,
	"p95_latency": splunkTraceP95Col,
	"max_latency": splunkTraceMaxCol,
	"duration_ns": splunkTraceP99Col,
}

// splunkEnterpriseTraceGroupKeyCols are the stats BY columns, i.e. one group's identity.
var splunkEnterpriseTraceGroupKeyCols = []string{
	splunkTraceServiceCol, splunkTraceNamespaceCol, splunkTraceNameCol,
	splunkTraceResourceCol, splunkTraceDestCol, splunkTraceHTTPCodeCol,
}

// splunkEnterpriseTraceGroupSortClause renders order_by over the group rows, defaulting
// to the busiest groups first. The group key is appended as a tie-break, so equal counts
// fall in the same order on every page request and paging cannot repeat or skip a group.
func splunkEnterpriseTraceGroupSortClause(orderBy []query.QueryOrderBy) (string, error) {
	keys := make([]string, 0, len(orderBy)+len(splunkEnterpriseTraceGroupKeyCols))
	for _, ob := range orderBy {
		col, ok := splunkEnterpriseTraceGroupSortColumns[ob.Column]
		if !ok {
			return "", fmt.Errorf("splunk enterprise: trace groups can only be ordered by count, error_count, "+
				"p95_latency, p99_latency, max_latency or duration_ns, got %q", ob.Column)
		}
		asc, err := splunkEnterpriseSortAscending(ob.Order)
		if err != nil {
			return "", err
		}
		sign := "-"
		if asc {
			sign = "+"
		}
		keys = append(keys, sign+col)
	}
	if len(keys) == 0 {
		keys = append(keys, "-"+splunkTraceCountCol)
	}
	for _, col := range splunkEnterpriseTraceGroupKeyCols {
		keys = append(keys, "+"+col)
	}
	return strings.Join(keys, ", "), nil
}

// buildSplunkEnterpriseTraceGroupSPL aggregates spans into the Trace Group view.
//
// Every grouped field is coalesced to a non-null default for the same load-bearing reason
// as the log-group query: `stats ... BY f` drops every event where f is null, so one
// absent field would empty the whole view rather than degrading one column.
//
// Order and offset apply to the group rows after `stats`. The group set is small, so
// sorting it is cheap — unlike a sort over spans.
func buildSplunkEnterpriseTraceGroupSPL(req TracesV3Request, index string, limit int) (string, error) {
	if !integrations.IsSafeSplunkIndexName(index) {
		return "", fmt.Errorf("invalid or unsafe index name: %q", index)
	}
	if limit <= 0 || limit > splunkEnterpriseTraceGroupLimit {
		limit = splunkEnterpriseTraceGroupLimit
	}
	offset := max(req.QueryRequest.Offset, 0)
	if offset > splunkEnterpriseTraceMaxLimit-limit { // subtraction: cannot overflow
		return "", fmt.Errorf("splunk enterprise: offset %d + limit %d is past the %d-row search ceiling",
			offset, limit, splunkEnterpriseTraceMaxLimit)
	}
	sortKeys, err := splunkEnterpriseTraceGroupSortClause(req.QueryRequest.OrderBy)
	if err != nil {
		return "", err
	}

	filter, err := buildSplunkEnterpriseTraceFilter(req.QueryRequest.Where)
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

	durationExpr, err := splunkEnterpriseTraceDurationExpr()
	if err != nil {
		return "", err
	}
	assignments = append(assignments, fmt.Sprintf("%s=%s", splunkTraceDurationCol, durationExpr))

	spl := filter.render(index)
	spl += " | eval " + strings.Join(assignments, ", ")

	// The error count is a conditional count rather than a second search: OTel writes the
	// error status as STATUS_CODE_ERROR and some pipelines as the numeric 2, so both are
	// matched, case-insensitively.
	spl += fmt.Sprintf(
		` | stats count AS %s, count(eval(match(%s, "(?i)error") OR %s=2)) AS %s,`+
			` perc99(%s) AS %s, perc95(%s) AS %s, max(%s) AS %s BY %s | sort limit=%d %s`,
		splunkTraceCountCol,
		splunkTraceStatusCol, splunkTraceStatusCol, splunkTraceErrorsCol,
		splunkTraceDurationCol, splunkTraceP99Col,
		splunkTraceDurationCol, splunkTraceP95Col,
		splunkTraceDurationCol, splunkTraceMaxCol,
		strings.Join(splunkEnterpriseTraceGroupKeyCols, ", "),
		offset+limit, sortKeys,
	)
	spl += splunkEnterpriseOffsetStage(offset)
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
	// The count is of every group (up to the group cap), not of one page of them: a caller
	// that reuses the list request, paging and all, must not get the page size back.
	req.QueryRequest.Limit = 0
	req.QueryRequest.Offset = 0
	req.QueryRequest.OrderBy = nil
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

	spans := sortSplunkEnterpriseSpansByStart(convertSplunkEnterpriseSpans(rows))

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

// sortSplunkEnterpriseSpansByStart orders spans by execution, not ingest: the collector
// batches, so _time ordering can interleave spans that ran sequentially and the waterfall
// would render out of order. The key is the numeric start in nanoseconds. Comparing the
// RFC3339Nano strings (the old key) mis-orders spans within a second, because the format
// trims trailing zeros: "…:05.1Z" sorts after "…:05.123Z" although it started 23ms earlier.
func sortSplunkEnterpriseSpansByStart(spans []common.OpenTelemetryTrace) []common.OpenTelemetryTrace {
	type keyed struct {
		start int64
		span  common.OpenTelemetryTrace
	}
	ordered := make([]keyed, len(spans))
	for i, span := range spans {
		start, err := strconv.ParseInt(span.StartTimeUnixNano, 10, 64)
		if err != nil {
			// No start_time on the span: its Timestamp is the ingest time instead.
			if t, perr := time.Parse(time.RFC3339Nano, span.Timestamp); perr == nil {
				start = t.UnixNano()
			}
		}
		ordered[i] = keyed{start: start, span: span}
	}
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].start < ordered[j].start })
	out := make([]common.OpenTelemetryTrace, len(ordered))
	for i, k := range ordered {
		out[i] = k.span
	}
	return out
}

// splunkEnterpriseTraceTransformCommands are the only commands a caller-supplied trace
// query may run after its opening search. Each one reshapes the events already in the
// pipeline and reads nothing else. An allowlist rather than a longer denylist, because the
// commands that read data need not name an index at all: `| tstats count from
// datamodel=Authentication`, `| from datamodel:...`, `| pivot`, `| metadata`, `lookup`,
// `join`/`append` all take their scope from something other than an index selector, so
// they pass any index check. `search` stays allowed; its selectors are still checked.
var splunkEnterpriseTraceTransformCommands = map[string]bool{
	"where": true, "eval": true, "search": true,
	"stats": true, "eventstats": true, "streamstats": true, "timechart": true, "chart": true,
	"sort": true, "rename": true, "table": true, "fields": true,
	"head": true, "tail": true, "reverse": true, "dedup": true, "fillnull": true,
	"bin": true, "bucket": true, "top": true, "rare": true, "convert": true,
}

// validateSplunkEnterpriseTraceQueryScope keeps a caller-supplied trace query on the
// account's trace index, failing closed on anything it cannot vouch for. The read-only
// guard (validateSplunkEnterpriseQuery) runs separately. Without this a free-form query
// could read any index the integration's Splunk role can see.
//
// The same rules the metric source applies to its queries:
//   - after the opening search, every command must be in
//     splunkEnterpriseTraceTransformCommands;
//   - subsearches, macros and the lookup() function are refused outright (each reads data
//     no selector in the query describes);
//   - every index selector, in any spelling Splunk honours (`index::x`, `index IN (...)`,
//     `"index"=x`, `!=`, comparisons), must name the configured index and must not be
//     negated — `OR NOT index="otel_traces"` names only that index yet selects every other;
//   - the opening search must select it as index="<index>", at the top level and not as
//     an OR operand: `index="otel_traces" OR sourcetype=*` also reads the role's default
//     indexes. (SPL binds OR tighter than the implicit AND, so a selector that is not
//     directly beside an OR constrains the whole search.)
func validateSplunkEnterpriseTraceQueryScope(spl, index string) error {
	trimmed := strings.TrimSpace(spl)
	for i, segment := range splunkEnterpriseSplitPipeline(trimmed) {
		fields := strings.Fields(segment)
		if len(fields) == 0 {
			if i == 0 {
				return fmt.Errorf(`splunk enterprise: a trace query must begin with a search of index="%s", not a generating command`, index)
			}
			return fmt.Errorf("splunk enterprise: a trace query may not contain an empty pipeline stage")
		}
		// The opening segment is the (implicit or explicit) search; its terms are checked
		// through the selectors below.
		if command := strings.ToLower(fields[0]); i > 0 && !splunkEnterpriseTraceTransformCommands[command] {
			return fmt.Errorf("splunk enterprise: command %q is not permitted in a trace query - after the opening search "+
				"only commands that reshape its events are allowed", command)
		}
	}

	selectors, err := splunkEnterpriseTraceIndexSelectors(trimmed)
	if err != nil {
		return err
	}
	scoped := false
	for _, sel := range selectors {
		if sel.negated {
			return fmt.Errorf(`splunk enterprise: a negated index selector (NOT index=...) is not permitted in a trace query`)
		}
		if (sel.op != "=" && sel.op != "::") || sel.value != index {
			return fmt.Errorf(`splunk enterprise: a trace query may only read the configured trace index (index="%s"), got index%s%s`,
				index, sel.op, sel.value)
		}
		// Quoted, so `otel_traces_secret` cannot pass as `otel_traces`.
		if sel.op == "=" && sel.quoted && sel.segment == 0 && sel.depth == 0 && !sel.orOperand {
			scoped = true
		}
	}
	if !scoped {
		return fmt.Errorf(`splunk enterprise: a trace query must open with a search scoped to the configured trace index `+
			`(index="%s", ANDed with the rest of the search)`, index)
	}
	return nil
}

// splunkEnterpriseTraceIndexSelector is one place a query selects an index.
type splunkEnterpriseTraceIndexSelector struct {
	op        string // "=" (also for "=="), "::", "!=", "<", ">", " IN"
	value     string // empty for IN
	quoted    bool
	negated   bool // preceded by NOT or !, directly or through an enclosing group
	segment   int  // pipeline stage, 0 being the opening search
	depth     int  // parenthesis depth
	orOperand bool // directly beside an OR
}

// splunkEnterpriseTraceIndexSelectors finds every index selector in an SPL string,
// outside quoted literals. The field name is matched case-insensitively, which is stricter
// than Splunk. A quoted literal is skipped as a unit, so `name="index=_audit"` is a value,
// not a selector. An unterminated quote is an error: it would hide everything after it.
// Subsearches, macros and lookup() are refused here, where quoting is known.
func splunkEnterpriseTraceIndexSelectors(spl string) ([]splunkEnterpriseTraceIndexSelector, error) {
	var selectors []splunkEnterpriseTraceIndexSelector
	var groups []bool // per open parenthesis: is the group negated
	inNegatedGroup := func() bool { return len(groups) > 0 && groups[len(groups)-1] }
	segment := 0
	n := len(spl)
	for i := 0; i < n; {
		var name string
		next := i
		switch c := spl[i]; {
		case c == '[':
			return nil, fmt.Errorf("splunk enterprise: subsearches ([ ... ]) are not permitted in a trace query")
		case c == '`':
			return nil, fmt.Errorf("splunk enterprise: search macros (`...`) are not permitted in a trace query")
		case c == '|':
			segment++
			groups = nil
			i++
			continue
		case c == '(':
			groups = append(groups, inNegatedGroup() || splunkEnterpriseTraceNegatedBefore(spl, i))
			i++
			continue
		case c == ')':
			if len(groups) > 0 {
				groups = groups[:len(groups)-1]
			}
			i++
			continue
		case c == '"':
			end, ok := splunkEnterpriseTraceQuotedEnd(spl, i)
			if !ok {
				return nil, fmt.Errorf("splunk enterprise: trace query has an unterminated quote")
			}
			// Backslashes dropped so an escaped spelling of the name is still caught.
			name, next = strings.ReplaceAll(spl[i+1:end-1], `\`, ""), end
		case c == '\'':
			// eval/where name a field in single quotes ('index'). Only that exact token is
			// read: search does not treat apostrophes as quotes, so pairing them up would
			// let `don't ... index=_audit ... '` hide a selector between two of them.
			if n-i >= 7 && strings.EqualFold(spl[i:i+7], "'index'") {
				name, next = "index", i+7
			} else {
				i++
				continue
			}
		case isSplunkEnterpriseTraceNameByte(c):
			for next < n && isSplunkEnterpriseTraceNameByte(spl[next]) {
				next++
			}
			name = spl[i:next]
		default:
			i++
			continue
		}
		if strings.EqualFold(name, "lookup") && splunkEnterpriseTraceFollowedByParen(spl, next) {
			// The eval/where lookup() function reads a lookup table on the search head, the
			// same data `| inputlookup` is denied for.
			return nil, fmt.Errorf("splunk enterprise: the lookup() function is not permitted in a trace query")
		}
		if !strings.EqualFold(name, "index") {
			i = next
			continue
		}
		op, valueAt := splunkEnterpriseTraceIndexOperator(spl, next)
		if op == "" {
			// `BY index`, `| fields index`: a reference to the field, not a selector.
			i = next
			continue
		}
		sel := splunkEnterpriseTraceIndexSelector{
			op: op, segment: segment, depth: len(groups),
			negated:   inNegatedGroup() || splunkEnterpriseTraceNegatedBefore(spl, i),
			orOperand: splunkEnterpriseTraceWordBefore(spl, i) == "or",
		}
		i = valueAt
		if op != " IN" {
			for i < n && strings.ContainsRune(" \t\r\n", rune(spl[i])) {
				i++
			}
			if i < n && spl[i] == '"' {
				end, ok := splunkEnterpriseTraceQuotedEnd(spl, i)
				if !ok {
					return nil, fmt.Errorf("splunk enterprise: trace query has an unterminated quote")
				}
				sel.value, sel.quoted, i = spl[i+1:end-1], true, end
			} else {
				start := i
				for i < n && !strings.ContainsRune(" \t\r\n()[]|,\"", rune(spl[i])) {
					i++
				}
				sel.value = spl[start:i]
			}
		}
		sel.orOperand = sel.orOperand || splunkEnterpriseTraceWordAfter(spl, i) == "or"
		selectors = append(selectors, sel)
	}
	return selectors, nil
}

// splunkEnterpriseTraceWordBefore returns, lower-cased, the word that ends just before pos
// across whitespace ("" when something other than a word precedes it).
func splunkEnterpriseTraceWordBefore(spl string, pos int) string {
	j := pos
	for j > 0 && strings.ContainsRune(" \t\r\n", rune(spl[j-1])) {
		j--
	}
	start := j
	for start > 0 && isSplunkEnterpriseTraceNameByte(spl[start-1]) {
		start--
	}
	return strings.ToLower(spl[start:j])
}

// splunkEnterpriseTraceWordAfter returns, lower-cased, the word that starts at or after
// pos across whitespace ("" when something other than a word follows).
func splunkEnterpriseTraceWordAfter(spl string, pos int) string {
	for pos < len(spl) && strings.ContainsRune(" \t\r\n", rune(spl[pos])) {
		pos++
	}
	end := pos
	for end < len(spl) && isSplunkEnterpriseTraceNameByte(spl[end]) {
		end++
	}
	return strings.ToLower(spl[pos:end])
}

// splunkEnterpriseTraceNegatedBefore reports whether the token starting at pos is negated:
// preceded, across whitespace, by `!` or by the word NOT.
func splunkEnterpriseTraceNegatedBefore(spl string, pos int) bool {
	j := pos
	for j > 0 && strings.ContainsRune(" \t\r\n", rune(spl[j-1])) {
		j--
	}
	if j > 0 && spl[j-1] == '!' {
		return true
	}
	return splunkEnterpriseTraceWordBefore(spl, pos) == "not"
}

// splunkEnterpriseTraceFollowedByParen reports whether the next non-whitespace byte at or
// after i is '(' — i.e. the token before i is called as a function.
func splunkEnterpriseTraceFollowedByParen(spl string, i int) bool {
	for i < len(spl) && strings.ContainsRune(" \t\r\n", rune(spl[i])) {
		i++
	}
	return i < len(spl) && spl[i] == '('
}

// splunkEnterpriseTraceIndexOperator reads the operator after an `index` token, skipping
// whitespace. It returns "" when none follows, and where the value starts.
func splunkEnterpriseTraceIndexOperator(spl string, i int) (string, int) {
	for i < len(spl) && strings.ContainsRune(" \t\r\n", rune(spl[i])) {
		i++
	}
	rest := spl[i:]
	switch {
	case strings.HasPrefix(rest, "=="):
		return "=", i + 2
	case strings.HasPrefix(rest, "="):
		return "=", i + 1
	case strings.HasPrefix(rest, "::"):
		return "::", i + 2
	case strings.HasPrefix(rest, "!="):
		return "!=", i + 2
	case strings.HasPrefix(rest, "<"), strings.HasPrefix(rest, ">"):
		return rest[:1], i + 1
	case len(rest) > 2 && strings.EqualFold(rest[:2], "in") && strings.ContainsRune(" \t\n\r(", rune(rest[2])):
		return " IN", i + 2
	}
	return "", i
}

// splunkEnterpriseTraceQuotedEnd returns the index just past the quote that closes the one
// at start, honouring backslash escapes as splunkEnterpriseSplitPipeline does.
func splunkEnterpriseTraceQuotedEnd(spl string, start int) (int, bool) {
	quote := spl[start]
	for i := start + 1; i < len(spl); i++ {
		switch spl[i] {
		case '\\':
			i++
		case quote:
			return i + 1, true
		}
	}
	return 0, false
}

// isSplunkEnterpriseTraceNameByte reports whether b can continue a field name, so
// `k8s.index.name` or `my_index` is one token rather than a selector. ':' is excluded
// because `index::x` is itself a selector.
func isSplunkEnterpriseTraceNameByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') ||
		b == '_' || b == '.' || b == '-'
}
