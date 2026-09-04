package observability

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
// CubeAPM's trace API speaks Jaeger's protobuf-JSON: ids are base64-encoded byte
// arrays rather than hex strings, tag values are carried in a typed union
// (v_str / v_int64 / v_float64 / v_bool), and durations are bare nanosecond
// counts. Everything in this file that looks like ceremony is that translation.
type CubeAPMTraceSource struct{}

const (
	cubeAPMTraceSearchPath = "/api/traces/api/v1/search"
	cubeAPMTraceFetchPath  = "/api/traces/api/v1/traces/"
)

const (
	cubeAPMTraceQueryTimeout = 30 * time.Second
	cubeAPMDefaultTraceLimit = 100
	cubeAPMMaxTraceLimit     = 2000
)

// cubeAPMTraceOverFetch multiplies the requested page size when filters have to be
// applied locally (see filterCubeAPMSpans). Without it, a filter that matches one
// span in ten would return a tenth of a page and read as "no more data".
const cubeAPMTraceOverFetch = 5

// The trace search API rejects a request missing any of index, env, service or
// spanKind with a 400 — none of which appear in CubeAPM's published example
// (`?query=*&env=UNSET&service=order&start=…&end=…&limit=10`). Verified against a
// live instance, where omitting each in turn produced "index is required",
// "env is required", "service is required", "spanKind is required".
const (
	// cubeAPMTraceIndex is the traces index to search. The server accepts any
	// value here; "traces" is the meaningful one.
	cubeAPMTraceIndex = "traces"

	// cubeAPMTraceSpanKind is sent because the parameter is mandatory, not because
	// it selects anything: on a live instance every value (server/client/internal/
	// producer/consumer/all/*) returned an identical 32 matches, so the server
	// requires it to be present and non-empty and then ignores it.
	//
	// "all" rather than a real span kind on purpose. If a later version starts
	// honouring the parameter, "all" most likely fails loudly with a 400, whereas
	// "server" would silently drop every client and internal span — data loss that
	// looks like a quiet trace backend.
	cubeAPMTraceSpanKind = "all"

	// cubeAPMDefaultEnv is the environment tag searched when the integration has
	// none configured. CubeAPM files telemetry with no explicit env under "UNSET",
	// and env accepts no wildcard — "*" is accepted but matches nothing — so a
	// concrete value has to be sent.
	cubeAPMDefaultEnv = "UNSET"
)

// cubeAPMMaxFanoutServices bounds how many services an unfiltered trace query
// fans out over. The search API requires an exact service name and supports no
// wildcard, so "show me recent traces" has to be answered by asking per service;
// the cap keeps that from becoming an unbounded request burst on a large install.
const cubeAPMMaxFanoutServices = 20

// cubeAPMHeatmapDefaultLookback is how far back a by-id trace fetch searches when
// the caller supplies no window. The trace-detail view asks by trace_id alone, and
// an hour-long default silently returns nothing for any older trace — the same
// trap the OpenObserve and New Relic heatmaps document.
const cubeAPMHeatmapDefaultLookback = 30 * 24 * time.Hour

// cubeAPMTraceLabelMapping maps canonical trace field names onto the span
// attribute a filter should compare against. Unmapped names are matched verbatim,
// so any raw OTel attribute the user types still works.
var cubeAPMTraceLabelMapping = map[string]string{
	"workload_name":             "service.name",
	"service_name":              "service.name",
	"workload_namespace":        "k8s.namespace.name",
	"destination_workload_name": "net.peer.name",
	"span_name":                 "operation_name",
	"http_status_code":          "http.status_code",
	"status_code":               "otel.status_code",
	"trace_id":                  "trace_id",
	"span_id":                   "span_id",
	"parent_id":                 "parent_span_id",
}

