package observability

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"sort"
	"strconv"
	"strings"
	"time"
)

type LogglyLog struct {
	Next   string           `json:"next"`
	Events []LogglyLogEvent `json:"events"`
}

type LogglyGetLabelRsid struct {
	Id        string `json:"id"`
	Status    string `json:"status"`
	EndDate   int64  `json:"date_to"`
	StartDate int64  `json:"date_from"`
}

type LogglyGetLabelFields struct {
	Name string `json:"name"`
}
type LogglyGetLabelResponse struct {
	Rsid   LogglyGetLabelRsid     `json:"rsid"`
	Fields []LogglyGetLabelFields `json:"fields"`
}

type LogglyGetLabelValueObj struct {
	Term  string  `json:"term"`
	Count float64 `json:"count"`
}

type LogglyGetLabelValueResponse struct {
	TotalEvent       float64                  `json:"total_events"`
	UniqueFieldCount float64                  `json:"unique_field_count"`
	Values           []LogglyGetLabelValueObj `json:"values"`
}

type LogglyLogEvent struct {
	Id        string         `json:"id"`
	Timestamp int64          `json:"timestamp"`
	LogMsg    string         `json:"logmsg"`
	Raw       string         `json:"raw"`
	Event     map[string]any `json:"event"`
	LogTypes  []string       `json:"logtypes"`
	// unparsed           string `json:"meta"`
	Tags []string `json:"tags"`
}

// LogglySource is a LogSource implementation for Loggly.
type LogglySource struct{}

// logglyLogLabelMapping maps Nudgebee canonical field names to Loggly's
// auto-parsed JSON field names. Loggly's HTTP/JSON parser decorates K8s log
// events with json.kubernetes.* — the same names extractLogglyK8sMeta and
// buildLogGroupLuceneQuery already use to read those events.
//
// `app`/`service` target the Helm-standard `app.kubernetes.io/name` label
// (Loggly indexes the dot-replaced form: app_kubernetes_io/name). The slash
// is escaped to `\/` at emit time by EscapeO11yQueryString. Legacy workloads
// that only set the bare `app` label can be remapped via a per-tenant
// `log_labels` override (see log_labels.go).
var logglyLogLabelMapping = map[string]string{
	"timestamp": "timestamp",
	"body":      "json.log",
	"message":   "json.log",
	"namespace": "json.kubernetes.namespace_name",
	"container": "json.kubernetes.container_name",
	"pod":       "json.kubernetes.pod_name",
	"node":      "json.kubernetes.host",
	"host":      "host",
	"hostname":  "host",
	"service":   "json.kubernetes.labels.app_kubernetes_io/name",
	"app":       "json.kubernetes.labels.app_kubernetes_io/name",
	"level":     "json.level",
	"severity":  "json.level",
}

func (s *LogglySource) QueryLogs(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) ([]OutputLog, error) {
	logly_cnfg, err := integrations.GetLogglyConfigs(ctx, fetchLogRequest.AccountId)
	logly := integrations.Loggly{}
	if err != nil {
		return nil, fmt.Errorf("failed to get loggly configs: %w", err)
	}
	if fetchLogRequest.Query == "" {
		q, err := s.GetQuery(ctx, fetchLogRequest)
		if err != nil {
			return nil, fmt.Errorf("loggly: build query: %w", err)
		}
		if q == "" {
			q = "*"
		}
		fetchLogRequest.Query = q
	}

	if fetchLogRequest.Limit == 0 {
		fetchLogRequest.Limit = 1000
	} else if fetchLogRequest.Limit > 1000 {
		return nil, fmt.Errorf("loggly: limit exceeds maximum of 1000")
	}

	executeQueryURL := fmt.Sprintf("%s/apiv2/events/iterate", logly.BaseURL(logly_cnfg))
	logApiRequest := integrations.LogglyLogRequestBody{}
	m := map[string]string{
		"q":     fetchLogRequest.Query,
		"from":  time.UnixMilli(fetchLogRequest.StartTime).UTC().Format(time.RFC3339Nano),
		"until": time.UnixMilli(fetchLogRequest.EndTime).UTC().Format(time.RFC3339Nano),
		"size":  strconv.Itoa(fetchLogRequest.Limit),
	}
	headers := map[string]string{
		"Content-Type":  "application/json",
		"Accept":        "application/json",
		"Authorization": fmt.Sprintf("Bearer %s", logly_cnfg.ApiToken),
	}

	res, err := common.HttpGet(executeQueryURL, common.HttpWithHeaders(headers), common.HttpWithQueryParams(m), common.HttpWithJsonBody(logApiRequest))
	if err != nil {
		ctx.GetLogger().Error("loggly: failed to execute loggly log query", "error", err)
		return nil, fmt.Errorf("failed to execute loggly log query: %w", err)
	}
	defer func() {
		err := res.Body.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing response body", "error", err)
		}
	}()
	var results LogglyLog
	body, err := io.ReadAll(res.Body)
	if err != nil {
		ctx.GetLogger().Error("loggly: failed to read response body", "error", err)
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	err = common.UnmarshalJson(body, &results)
	if err != nil {
		return nil, err
	}

	outputLog, err := s.convertLogglyToOutputLogs(results)
	if err != nil {
		ctx.GetLogger().Error("loggly: failed to get loggly configs", "error", err)
		return nil, fmt.Errorf("failed to get loggly configs: %w", err)
	}
	return outputLog, nil
}

