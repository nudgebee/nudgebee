package observability

import (
	"encoding/json"
	"fmt"
	"net/http"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"sort"
	"strings"
	"time"
)

// SolarWindsLogSource implements the LogSource interface for SolarWinds Observability.
type SolarWindsLogSource struct{}

// solarWindsLogLabelMapping maps canonical/alias field names to SolarWinds log field names.
// A /v1/logs response event has exactly 6 fields: id, time, message, hostname, severity, program.
//   - hostname = pod name (for k8s workload logs) or node hostname (for node-level logs)
//   - program  = container / service name within the pod
//
// The search filter also matches the Kubernetes resource attributes the collector attaches
// (k8s.namespace.name, k8s.node.name, ...) even though the response does not echo them.
// Search names "hostname" as "host"; swSearchField applies that rename when building terms.
//
// Exactness differs by field (live probe, 2026-10-07): a quoted value on an event field
// (host, program) is an exact, case-sensitive match, while a resource attribute (k8s.*,
// service.name) matches as a case-insensitive substring quoted or not. That is why "pod"
// stays on hostname: host:"<pod>" is exact, k8s.pod.name:"<pod>" is not.
//
// IMPORTANT: each SW field that a response event carries (right-hand value) appears exactly
// once so that normalizeOutputLogLabels(), which inverts this map, produces deterministic
// output aliases. Search-only attributes may be shared: no event carries them to invert.
// "app"/"workload" map to swWorkloadField, which the builder expands to every workload kind.
// "trace_id" is deliberately unmapped and refused: no trace id field has been confirmed.
// Fields not listed here (e.g. "hostname", "program", "severity") are passed through
// unchanged by convertWhereClauseWithMApping.
// No `message` entry, deliberately: SolarWinds' own schema is
// `id, time, message, hostname, severity, program`, and buildSWBinaryClause renders
// the field verbatim — so the canonical `message` (#39573) already reaches the right
// column. Mapping it to itself would be a no-op. `body` is mapped because OTel
// callers spell it that way and SolarWinds does not.
var solarWindsLogLabelMapping = map[string]string{
	"timestamp": "time",               // OTEL canonical → SW time
	"body":      "message",            // OTEL canonical → SW message
	"pod":       "hostname",           // k8s pod name → SW hostname
	"container": "program",            // k8s container name → SW program
	"level":     "severity",           // OTEL log level → SW severity
	"namespace": "k8s.namespace.name", // k8s namespace → SW resource attribute (search only)
	"node":      "k8s.node.name",      // k8s node → SW resource attribute (search only)
	"cluster":   "k8s.cluster.name",   // k8s cluster → SW resource attribute (search only)
	"service":   "service.name",       // OTel service → SW resource attribute (search only)
	"app":       swWorkloadField,      // k8s workload of any kind (search only)
	"workload":  swWorkloadField,
}

// swWorkloadField is not a SolarWinds field. The collector tags a pod with the attribute of
// its owner's kind, so a workload filter has to try all three; buildSWBinaryClause expands
// it into swWorkloadKindFields.
const swWorkloadField = "k8s.workload.name"

// swWorkloadKindFields are the per-kind workload attributes SolarWinds log search matches.
var swWorkloadKindFields = []string{"k8s.deployment.name", "k8s.daemonset.name", "k8s.statefulset.name"}

// solarWindsKnownLabels are the queryable fields in the SolarWinds /v1/logs API.
var solarWindsKnownLabels = []string{
	"hostname",
	"severity",
	"program",
	"k8s.namespace.name",
	"k8s.deployment.name",
	"k8s.statefulset.name",
	"k8s.daemonset.name",
	"k8s.node.name",
	"k8s.cluster.name",
	"service.name",
}

// solarWindsEntityTypesForField names the /v1/entities types that list the values of a
// search-only field. Log events do not echo these fields, so sampling could never find them.
var solarWindsEntityTypesForField = map[string][]string{
	"k8s.namespace.name":   {"KubernetesNamespace"},
	"k8s.deployment.name":  {"KubernetesDeployment"},
	"k8s.statefulset.name": {"KubernetesStatefulSet"},
	"k8s.daemonset.name":   {"KubernetesDaemonSet"},
	"k8s.node.name":        {"KubernetesNode"},
	"k8s.cluster.name":     {"KubernetesCluster"},
	swWorkloadField:        {"KubernetesDeployment", "KubernetesStatefulSet", "KubernetesDaemonSet"},
}

