package observability

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/integrations"
	"nudgebee/services/internal/database"
	"nudgebee/services/query"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"sort"
	"strconv"
	"strings"
	"time"
)

func init() {
	playbooks.RegisterAction("traces", &observabilityTracesAction{})
	playbooks.RegisterAction("chronosphere_traces_enricher", &chronosphereTracesAction{})
	playbooks.RegisterAction("datadog_traces", &datadogTracesAction{})
}

type TracesActionResponse struct {
	Metadata       map[string]any                            `json:"metadata"`
	Data           string                                    `json:"data"`
	AdditionalInfo map[string]any                            `json:"additional_info"`
	Insight        []playbooks.PlaybookActionResponseInsight `json:"insight"`
	labels         map[string]any
}

func (m TracesActionResponse) ExtractLabels() map[string]any {
	return m.labels
}

func (m TracesActionResponse) GetFormatName() string {
	return "api_traces_enricher_v2"
}

func (m TracesActionResponse) GetData() any {
	return m.Data
}

func (m TracesActionResponse) GetAdditionalInfo() map[string]any {
	return m.AdditionalInfo
}

func (m TracesActionResponse) GetInsights() []playbooks.PlaybookActionResponseInsight {
	return m.Insight
}

func NewTracesActionResponse(data *database.AgentWarehouseRows, metadata map[string]any, additionalInfo map[string]any) TracesActionResponse {
	if data == nil {
		return TracesActionResponse{
			Metadata:       metadata,
			Data:           "{}",
			AdditionalInfo: additionalInfo,
			Insight:        []playbooks.PlaybookActionResponseInsight{},
		}
	}

	rows := make([]map[string]any, 0, 20)
	cols := data.Columns()
	for data.HasNextResultSet() {
		rowMap := make(map[string]any, len(cols))
		rowArr := make([]driver.Value, len(cols))
		for i := range cols {
			rowArr[i] = new(any)
		}
		err := data.Next(rowArr)
		if err != nil {
			continue
		}
		for i, col := range cols {
			rowMap[col] = rowArr[i]
		}
		rows = append(rows, rowMap)
	}

	insight := []playbooks.PlaybookActionResponseInsight{}
	if len(rows) > 0 {
		// Count unique trace IDs instead of spans
		traceIDs := make(map[string]bool)
		for _, row := range rows {
			if traceID, ok := row["trace_id"].(string); ok && traceID != "" {
				traceIDs[traceID] = true
			}
		}

		uniqueTraces := len(traceIDs)
		totalSpans := len(rows)

		if uniqueTraces > 0 {
			insight = append(insight, playbooks.PlaybookActionResponseInsight{
				Message:  fmt.Sprintf("%d unique traces with %d spans", uniqueTraces, totalSpans),
				Severity: "info",
			})
		} else {
			insight = append(insight, playbooks.PlaybookActionResponseInsight{
				Message:  fmt.Sprintf("%d spans (trace IDs not available)", totalSpans),
				Severity: "info",
			})
		}
	}

	labels := map[string]any{}
	if len(rows) > 0 {
		labels = rows[0]

		// Add exception details to labels if available from any trace
		for _, row := range rows {
			if eventsStr, ok := row["events_attributes"].(string); ok && eventsStr != "" && eventsStr != "{}" {
				var eventsData map[string]any
				if err := json.Unmarshal([]byte(eventsStr), &eventsData); err == nil {
					// Add exception message if available
					if exceptionMessage, hasMessage := eventsData["event.exception.message"].(string); hasMessage && exceptionMessage != "" {
						labels["exception_message"] = exceptionMessage
					}

					// Add exception stacktrace if available
					if exceptionStacktrace, hasStacktrace := eventsData["event.exception.stacktrace"].(string); hasStacktrace && exceptionStacktrace != "" {
						labels["exception_stacktrace"] = exceptionStacktrace
					}

					// Break after finding the first exception
					if _, hasMessage := labels["exception_message"]; hasMessage {
						break
					}
				}
			}
		}
	}

	rowData := map[string]any{
		"name": "api_traces_enricher_v2",
		"data": rows,
	}
	tracesInsights := traceHandleTracesInsight(rows)
	if len(tracesInsights) > 0 {
		insight = append(insight, tracesInsights...)
	}

	jsonData, err := json.Marshal(rowData)
	if err != nil {
		jsonData = []byte("{}")
	}

	return TracesActionResponse{
		Metadata:       metadata,
		Data:           string(jsonData),
		Insight:        insight,
		AdditionalInfo: additionalInfo,
		labels:         labels,
	}
}

// OTel attribute names we reference. Only attributes on the hot path — other
// keys are looked up at call-site. Keeping these as constants documents which
// fields the error-signature extractor will inspect.
const (
	TraceDBStatement        = "db.statement"
	TraceDBSystem           = "db.system"
	TraceDBOperation        = "db.operation"
	TraceDBName             = "db.name"
	TraceHTTPStatusCode     = "http.status_code"
	TraceHTTPMethod         = "http.method"
	TraceHTTPTarget         = "http.target"
	TraceHTTPURL            = "http.url"
	TraceHTTPRoute          = "http.route"
	TraceRPCSystem          = "rpc.system"
	TraceRPCService         = "rpc.service"
	TraceRPCMethod          = "rpc.method"
	TraceRPCGRPCStatusCode  = "rpc.grpc.status_code"
	TraceMessagingSystem    = "messaging.system"
	TraceMessagingDestName  = "messaging.destination.name"
	TraceMessagingOperation = "messaging.operation"
	TraceExceptionType      = "exception.type"
	TraceExceptionMessage   = "exception.message"
	TraceServerAddress      = "server.address"
	TraceServerPort         = "server.port"
	TracePeerName           = "net.peer.name"
	TraceStatusCode         = "status_code"
	TraceStatusCodeLegacy   = "StatusCode"
	TraceStatusMessage      = "status_message"
)

type traceServiceInfo struct {
	Name       string
	SpanName   string
	HTTPMethod string
}

// errorSignature is the fingerprint that identifies "the same error happening
// repeatedly". It is protocol-agnostic: only the fields present on the span
// get populated. Two spans with the same signature represent the same failure
// mode — aggregating on this key lets the LLM see "N× same error" rather than
// N separate single-occurrence messages.
type errorSignature struct {
	Service          string // caller service (resource.attributes['service.name'] || workload_name)
	Destination      string // callee/peer if the span is a client call
	Operation        string // rpc.method, or http.method+http.route, else span_name
	Protocol         string // "grpc" | "http" | "db" | "messaging" | "" (generic/span-only)
	Status           string // raw status code: "9", "503", "STATUS_CODE_ERROR"
	StatusName       string // decoded: "FAILED_PRECONDITION", "Service Unavailable", ""
	ExceptionType    string // if span carries an exception event
	ExceptionMessage string // trimmed to avoid cross-signature drift from long message bodies
}

// aggregatedError pairs a signature with the count of spans matching it and
// one representative trace_id so the LLM can drill into a concrete example.
type aggregatedError struct {
	Signature      errorSignature
	Count          int
	ExampleTraceID string
}