// logglyFieldsPollAttempts and logglyFieldsPollDelay govern how long QueryLabels waits
// for the async /apiv2/fields aggregation to populate. Each attempt re-fetches with the
// rsid until fields are non-empty or the server reports a terminal status. Terminates
// after ~5s total (10 × 500ms) even if still PENDING — callers get whatever is populated.
const (
	logglyFieldsPollAttempts = 10
	logglyFieldsPollDelay    = 500 * time.Millisecond
)

func (s *LogglySource) QueryLabels(ctx *security.RequestContext, fetchLogRequest FetchLogLabelRequest) ([]OutputLogLabel, error) {
	logly_cnfg, err := integrations.GetLogglyConfigs(ctx, fetchLogRequest.AccountId)
	logly := integrations.Loggly{}
	if err != nil {
		return nil, fmt.Errorf("failed to get loggly configs: %w", err)
	}

	baseURL := fmt.Sprintf("%s/apiv2/fields", logly.BaseURL(logly_cnfg))
	headers := map[string]string{
		"Content-Type":  "application/json",
		"Accept":        "application/json",
		"Authorization": fmt.Sprintf("Bearer %s", logly_cnfg.ApiToken),
	}

	// Initial request. /apiv2/fields is asynchronous — the first response typically
	// returns rsid.status=PENDING with fields=[]. Re-fetch with ?rsid={id} to let the
	// server-side aggregation deliver populated fields.
	results, err := s.fetchLogglyFields(ctx, baseURL, headers, map[string]string{"from": "-7d", "until": "now"})
	if err != nil {
		return nil, err
	}

	for attempt := 0; attempt < logglyFieldsPollAttempts && len(results.Fields) == 0 && strings.EqualFold(results.Rsid.Status, "PENDING") && results.Rsid.Id != ""; attempt++ {
		time.Sleep(logglyFieldsPollDelay)
		pollResults, err := s.fetchLogglyFields(ctx, baseURL, headers, map[string]string{"rsid": results.Rsid.Id})
		if err != nil {
			return nil, err
		}
		results = pollResults
	}

	outputLog, err := s.convertLogglyToOutputLabels(results)
	if err != nil {
		ctx.GetLogger().Error("loggly: failed to convert label response", "error", err)
		return nil, fmt.Errorf("failed to convert loggly label response: %w", err)
	}
	return outputLog, nil
}

func (s *LogglySource) fetchLogglyFields(ctx *security.RequestContext, url string, headers, params map[string]string) (LogglyGetLabelResponse, error) {
	res, err := common.HttpGet(url, common.HttpWithHeaders(headers), common.HttpWithQueryParams(params), common.HttpWithJsonBody(integrations.LogglyLogRequestBody{}))
	if err != nil {
		ctx.GetLogger().Error("loggly: failed to execute get label api", "error", err)
		return LogglyGetLabelResponse{}, fmt.Errorf("failed to execute loggly log get label: %w", err)
	}
	defer func() {
		if cerr := res.Body.Close(); cerr != nil {
			ctx.GetLogger().Error("Error closing response body get label", "error", cerr)
		}
	}()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		ctx.GetLogger().Error("loggly: failed to read response body", "error", err)
		return LogglyGetLabelResponse{}, fmt.Errorf("failed to read response body: %w", err)
	}
	var results LogglyGetLabelResponse
	if err := common.UnmarshalJson(body, &results); err != nil {
		return LogglyGetLabelResponse{}, err
	}
	return results, nil
}