// swLogResponse is the JSON envelope returned by GET /v1/logs.
type swLogResponse struct {
	Logs     []map[string]any `json:"logs"`
	PageInfo struct {
		PrevPage string `json:"prevPage"`
		NextPage string `json:"nextPage"`
	} `json:"pageInfo"`
}

// swEntityResponse is the JSON envelope returned by GET /v1/entities.
type swEntityResponse struct {
	Entities []struct {
		Name string `json:"name"`
	} `json:"entities"`
}

// swPodEntityResponse parses KubernetesPod entities including namespace from attributes.
type swPodEntityResponse struct {
	Entities []struct {
		Name       string `json:"name"`
		Attributes struct {
			NamespaceName string `json:"namespaceName"`
		} `json:"attributes"`
	} `json:"entities"`
}

// solarWindsSeverityValues are the standard syslog severity levels used by SolarWinds.
var solarWindsSeverityValues = []string{"DEBUG", "INFO", "WARN", "ERROR", "CRITICAL"}

// solarWindsDoGET is the HTTP GET function used to call the SolarWinds REST API.
// Replaced in tests for deterministic behavior without monkey-patching.
var solarWindsDoGET = integrations.DoSolarWindsGET

// solarWindsGetConfigs fetches the SolarWinds API token and data-center region.
// Replaced in tests for deterministic behavior without monkey-patching.
var solarWindsGetConfigs = integrations.GetSolarWindsConfigs

// querySWEntityNames fetches entity names of the given type from the SolarWinds entities API.
// Duplicate names (e.g. same container image in multiple pods) are deduplicated.
func querySWEntityNames(apiToken, baseURL, entityType string) ([]string, error) {
	params := map[string]string{
		"type":     entityType,
		"pageSize": "200",
	}
	body, statusCode, err := solarWindsDoGET(apiToken, baseURL, "/v1/entities", params)
	if err != nil {
		return nil, fmt.Errorf("SolarWinds entities request failed: %w", err)
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("SolarWinds entities API returned HTTP %d", statusCode)
	}
	var resp swEntityResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse SolarWinds entities response: %w", err)
	}
	seen := map[string]struct{}{}
	var names []string
	for _, e := range resp.Entities {
		if e.Name == "" {
			continue
		}
		if _, exists := seen[e.Name]; !exists {
			seen[e.Name] = struct{}{}
			names = append(names, e.Name)
		}
	}
	return names, nil
}

// querySWPodNamespaces returns a map of pod name → Kubernetes namespace by fetching
// KubernetesPod entities. The namespace is stored in attributes.namespaceName.
// Returns an error if the entity API call fails or returns a non-200 status.
func querySWPodNamespaces(apiToken, baseURL string) (map[string]string, error) {
	params := map[string]string{
		"type":     "KubernetesPod",
		"pageSize": "1000",
	}
	body, statusCode, err := solarWindsDoGET(apiToken, baseURL, "/v1/entities", params)
	if err != nil {
		return nil, fmt.Errorf("SolarWinds pod namespace lookup failed: %w", err)
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("SolarWinds entities API returned HTTP %d", statusCode)
	}
	var resp swPodEntityResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse SolarWinds pod entity response: %w", err)
	}
	m := make(map[string]string, len(resp.Entities))
	for _, e := range resp.Entities {
		if e.Name != "" && e.Attributes.NamespaceName != "" {
			m[e.Name] = e.Attributes.NamespaceName
		}
	}
	return m, nil
}