// cubeAPMTraceFieldCandidates lists accepted attribute names per rendered column,
// in priority order. A cluster runs SDKs from several OpenTelemetry
// semantic-convention generations at once — HTTP attributes were renamed between
// them (http.status_code → http.response.status_code, http.target → url.path) —
// so reading a single name leaves the column blank for every span using another.
var cubeAPMTraceFieldCandidates = struct {
	Resource       []string
	HTTPStatusCode []string
	Destination    []string
	Namespace      []string
}{
	Resource:       []string{"http.route", "http.target", "url.path", "url.full", "http.url", "db.statement"},
	HTTPStatusCode: []string{"http.response.status_code", "http.status_code", "rpc.grpc.status_code"},
	Destination:    []string{"peer.service", "server.address", "net.peer.name", "net.sock.peer.addr", "http.host"},
	Namespace:      []string{"k8s.namespace.name", "service.namespace"},
}

func (s *CubeAPMTraceSource) GetLabelMapping() map[string]string {
	return cubeAPMTraceLabelMapping
}

func (s *CubeAPMTraceSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_contains"}
}

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

// cubeAPMSearchMatch is one entry of the search response: a key span plus the
// trace it belongs to.
type cubeAPMSearchMatch struct {
	KeySpanID string `json:"keySpanId"`
	Trace     struct {
		Spans []cubeAPMSpan `json:"spans"`
	} `json:"trace"`
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

func cubeAPMTraceLimit(req TracesV3Request) int {
	limit := req.QueryRequest.Limit
	if limit <= 0 {
		limit = cubeAPMDefaultTraceLimit
	}
	if limit > cubeAPMMaxTraceLimit {
		limit = cubeAPMMaxTraceLimit
	}
	return limit
}

// cubeAPMSearchParams builds the query string for one search call.
//
// index, env, service and spanKind are all mandatory (see the constants above);
// service is the caller's because an unfiltered query has to fan out over
// discovered services. `query` is sent as the documented wildcard: every real
// filter is applied to the decoded spans (see filterCubeAPMSpans) rather than
// guessed at in an undocumented query syntax — a filter the server silently
// ignored would render unfiltered results as though they were filtered, which is
// worse than fetching a little more than needed.
func cubeAPMSearchParams(req TracesV3Request, env, service string, fetchLimit int) string {
	params := neturl.Values{}
	params.Set("query", "*")
	params.Set("index", cubeAPMTraceIndex)
	params.Set("spanKind", cubeAPMTraceSpanKind)
	params.Set("limit", strconv.Itoa(fetchLimit))

	startMs, endMs := cubeAPMTimeRangeMillis(req.StartTime, req.EndTime, time.Now())
	params.Set("start", strconv.FormatInt(startMs/1000, 10))
	params.Set("end", strconv.FormatInt(endMs/1000, 10))

	if env == "" {
		env = cubeAPMDefaultEnv
	}
	params.Set("env", env)
	params.Set("service", service)

	return "?" + params.Encode()
}

// cubeAPMRequestedService reads the service a query is scoped to, if any. This is
// the one filter the API can apply itself.
func cubeAPMRequestedService(req TracesV3Request) string {
	return extractFirstValueFromBinaryFilter(req.QueryRequest.Where.Binary,
		"workload_name", "service_name", "service.name")
}

// discoverCubeAPMServices lists the services that have trace data in the window.
//
// The traces API exposes no services endpoint (/services, /indexes and /streams
// all answer "unsupported path requested"), so the service list is read from the
// `service` label on CubeAPM's own APM metrics — the same label its alert-rule
// examples use, and verified on a live instance to return exactly the services
// that have spans.
func discoverCubeAPMServices(cfg integrations.CubeAPMConfig, startMs, endMs int64) ([]string, error) {
	endpoint := cfg.URL + cubeAPMMetricsAPIPath + "/label/service/values" +
		cubeAPMMetadataQuery(startMs, endMs, nil, time.Now())

	services, err := cubeAPMStringList(cfg, endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to discover CubeAPM services: %w", err)
	}

	sort.Strings(services)
	if len(services) > cubeAPMMaxFanoutServices {
		services = services[:cubeAPMMaxFanoutServices]
	}
	return services, nil
}

func (s *CubeAPMTraceSource) GetQuery(ctx *security.RequestContext, req TracesV3Request) (string, error) {
	cfg, err := integrations.GetCubeAPMConfigs(ctx, req.AccountId)
	if err != nil {
		return "", fmt.Errorf("failed to get CubeAPM configs: %w", err)
	}
	// Reports the shape of the call; an unfiltered query actually issues one of
	// these per discovered service.
	service := cubeAPMRequestedService(req)
	return cfg.URL + cubeAPMTraceSearchPath +
		cubeAPMSearchParams(req, cfg.Env, service, cubeAPMTraceLimit(req)), nil
}

func (s *CubeAPMTraceSource) QueryTraces(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	spans, err := s.fetchSpans(ctx, req)
	if err != nil {
		return nil, err
	}
	return spans, nil
}

// fetchSpans runs the search, decodes every span of every matched trace, applies
// the filters the API could not, and truncates to the requested page size.
func (s *CubeAPMTraceSource) fetchSpans(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	cfg, err := integrations.GetCubeAPMConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get CubeAPM configs: %w", err)
	}

	limit := cubeAPMTraceLimit(req)
	fetchLimit := limit
	if cubeAPMWhereHasFilters(req.QueryRequest.Where) {
		fetchLimit = min(limit*cubeAPMTraceOverFetch, cubeAPMMaxTraceLimit)
	}

	// The API requires an exact service and supports no wildcard, so a query that
	// names one asks only about it, and a query that does not has to ask about
	// each service that has traces in the window.
	services := []string{cubeAPMRequestedService(req)}
	if services[0] == "" {
		startMs, endMs := cubeAPMTimeRangeMillis(req.StartTime, req.EndTime, time.Now())
		services, err = discoverCubeAPMServices(cfg, startMs, endMs)
		if err != nil {
			return nil, err
		}
		if len(services) == 0 {
			return nil, nil
		}
	}

	var spans []common.OpenTelemetryTrace
	var firstErr error
	failures := 0

	for _, service := range services {
		endpoint := cfg.URL + cubeAPMTraceSearchPath +
			cubeAPMSearchParams(req, cfg.Env, service, fetchLimit)
		ctx.GetLogger().Info("CubeAPM Trace Search", "endpoint", endpoint)

		body, err := cubeAPMGet(cfg, endpoint, cubeAPMTraceQueryTimeout)
		if err != nil {
			// One service failing says nothing about the others, so the fan-out
			// keeps going and only reports if every leg failed.
			failures++
			if firstErr == nil {
				firstErr = err
			}
			ctx.GetLogger().Warn("CubeAPM trace search failed for service", "service", service, "error", err)
			continue
		}

		var matches []cubeAPMSearchMatch
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		if err := dec.Decode(&matches); err != nil {
			failures++
			if firstErr == nil {
				firstErr = fmt.Errorf("failed to decode CubeAPM trace search response: %w", err)
			}
			continue
		}

		for _, match := range matches {
			for _, span := range match.Trace.Spans {
				spans = append(spans, cubeAPMSpanToTrace(span))
			}
		}
	}

	// Every leg failing means the backend is unreachable, not that there are no
	// traces — surfacing it beats returning a misleading empty result.
	if failures == len(services) && firstErr != nil {
		return nil, firstErr
	}

	spans, err = filterCubeAPMSpans(spans, req.QueryRequest.Where)
	if err != nil {
		return nil, err
	}
	sortCubeAPMSpans(spans, req.QueryRequest.OrderBy)

	if len(spans) > limit {
		spans = spans[:limit]
	}
	return spans, nil
}