const (
	// maxErrorBuckets caps the insight output so a span storm doesn't flood the
	// evidence. Buckets are sorted by count desc; smaller buckets are truncated.
	maxErrorBuckets = 10
	// maxExceptionMessageLen trims long exception messages for stable grouping.
	// Two exceptions with the same type but different end-of-message diagnostics
	// should still aggregate; a 120-char prefix keeps the interesting bit.
	maxExceptionMessageLen = 120
)

// grpcStatusNames maps canonical gRPC status codes (integer-as-string, per the
// rpc.grpc.status_code OTel attribute) to their canonical names. Codes are
// stable per the gRPC spec, so a lookup table is safe.
var grpcStatusNames = map[string]string{
	"0": "OK", "1": "CANCELLED", "2": "UNKNOWN", "3": "INVALID_ARGUMENT",
	"4": "DEADLINE_EXCEEDED", "5": "NOT_FOUND", "6": "ALREADY_EXISTS",
	"7": "PERMISSION_DENIED", "8": "RESOURCE_EXHAUSTED", "9": "FAILED_PRECONDITION",
	"10": "ABORTED", "11": "OUT_OF_RANGE", "12": "UNIMPLEMENTED",
	"13": "INTERNAL", "14": "UNAVAILABLE", "15": "DATA_LOSS",
	"16": "UNAUTHENTICATED",
}

// httpStatusNames covers the common 4xx/5xx codes for readability. Not
// exhaustive — unknown codes are rendered bare, which is fine because the
// integer itself is meaningful to both humans and LLMs.
var httpStatusNames = map[string]string{
	"400": "Bad Request", "401": "Unauthorized", "403": "Forbidden",
	"404": "Not Found", "408": "Request Timeout", "409": "Conflict",
	"422": "Unprocessable Entity", "429": "Too Many Requests",
	"500": "Internal Server Error", "501": "Not Implemented",
	"502": "Bad Gateway", "503": "Service Unavailable", "504": "Gateway Timeout",
}

// traceGetStatusCode returns the span status in a single, canonical form.
// Handles both the legacy CamelCase ("StatusCode") and the current snake_case
// ("status_code") evidence shapes.
func traceGetStatusCode(trace map[string]any) string {
	if v := traceGetString(trace, TraceStatusCode); v != "" {
		return v
	}
	return traceGetString(trace, TraceStatusCodeLegacy)
}

// traceGetExceptionDetails looks for exception info in both places OTel SDKs
// put it: on the span itself (exception.type/exception.message as span
// attributes — used by some SDK versions) AND as span events (events_attributes
// array of maps, or a stringified JSON for back-compat with an older shape).
func traceGetExceptionDetails(trace map[string]any, spanAttrs map[string]any) (excType, excMsg string) {
	// Shape 1: flattened onto span attributes (SDK-dependent).
	if t := traceGetString(spanAttrs, TraceExceptionType); t != "" {
		return t, traceGetString(spanAttrs, TraceExceptionMessage)
	}

	raw, ok := trace["events_attributes"]
	if !ok || raw == nil {
		return "", ""
	}

	// Shape 2: already-decoded array of event attribute maps (modern OTel).
	if arr, ok := raw.([]any); ok {
		for _, el := range arr {
			m, _ := el.(map[string]any)
			if m == nil {
				continue
			}
			if t := traceGetString(m, TraceExceptionType); t != "" {
				return t, traceGetString(m, TraceExceptionMessage)
			}
			// Some collectors namespace keys as "event.exception.type".
			if t := traceGetString(m, "event.exception.type"); t != "" {
				return t, traceGetString(m, "event.exception.message")
			}
		}
	}

	// Shape 3: stringified JSON of a single event map (legacy).
	if s, ok := raw.(string); ok && s != "" && s != "{}" {
		var obj map[string]any
		if err := json.Unmarshal([]byte(s), &obj); err == nil {
			if t := traceGetString(obj, "event.exception.type"); t != "" {
				return t, traceGetString(obj, "event.exception.message")
			}
			if t := traceGetString(obj, TraceExceptionType); t != "" {
				return t, traceGetString(obj, TraceExceptionMessage)
			}
		}
	}
	return "", ""
}

// traceHasError returns true when a span carries any recognisable error signal.
// Wider than the previous `traceIsError` — covers HTTP 4xx (not just 5xx),
// non-zero gRPC status, explicit Error status, and spans with exception events.
func traceHasError(trace map[string]any, spanAttrs map[string]any) bool {
	status := traceGetStatusCode(trace)
	if status == "STATUS_CODE_ERROR" || status == "Error" {
		return true
	}
	if grpc := traceGetString(spanAttrs, TraceRPCGRPCStatusCode); grpc != "" && grpc != "0" && grpc != "OK" {
		return true
	}
	if http := traceGetString(spanAttrs, TraceHTTPStatusCode); http != "" {
		if c := http[0]; c == '4' || c == '5' {
			return true
		}
	}
	if excType, _ := traceGetExceptionDetails(trace, spanAttrs); excType != "" {
		return true
	}
	return false
}

// extractErrorSignature inspects one span and returns its error signature, or
// nil when the span has no error signal at all. It is populated field-by-field
// from whichever attribute family is present — no per-protocol dispatch, no
// "add a case when you add a protocol".
func extractErrorSignature(trace map[string]any) *errorSignature {
	spanAttrs := traceGetSpanAttributes(trace)
	if !traceHasError(trace, spanAttrs) {
		return nil
	}

	sig := errorSignature{
		Service: traceGetServiceName(trace),
		Status:  traceGetStatusCode(trace),
	}

	// Exception data is orthogonal — a span may be, say, a gRPC error with
	// exception details attached. Capture both.
	if excType, excMsg := traceGetExceptionDetails(trace, spanAttrs); excType != "" {
		sig.ExceptionType = excType
		sig.ExceptionMessage = truncate(excMsg, maxExceptionMessageLen)
	}

	// Identify protocol. Checks run in order of specificity; the first match
	// populates Protocol/Status/StatusName/Operation/Destination and returns.
	switch {
	case traceGetString(spanAttrs, TraceRPCGRPCStatusCode) != "" || traceGetString(spanAttrs, TraceRPCSystem) == "grpc":
		grpc := traceGetString(spanAttrs, TraceRPCGRPCStatusCode)
		sig.Protocol = "grpc"
		if grpc != "" {
			sig.Status = grpc
			sig.StatusName = grpcStatusNames[grpc]
		}
		sig.Operation = traceGetString(spanAttrs, TraceRPCMethod)
		sig.Destination = traceGetString(spanAttrs, TraceRPCService)
		if sig.Destination == "" {
			sig.Destination = joinHostPort(
				traceGetString(spanAttrs, TraceServerAddress),
				traceGetString(spanAttrs, TraceServerPort),
			)
		}
	case traceGetString(spanAttrs, TraceHTTPStatusCode) != "":
		code := traceGetString(spanAttrs, TraceHTTPStatusCode)
		sig.Protocol = "http"
		sig.Status = code
		sig.StatusName = httpStatusNames[code]
		sig.Operation = firstNonEmpty(
			joinSpace(traceGetString(spanAttrs, TraceHTTPMethod), traceGetString(spanAttrs, TraceHTTPRoute)),
			traceGetString(spanAttrs, TraceHTTPURL),
			traceGetString(spanAttrs, TraceHTTPTarget),
		)
		sig.Destination = firstNonEmpty(
			traceGetString(spanAttrs, TraceServerAddress),
			traceGetString(spanAttrs, TracePeerName),
		)
	case traceGetString(spanAttrs, TraceDBStatement) != "" || traceGetString(spanAttrs, TraceDBSystem) != "":
		sig.Protocol = "db"
		sig.Operation = firstNonEmpty(
			traceGetString(spanAttrs, TraceDBOperation),
			traceGetString(spanAttrs, TraceDBStatement),
		)
		sig.Destination = firstNonEmpty(
			traceGetString(spanAttrs, TraceDBName),
			traceGetString(spanAttrs, TraceDBSystem),
			traceGetString(spanAttrs, TracePeerName),
		)
	case traceGetString(spanAttrs, TraceMessagingSystem) != "":
		sig.Protocol = "messaging"
		sig.Operation = traceGetString(spanAttrs, TraceMessagingOperation)
		sig.Destination = firstNonEmpty(
			traceGetString(spanAttrs, TraceMessagingDestName),
			traceGetString(spanAttrs, TraceMessagingSystem),
		)
	}

	// If the branches above left Operation unset, fall back to span_name —
	// often the most meaningful identifier we have for a generic error.
	if sig.Operation == "" {
		sig.Operation = traceGetString(trace, "span_name")
	}
	return &sig
}