// QueryLogs fetches logs from SolarWinds using the REST /v1/logs endpoint.
func (s *SolarWindsLogSource) QueryLogs(ctx *security.RequestContext, req FetchLogRequest) ([]OutputLog, error) {
	apiToken, dataCenter, err := solarWindsGetConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("SolarWindsLogSource.QueryLogs: failed to get configs", "error", err)
		return nil, fmt.Errorf("failed to get SolarWinds configs: %w", err)
	}

	req, err = swFoldTimeRange(req)
	if err != nil {
		return nil, fmt.Errorf("failed to build SolarWinds log filter: %w", err)
	}
	params := s.buildQueryParams(req)

	filterStr, err := s.buildFilter(req)
	if err != nil {
		return nil, fmt.Errorf("failed to build SolarWinds log filter: %w", err)
	}
	if filterStr != "" {
		params["filter"] = filterStr
	}

	ctx.GetLogger().Debug("SolarWinds log query built", "has_filter", filterStr != "")

	baseURL := integrations.SolarWindsAPIBaseURL(dataCenter)
	body, statusCode, err := solarWindsDoGET(apiToken, baseURL, "/v1/logs", params)
	if err != nil {
		return nil, fmt.Errorf("SolarWinds logs request failed: %w", err)
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("SolarWinds logs API returned HTTP %d: %s", statusCode, string(body))
	}

	var resp swLogResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse SolarWinds log response: %w", err)
	}

	logs := convertSWLogsToOutputLogs(resp.Logs)
	if hasWhereConditions(req.QueryRequest.Where) {
		// The search returned a superset; keep the rows the filter really means.
		return filterSWRows(apiToken, baseURL, req.QueryRequest.Where, logs)
	}
	return logs, nil
}

// QueryLabels returns the known OTEL/k8s log fields available in SolarWinds.
func (s *SolarWindsLogSource) QueryLabels(ctx *security.RequestContext, req FetchLogLabelRequest) ([]OutputLogLabel, error) {
	labels := make([]OutputLogLabel, 0, len(solarWindsKnownLabels))
	for _, field := range solarWindsKnownLabels {
		labels = append(labels, OutputLogLabel{
			Label:      field,
			Attributes: map[string]any{},
		})
	}
	return labels, nil
}

// QueryLabelValues returns distinct values for a specific log field.
//
// For hostname (pod names) and program (container names), the SolarWinds /v1/entities API is
// used — it returns the full registry of current entities rather than a sample of recent logs,
// giving a more complete and accurate list.
//
// For severity, the standard syslog levels are returned directly (they never change).
//
// All other fields fall back to sampling recent logs.
func (s *SolarWindsLogSource) QueryLabelValues(ctx *security.RequestContext, req FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	apiToken, dataCenter, err := solarWindsGetConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get SolarWinds configs: %w", err)
	}
	baseURL := integrations.SolarWindsAPIBaseURL(dataCenter)
	fieldName := swMapField(req.LabelName)

	switch fieldName {
	case "severity":
		return stringsToLabelValues(solarWindsSeverityValues), nil

	case "hostname":
		names, err := querySWEntityNames(apiToken, baseURL, "KubernetesPod")
		if err != nil {
			ctx.GetLogger().Warn("SolarWinds: entities API failed for hostname, falling back to log sampling", "error", err)
		} else if len(names) > 0 {
			return stringsToLabelValues(names), nil
		}
		// Empty result: non-k8s deployment or no entities discovered yet — fall through to log sampling.

	case "program":
		names, err := querySWEntityNames(apiToken, baseURL, "KubernetesContainer")
		if err != nil {
			ctx.GetLogger().Warn("SolarWinds: entities API failed for program, falling back to log sampling", "error", err)
		} else if len(names) > 0 {
			return stringsToLabelValues(names), nil
		}
		// Empty result: non-k8s deployment or no entities discovered yet — fall through to log sampling.

	case "k8s.namespace.name", "k8s.deployment.name", "k8s.node.name", "k8s.cluster.name", swWorkloadField:
		seen := map[string]struct{}{}
		var names []string
		for _, entityType := range solarWindsEntityTypesForField[fieldName] {
			typeNames, err := querySWEntityNames(apiToken, baseURL, entityType)
			if err != nil {
				return nil, fmt.Errorf("SolarWinds %s lookup failed: %w", fieldName, err)
			}
			for _, n := range typeNames {
				if _, dup := seen[n]; !dup {
					seen[n] = struct{}{}
					names = append(names, n)
				}
			}
		}
		return stringsToLabelValues(names), nil
	}

	// Fallback: sample recent logs and extract distinct values for the field.
	// Applies CurrentFilters for cascading accuracy when filters are active.
	params := map[string]string{
		"pageSize": "200",
	}
	if filter := buildLabelValuesFilter(req.CurrentFilters); filter != "" {
		params["filter"] = filter
	}
	if req.StartTime > 0 {
		params["startTime"] = swMsToISO8601(req.StartTime)
	}
	if req.EndTime > 0 {
		params["endTime"] = swMsToISO8601(req.EndTime)
	}
	body, statusCode, err := solarWindsDoGET(apiToken, baseURL, "/v1/logs", params)
	if err != nil {
		return nil, fmt.Errorf("SolarWinds label values request failed: %w", err)
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("SolarWinds logs API returned HTTP %d", statusCode)
	}
	var resp swLogResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse SolarWinds log response: %w", err)
	}
	return extractDistinctFieldValues(resp.Logs, fieldName), nil
}