// cubeAPMWhereHasFilters reports whether a where-clause carries any predicate at
// all, which is what decides whether the fetch needs to over-fetch.
func cubeAPMWhereHasFilters(where query.QueryWhereClause) bool {
	if len(where.Binary) > 0 || len(where.And) > 0 || len(where.Or) > 0 || where.Not != nil {
		return true
	}
	return false
}

// cubeAPMSpanFieldValue resolves a filter field against a decoded span, checking
// the promoted columns before falling back to the raw attribute maps.
func cubeAPMSpanFieldValue(span common.OpenTelemetryTrace, field string) string {
	if mapped, ok := cubeAPMTraceLabelMapping[field]; ok {
		field = mapped
	}
	switch field {
	case "operation_name", "span_name":
		return span.SpanName
	case "service.name":
		return span.ServiceName
	case "k8s.namespace.name":
		return span.WorkloadNamespace
	case "trace_id":
		return span.TraceID
	case "span_id":
		return span.SpanID
	case "parent_span_id":
		return span.ParentSpanID
	case "otel.status_code":
		return span.StatusCode
	case "http.status_code":
		return span.HTTPStatusCode
	case "span.kind":
		return span.SpanKind
	}
	if v := span.SpanAttributes[field]; v != "" {
		return v
	}
	return span.ResourceAttributes[field]
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

// evalCubeAPMBinary evaluates one binary clause against a span. Conditions within
// a clause are ANDed, matching how every other provider here reads the shape.
func evalCubeAPMBinary(span common.OpenTelemetryTrace, binary query.BinaryWhereClause) (bool, error) {
	for field, ops := range binary {
		actual := cubeAPMSpanFieldValue(span, field)
		for op, val := range ops {
			values := cubeAPMFilterValues(val)

			matchesAny := func() bool {
				for _, want := range values {
					if strings.EqualFold(actual, want) {
						return true
					}
				}
				return false
			}

			var ok bool
			switch op {
			case query.Eq:
				ok = matchesAny()
			case query.Nq:
				ok = !matchesAny()
			case query.In:
				ok = matchesAny()
			case query.NotIn:
				ok = !matchesAny()
			case query.Contains, query.ILike:
				ok = false
				for _, want := range values {
					if strings.Contains(strings.ToLower(actual), strings.ToLower(want)) {
						ok = true
						break
					}
				}
			default:
				// Refusing beats guessing. An operator evaluated as "matches" would
				// return unfiltered spans as though the filter had been applied —
				// the same failure this source avoids by not inventing a server-side
				// query syntax.
				return false, fmt.Errorf("unsupported operator %q for CubeAPM traces (field %q)", op, field)
			}
			if !ok {
				return false, nil
			}
		}
	}
	return true, nil
}

// evalCubeAPMWhere evaluates a full where-clause tree against one span.
//
// This is recursive rather than a flat predicate list on purpose. CubeAPM's search
// API documents no filter syntax beyond `service` and `env`, so every other
// predicate has to be decided here — and a flat list silently drops OR and NOT
// subtrees, which means an OR-shaped filter returns every span as though it had
// matched. That is the precise failure mode this source refuses to accept from the
// server, so it must not introduce it on the client.
func evalCubeAPMWhere(span common.OpenTelemetryTrace, where query.QueryWhereClause) (bool, error) {
	if len(where.Binary) > 0 {
		ok, err := evalCubeAPMBinary(span, where.Binary)
		if err != nil || !ok {
			return false, err
		}
	}

	for _, sub := range where.And {
		ok, err := evalCubeAPMWhere(span, sub)
		if err != nil || !ok {
			return false, err
		}
	}

	if len(where.Or) > 0 {
		matched := false
		for _, sub := range where.Or {
			ok, err := evalCubeAPMWhere(span, sub)
			if err != nil {
				return false, err
			}
			if ok {
				matched = true
				break
			}
		}
		if !matched {
			return false, nil
		}
	}

	if where.Not != nil {
		ok, err := evalCubeAPMWhere(span, *where.Not)
		if err != nil {
			return false, err
		}
		if ok {
			return false, nil
		}
	}

	return true, nil
}

// filterCubeAPMSpans keeps the spans matching the where-clause. An equality on the
// service is also re-evaluated here even though it was pushed down as the API's
// `service` parameter: it is already true of every span fetched, so re-checking
// costs nothing and keeps the evaluator free of push-down special cases that would
// be wrong inside an OR branch.
func filterCubeAPMSpans(spans []common.OpenTelemetryTrace, where query.QueryWhereClause) ([]common.OpenTelemetryTrace, error) {
	if !cubeAPMWhereHasFilters(where) {
		return spans, nil
	}

	kept := spans[:0]
	for _, span := range spans {
		ok, err := evalCubeAPMWhere(span, where)
		if err != nil {
			return nil, err
		}
		if ok {
			kept = append(kept, span)
		}
	}
	return kept, nil
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

// CountTraces returns -1: CubeAPM exposes no trace-count endpoint, and the
// frontend already treats -1 as an estimate for pagination.
func (s *CubeAPMTraceSource) CountTraces(_ *security.RequestContext, _ TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	return common.OpenTelemetryTraceCount{Count: -1}, nil
}

func (s *CubeAPMTraceSource) CountTracesByTrace(_ *security.RequestContext, _ TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	return countTracesByTraceEstimate()
}

// QueryRootSpansByTrace backs the "By Traces" listing, reducing the span result to
// one representative root per trace via the shared helper.
func (s *CubeAPMTraceSource) QueryRootSpansByTrace(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	return queryRootSpansViaSpans(ctx, s, req)
}

// GetLabelValues answers the filter dropdowns from a sample of real spans.
// CubeAPM has no tag-value endpoint, so the values offered are the ones actually
// present in the window — which is also the only set that can return results.
func (s *CubeAPMTraceSource) GetLabelValues(ctx *security.RequestContext, req TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	if !ctx.GetSecurityContext().CanReadAccountData(req.AccountId, "traces") {
		return common.OpenTelemetryTraceLabelValues{Label: req.Label, Values: []string{}},
			fmt.Errorf("access denied for account: %s", req.AccountId)
	}

	spans, err := s.fetchSpans(ctx, TracesV3Request{
		AccountId:    req.AccountId,
		StartTime:    req.StartTime,
		EndTime:      req.EndTime,
		QueryRequest: TracesQueryBuilderRequest{Limit: cubeAPMMaxTraceLimit},
	})
	if err != nil {
		return common.OpenTelemetryTraceLabelValues{Label: req.Label, Values: []string{}}, err
	}

	field := req.Label
	if mapped, ok := cubeAPMTraceLabelMapping[field]; ok {
		field = mapped
	}

	seen := map[string]struct{}{}
	values := []string{}
	for _, span := range spans {
		v := cubeAPMSpanFieldValue(span, field)
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		values = append(values, v)
	}
	sort.Strings(values)

	return common.OpenTelemetryTraceLabelValues{Label: req.Label, Values: values}, nil
}

// QueryLabels reports the attribute keys actually present on recent spans. Unlike
// most sources here this is answerable for CubeAPM — the span payload carries its
// full tag set — so the label picker lists this deployment's real attributes
// instead of falling back to the derived canonical set.
func (s *CubeAPMTraceSource) QueryLabels(ctx *security.RequestContext, req FetchTraceLabelRequest) ([]OutputTraceLabel, error) {
	spans, err := s.fetchSpans(ctx, TracesV3Request{
		AccountId:    req.AccountId,
		StartTime:    req.StartTime,
		EndTime:      req.EndTime,
		QueryRequest: TracesQueryBuilderRequest{Limit: cubeAPMDefaultTraceLimit},
	})
	if err != nil {
		// An empty list is the documented "no backend discovery" answer, and
		// FetchTraceLabels falls back to the canonical set — a better outcome than
		// failing the whole label request because the sample query timed out.
		ctx.GetLogger().Warn("CubeAPMTraceSource.QueryLabels: sample query failed", "error", err)
		return []OutputTraceLabel{}, nil
	}

	keys := map[string]struct{}{}
	for _, span := range spans {
		for k := range span.SpanAttributes {
			keys[k] = struct{}{}
		}
		for k := range span.ResourceAttributes {
			keys[k] = struct{}{}
		}
	}

	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)

	labels := make([]OutputTraceLabel, 0, len(names))
	for _, name := range names {
		labels = append(labels, OutputTraceLabel{Label: name, Attributes: map[string]any{"type": "string"}})
	}
	return labels, nil
}

// QueryGroupedTraces aggregates the span page into (service, operation) groups.
// CubeAPM has no server-side aggregation for traces, so this groups what the
// search returned — the same approach the Splunk source takes.
func (s *CubeAPMTraceSource) QueryGroupedTraces(ctx *security.RequestContext, req TracesV3Request) ([]TraceGroupingValues, error) {
	spans, err := s.fetchSpans(ctx, req)
	if err != nil {
		return nil, err
	}
	return aggregateCubeAPMTraceGroups(spans), nil
}

func (s *CubeAPMTraceSource) QueryGroupedTracesCount(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	groups, err := s.QueryGroupedTraces(ctx, req)
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{}, err
	}
	return common.OpenTelemetryTraceGroupCount{Count: len(groups)}, nil
}