func (s *LogglySource) convertLogglyToOutputLabels(logglyLabels LogglyGetLabelResponse) ([]OutputLogLabel, error) {
	outputLabels := make([]OutputLogLabel, 0, len(logglyLabels.Fields))

	for _, labelObj := range logglyLabels.Fields {
		obj := OutputLogLabel{
			Label:      labelObj.Name,
			Attributes: map[string]interface{}{},
		}

		outputLabels = append(outputLabels, obj)
	}

	return outputLabels, nil
}

func (s *LogglySource) convertLogglyToOutputLabelsValues(logglyLabelsValues LogglyGetLabelValueResponse) ([]OutputLogLabelValue, error) {
	outputLabelsValues := make([]OutputLogLabelValue, 0, len(logglyLabelsValues.Values))

	for _, labelObj := range logglyLabelsValues.Values {
		obj := OutputLogLabelValue{
			Value:      labelObj.Term,
			Attributes: map[string]interface{}{"count": labelObj.Count},
		}

		outputLabelsValues = append(outputLabelsValues, obj)
	}

	return outputLabelsValues, nil
}

func (s *LogglySource) QueryLabelValues(ctx *security.RequestContext, fetchLogRequest FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	loggly_cnfg, err := integrations.GetLogglyConfigs(ctx, fetchLogRequest.AccountId)
	logly := integrations.Loggly{}
	if err != nil {
		return nil, fmt.Errorf("failed to get loggly configs: %w", err)
	}
	if fetchLogRequest.LabelName == "" {
		return nil, fmt.Errorf("label name must not be empty")
	}

	// Translate canonical field names ("namespace", "pod", …) to Loggly's
	// provider-specific names so /apiv2/fields/{name} hits a real field.
	labelName := fetchLogRequest.LabelName
	if mapped, ok := logglyLogLabelMapping[labelName]; ok {
		labelName = mapped
	}

	// PathEscape so untranslated label names from the caller (raw or with spaces,
	// slashes, query chars) cannot break the URL or smuggle path segments into the
	// Loggly request. Dots are safe and are preserved, so json.kubernetes.* style
	// fields survive intact.
	executeQueryURL := fmt.Sprintf("%s/apiv2/fields/%s", logly.BaseURL(loggly_cnfg), url.PathEscape(labelName))
	logApiRequest := integrations.LogglyLogRequestBody{}
	// Default to a 7-day window — sparse tenants have no values in the last 24h
	// and Loggly's default window is too narrow to surface anything useful.
	params := map[string]string{
		"from":  "-7d",
		"until": "now",
	}
	headers := map[string]string{
		"Content-Type":  "application/json",
		"Accept":        "application/json",
		"Authorization": fmt.Sprintf("Bearer %s", loggly_cnfg.ApiToken),
	}

	res, err := common.HttpGet(executeQueryURL, common.HttpWithHeaders(headers), common.HttpWithQueryParams(params), common.HttpWithJsonBody(logApiRequest))
	if err != nil {
		ctx.GetLogger().Error("loggly: failed to execute get label value api", "error", err)
		return nil, fmt.Errorf("failed to execute loggly log get label value: %w", err)
	}
	defer func() {
		err := res.Body.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing response body get label value", "error", err)
		}
	}()
	var results map[string]any
	body, err := io.ReadAll(res.Body)
	if err != nil {
		ctx.GetLogger().Error("loggly: failed to read response body", "error", err)
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	err = common.UnmarshalJson(body, &results)
	if err != nil {
		return nil, err
	}
	// Safe lookup: Loggly may legitimately return no values for an unknown or
	// stale field, in which case the key is absent or the value is not an array.
	// A direct .([]any) assertion would panic; a comma-ok form yields nil and
	// the range loop becomes a no-op.
	labelValue, _ := results[labelName].([]any)
	var logglyValues []LogglyGetLabelValueObj
	for _, v := range labelValue {
		valueBytes, _ := json.Marshal(v)
		var valueObj LogglyGetLabelValueObj
		_ = json.Unmarshal(valueBytes, &valueObj)
		logglyValues = append(logglyValues, valueObj)
	}

	totalEvents, _ := results["total_events"].(float64)
	uniqueFieldCount, _ := results["unique_field_count"].(float64)
	logglyResp := LogglyGetLabelValueResponse{
		TotalEvent:       totalEvents,
		UniqueFieldCount: uniqueFieldCount,
		Values:           logglyValues,
	}

	outputLog, err := s.convertLogglyToOutputLabelsValues(logglyResp)
	if err != nil {
		ctx.GetLogger().Error("loggly: failed to get loggly configs", "error", err)
		return nil, fmt.Errorf("failed to get loggly configs: %w", err)
	}
	return outputLog, nil
}