// aggregateErrors buckets per-span signatures into (signature → count) entries,
// sorted by count desc, capped at maxErrorBuckets. Stable: identical inputs
// always produce identical output order (ties break by signature string).
func aggregateErrors(traces []map[string]any) []aggregatedError {
	type bucket struct {
		sig     errorSignature
		count   int
		traceID string
	}
	buckets := make(map[string]*bucket)
	for _, trace := range traces {
		sig := extractErrorSignature(trace)
		if sig == nil {
			continue
		}
		key := sig.aggregationKey()
		b, ok := buckets[key]
		if !ok {
			b = &bucket{sig: *sig, traceID: traceGetString(trace, "trace_id")}
			buckets[key] = b
		}
		b.count++
		if b.traceID == "" {
			b.traceID = traceGetString(trace, "trace_id")
		}
	}
	if len(buckets) == 0 {
		return nil
	}

	out := make([]aggregatedError, 0, len(buckets))
	for _, b := range buckets {
		out = append(out, aggregatedError{Signature: b.sig, Count: b.count, ExampleTraceID: b.traceID})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Signature.aggregationKey() < out[j].Signature.aggregationKey()
	})
	if len(out) > maxErrorBuckets {
		out = out[:maxErrorBuckets]
	}
	return out
}

// aggregationKey is the stable join of the fields that define "the same error".
// Excludes example trace_id and count.
func (s errorSignature) aggregationKey() string {
	return strings.Join([]string{
		s.Service, s.Destination, s.Operation, s.Protocol,
		s.Status, s.ExceptionType,
	}, "\x00")
}

// formatErrorInsight produces one Critical insight per aggregated bucket.
// Output format is a single line so the LLM can read it as a bullet and the UI
// can render it in a list; the format is protocol-generic so that adding a new
// protocol to extractErrorSignature does not require changes here.
//
// Shape: "Nx [protocol] service → destination (operation): status=code (name); exception: type: msg  [example: traceid]"
func formatErrorInsight(a aggregatedError) playbooks.PlaybookActionResponseInsight {
	s := a.Signature
	var b strings.Builder
	fmt.Fprintf(&b, "%d× ", a.Count)
	if s.Protocol != "" {
		fmt.Fprintf(&b, "[%s] ", s.Protocol)
	}
	service := s.Service
	if service == "" || service == "Unknown" {
		service = "unknown service"
	}
	b.WriteString(service)
	if s.Destination != "" {
		fmt.Fprintf(&b, " → %s", s.Destination)
	}
	if s.Operation != "" {
		fmt.Fprintf(&b, " (%s)", s.Operation)
	}
	if s.Status != "" {
		fmt.Fprintf(&b, ": status=%s", s.Status)
		if s.StatusName != "" {
			fmt.Fprintf(&b, " (%s)", s.StatusName)
		}
	}
	if s.ExceptionType != "" {
		fmt.Fprintf(&b, "; exception: %s", s.ExceptionType)
		if s.ExceptionMessage != "" {
			fmt.Fprintf(&b, ": %s", s.ExceptionMessage)
		}
	}
	if a.ExampleTraceID != "" {
		// 8-char prefix is enough to disambiguate and search; full ID is noisy.
		prefix := a.ExampleTraceID
		if len(prefix) > 8 {
			prefix = prefix[:8]
		}
		fmt.Fprintf(&b, "  [example trace: %s]", prefix)
	}
	return playbooks.PlaybookActionResponseInsight{Message: b.String(), Severity: "Critical"}
}

// --- small string helpers (private, only used here) ---

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func joinSpace(a, b string) string {
	switch {
	case a != "" && b != "":
		return a + " " + b
	case a != "":
		return a
	default:
		return b
	}
}