// aggregateCubeAPMTraceGroups rolls spans up by (service, operation), reporting
// call count, error count and latency percentiles.
func aggregateCubeAPMTraceGroups(spans []common.OpenTelemetryTrace) []TraceGroupingValues {
	type groupKey struct{ service, operation, namespace string }

	type groupAgg struct {
		count      int
		errCount   int
		totalDurNs int64
		maxDurNs   int64
		durations  []int64
	}

	groups := map[groupKey]*groupAgg{}
	order := []groupKey{}

	for _, span := range spans {
		key := groupKey{
			service:   span.ServiceName,
			operation: span.SpanName,
			namespace: span.WorkloadNamespace,
		}
		g, exists := groups[key]
		if !exists {
			g = &groupAgg{}
			groups[key] = g
			order = append(order, key)
		}
		g.count++
		if isCubeAPMErrorSpan(span) {
			g.errCount++
		}
		g.totalDurNs += span.DurationNs
		if span.DurationNs > g.maxDurNs {
			g.maxDurNs = span.DurationNs
		}
		g.durations = append(g.durations, span.DurationNs)
	}

	result := make([]TraceGroupingValues, 0, len(order))
	for _, key := range order {
		g := groups[key]
		sort.Slice(g.durations, func(i, j int) bool { return g.durations[i] < g.durations[j] })
		result = append(result, TraceGroupingValues{
			Count:             g.count,
			ErrorCount:        g.errCount,
			P95Latency:        cubeAPMPercentile(g.durations, 0.95),
			P99Latency:        cubeAPMPercentile(g.durations, 0.99),
			MaxLatency:        g.maxDurNs,
			WorkloadName:      key.service,
			WorkloadNamespace: key.namespace,
			SpanName:          key.operation,
			DurationNS:        g.totalDurNs,
		})
	}
	return result
}

func isCubeAPMErrorSpan(span common.OpenTelemetryTrace) bool {
	if strings.EqualFold(span.StatusCode, "ERROR") || span.StatusCode == "2" {
		return true
	}
	if code, err := strconv.Atoi(span.HTTPStatusCode); err == nil && code >= 500 {
		return true
	}
	return false
}

// cubeAPMPercentile returns the nearest-rank percentile of a sorted slice.
func cubeAPMPercentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
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