func (s *LogglySource) convertLogglyToOutputLogs(logglyLog LogglyLog) ([]OutputLog, error) {
	var outputLogs []OutputLog

	for _, log := range logglyLog.Events {
		labels := make(map[string]interface{})
		flattenLabels := make(map[string]string)
		common.MapFlatten("", log.Event, flattenLabels)
		// Merge attributes
		for key, value := range flattenLabels {
			labels[key] = value
		}

		// Add fixed labels
		labels["raw"] = log.Raw
		labels["id"] = log.Id
		labels["log_types"] = log.LogTypes

		// // Parse timestamp
		// ts, err := strconv.ParseInt(log.Timestamp, 10, 64)
		// if err != nil {
		// 	return nil, fmt.Errorf("failed to parse timestamp %s: %w", log.Timestamp, err)
		// }

		outputLog := OutputLog{
			Message:   log.LogMsg,
			Labels:    labels,
			Severity:  GetSeverityLevels(log.LogMsg),
			Timestamp: time.Unix(log.Timestamp/1000, (log.Timestamp%1000)*int64(time.Millisecond)).UTC().Format(time.RFC3339Nano),
		}
		outputLogs = append(outputLogs, outputLog)
	}

	return outputLogs, nil
} // extractSeverity attempts to extract severity level from the log message

func (s *LogglySource) GetQuery(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) (string, error) {
	if fetchLogRequest.Query != "" {
		return fetchLogRequest.Query, nil
	}
	if !hasWhereConditions(fetchLogRequest.QueryRequest.Where) {
		return "", nil
	}
	return buildLogglyWhereClause(fetchLogRequest.QueryRequest.Where)
}

// buildLogglyWhereClause converts a structured QueryWhereClause into a Loggly
// Lucene query string. Mirrors the tree-recursion shape of buildO11yWhereClause
// but emits only the operators Loggly advertises via GetSupportedOperators.
func buildLogglyWhereClause(where query.QueryWhereClause) (string, error) {
	if len(where.Binary) > 0 {
		return buildLogglyBinaryClause(where.Binary)
	}

	if len(where.And) > 0 {
		var parts []string
		for _, c := range where.And {
			part, err := buildLogglyWhereClause(c)
			if err != nil {
				return "", err
			}
			if part != "" {
				parts = append(parts, part)
			}
		}
		if len(parts) == 0 {
			return "", nil
		}
		if len(parts) == 1 {
			return parts[0], nil
		}
		return "(" + strings.Join(parts, " AND ") + ")", nil
	}

	if len(where.Or) > 0 {
		var parts []string
		for _, c := range where.Or {
			part, err := buildLogglyWhereClause(c)
			if err != nil {
				return "", err
			}
			if part != "" {
				parts = append(parts, part)
			}
		}
		if len(parts) == 0 {
			return "", nil
		}
		if len(parts) == 1 {
			return parts[0], nil
		}
		return "(" + strings.Join(parts, " OR ") + ")", nil
	}

	if where.Not != nil {
		notPart, err := buildLogglyWhereClause(*where.Not)
		if err != nil {
			return "", err
		}
		if notPart != "" {
			return "NOT (" + notPart + ")", nil
		}
	}

	return "", nil
}