// stringsToLabelValues converts a slice of strings to OutputLogLabelValue entries.
func stringsToLabelValues(values []string) []OutputLogLabelValue {
	out := make([]OutputLogLabelValue, 0, len(values))
	for _, v := range values {
		out = append(out, OutputLogLabelValue{Value: v, Attributes: map[string]any{}})
	}
	return out
}

// swMapField translates a canonical/alias field name to its SolarWinds syslog equivalent.
// Fields not present in the mapping are returned unchanged.
func swMapField(name string) string {
	if mapped, ok := solarWindsLogLabelMapping[name]; ok {
		return mapped
	}
	return name
}

// buildLabelValuesFilter builds a SolarWinds filter string for label value sampling from the
// active CurrentFilters, joined with AND, so cascading label pickers only return values
// relevant to the current selection. A picker selection is an equality, so it matches exactly. Returns "" when there are none. SolarWinds search cannot
// express "<field> IS NOT EMPTY"; events missing the field are skipped by
// extractDistinctFieldValues instead.
func buildLabelValuesFilter(currentFilters map[string]string) string {
	parts := make([]string, 0, len(currentFilters))
	for k, v := range currentFilters {
		field := swMapField(k)
		parts = append(parts, swTerm(field, v, field != "severity"))
	}
	return strings.Join(parts, " AND ")
}

// extractDistinctFieldValues collects unique non-empty string representations of fieldName
// across logEntries. Using fmt.Sprintf handles non-string API values (numbers, booleans).
func extractDistinctFieldValues(logEntries []map[string]any, fieldName string) []OutputLogLabelValue {
	seen := map[string]struct{}{}
	var values []OutputLogLabelValue
	for _, entry := range logEntries {
		val, ok := entry[fieldName]
		if !ok || val == nil {
			continue
		}
		strVal := fmt.Sprintf("%v", val)
		if strVal == "" {
			continue
		}
		if _, exists := seen[strVal]; !exists {
			seen[strVal] = struct{}{}
			values = append(values, OutputLogLabelValue{Value: strVal, Attributes: map[string]any{}})
		}
	}
	return values
}

// GetQuery returns the SolarWinds filter string for the given log request.
func (s *SolarWindsLogSource) GetQuery(ctx *security.RequestContext, req FetchLogRequest) (string, error) {
	req, err := swFoldTimeRange(req)
	if err != nil {
		return "", err
	}
	return s.buildFilter(req)
}

// RefusedLogFields withholds the fields buildSWBinaryClause refuses from the advertised
// field list: no trace id field has been confirmed in SolarWinds log search.
func (s *SolarWindsLogSource) RefusedLogFields() []string {
	return []string{"trace_id"}
}

// GetLabelMapping returns the mapping from canonical label names to SolarWinds field names.
func (s *SolarWindsLogSource) GetLabelMapping() map[string]string {
	return solarWindsLogLabelMapping
}