func joinHostPort(host, port string) string {
	if host == "" {
		return ""
	}
	if port == "" {
		return host
	}
	return host + ":" + port
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// traceExtractServiceFlow now uses the new getServiceName helper.
func traceExtractServiceFlow(traces []map[string]any) []traceServiceInfo {
	sort.Slice(traces, func(i, j int) bool {
		return traceGetString(traces[i], "timestamp") < traceGetString(traces[j], "timestamp")
	})

	seenServices := make(map[string]bool)
	var serviceFlow []traceServiceInfo

	for _, trace := range traces {
		serviceName := traceGetServiceName(trace) // CHANGED
		if serviceName == "Unknown" || seenServices[serviceName] {
			continue
		}

		spanAttrs := traceGetSpanAttributes(trace)

		serviceFlow = append(serviceFlow, traceServiceInfo{
			Name:       serviceName,
			SpanName:   traceGetString(trace, "span_name"),
			HTTPMethod: traceGetString(spanAttrs, TraceHTTPMethod),
		})
		seenServices[serviceName] = true
	}
	return serviceFlow
}

// --- Helper functions ---

// NEW FUNCTION: Centralizes the logic for finding the service name.
func traceGetServiceName(trace map[string]any) string {
	// Priority 1: Check for 'service.name' inside span attributes. This is the OTel standard.
	spanAttrs := traceGetSpanAttributes(trace)
	if serviceName := traceGetString(spanAttrs, "service.name"); serviceName != "" {
		return serviceName
	}

	// Priority 2: Fallback to the top-level 'workload_name'.
	if serviceName := traceGetString(trace, "workload_name"); serviceName != "" {
		return serviceName
	}

	// Priority 3: Fallback to the top-level 'ServiceName'.
	if serviceName := traceGetString(trace, "ServiceName"); serviceName != "" {
		return serviceName
	}

	return "Unknown"
}

func traceGetString(data map[string]any, key string) string {
	if val, ok := data[key]; ok {
		switch v := val.(type) {
		case string:
			return v
		case float64:
			return fmt.Sprintf("%.0f", v)
		case int, int32, int64, uint, uint32, uint64:
			return fmt.Sprintf("%d", v)
		}
	}
	return ""
}

// traceGetMap returns data[key] as map[string]any, handling the three shapes
// trace attribute maps arrive in across the codebase:
//  1. map[string]any — decoded from JSONB directly
//  2. map[string]string — the Go struct field type on common.OpenTelemetryTrace;
//     converted in convertOTelTracesToMapRows and stored as-is. Without this
//     branch every attribute lookup silently returns empty.
//  3. string — legacy stringified JSON (still produced by some code paths)
func traceGetMap(data map[string]any, key string) map[string]any {
	if val, ok := data[key].(map[string]any); ok {
		return val
	}
	if val, ok := data[key].(map[string]string); ok {
		out := make(map[string]any, len(val))
		for k, v := range val {
			out[k] = v
		}
		return out
	}
	if strVal, ok := data[key].(string); ok {
		var nestedMap map[string]any
		if err := json.Unmarshal([]byte(strVal), &nestedMap); err == nil {
			return nestedMap
		}
	}
	return make(map[string]any)
}

// traceHandleTracesInsight converts a batch of spans into RCA-oriented insights.
// Behaviour:
//  1. Group error spans by error signature (service / destination / operation /
//     protocol / status / exception_type) and emit one Critical insight per
//     bucket with its count and an example trace_id. Capped at maxErrorBuckets
//     so a span storm can't flood the evidence.
//  2. Always emit a single Info insight describing the service call path —
//     format "Trace Path: A (method) -> B (method) -> …" is preserved byte-for-
//     byte from the pre-refactor behaviour to stay compatible with any downstream
//     consumer that renders it verbatim.
func traceHandleTracesInsight(traces []map[string]any) []playbooks.PlaybookActionResponseInsight {
	var insights []playbooks.PlaybookActionResponseInsight

	for _, bucket := range aggregateErrors(traces) {
		insights = append(insights, formatErrorInsight(bucket))
	}

	serviceFlow := traceExtractServiceFlow(traces)
	if len(serviceFlow) > 0 {
		pathParts := make([]string, 0, len(serviceFlow))
		for _, service := range serviceFlow {
			method := service.HTTPMethod
			if method == "" || method == "N/A" {
				method = "internal"
			}
			pathParts = append(pathParts, fmt.Sprintf("%s (%s)", service.Name, method))
		}
		insights = append(insights, playbooks.PlaybookActionResponseInsight{
			Message:  "Trace Path: " + strings.Join(pathParts, " -> "),
			Severity: "Info",
		})
	}
	return insights
}

func traceGetSpanAttributes(trace map[string]any) map[string]any {
	for _, key := range []string{"spanattributes", "span_attributes", "SpanAttributes"} {
		if attrs := traceGetMap(trace, key); len(attrs) > 0 {
			return attrs
		}
	}
	return make(map[string]any)
}

type chronosphereTracesAction struct{}
type chronosphereTracesEnricherParams struct {
	QueryType  string   `json:"query_type,omitempty"`
	TraceIDs   []string `json:"trace_ids,omitempty"`
	TagFilters []any    `json:"tag_filters,omitempty"`
	EndTime    string   `json:"end_time,omitempty"`
	Service    string   `json:"service,omitempty"`
	StartTime  string   `json:"start_time,omitempty"`
}

func (a *chronosphereTracesAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params chronosphereTracesEnricherParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	endTime := time.Now()
	durationMinutes := 10
	startTime := endTime.Add(-time.Duration(durationMinutes) * time.Minute)
	if ctx.GetEvent().StartedAt != nil {
		startTime = *ctx.GetEvent().StartedAt
	}
	if ctx.GetEvent().EndedAt != nil {
		endTime = *ctx.GetEvent().EndedAt
	}

	// Auto-detect query type if not provided
	if params.QueryType == "" && params.Service != "" {
		params.QueryType = "SERVICE_OPERATION"
	}
	if params.QueryType == "" && len(params.TraceIDs) > 0 {
		params.QueryType = "TRACE_IDS"
	}

	if params.QueryType == "" {
		return nil, errors.New("query_type is required")
	}

	// Check if we need batch processing for durations > 15 minutes
	const maxChunkDuration = 15 * time.Minute
	totalDuration := endTime.Sub(startTime)

	if totalDuration <= maxChunkDuration {
		// Single query for durations <= 15 minutes
		result, err := a.executeQuery(ctx, params, startTime, endTime, rawParams, false)
		if err != nil {
			return nil, err
		}
		return result.(playbooks.PlaybookActionResponse), nil
	}

	// Batch processing for durations > 15 minutes
	numChunks := int((totalDuration + maxChunkDuration - 1) / maxChunkDuration)
	ctx.GetLogger().Info("chronosphereTracesEnricherAction: executing batch query",
		"total_duration_minutes", totalDuration.Minutes(),
		"num_chunks", numChunks)

	var allTraces []any
	successfulChunks := 0

	for i := range numChunks {
		chunkStart := startTime.Add(time.Duration(i) * maxChunkDuration)
		chunkEnd := chunkStart.Add(maxChunkDuration)
		if chunkEnd.After(endTime) {
			chunkEnd = endTime
		}

		result, err := a.executeQuery(ctx, params, chunkStart, chunkEnd, rawParams, true)
		if err != nil {
			ctx.GetLogger().Error("chronosphereTracesEnricherAction: chunk query failed",
				"chunk", i+1, "error", err)
			continue
		}

		// Extract traces from chunk response
		chunkData := result.(map[string]any)
		if chunkTraces, ok := chunkData["traces"].([]any); ok {
			allTraces = append(allTraces, chunkTraces...)
			successfulChunks++
		}
	}

	if successfulChunks == 0 {
		return nil, fmt.Errorf("all %d chunks failed", numChunks)
	}

	// Create merged data and warehouse rows
	mergedData := map[string]any{"traces": allTraces}
	warehouseRows, err := database.NewAgentWarehouseRows(mergedData, ctx.GetAccountId(), "agent_warehouse_chronosphere")
	if err != nil {
		return nil, fmt.Errorf("failed to create warehouse rows: %w", err)
	}

	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
		"batch_info": map[string]any{
			"total_chunks":           numChunks,
			"successful_chunks":      successfulChunks,
			"failed_chunks":          numChunks - successfulChunks,
			"total_duration_minutes": totalDuration.Minutes(),
		},
	}

	return NewTracesActionResponse(warehouseRows, metadata, map[string]any{}), nil
}