// buildLogglyBinaryClause AND-joins every (field, op, value) triple in a
// BinaryWhereClause into a single Lucene fragment.
func buildLogglyBinaryClause(binary query.BinaryWhereClause) (string, error) {
	var parts []string
	for field, ops := range binary {
		for op, val := range ops {
			clause, err := buildLogglyOperatorClause(field, op, val)
			if err != nil {
				return "", err
			}
			if clause != "" {
				parts = append(parts, clause)
			}
		}
	}
	return strings.Join(parts, " AND "), nil
}

// buildLogglyOperatorClause maps a single operator on a single field to a
// Lucene fragment. Only the operators advertised by GetSupportedOperators are
// translated — anything else returns an error so callers know the predicate
// could not be encoded (instead of silently dropping it and broadening the
// result set).
//
// Loggly behaviors verified against the live API that shape these translations:
//   - Field matching is implicitly case-insensitive on indexed string fields,
//     so _ilike/_icontains use the same emission as _like/_contains.
//   - Inline (?i) regex flags are silently ignored; do not emit them. Loggly's
//     regex on indexed fields is already case-insensitive.
//   - Range operators (_gt/_lt/_gte/_lte) only work on numeric fields. Sending
//     them against a string field returns HTTP 500 from Loggly, so we reject
//     non-numeric values up front with a clear error.
func buildLogglyOperatorClause(field string, op query.BinaryWhereClauseType, val any) (string, error) {
	safeField := integrations.EscapeO11yQueryString(field)
	strVal := fmt.Sprintf("%v", val)

	switch op {
	case query.Eq:
		return fmt.Sprintf("%s:%s", safeField, integrations.EscapeO11yFieldValue(strVal)), nil
	case query.Nq:
		return fmt.Sprintf("NOT %s:%s", safeField, integrations.EscapeO11yFieldValue(strVal)), nil
	case query.Contains, query.IContains:
		return fmt.Sprintf("%s:*%s*", safeField, integrations.EscapeO11yQueryString(strVal)), nil
	case query.NIContains:
		return fmt.Sprintf("NOT %s:*%s*", safeField, integrations.EscapeO11yQueryString(strVal)), nil
	case query.Like, query.ILike:
		return fmt.Sprintf("%s:%s", safeField, sqlLikeToLuceneWildcard(strVal)), nil
	case query.NLike:
		return fmt.Sprintf("NOT %s:%s", safeField, sqlLikeToLuceneWildcard(strVal)), nil
	case query.Regex, query.NRegex:
		return buildLogglyRegexClause(safeField, field, strVal, op == query.NRegex)
	case query.In, query.NotIn:
		return buildLogglyInClause(safeField, field, val, op)
	case query.Gt, query.Gte, query.Lt, query.Lte:
		return buildLogglyRangeClause(safeField, field, strVal, op)
	default:
		return "", fmt.Errorf("loggly: unsupported operator %q for field %q", op, field)
	}
}

func buildLogglyRegexClause(safeField, field, pattern string, negate bool) (string, error) {
	if err := validateUserRegex(pattern); err != nil {
		return "", fmt.Errorf("loggly: invalid regex for field %q: %w", field, err)
	}
	// Lucene regex literals are delimited by /.../, so any literal '/' inside
	// the user pattern (e.g. file paths like /var/log) would prematurely close
	// the literal and produce a syntax error from Loggly. Escape it.
	escaped := strings.ReplaceAll(pattern, `/`, `\/`)
	if negate {
		return fmt.Sprintf("NOT %s:/%s/", safeField, escaped), nil
	}
	return fmt.Sprintf("%s:/%s/", safeField, escaped), nil
}

func buildLogglyInClause(safeField, field string, val any, op query.BinaryWhereClauseType) (string, error) {
	arr, err := toStringArray(val)
	if err != nil {
		return "", fmt.Errorf("loggly: %s on field %q expects an array: %w", op, field, err)
	}
	if len(arr) == 0 {
		// An empty _in is a contradiction (matches nothing); silently emitting
		// "" would make buildLogglyBinaryClause drop the predicate and broaden
		// the result set. Reject it instead. Empty _not_in is the identity
		// (matches everything) — drop it cleanly.
		if op == query.In {
			return "", fmt.Errorf("loggly: %s on field %q requires a non-empty array", op, field)
		}
		return "", nil
	}
	escaped := make([]string, len(arr))
	for i, v := range arr {
		escaped[i] = integrations.EscapeO11yFieldValue(v)
	}
	clause := fmt.Sprintf("%s:(%s)", safeField, strings.Join(escaped, " OR "))
	if op == query.NotIn {
		clause = "NOT " + clause
	}
	return clause, nil
}