// GetSupportedOperators returns the UI-facing operator tokens for SolarWinds
// logs. The list mirrors the operators actually handled by buildSWBinaryClause
// below — every token here has a corresponding case in that switch so filters
// are not silently dropped. The internal `_*_f` float-field variants remain
// accepted on input (legacy saved queries) but are not advertised because they
// rendered as raw `_eq_f`/`_like_f` chips in the dropdown. See issue #29227.
// `_is_null` is not offered: SolarWinds search cannot test for an empty field.
// `_gt`/`_gte`/`_lt`/`_lte` are not offered either: SolarWinds answers "time:>…" with
// HTTP 500, and the time range belongs in startTime/endTime, not in a filter.
func (s *SolarWindsLogSource) GetSupportedOperators() []string {
	return []string{
		"_eq", "_neq",
		"_contains", "_icontains", "_nicontains",
		"_like", "_ilike", "_nlike",
	}
}

// buildQueryParams builds REST query parameters for a log request (time range, page size, sort).
// The label mapping has already been applied to req.QueryRequest.Where by the service layer.
//
// Pagination note: SolarWinds uses cursor-based pagination (pageInfo.nextPage token), not
// integer offsets. req.Offset is therefore not supported and is silently ignored.
//
// Sort note: SolarWinds supports only time-based ordering via the "direction" parameter
// (backward = newest-first, forward = oldest-first). If req.SortFields contains a field
// named "time" or "timestamp" with ascending order, direction is set to "forward".
func (s *SolarWindsLogSource) buildQueryParams(req FetchLogRequest) map[string]string {
	pageSize := 100
	if req.Limit > 0 {
		pageSize = req.Limit
		if pageSize > 1000 {
			pageSize = 1000
		}
	}
	direction := "backward"
	for _, sf := range req.SortFields {
		if sf.ColumnName == "time" || sf.ColumnName == "timestamp" {
			if sf.Order == "asc" || sf.Order == "ascending" {
				direction = "forward"
			}
			break
		}
	}
	params := map[string]string{
		"pageSize":  fmt.Sprintf("%d", pageSize),
		"direction": direction,
	}
	if req.StartTime > 0 {
		params["startTime"] = swMsToISO8601(req.StartTime)
	}
	if req.EndTime > 0 {
		params["endTime"] = swMsToISO8601(req.EndTime)
	}
	return params
}

// buildFilter constructs a SolarWinds search filter string.
// Priority: structured WHERE clause > raw req.Query string.
// The service layer has already applied the label mapping to req.QueryRequest.Where.
func (s *SolarWindsLogSource) buildFilter(req FetchLogRequest) (string, error) {
	if hasWhereConditions(req.QueryRequest.Where) {
		filter, err := buildSWFilterClause(req.QueryRequest.Where)
		if err != nil {
			return "", fmt.Errorf("failed to build SolarWinds filter: %w", err)
		}
		return filter, nil
	}
	// Fall back to raw query string (e.g. typed by user or stored from a previous GetQuery call)
	return req.Query, nil
}

// buildSWFilterClause converts a QueryWhereClause tree to the SolarWinds search string sent
// with the request: a superset of the rows the filter means, which QueryLogs then narrows
// row by row (see solarwinds_logs_filter.go). "" means the search cannot narrow at all.
func buildSWFilterClause(w query.QueryWhereClause) (string, error) {
	c, err := renderSWWhere(w, swSuperset)
	if err != nil {
		return "", err
	}
	if c.kind == swFalse {
		// e.g. "not contains ''": no line can match. Say so rather than send a search
		// that would return lines.
		return "", fmt.Errorf("SolarWinds log filter can never match a line")
	}
	return c.expr, nil
}

// buildSWBinaryClause renders a single condition the way buildSWFilterClause does.
//
// SolarWinds search has no wildcards, IN operator or emptiness test — each of those matches
// nothing. An unquoted "field:value" is a case-insensitive substring match, so contains and
// like patterns drop their wildcards. Equality and value lists quote the value, which makes
// them exact on event fields (host, program); a value list becomes an OR group ("[a,b]"
// matches nothing in logs). Severity stays unquoted: severity:"ERROR" matches nothing.
// A negated condition with no exact search form renders as "" and is applied to the rows.
// An operator without a case is refused, never rendered as "" (which drops the filter).
func buildSWBinaryClause(field string, op query.BinaryWhereClauseType, val any) (string, error) {
	c, err := renderSWLeaf(field, op, val, swSuperset)
	if err != nil {
		return "", err
	}
	return c.expr, nil
}