// executeQuery executes a single Chronosphere traces query
func (a *chronosphereTracesAction) executeQuery(ctx playbooks.PlaybookActionContext, params chronosphereTracesEnricherParams, startTime, endTime time.Time, rawParams map[string]any, returnRawData bool) (any, error) {
	actionParams := map[string]any{
		"end_time":   endTime.UTC().Format(time.RFC3339),
		"start_time": startTime.UTC().Format(time.RFC3339),
		"query_type": params.QueryType,
	}

	switch params.QueryType {
	case "SERVICE_OPERATION":
		actionParams["service"] = params.Service
		if len(params.TagFilters) > 0 {
			tagFiltersUpdated := make([]map[string]any, 0, len(params.TagFilters))
			for _, t := range params.TagFilters {
				if t == nil {
					continue
				}
				if tagFilter, ok := t.(map[string]any); ok {
					// Handle numeric value conversion
					if numericValue, ok := tagFilter["numeric_value"].(map[string]any); ok {
						if value, ok := numericValue["value"].(string); ok && value != "" {
							parsedValue, err := strconv.ParseFloat(value, 64)
							if err != nil {
								return nil, fmt.Errorf("failed to parse numeric value '%s': %w", value, err)
							}
							numericValue["value"] = parsedValue
						}
					}
					tagFiltersUpdated = append(tagFiltersUpdated, tagFilter)
				} else {
					return nil, fmt.Errorf("tag filter is not a valid map")
				}
			}
			actionParams["tag_filters"] = tagFiltersUpdated
		}
	case "TRACE_IDS":
		actionParams["trace_ids"] = params.TraceIDs
		delete(actionParams, "end_time")
		delete(actionParams, "start_time")
	}

	relayRequest := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:    ctx.GetAccountId(),
			ActionName:   "chronosphere_query_traces",
			ActionParams: actionParams,
			Origin:       "services-server",
		},
		NoSinks: true,
	}

	relayResponse, err := relay.Execute(relayRequest)
	if err != nil {
		return nil, fmt.Errorf("relay execution failed: %w", err)
	}

	// Extract data from relay response
	relayDataAny := relayResponse["data"]
	if relayDataAny == nil {
		return nil, errors.New("relay: unable to execute relay query - no data")
	}

	relayData, ok := relayDataAny.(map[string]any)
	if !ok {
		return nil, errors.New("relay: unable to execute relay query - invalid data type")
	}

	if success, ok := relayData["success"].(bool); !ok || !success {
		return nil, errors.New("relay: unable to execute relay query - query failed")
	}

	data, ok := relayData["data"]
	if !ok {
		return nil, errors.New("relay: unable to execute relay query - no result data")
	}

	dataMap, ok := data.(map[string]any)
	if !ok {
		return nil, errors.New("relay: result data is not a map")
	}

	if returnRawData {
		return dataMap, nil
	}

	// For single queries, return the full TracesActionResponse
	warehouseRows, err := database.NewAgentWarehouseRows(dataMap, ctx.GetAccountId(), "agent_warehouse_chronosphere")
	if err != nil {
		return nil, fmt.Errorf("failed to create warehouse rows: %w", err)
	}

	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
	}

	return NewTracesActionResponse(warehouseRows, metadata, map[string]any{}), nil
}

func (a *chronosphereTracesAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	if ctx.GetEvent().Labels == nil {
		return false
	}

	if ctx.GetEvent().Labels["trace_id"] != "" {
		return true
	}

	if ctx.GetEvent().Labels["http_status_code"] != "" && (ctx.GetEvent().Labels["service"] != "" || ctx.GetEvent().Labels["job"] != "") {
		return true
	}

	return false
}

func (a *chronosphereTracesAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	params := map[string]any{}
	if ctx.GetEvent().Labels["trace_id"] != "" {
		params = map[string]any{
			"trace_ids": []string{ctx.GetEvent().Labels["trace_id"]},
		}
	} else if ctx.GetEvent().Labels["http_status_code"] != "" && (ctx.GetEvent().Labels["service"] != "" || ctx.GetEvent().Labels["job"] != "") {
		statusCode := ctx.GetEvent().Labels["http_status_code"]
		service := ctx.GetEvent().Labels["service"]
		if service == "" {
			service = ctx.GetEvent().Labels["job"]
		}
		params = map[string]any{
			"service": service,
			"tag_filters": []map[string]any{
				{
					"key": "http.status_code",
					"numeric_value": map[string]any{
						"comparison": "EQUAL",
						"value":      statusCode,
					},
				},
			},
		}

	}

	return a.Execute(ctx, params)
}

// get_resource, kubectl_command_executor, pod_bash_enricher, nubi_enricher, pod_script_run_enricher

type observabilityTracesAction struct{}
type observabilityTracesActionParams struct {
	Query        string         `json:"query,omitempty"`
	AccountId    string         `json:"account_id,omitempty"`
	Duration     int            `json:"duration,omitempty"`
	QueryOptions map[string]any `json:"query_options,omitempty"`
}

func (a *observabilityTracesAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)
	source, err := getTraceSourceForAccount(requestCtx, ctx.GetAccountId(), "", "")
	if err != nil || source == nil {
		requestCtx.GetLogger().Info("observability: unable to identify trace source", "account", ctx.GetAccountId())
		return false
	}

	if autoExecutor, ok := source.(PlaybookQueryGenerator); ok {
		return autoExecutor.CanGenerateQuery(ctx)
	}

	// Fallback: auto-execute when event has workload name + namespace
	if ctx.GetEvent().SubjectName != "" && getEventNamespace(ctx.GetEvent()) != "" {
		return true
	}

	return false
}

func (a *observabilityTracesAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)
	source, err := getTraceSourceForAccount(requestCtx, ctx.GetAccountId(), "", "")
	if err != nil || source == nil {
		requestCtx.GetLogger().Info("observability: unable to identify source", "error", err, "account", ctx.GetAccountId())
		return nil, errors.New("observability: unable to identify source")
	}

	if autoExecutor, ok := source.(PlaybookQueryGenerator); ok {
		q, request, err := autoExecutor.GenerateQuery(ctx)
		if err != nil {
			return nil, err
		}
		params := map[string]any{
			"query":         q,
			"query_options": request,
		}
		return a.Execute(ctx, params)
	}

	// Fallback: query traces by workload name + namespace, mirroring the relay-side api_traces_enricher
	return a.autoExecuteByWorkload(ctx)
}