var logglyRangeSymbols = map[query.BinaryWhereClauseType]string{
	query.Gt:  ">",
	query.Gte: ">=",
	query.Lt:  "<",
	query.Lte: "<=",
}

func buildLogglyRangeClause(safeField, field, strVal string, op query.BinaryWhereClauseType) (string, error) {
	if err := assertNumericValue(strVal); err != nil {
		return "", fmt.Errorf("loggly: %s on field %q requires a numeric value: %w", op, field, err)
	}
	return fmt.Sprintf("%s:%s%s", safeField, logglyRangeSymbols[op], strVal), nil
}

// sqlLikeToLuceneWildcard converts a SQL LIKE pattern to a Lucene wildcard
// expression after escaping Lucene specials. % -> * (any chars), _ -> ?
// (single char). '%' and '_' are not in the Lucene special set so they
// survive EscapeO11yQueryString untouched.
func sqlLikeToLuceneWildcard(pattern string) string {
	escaped := integrations.EscapeO11yQueryString(pattern)
	escaped = strings.ReplaceAll(escaped, "%", "*")
	escaped = strings.ReplaceAll(escaped, "_", "?")
	return escaped
}

// assertNumericValue ensures the value can be parsed as a number before being
// embedded in a Loggly range query. Range operators against non-numeric fields
// return HTTP 500 from Loggly, so we fail fast with a clearer message.
func assertNumericValue(s string) error {
	if _, err := strconv.ParseFloat(s, 64); err != nil {
		return fmt.Errorf("value %q is not numeric", s)
	}
	return nil
}

func (s *LogglySource) GetLabelMapping() map[string]string {
	return logglyLogLabelMapping
}

// GetSupportedOperators reports the predicate operators that
// buildLogglyOperatorClause can translate to a Loggly Lucene fragment.
// Surfaced via getProviderCapabilities and consumed by the frontend Query
// Builder to populate the per-provider operator dropdown.
func (s *LogglySource) GetSupportedOperators() []string {
	return []string{
		"_eq", "_neq",
		"_contains", "_icontains", "_nicontains",
		"_like", "_ilike", "_nlike",
		"_regex", "_nregex",
		"_in", "_not_in",
	}
}

// QueryLogGroup implements LogGroupSource for Loggly. Loggly exposes no
// server-side message-pattern aggregation (only term-facet counts on a single
// field via /apiv2/fields/{name}), so we fetch up to 1000 error-level events
// via /apiv2/events/iterate and cluster in-memory — the same strategy
// LokiSource uses.
func (s *LogglySource) QueryLogGroup(ctx *security.RequestContext, req FetchLogGroupRequest) (LogGroupOutput, error) {
	query := s.buildLogGroupLuceneQuery(req)

	logs, err := s.QueryLogs(ctx, FetchLogRequest{
		AccountId: req.AccountId,
		Query:     query,
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
		Limit:     1000,
	})
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("loggly.QueryLogGroup: failed to fetch logs: %w", err)
	}

	return s.groupLogglyLogsByPattern(logs, req.EndTime), nil
}

// buildLogGroupLuceneQuery builds a Loggly Lucene query that filters for
// error/critical/fatal log lines, optionally scoped by namespace/workload.
func (s *LogglySource) buildLogGroupLuceneQuery(req FetchLogGroupRequest) string {
	parts := []string{`(json.log:*error* OR json.log:*ERROR* OR json.log:*critical* OR json.log:*CRITICAL* OR json.log:*fatal* OR json.log:*FATAL*)`}

	if ns := common.GetString(req.Request, "selectedNamespace"); ns != "" {
		parts = append(parts, fmt.Sprintf(`json.kubernetes.namespace_name:%s`, integrations.EscapeO11yFieldValue(ns)))
	}
	if wl := common.GetString(req.Request, "selectedWorkload"); wl != "" {
		parts = append(parts, fmt.Sprintf(`json.kubernetes.pod_name:%s-*`, integrations.EscapeO11yQueryString(wl)))
	}

	return strings.Join(parts, " AND ")
}