// swSearchField returns the name SolarWinds search uses for a log field. The response calls
// the pod "hostname" but search only matches it as "host"; "hostname:<pod>" matches nothing.
func swSearchField(field string) string {
	if field == "hostname" {
		return "host"
	}
	return field
}

// swMatch produces a SolarWinds field:value filter term.
func swMatch(field, val string) string {
	return fmt.Sprintf("%s:%s", swSearchField(field), swEscapeValue(val))
}

// swTerm produces the filter term for one value. exact quotes the value; swWorkloadField
// expands into an OR group over every workload kind.
func swTerm(field, val string, exact bool) string {
	if field == swWorkloadField {
		terms := make([]string, 0, len(swWorkloadKindFields))
		for _, kindField := range swWorkloadKindFields {
			terms = append(terms, swTerm(kindField, val, exact))
		}
		return "(" + strings.Join(terms, " OR ") + ")"
	}
	if exact {
		return swSearchField(field) + ":" + swQuote(val)
	}
	return swMatch(field, val)
}

// swQuote always double-quotes a value, escaping backslashes and quotes. Unlike
// swEscapeValue, which quotes only when the value needs it, a quoted value is what makes
// a term on an event field (host, program) exact.
func swQuote(val string) string {
	escaped := strings.ReplaceAll(val, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

// swNegate wraps a filter term with SolarWinds negation syntax.
func swNegate(term string) string {
	return "-(" + term + ")"
}

// swLikeClause turns a contains/like pattern into SolarWinds terms. Search has no wildcards,
// but each "field:value" term already matches value as a substring, so the pattern is split
// on its % and * wildcards and the fragments are ANDed: "%invalid%customer%" becomes
// "(message:invalid AND message:customer)". A pattern of only wildcards returns "" (no
// constraint), negated or not.
func swLikeClause(field, pattern string, negate bool) string {
	fragments := strings.FieldsFunc(pattern, func(r rune) bool { return r == '%' || r == '*' })
	terms := make([]string, 0, len(fragments))
	for _, f := range fragments {
		terms = append(terms, swTerm(field, f, false))
	}
	clause := strings.Join(terms, " AND ")
	switch {
	case clause == "":
		return ""
	case negate:
		return swNegate(clause)
	case len(terms) > 1:
		return "(" + clause + ")"
	default:
		return clause
	}
}

// swEscapeValue wraps a value in double-quotes if it contains spaces or filter-syntax characters.
// Backslashes are escaped before quoting to avoid invalid escape sequences.
func swEscapeValue(val string) string {
	if strings.ContainsAny(val, " \t:\"()\\") {
		escaped := strings.ReplaceAll(val, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		return `"` + escaped + `"`
	}
	return val
}

// convertSWLogsToOutputLogs maps the raw SolarWinds log event maps to OutputLog structs.
// The SolarWinds /v1/logs response uses exactly these fields: id, time, message, hostname, severity, program.
func convertSWLogsToOutputLogs(logEvents []map[string]any) []OutputLog {
	out := make([]OutputLog, 0, len(logEvents))
	for _, event := range logEvents {
		log := OutputLog{Labels: map[string]any{}}

		for k, v := range event {
			strV := fmt.Sprintf("%v", v)
			switch k {
			case "time":
				log.Timestamp = strV
			case "message":
				log.Message = strV
			case "severity":
				log.Severity = strings.ToLower(strV)
			default:
				log.Labels[k] = v
			}
		}

		if log.Severity == "" && log.Message != "" {
			log.Severity = inferSWLogSeverity(log.Message)
		}

		out = append(out, log)
	}
	return out
}

// inferSWLogSeverity infers a severity level from common keywords in the log message.
func inferSWLogSeverity(msg string) string {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "error") || strings.Contains(lower, "fatal") || strings.Contains(lower, "critical"):
		return "error"
	case strings.Contains(lower, "warn"):
		return "warn"
	case strings.Contains(lower, "debug") || strings.Contains(lower, "trace"):
		return "debug"
	default:
		return "info"
	}
}

// swExtractMsgForHash extracts the stable human-readable part of a log message for pattern hashing.
// SolarWinds wraps each log line as a JSON object containing a timestamp, trace IDs, and other
// volatile fields alongside the actual message. Hashing the raw string would make every entry
// unique (count=1). The function performs two normalisation steps:
//  1. Extract "msg" (Go) or "message" (Python) from top-level JSON.
//  2. Strip any trailing embedded-JSON arguments (e.g. "Executing Query {...}" → "Executing Query").
//     This also covers non-JSON log lines that end with timing data and a JSON blob
//     (e.g. "[graphql-proxy] Foo 145ms {…}" → "[graphql-proxy] Foo 145ms").
func swExtractMsgForHash(raw string) string {
	msg := raw
	if len(raw) > 0 && raw[0] == '{' {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &m); err == nil {
			for _, key := range []string{"msg", "message"} {
				if v, ok := m[key]; ok {
					var s string
					if err := json.Unmarshal(v, &s); err == nil && s != "" {
						msg = s
						break
					}
				}
			}
		}
	}
	// Strip trailing embedded JSON arguments so that calls with different parameters
	// (e.g. query WHERE clauses, request payloads) hash to the same pattern.
	if idx := strings.Index(msg, " {"); idx > 0 {
		msg = strings.TrimSpace(msg[:idx])
	}
	return msg
}