// autoExecuteByWorkload queries traces around an event, prioritising error-bearing
// traces so the LLM sees the actual RCA signal.
//
// The event's SubjectName/namespace identifies the workload of interest. Plain
// "latest 100 spans for this workload" is almost always dominated by healthy
// spans in busy systems, which drowns out the handful of error spans that
// actually explain the alert. Instead we do a three-phase fetch:
//
//  1. Find error-bearing spans via top-level columns — StatusCode in
//     {Error, STATUS_CODE_ERROR} or http_status_code 4xx/5xx. These are
//     first-class ClickHouse columns, so the filter is cheap.
//  1b. If Phase 1 returns no rows, scan a larger recent sample and post-filter
//     via traceHasError to recover errors that only live inside span_attributes
//     (rpc.grpc.status_code != 0, exception events, etc). The query builder
//     does not support `In` / `!= '0'` on map values, so we detect these in
//     memory rather than widening the SQL.
//  2. Expand any discovered error spans into full trace trees by trace_id so
//     the call graph shows upstream/downstream context around each failure.
//
// If both phases 1 and 1b find no errors we fall through to a small recent
// sample so the evidence payload still carries traffic context.
func (a *observabilityTracesAction) autoExecuteByWorkload(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	const (
		errorSpanLimit      = 50  // phase 1: top N recent error spans
		errorScanLimit      = 200 // phase 1b: recent sample size to post-filter for attr-only errors
		traceExpansionLimit = 10  // phase 2: fetch spans for up to N unique error traces
		traceSpanLimit      = 500 // phase 2: cap total spans returned for expansion
		fallbackSpanLimit   = 30  // phase 3: no errors — small recent sample
	)

	workloadName := ctx.GetEvent().SubjectName
	namespace := getEventNamespace(ctx.GetEvent())

	endTime := time.Now().UnixMilli()
	startTime := endTime - int64(15*60*1000) // 15 minutes default
	if ctx.GetEvent().StartedAt != nil {
		startTime = ctx.GetEvent().StartedAt.UnixMilli()
	}
	if ctx.GetEvent().EndedAt != nil {
		endTime = ctx.GetEvent().EndedAt.UnixMilli()
	}

	reqCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)

	// Phase 1: error spans involving the workload as caller OR callee via top-level columns.
	errorSpans, err := a.queryErrorSpansForWorkload(reqCtx, ctx.GetAccountId(), workloadName, namespace, startTime, endTime, errorSpanLimit)
	if err != nil {
		ctx.GetLogger().Warn("traces auto action: phase 1 error-span query failed, falling back", "error", err)
	}

	queryMode := "error_plus_expansion"

	// Phase 1b: if top-level filters found nothing, sample recent spans and post-filter
	// for errors that only show up in span_attributes (gRPC non-zero, exception events).
	if len(errorSpans) == 0 {
		sample, sampleErr := a.queryRecentSpansForWorkload(reqCtx, ctx.GetAccountId(), workloadName, namespace, startTime, endTime, errorScanLimit)
		if sampleErr != nil {
			ctx.GetLogger().Warn("traces auto action: phase 1b sample query failed", "error", sampleErr)
		} else if attrErrors := filterErrorSpans(sample); len(attrErrors) > 0 {
			errorSpans = attrErrors
			queryMode = "error_plus_expansion_attr"
			ctx.GetLogger().Info("traces auto action: recovered errors via attribute post-filter",
				"workload", workloadName, "namespace", namespace, "sample_size", len(sample), "attr_errors", len(attrErrors))
		}
	}

	var finalSpans []common.OpenTelemetryTrace

	if len(errorSpans) > 0 {
		// Phase 2: expand by trace_id to bring in the full call graph for each error trace.
		traceIDs := uniqueTraceIDs(errorSpans, traceExpansionLimit)
		expansion, expErr := a.queryTraceTrees(reqCtx, ctx.GetAccountId(), traceIDs, startTime, endTime, traceSpanLimit)
		if expErr != nil {
			ctx.GetLogger().Warn("traces auto action: phase 2 trace expansion failed, returning errors only", "error", expErr)
			finalSpans = errorSpans
		} else {
			finalSpans = mergeSpansDedup(errorSpans, expansion)
		}
		ctx.GetLogger().Info("traces auto action: error-centric result",
			"workload", workloadName, "namespace", namespace,
			"error_spans", len(errorSpans), "total_spans", len(finalSpans), "traces_expanded", len(traceIDs))
	} else {
		// Phase 3: fallback — no errors found for the workload window. Return a small
		// recent sample so the evidence still carries traffic context.
		queryMode = "fallback_recent_sample"
		fallback, fbErr := a.queryRecentSpansForWorkload(reqCtx, ctx.GetAccountId(), workloadName, namespace, startTime, endTime, fallbackSpanLimit)
		if fbErr != nil {
			return nil, fbErr
		}
		finalSpans = fallback
		ctx.GetLogger().Info("traces auto action: no error spans found, using recent-sample fallback",
			"workload", workloadName, "namespace", namespace, "spans", len(finalSpans))
	}

	if len(finalSpans) == 0 {
		return nil, nil
	}

	traceRows := convertOTelTracesToMapRows(finalSpans)
	insights := traceHandleTracesInsight(traceRows)

	metadata := map[string]any{
		"query-result-version": "1.0",
		"query": map[string]any{
			"workload_name":    workloadName,
			"namespace":        namespace,
			"mode":             queryMode,
			"error_span_count": len(errorSpans),
		},
	}
	return playbooks.NewPlaybookActionResponseJson(map[string]any{"data": finalSpans}, map[string]any{}, insights, metadata), nil
}

// queryErrorSpansForWorkload returns spans involving the workload (as source
// OR destination) that carry an error signal — StatusCode=Error/STATUS_CODE_ERROR
// or HTTP 4xx/5xx. In both the OTel demo and most instrumented systems, the
// error is visible on the caller span even when the callee's server span is
// STATUS_CODE_UNSET, so querying caller-OR-callee is essential.
func (a *observabilityTracesAction) queryErrorSpansForWorkload(ctx *security.RequestContext, accountId, workload, namespace string, startMs, endMs int64, limit int) ([]common.OpenTelemetryTrace, error) {
	workloadSide := query.QueryWhereClause{
		Or: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{
				"destination_workload_name":      {query.Eq: workload},
				"destination_workload_namespace": {query.Eq: namespace},
			}},
			{Binary: query.BinaryWhereClause{
				"workload_name":      {query.Eq: workload},
				"workload_namespace": {query.Eq: namespace},
			}},
		},
	}
	errorSide := query.QueryWhereClause{
		Or: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{"status_code": {query.Eq: "Error"}}},
			{Binary: query.BinaryWhereClause{"status_code": {query.Eq: "STATUS_CODE_ERROR"}}},
			{Binary: query.BinaryWhereClause{"http_status_code": {query.Like: "4%"}}},
			{Binary: query.BinaryWhereClause{"http_status_code": {query.Like: "5%"}}},
		},
	}

	return GetTraces(ctx, TracesV3Request{
		AccountId: accountId,
		StartTime: startMs,
		EndTime:   endMs,
		QueryRequest: TracesQueryBuilderRequest{
			Where: query.QueryWhereClause{
				And: []query.QueryWhereClause{workloadSide, errorSide},
			},
			Limit:   limit,
			OrderBy: []query.QueryOrderBy{{Column: "timestamp", Order: query.Desc}},
		},
	})
}