// extractLogglyK8sMeta pulls namespace, pod, container, and the actual log line
// from an OutputLog produced by convertLogglyToOutputLogs. Tries flattened
// labels first (populated when Loggly parses the event's JSON), falls back to
// decoding the raw JSON payload when server-side parsing is disabled.
func extractLogglyK8sMeta(log OutputLog) (namespace, pod, container, logLine string) {
	getLabel := func(key string) string {
		if v, ok := log.Labels[key].(string); ok {
			return v
		}
		return ""
	}

	namespace = getLabel("json.kubernetes.namespace_name")
	pod = getLabel("json.kubernetes.pod_name")
	container = getLabel("json.kubernetes.container_name")
	logLine = getLabel("json.log")

	if namespace != "" && pod != "" && logLine != "" {
		return
	}

	raw := getLabel("raw")
	if raw == "" {
		if logLine == "" {
			logLine = log.Message
		}
		return
	}

	var parsed struct {
		Log        string `json:"log"`
		Kubernetes struct {
			NamespaceName string `json:"namespace_name"`
			PodName       string `json:"pod_name"`
			ContainerName string `json:"container_name"`
		} `json:"kubernetes"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		if logLine == "" {
			logLine = log.Message
		}
		return
	}

	if namespace == "" {
		namespace = parsed.Kubernetes.NamespaceName
	}
	if pod == "" {
		pod = parsed.Kubernetes.PodName
	}
	if container == "" {
		container = parsed.Kubernetes.ContainerName
	}
	if logLine == "" {
		logLine = parsed.Log
	}
	if logLine == "" {
		logLine = log.Message
	}
	return
}

type logglyGroupEntry struct {
	sample      string
	patternHash string
	namespace   string
	workload    string
	container   string
	level       string
	count       int64
}

func (e *logglyGroupEntry) toLogGroup(endTimeSec int64) LogGroup {
	containerID := ""
	if e.namespace != "" && e.workload != "" {
		containerID = fmt.Sprintf("/k8s/%s/%s", e.namespace, e.workload)
	}
	level := e.level
	if level == "" {
		level = "error"
	}
	return LogGroup{
		Sample:      e.sample,
		Namespace:   e.namespace,
		Workload:    e.workload,
		Container:   e.container,
		ContainerID: containerID,
		PatternHash: e.patternHash,
		Level:       level,
		Count:       e.count,
		Timestamps:  []int64{endTimeSec},
		Values:      []float64{float64(e.count)},
	}
}

func normalizeLogglyEndTimeSec(endTime int64) int64 {
	if endTime <= 0 {
		return time.Now().Unix()
	}
	if endTime >= 1e12 {
		return endTime / 1000
	}
	return endTime
}

// groupLogglyLogsByPattern clusters log entries by SHA1 pattern hash +
// namespace + workload + level, then emits at most 100 groups sorted by count
// desc. Mirrors LokiSource.groupLogsByPattern.
func (s *LogglySource) groupLogglyLogsByPattern(logs []OutputLog, endTime int64) LogGroupOutput {
	grouped := make(map[string]*logglyGroupEntry)

	for _, log := range logs {
		namespace, pod, container, logLine := extractLogglyK8sMeta(log)
		if logLine == "" {
			continue
		}

		workload := extractWorkloadFromPodName(pod)
		level := GetSeverityLevels(logLine)
		hash := generatePatternHash(logLine)
		compositeKey := hash + "|" + namespace + "|" + workload + "|" + level

		entry, exists := grouped[compositeKey]
		if !exists {
			sample := logLine
			if runes := []rune(sample); len(runes) > 500 {
				sample = string(runes[:500])
			}
			entry = &logglyGroupEntry{
				sample:      sample,
				patternHash: hash,
				namespace:   namespace,
				workload:    workload,
				container:   container,
				level:       level,
			}
			grouped[compositeKey] = entry
		}
		entry.count++
	}

	endTimeSec := normalizeLogglyEndTimeSec(endTime)

	groups := make([]LogGroup, 0, len(grouped))
	for _, entry := range grouped {
		groups = append(groups, entry.toLogGroup(endTimeSec))
	}

	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Count > groups[j].Count
	})

	if len(groups) > 100 {
		groups = groups[:100]
	}

	return LogGroupOutput{Groups: groups}
}