// swMsToISO8601 converts a Unix millisecond timestamp to an ISO 8601 UTC string
// as required by the SolarWinds REST API.
func swMsToISO8601(ms int64) string {
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// QueryLogGroup fetches error-level logs from SolarWinds and groups them by message pattern.
// SolarWinds has no native aggregation API, so grouping is performed in-memory.
// SolarWinds syslog schema has no namespace field — Namespace in the result is always empty.
// SolarWinds stores severity values in upper case (ERROR, CRITICAL).
func (s *SolarWindsLogSource) QueryLogGroup(ctx *security.RequestContext, req FetchLogGroupRequest) (LogGroupOutput, error) {
	apiToken, dataCenter, err := solarWindsGetConfigs(ctx, req.AccountId)
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("failed to get SolarWinds configs: %w", err)
	}
	baseURL := integrations.SolarWindsAPIBaseURL(dataCenter)

	// SolarWinds agents forward all logs with severity=INFO regardless of the actual log level.
	// The real level is embedded in the message as a structured JSON token (e.g. "level":"ERROR").
	// We therefore match on the severity field AND on message tokens to capture both cases.
	errorFilter := "(severity:ERROR OR severity:CRITICAL OR severity:FATAL OR message:ERROR OR message:FATAL OR message:CRITICAL)"

	var filter string
	selectedWorkload := common.GetString(req.Request, "selectedWorkload")
	selectedNamespace := common.GetString(req.Request, "selectedNamespace")
	if selectedWorkload != "" {
		// Pods are named after their workload, and a host term matches as a substring, so
		// "host:<workload>" narrows to the workload's pods (groupSWLogsByPattern then keeps
		// exact workload matches). Search has no wildcards: "host:<workload>*" matched nothing.
		filter = fmt.Sprintf("%s AND %s", errorFilter, swMatch("hostname", selectedWorkload))
	} else {
		filter = errorFilter
	}

	params := map[string]string{
		"filter":   filter,
		"pageSize": "1000",
	}
	if req.StartTime > 0 {
		params["startTime"] = swMsToISO8601(req.StartTime)
	}
	if req.EndTime > 0 {
		params["endTime"] = swMsToISO8601(req.EndTime)
	}

	body, statusCode, err := solarWindsDoGET(apiToken, baseURL, "/v1/logs", params)
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("SolarWinds log group request failed: %w", err)
	}
	if statusCode != http.StatusOK {
		return LogGroupOutput{}, fmt.Errorf("SolarWinds logs API returned HTTP %d: %s", statusCode, string(body))
	}

	var resp swLogResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return LogGroupOutput{}, fmt.Errorf("failed to parse SolarWinds log response: %w", err)
	}

	podNamespaces, nsErr := querySWPodNamespaces(apiToken, baseURL)
	if nsErr != nil {
		if selectedNamespace != "" {
			// Cannot correctly apply the namespace filter without the pod→namespace mapping.
			return LogGroupOutput{}, fmt.Errorf("SolarWinds log group: failed to fetch pod namespaces for namespace filter: %w", nsErr)
		}
		ctx.GetLogger().Warn("SolarWinds log group: pod namespace lookup failed, namespace field will be empty", "error", nsErr)
	}
	logs := convertSWLogsToOutputLogs(resp.Logs)
	return groupSWLogsByPattern(logs, podNamespaces, selectedNamespace, selectedWorkload, req.EndTime), nil
}