// queryTraceTrees returns all spans for the given trace_ids within the time
// window. Caller is responsible for bounding len(traceIDs); we additionally cap
// the total spans returned via `limit` to prevent runaway payloads on very
// chatty traces (e.g. a load-generator burst).
func (a *observabilityTracesAction) queryTraceTrees(ctx *security.RequestContext, accountId string, traceIDs []string, startMs, endMs int64, limit int) ([]common.OpenTelemetryTrace, error) {
	if len(traceIDs) == 0 {
		return nil, nil
	}
	ids := make([]any, 0, len(traceIDs))
	for _, id := range traceIDs {
		ids = append(ids, id)
	}
	return GetTraces(ctx, TracesV3Request{
		AccountId: accountId,
		StartTime: startMs,
		EndTime:   endMs,
		QueryRequest: TracesQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"trace_id": {query.In: ids},
				},
			},
			Limit: limit,
			OrderBy: []query.QueryOrderBy{
				{Column: "trace_id", Order: query.Asc},
				{Column: "timestamp", Order: query.Asc},
			},
		},
	})
}

// queryRecentSpansForWorkload is the no-errors fallback: small recent sample,
// mirrors the pre-Fix-4 behaviour but with a smaller limit.
func (a *observabilityTracesAction) queryRecentSpansForWorkload(ctx *security.RequestContext, accountId, workload, namespace string, startMs, endMs int64, limit int) ([]common.OpenTelemetryTrace, error) {
	return GetTraces(ctx, TracesV3Request{
		AccountId: accountId,
		StartTime: startMs,
		EndTime:   endMs,
		QueryRequest: TracesQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Or: []query.QueryWhereClause{
					{Binary: query.BinaryWhereClause{
						"destination_workload_name":      {query.Eq: workload},
						"destination_workload_namespace": {query.Eq: namespace},
					}},
					{Binary: query.BinaryWhereClause{
						"workload_name":      {query.Eq: workload},
						"workload_namespace": {query.Eq: namespace},
					}},
				},
			},
			Limit:   limit,
			OrderBy: []query.QueryOrderBy{{Column: "timestamp", Order: query.Desc}},
		},
	})
}

// filterErrorSpans returns the subset of spans that traceHasError flags as
// carrying an error signal. Used by Phase 1b to recover attribute-only errors
// (e.g. rpc.grpc.status_code != 0, exception events) that the top-level column
// filter in Phase 1 can't express — ClickHouse map columns are not queryable
// via the `In` / `!= '0'` operators in this query builder.
func filterErrorSpans(spans []common.OpenTelemetryTrace) []common.OpenTelemetryTrace {
	if len(spans) == 0 {
		return nil
	}
	rows := convertOTelTracesToMapRows(spans)
	errorSpanIDs := make(map[string]struct{})
	for _, row := range rows {
		if traceHasError(row, traceGetSpanAttributes(row)) {
			if id, ok := row["span_id"].(string); ok && id != "" {
				errorSpanIDs[id] = struct{}{}
			}
		}
	}
	if len(errorSpanIDs) == 0 {
		return nil
	}
	out := make([]common.OpenTelemetryTrace, 0, len(errorSpanIDs))
	for _, span := range spans {
		if _, ok := errorSpanIDs[span.SpanID]; ok {
			out = append(out, span)
		}
	}
	return out
}

// uniqueTraceIDs returns up to `limit` distinct trace IDs from spans, preserving order.
// An empty trace_id is skipped — it would only broaden the follow-up query pointlessly.
func uniqueTraceIDs(spans []common.OpenTelemetryTrace, limit int) []string {
	seen := make(map[string]struct{}, limit)
	out := make([]string, 0, limit)
	for _, s := range spans {
		if s.TraceID == "" {
			continue
		}
		if _, ok := seen[s.TraceID]; ok {
			continue
		}
		seen[s.TraceID] = struct{}{}
		out = append(out, s.TraceID)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// mergeSpansDedup concatenates span slices, dropping duplicates by span_id so
// the union of the error-phase and expansion-phase results contains each span
// exactly once. Error spans appear first so downstream insight logic can lean
// on their ordering as a relevance hint.
func mergeSpansDedup(primary, secondary []common.OpenTelemetryTrace) []common.OpenTelemetryTrace {
	seen := make(map[string]struct{}, len(primary)+len(secondary))
	out := make([]common.OpenTelemetryTrace, 0, len(primary)+len(secondary))
	for _, s := range primary {
		if _, ok := seen[s.SpanID]; ok {
			continue
		}
		seen[s.SpanID] = struct{}{}
		out = append(out, s)
	}
	for _, s := range secondary {
		if _, ok := seen[s.SpanID]; ok {
			continue
		}
		seen[s.SpanID] = struct{}{}
		out = append(out, s)
	}
	return out
}

// convertOTelTracesToMapRows converts OpenTelemetryTrace slice to map format for insight analysis
// This enables the existing traceHandleTracesInsight() function to work with observability traces
func convertOTelTracesToMapRows(traces []common.OpenTelemetryTrace) []map[string]any {
	rows := make([]map[string]any, 0, len(traces))
	for _, trace := range traces {
		row := map[string]any{
			"trace_id":           trace.TraceID,
			"span_id":            trace.SpanID,
			"parent_span_id":     trace.ParentSpanID,
			"span_name":          trace.SpanName,
			"service_name":       trace.ServiceName,
			"status_code":        trace.StatusCode,
			"status_message":     trace.StatusMessage,
			"duration_ns":        trace.DurationNs,
			"workload_name":      trace.WorkloadName,
			"workload_namespace": trace.WorkloadNamespace,
			"destination_name":   trace.DestinationName,
			"http_status_code":   trace.HTTPStatusCode,
			"spanattributes":     trace.SpanAttributes,
		}

		// Convert events_attributes from []map[string]string to JSON string (required format)
		if len(trace.EventsAttributes) > 0 {
			// Flatten event attributes into single map
			eventsMap := make(map[string]any)
			for _, eventAttr := range trace.EventsAttributes {
				for k, v := range eventAttr {
					// Prefix with "event." for consistency with existing logic
					eventsMap["event."+k] = v
				}
			}
			if eventsJSON, err := json.Marshal(eventsMap); err == nil {
				row["events_attributes"] = string(eventsJSON)
			}
		}

		rows = append(rows, row)
	}
	return rows
}

func (a *observabilityTracesAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params observabilityTracesActionParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	if params.AccountId == "" {
		params.AccountId = ctx.GetAccountId()
	}

	if params.AccountId == "" {
		return nil, errors.New("account_id is required")
	}

	if params.Query == "" {
		return nil, errors.New("query is required")
	}

	startTime := int64(0)
	endTime := int64(0)
	if ctx.GetEvent().StartedAt != nil {
		startTime = ctx.GetEvent().StartedAt.UnixMilli()
	}
	if ctx.GetEvent().EndedAt != nil {
		endTime = ctx.GetEvent().EndedAt.UnixMilli()
	}

	if endTime == 0 {
		endTime = time.Now().UnixMilli()
	}

	if startTime == 0 {
		if params.Duration < 1 {
			params.Duration = 60
		}
		startTime = endTime - int64(params.Duration*60*1000)
	}

	traceoutput, err := GetTraces(security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil), TracesV3Request{
		AccountId: params.AccountId,
		Query:     params.Query,
		StartTime: startTime,
		EndTime:   endTime,
		Request:   params.QueryOptions,
	})

	if err != nil {
		return nil, err
	}

	if len(traceoutput) == 0 {
		return nil, nil
	}

	// Convert traces to map format for insight analysis
	traceRows := convertOTelTracesToMapRows(traceoutput)

	// Generate insights from event attributes, errors, and service flow
	insights := traceHandleTracesInsight(traceRows)

	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
	}
	return playbooks.NewPlaybookActionResponseJson(map[string]any{"data": traceoutput}, map[string]any{}, insights, metadata), err
}