// groupSWLogsByPattern groups SolarWinds log entries by message pattern hash in-memory.
// SolarWinds syslog fields map as: hostname → pod name, program → container name.
// There is no namespace field in the SolarWinds log schema; namespace is resolved via the entity API.
// When selectedNamespace is non-empty, only groups belonging to that namespace are returned.
// When selectedWorkload is non-empty, only groups belonging to that workload are returned.
func groupSWLogsByPattern(logs []OutputLog, podNamespaces map[string]string, selectedNamespace string, selectedWorkload string, endTime int64) LogGroupOutput {
	type groupEntry struct {
		sample    string
		hash      string // pre-computed from full message, reused for PatternHash
		namespace string
		workload  string
		container string
		level     string
		count     int64
		lastSeen  int64 // unix seconds of the newest line in the group
	}

	grouped := make(map[string]*groupEntry)

	for _, log := range logs {
		if log.Message == "" {
			continue
		}

		hash := generatePatternHash(swExtractMsgForHash(log.Message))
		pod, _ := log.Labels["hostname"].(string)
		container, _ := log.Labels["program"].(string)
		workload := extractWorkloadFromPodName(pod)
		namespace := podNamespaces[pod]
		// SolarWinds agents report all logs as severity "info" regardless of actual level.
		// Infer the real level from the message content (handles structured JSON logs).
		level := inferSWLogSeverity(log.Message)

		compositeKey := hash + "|" + namespace + "|" + workload + "|" + level

		entry, exists := grouped[compositeKey]
		if !exists {
			sample := log.Message
			if runes := []rune(sample); len(runes) > 500 {
				sample = string(runes[:500])
			}
			entry = &groupEntry{
				sample:    sample,
				hash:      hash,
				namespace: namespace,
				workload:  workload,
				container: container,
				level:     level,
			}
			grouped[compositeKey] = entry
		}
		entry.count++

		if ts := logLastSeenUnix(log.Timestamp); ts > entry.lastSeen {
			entry.lastSeen = ts
		}
	}

	// Only used when a group's own lines carry no parseable timestamp.
	endTimeSec := logGroupWindowEndUnix(endTime)

	groups := make([]LogGroup, 0, len(grouped))
	for _, entry := range grouped {
		if selectedNamespace != "" && entry.namespace != selectedNamespace {
			continue
		}
		if selectedWorkload != "" && entry.workload != selectedWorkload {
			continue
		}
		containerID := ""
		if entry.namespace != "" && entry.workload != "" {
			containerID = fmt.Sprintf("/k8s/%s/%s", entry.namespace, entry.workload)
		} else if entry.workload != "" {
			containerID = fmt.Sprintf("/solarwinds/%s", entry.workload)
		}

		level := entry.level
		if level == "" {
			level = "error"
		}

		lastSeen := entry.lastSeen
		if lastSeen <= 0 {
			lastSeen = endTimeSec
		}

		groups = append(groups, LogGroup{
			Sample:      entry.sample,
			Namespace:   entry.namespace,
			Workload:    entry.workload,
			Container:   entry.container,
			ContainerID: containerID,
			PatternHash: entry.hash,
			Level:       level,
			Count:       entry.count,
			Timestamps:  []int64{lastSeen},
			Values:      []float64{float64(entry.count)},
		})
	}

	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Count > groups[j].Count
	})

	if len(groups) > 100 {
		groups = groups[:100]
	}

	return LogGroupOutput{Groups: groups}
}