// Datadog Traces Action
type datadogTracesAction struct{}

type datadogTracesActionParams struct {
	TraceQuery  string `json:"trace_query,omitempty"`
	TraceFromTs int64  `json:"trace_from_ts,omitempty"`
	TraceToTs   int64  `json:"trace_to_ts,omitempty"`
	TracesURL   string `json:"traces_url,omitempty"`
}

func (a *datadogTracesAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	labels := ctx.GetEvent().Labels
	if labels == nil {
		return false
	}

	// Check if we have tracesURL or trace query parameters
	if labels["traces_url"] != "" || labels["trace_query"] != "" {
		return true
	}

	return false
}

func (a *datadogTracesAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	labels := ctx.GetEvent().Labels
	params := map[string]any{}

	if tracesURL := labels["traces_url"]; tracesURL != "" {
		params["traces_url"] = tracesURL
	}

	if traceQuery := labels["trace_query"]; traceQuery != "" {
		params["trace_query"] = traceQuery
	}

	// Set default time range if not provided
	if ctx.GetEvent().StartedAt != nil && ctx.GetEvent().EndedAt != nil {
		params["trace_from_ts"] = ctx.GetEvent().StartedAt.Unix()
		params["trace_to_ts"] = ctx.GetEvent().EndedAt.Unix()
	}

	return a.Execute(ctx, params)
}

func (a *datadogTracesAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params datadogTracesActionParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	sc := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)

	// Get Datadog credentials
	apiKey, appKey, site, err := integrations.GetDatadogConfigs(sc, ctx.GetAccountId())
	if err != nil {
		return nil, fmt.Errorf("failed to get datadog configs: %w", err)
	}

	// Parse traces URL if provided
	if params.TracesURL != "" && params.TraceQuery == "" {
		traces, err := url.Parse(params.TracesURL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse datadog trace url: %w", err)
		}

		tracesQueryParams := traces.Query()
		if query := tracesQueryParams.Get("query"); query != "" {
			params.TraceQuery = query
		}

		if fromTs := tracesQueryParams.Get("from_ts"); fromTs != "" {
			if fromInt, err := strconv.Atoi(fromTs); err == nil {
				params.TraceFromTs = int64(fromInt)
			}
		}

		if toTs := tracesQueryParams.Get("to_ts"); toTs != "" {
			if toInt, err := strconv.Atoi(toTs); err == nil {
				params.TraceToTs = int64(toInt)
			}
		}
	}

	// Set default time range if not provided
	if params.TraceFromTs == 0 || params.TraceToTs == 0 {
		now := time.Now()
		params.TraceToTs = now.Unix()
		params.TraceFromTs = now.Add(-10 * time.Minute).Unix()
	}

	if params.TraceQuery == "" {
		return nil, errors.New("trace_query or traces_url is required")
	}

	// Create query params structure
	queryParams := integrations.DatadogQueryParams{
		TraceQuery:  params.TraceQuery,
		TraceFromTs: params.TraceFromTs,
		TraceToTs:   params.TraceToTs,
	}

	// Fetch traces from Datadog
	eventTraces, evidence, err := getDatadogTracesForAction(sc, apiKey, appKey, site, queryParams)
	if err != nil {
		return nil, fmt.Errorf("failed to get datadog traces: %w", err)
	}

	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
	}

	insights := []playbooks.PlaybookActionResponseInsight{}
	if len(evidence.Insight) > 0 {
		for _, insight := range evidence.Insight {
			insights = append(insights, playbooks.PlaybookActionResponseInsight{
				Message:  insight.Message,
				Severity: insight.Severity,
			})
		}
	}

	return playbooks.NewPlaybookActionResponseJson(eventTraces, evidence.AdditionalInfo, insights, metadata), nil
}

// Helper function to fetch Datadog traces
func getDatadogTracesForAction(sc *security.RequestContext, apiKey, appKey, site string, tracesQueryParams integrations.DatadogQueryParams) (map[string]any, event.EventEvidence, error) {
	encodedQuery := url.QueryEscape(tracesQueryParams.TraceQuery)
	url := fmt.Sprintf(
		"https://%s/api/v2/spans/events?filter[query]=%s&filter[from]=%d&filter[to]=%d&page[limit]=%d",
		site, encodedQuery, tracesQueryParams.TraceFromTs, tracesQueryParams.TraceToTs, 1000,
	)

	headers := map[string]string{
		"Content-Type":       "application/json",
		"DD-API-KEY":         apiKey,
		"DD-APPLICATION-KEY": appKey,
	}

	body, err := common.HttpGet(url, common.HttpWithHeaders(headers))
	if err != nil {
		return nil, event.EventEvidence{}, fmt.Errorf("failed to make request to datadog traces api: %w", err)
	}
	defer func() {
		closeErr := body.Body.Close()
		if closeErr != nil {
			if err == nil {
				err = fmt.Errorf("failed to close response body: %w", closeErr)
			}
		}
	}()
	bodyBytes, err := io.ReadAll(body.Body)
	if err != nil {
		return nil, event.EventEvidence{}, fmt.Errorf("failed to read datadog traces body: %w", err)
	}

	var eventTraces map[string]any
	if err := common.UnmarshalJson(bodyBytes, &eventTraces); err != nil {
		return nil, event.EventEvidence{}, fmt.Errorf("failed to unmarshal datadog traces: %w", err)
	}

	evidence := event.EventEvidence{}
	if raw, ok := eventTraces["data"]; ok {
		rawBytes, err := json.Marshal(raw)
		if err != nil {
			return nil, event.EventEvidence{}, fmt.Errorf("failed to marshal datadog traces: %w", err)
		}

		var ddTrace common.DatadogTrace
		err = json.Unmarshal(rawBytes, &ddTrace.Data)
		if err != nil {
			return nil, event.EventEvidence{}, fmt.Errorf("failed to unmarshal datadog traces: %w", err)
		}

		otelTraces := common.MapDatadogToOpenTelemetry(ddTrace)
		payload := map[string]any{
			"data": otelTraces,
			"name": "datadog_traces",
		}
		jsonStr, err := common.MarshalJson(payload)
		if err != nil {
			return nil, event.EventEvidence{}, fmt.Errorf("marshaling to JSON of otelTraces: %v", err)
		}
		evidence.Data = string(jsonStr)
		evidence.Type = "json"
		evidence.Insight = []event.EventEvidenceInsight{
			{
				Message:  "Datadog Traces retrieved",
				Severity: "info",
			},
		}
		evidence.AdditionalInfo = map[string]any{
			"action_name":            "datadog_traces",
			"actual_action_name":     "datadog_traces",
			"action_title":           "Datadog Traces",
			"conditional_expression": "",
		}
	}

	return eventTraces, evidence, nil
}
