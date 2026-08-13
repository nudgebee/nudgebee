package observability

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"sort"
	"strings"
	"time"
)

// OpenObserveLogSource implements LogSource interface for OpenObserve
type OpenObserveLogSource struct{}

// openObserveLogLabelMapping maps canonical field names to OpenObserve column names.
//
// OpenObserve has no fixed log schema — it flattens whatever the shipper sends, joining
// nested keys with '_'. These are the OpenTelemetry collector's names (resource attributes
// k8s.namespace.name → k8s_namespace_name), which is the dominant OpenObserve ingestion
// path. A Fluent Bit pipeline writes kubernetes_namespace_name instead; those deployments
// can remap per-account via the label-mapping override rather than editing this table.
var openObserveLogLabelMapping = map[string]string{
	"timestamp": "_timestamp",
	"body":      "body",
	"message":   "body",
	"namespace": "k8s_namespace_name",
	"pod":       "k8s_pod_name",
	"container": "k8s_container_name",
	"node":      "k8s_node_name",
	"workload":  "k8s_deployment_name",
	"app":       "k8s_deployment_name",
	"cluster":   "k8s_cluster",
	"host":      "host_name",
	"hostname":  "host_name",
	"service":   "service_name",
	// severity is the OTel severity number (Int64) on collector-fed streams, not a
	// level string — see the log-group column resolution for why that distinction matters.
	"severity": "severity",
	"level":    "severity",
}

func (s *OpenObserveLogSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_contains", "_ilike"}
}

func (s *OpenObserveLogSource) GetLabelMapping() map[string]string {
	return openObserveLogLabelMapping
}

func (s *OpenObserveLogSource) GetIgnoredQueryRequestKeys() []string {
	return []string{} // No keys ignored by default
}

type openObserveSearchRequest struct {
	Query struct {
		SQL       string `json:"sql"`
		StartTime int64  `json:"start_time"`
		EndTime   int64  `json:"end_time"`
		Size      int    `json:"size,omitempty"`
	} `json:"query"`
}

type openObserveSearchResponse struct {
	Hits []map[string]any `json:"hits"`
}

func escapeOpenObserveString(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

// openObserveAuthHeader builds the Basic auth header every OpenObserve API call needs.
func openObserveAuthHeader(username, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}

func isSafeIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func buildOpenObserveBinaryClause(binary query.BinaryWhereClause, mapping map[string]string) (string, error) {
	var parts []string
	for field, ops := range binary {
		col := field
		if mapped, ok := mapping[col]; ok {
			col = mapped
		}
		if !isSafeIdentifier(col) {
			return "", fmt.Errorf("invalid or unsafe column name: %q", col)
		}
		for op, val := range ops {
			strVal := fmt.Sprintf("%v", val)
			switch op {
			case query.Eq:
				parts = append(parts, fmt.Sprintf("%s = '%s'", col, escapeOpenObserveString(strVal)))
			case query.Nq:
				parts = append(parts, fmt.Sprintf("%s != '%s'", col, escapeOpenObserveString(strVal)))
			case query.Contains:
				parts = append(parts, fmt.Sprintf("str_match(%s, '%s')", col, escapeOpenObserveString(strVal)))
			case query.ILike:
				parts = append(parts, fmt.Sprintf("str_match_ignore_case(%s, '%s')", col, escapeOpenObserveString(strVal)))
			default:
				return "", fmt.Errorf("unsupported binary operator for OpenObserve: %s", op)
			}
		}
	}
	return strings.Join(parts, " AND "), nil
}

func buildOpenObserveSQLWhereClause(where query.QueryWhereClause) (string, error) {
	if len(where.Binary) > 0 {
		return buildOpenObserveBinaryClause(where.Binary, openObserveLogLabelMapping)
	}

	if len(where.And) > 0 {
		var parts []string
		for _, c := range where.And {
			part, err := buildOpenObserveSQLWhereClause(c)
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
			part, err := buildOpenObserveSQLWhereClause(c)
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
		notPart, err := buildOpenObserveSQLWhereClause(*where.Not)
		if err != nil {
			return "", err
		}
		if notPart != "" {
			return fmt.Sprintf("NOT (%s)", notPart), nil
		}
	}

	return "", nil
}

func (s *OpenObserveLogSource) buildSQL(req FetchLogRequest, stream string) (string, error) {
	whereClause, err := buildOpenObserveSQLWhereClause(req.QueryRequest.Where)
	if err != nil {
		return "", err
	}

	limit := req.Limit
	if limit == 0 {
		limit = 100
	}
	if limit > 10000 {
		limit = 10000
	}

	sql := fmt.Sprintf(`SELECT * FROM "%s"`, stream)
	if whereClause != "" {
		sql += " WHERE " + whereClause
	}

	sql += fmt.Sprintf(" ORDER BY _timestamp DESC LIMIT %d", limit)

	return sql, nil
}

func (s *OpenObserveLogSource) GetQuery(ctx *security.RequestContext, req FetchLogRequest) (string, error) {
	cfg, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		return "", fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}
	return s.buildSQL(req, cfg.LogStream)
}

func (s *OpenObserveLogSource) QueryLogs(ctx *security.RequestContext, req FetchLogRequest) ([]OutputLog, error) {
	cfg, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}

	sql, err := s.buildSQL(req, cfg.LogStream)
	if err != nil {
		return nil, err
	}

	// OpenObserve requires microseconds
	startTimeMicros := req.StartTime * 1000
	endTimeMicros := req.EndTime * 1000

	searchReq := openObserveSearchRequest{}
	searchReq.Query.SQL = sql
	searchReq.Query.StartTime = startTimeMicros
	searchReq.Query.EndTime = endTimeMicros

	payloadBytes, err := json.Marshal(searchReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal search request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/%s/_search", cfg.URL, cfg.OrgID)
	authHeader := openObserveAuthHeader(cfg.Username, cfg.Password)

	resp, err := common.HttpPost(endpoint,
		common.HttpWithHeaders(map[string]string{
			"Authorization": authHeader,
			"Content-Type":  "application/json",
		}),
		common.HttpWithBody(io.NopCloser(bytes.NewReader(payloadBytes))),
		common.HttpWithTimeout(30*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("OpenObserve API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		var errorBody map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errorBody)
		return nil, fmt.Errorf("OpenObserve query failed with status %d: %v", resp.StatusCode, errorBody)
	}

	var searchResp openObserveSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("failed to decode OpenObserve response: %w", err)
	}

	var outputs []OutputLog
	for _, hit := range searchResp.Hits {
		out := OutputLog{
			Labels: make(map[string]any),
		}

		for k, v := range hit {
			switch k {
			case "_timestamp":
				if tVal, ok := v.(float64); ok {
					// convert micros to format
					ts := time.UnixMicro(int64(tVal))
					out.Timestamp = ts.UTC().Format(time.RFC3339Nano)
				} else if tStr, ok := v.(string); ok {
					out.Timestamp = tStr
				}
				out.Labels[k] = v
			case "message", "log", "body":
				if str, ok := v.(string); ok {
					out.Message = str
				}
				out.Labels[k] = v
			case "severity", "level":
				if str, ok := v.(string); ok {
					out.Severity = str
				}
				out.Labels[k] = v
			default:
				out.Labels[k] = v
			}
		}

		outputs = append(outputs, out)
	}

	return outputs, nil
}

func buildOpenObserveLabelSampleRequest(req FetchLogLabelRequest, now time.Time) FetchLogRequest {
	startTime, endTime := req.StartTime, req.EndTime
	if startTime == 0 && endTime == 0 {
		endTime = now.UnixMilli()
		startTime = now.Add(-time.Hour).UnixMilli()
	}

	return FetchLogRequest{
		AccountId:         req.AccountId,
		LogProvider:       req.LogProvider,
		LogProviderSource: req.LogProviderSource,
		StartTime:         startTime,
		EndTime:           endTime,
		Limit:             100,
	}
}

func collectOpenObserveLogLabels(logs []OutputLog) []OutputLogLabel {
	labelSet := make(map[string]struct{})
	for _, log := range logs {
		for name := range log.Labels {
			labelSet[name] = struct{}{}
		}
	}

	labelNames := make([]string, 0, len(labelSet))
	for name := range labelSet {
		labelNames = append(labelNames, name)
	}
	sort.Strings(labelNames)

	labels := make([]OutputLogLabel, 0, len(labelNames))
	for _, name := range labelNames {
		labels = append(labels, OutputLogLabel{Label: name, Attributes: make(map[string]any)})
	}
	return labels
}

func (s *OpenObserveLogSource) QueryLabels(ctx *security.RequestContext, req FetchLogLabelRequest) ([]OutputLogLabel, error) {
	fetchReq := buildOpenObserveLabelSampleRequest(req, time.Now())

	logs, err := s.QueryLogs(ctx, fetchReq)
	if err != nil {
		return nil, err
	}

	return collectOpenObserveLogLabels(logs), nil
}

func (s *OpenObserveLogSource) QueryLabelValues(ctx *security.RequestContext, req FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	// Query unique values for the requested label using SQL GROUP BY
	cfg, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}

	col := req.LabelName
	if mapped, ok := openObserveLogLabelMapping[col]; ok {
		col = mapped
	}
	if !isSafeIdentifier(col) {
		return nil, fmt.Errorf("invalid or unsafe label name: %q", col)
	}

	// Skip nulls and blanks: they render as empty rows in the value dropdown, and the
	// dropdown is the only consumer of this call.
	sql := fmt.Sprintf(`SELECT %s FROM "%s" WHERE %s IS NOT NULL GROUP BY %s ORDER BY %s LIMIT 100`,
		col, cfg.LogStream, col, col, col)

	// The label-value dropdown fires before the user has committed a filter, and several
	// callers omit the window entirely. Without a default the range is 0→0 and OpenObserve
	// correctly returns nothing, which surfaces as "no suggestions".
	startTimeMicros, endTimeMicros := openObserveTimeRangeMicros(req.StartTime, req.EndTime, time.Now())

	searchReq := openObserveSearchRequest{}
	searchReq.Query.SQL = sql
	searchReq.Query.StartTime = startTimeMicros
	searchReq.Query.EndTime = endTimeMicros

	payloadBytes, err := json.Marshal(searchReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal search request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/%s/_search", cfg.URL, cfg.OrgID)
	authHeader := openObserveAuthHeader(cfg.Username, cfg.Password)

	resp, err := common.HttpPost(endpoint,
		common.HttpWithHeaders(map[string]string{
			"Authorization": authHeader,
			"Content-Type":  "application/json",
		}),
		common.HttpWithBody(io.NopCloser(bytes.NewReader(payloadBytes))),
		common.HttpWithTimeout(15*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("OpenObserve API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenObserve query failed with status %d", resp.StatusCode)
	}

	var searchResp openObserveSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("failed to decode OpenObserve response: %w", err)
	}

	var values []OutputLogLabelValue
	for _, hit := range searchResp.Hits {
		if val, ok := hit[col]; ok && val != nil {
			strVal := fmt.Sprintf("%v", val)
			if strVal != "" {
				values = append(values, OutputLogLabelValue{
					Value:      strVal,
					Attributes: make(map[string]any),
				})
			}
		}
	}

	return values, nil
}

// openObserveLogGroupLimit caps how many aggregated error patterns are returned,
// matching the other log-group providers.
const openObserveLogGroupLimit = 100

// openObserveErrorSeverities are the lower-cased severity values treated as errors.
var openObserveErrorSeverities = []string{"error", "critical", "fatal", "err", "crit"}

// openObserveErrorMessagePatterns are matched against the message body when a record
// carries no usable severity. Deliberately upper-case: a case-insensitive match on
// "error" hits ordinary prose ("no errors found") far too often.
var openObserveErrorMessagePatterns = []string{"ERROR", "FATAL", "CRITICAL"}

// openObserveExcludedContainers are infrastructure containers whose logs are noise in
// the error-pattern view.
var openObserveExcludedContainers = []string{"prometheus", "grafana", "nudgebee-agent"}

// openObserveLogGroupCandidates lists the accepted OpenObserve column names per logical
// role, in priority order. OpenObserve flattens nested ingest payloads with '_', so the
// same logical field lands under a different name depending on the shipper (OTel
// collector → k8s_namespace_name, Fluent Bit → kubernetes_namespace_name, and so on).
// The stream schema is read at runtime and the first candidate present wins, so a role
// with no match simply drops out of the GROUP BY instead of producing SQL that
// references a non-existent column — which OpenObserve rejects outright.
var openObserveLogGroupCandidates = struct {
	Message        []string
	Severity       []string
	SeverityNumber []string
	Namespace      []string
	Pod            []string
	Container      []string
	Workload       []string
}{
	Message:  []string{"body", "message", "log"},
	Severity: []string{"severity_text", "severity", "level", "log_level"},
	// Only the OTel-specified name is accepted for numeric severity. Other numeric
	// "level"/"severity" columns are ambiguous — syslog counts down (0 = emerg, 3 = err)
	// while OTel counts up (17+ = error), and guessing wrong inverts the filter so INFO
	// records would be reported as errors.
	SeverityNumber: []string{"severity_number"},
	Namespace:      []string{"k8s_namespace_name", "kubernetes_namespace_name", "namespace_name", "namespace"},
	Pod:            []string{"k8s_pod_name", "kubernetes_pod_name", "pod_name", "pod"},
	Container:      []string{"k8s_container_name", "kubernetes_container_name", "container_name", "container"},
	// The real workload name, when the shipper records one. Preferred over deriving it
	// from the pod name: the derived value is a guess, and the drill-down turns whatever
	// we report here into an `app` filter — which maps to the deployment column. If the
	// two disagree, clicking a group silently returns no logs.
	Workload: []string{"k8s_deployment_name", "kubernetes_deployment_name", "deployment"},
}

// openObserveOtelErrorSeverityNumber is the lowest OTel severity number meaning ERROR.
// The OTel spec assigns TRACE 1-4, DEBUG 5-8, INFO 9-12, WARN 13-16, ERROR 17-20, FATAL 21-24.
const openObserveOtelErrorSeverityNumber = 17

// openObserveOtelFatalSeverityNumber is the lowest OTel severity number meaning FATAL.
const openObserveOtelFatalSeverityNumber = 21

// openObserveLogGroupCols holds the stream column actually backing each logical role.
// Every field except Message may be empty when the stream has no matching column.
type openObserveLogGroupCols struct {
	Message string
	// Severity is a string-typed severity column (e.g. severity_text = "ERROR").
	Severity string
	// SeverityNumber is a numeric OTel severity column (severity_number = 17).
	// A stream can carry both; string severity is preferred for filtering because
	// it is what the record actually reads as.
	SeverityNumber string
	Namespace      string
	Pod            string
	// Workload is the shipper-recorded workload name. Empty on streams that don't
	// carry one, in which case it is derived from the pod name per row.
	Workload  string
	Container string
}

// openObserveStreamSchema is the subset of GET /api/{org}/streams/{stream}/schema we read.
type openObserveStreamSchema struct {
	Name   string `json:"name"`
	Schema []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"schema"`
}

// openObserveNonStringTypes are the Arrow type names OpenObserve reports for columns that
// cannot be used in a string context. Anything not listed — including an empty or
// unrecognised type name — is treated as usable: the schema endpoint is the only type
// source available, and dropping a column because its type name is unfamiliar would break
// working streams for no gain.
var openObserveNonStringTypes = map[string]struct{}{
	"int8": {}, "int16": {}, "int32": {}, "int64": {},
	"uint8": {}, "uint16": {}, "uint32": {}, "uint64": {},
	"float16": {}, "float32": {}, "float64": {},
	"boolean": {}, "bool": {}, "binary": {}, "largebinary": {},
	"date32": {}, "date64": {}, "null": {},
}

// isOpenObserveStringColumn reports whether a column can be used with LOWER(), LIKE, and
// string-literal comparison. Applying LOWER() to an Int64 column makes DataFusion reject
// the entire query with a type_coercion planning error, so every role that lands in a
// string context must be type-checked before it reaches the SQL.
func isOpenObserveStringColumn(fieldType string) bool {
	_, nonString := openObserveNonStringTypes[strings.ToLower(strings.TrimSpace(fieldType))]
	return !nonString
}

// isOpenObserveIntegerColumn reports whether a column holds an integer, so it can be
// compared numerically rather than as a string.
func isOpenObserveIntegerColumn(fieldType string) bool {
	switch strings.ToLower(strings.TrimSpace(fieldType)) {
	case "int8", "int16", "int32", "int64", "uint8", "uint16", "uint32", "uint64":
		return true
	default:
		return false
	}
}

// fetchOpenObserveStreamFields returns the columns defined on a log stream, keyed by name
// with the Arrow type name as the value. Types matter as much as names: the same logical
// field can arrive as Utf8 from one shipper and Int64 from another (severity_text vs a
// numeric severity), and using the wrong one in a string context fails the whole query.
func fetchOpenObserveStreamFields(url, orgID, username, password, stream string) (map[string]string, error) {
	endpoint := fmt.Sprintf("%s/api/%s/streams/%s/schema?type=logs",
		url, neturl.PathEscape(orgID), neturl.PathEscape(stream))

	resp, err := common.HttpGet(endpoint,
		common.HttpWithHeaders(map[string]string{
			"Authorization": openObserveAuthHeader(username, password),
			"Content-Type":  "application/json",
		}),
		common.HttpWithTimeout(15*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("OpenObserve schema request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OpenObserve schema request for stream %q failed with status %d: %s",
			stream, resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	var schema openObserveStreamSchema
	if err := json.NewDecoder(resp.Body).Decode(&schema); err != nil {
		return nil, fmt.Errorf("failed to decode OpenObserve schema response: %w", err)
	}

	fields := make(map[string]string, len(schema.Schema))
	for _, f := range schema.Schema {
		if f.Name != "" {
			fields[f.Name] = f.Type
		}
	}
	return fields, nil
}

// pickOpenObserveColumn returns the first candidate present in the stream schema whose
// type can be used in a string context. A candidate that exists but is numeric is skipped
// rather than accepted, so resolution falls through to the next spelling.
func pickOpenObserveColumn(fields map[string]string, candidates []string) string {
	for _, c := range candidates {
		if fieldType, ok := fields[c]; ok && isOpenObserveStringColumn(fieldType) {
			return c
		}
	}
	return ""
}

// pickOpenObserveIntegerColumn returns the first candidate present with an integer type.
func pickOpenObserveIntegerColumn(fields map[string]string, candidates []string) string {
	for _, c := range candidates {
		if fieldType, ok := fields[c]; ok && isOpenObserveIntegerColumn(fieldType) {
			return c
		}
	}
	return ""
}

// resolveOpenObserveLogGroupCols maps each logical role onto a real stream column.
func resolveOpenObserveLogGroupCols(fields map[string]string) openObserveLogGroupCols {
	return openObserveLogGroupCols{
		Message:        pickOpenObserveColumn(fields, openObserveLogGroupCandidates.Message),
		Severity:       pickOpenObserveColumn(fields, openObserveLogGroupCandidates.Severity),
		SeverityNumber: pickOpenObserveIntegerColumn(fields, openObserveLogGroupCandidates.SeverityNumber),
		Namespace:      pickOpenObserveColumn(fields, openObserveLogGroupCandidates.Namespace),
		Pod:            pickOpenObserveColumn(fields, openObserveLogGroupCandidates.Pod),
		Container:      pickOpenObserveColumn(fields, openObserveLogGroupCandidates.Container),
		Workload:       pickOpenObserveColumn(fields, openObserveLogGroupCandidates.Workload),
	}
}

// openObserveQuoteLiteralList renders a SQL list of escaped string literals.
func openObserveQuoteLiteralList(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = "'" + escapeOpenObserveString(v) + "'"
	}
	return strings.Join(quoted, ", ")
}

// buildOpenObserveLogGroupSQL emits a GROUP BY query that pushes the aggregation down to
// OpenObserve. Grouping on the exact message matches the other providers: generatePatternHash
// is a SHA1 of the raw message bytes, so client-side grouping would have no fidelity
// advantage while costing a full raw-row fetch. MAX(_timestamp) gives the UI a per-group
// "Last Time"; without it every row renders the same timestamp.
func buildOpenObserveLogGroupSQL(stream string, cols openObserveLogGroupCols, selectedNamespace, selectedWorkload string, limit int) string {
	if limit <= 0 {
		limit = openObserveLogGroupLimit
	}

	// Aliases are prefixed so they can never collide with a SQL reserved word.
	selectCols := []string{fmt.Sprintf("%s AS group_sample", cols.Message)}
	groupCols := []string{cols.Message}

	appendCol := func(col, alias string) {
		if col == "" {
			return
		}
		selectCols = append(selectCols, fmt.Sprintf("%s AS %s", col, alias))
		groupCols = append(groupCols, col)
	}
	appendCol(cols.Namespace, "group_namespace")
	appendCol(cols.Pod, "group_pod")
	appendCol(cols.Workload, "group_workload")
	appendCol(cols.Container, "group_container")
	appendCol(cols.Severity, "group_level")
	appendCol(cols.SeverityNumber, "group_level_num")

	selectCols = append(selectCols,
		"count(*) AS group_count",
		"max(_timestamp) AS group_last_ts",
	)

	// Message-content fallback for records that carry no usable severity at all.
	messageMatches := make([]string, 0, len(openObserveErrorMessagePatterns))
	for _, p := range openObserveErrorMessagePatterns {
		messageMatches = append(messageMatches,
			fmt.Sprintf("%s LIKE '%%%s%%'", cols.Message, escapeOpenObserveString(p)))
	}
	messageErrorFilter := "(" + strings.Join(messageMatches, " OR ") + ")"

	// What identifies an error when the string severity is absent or blank: a numeric OTel
	// severity if the stream has one, otherwise message content.
	fallbackErrorFilter := messageErrorFilter
	if cols.SeverityNumber != "" {
		fallbackErrorFilter = fmt.Sprintf("%s >= %d", cols.SeverityNumber, openObserveOtelErrorSeverityNumber)
	}

	conditions := []string{
		fmt.Sprintf("%s IS NOT NULL AND %s != ''", cols.Message, cols.Message),
	}

	if cols.Severity != "" {
		// LOWER() is only ever applied to a column the schema reported as string-typed —
		// DataFusion rejects the whole query with a type_coercion error otherwise.
		conditions = append(conditions, fmt.Sprintf("(LOWER(%s) IN (%s) OR ((%s IS NULL OR %s = '') AND %s))",
			cols.Severity, openObserveQuoteLiteralList(openObserveErrorSeverities),
			cols.Severity, cols.Severity, fallbackErrorFilter))
	} else {
		conditions = append(conditions, fallbackErrorFilter)
	}

	if cols.Container != "" {
		conditions = append(conditions, fmt.Sprintf("%s NOT IN (%s)",
			cols.Container, openObserveQuoteLiteralList(openObserveExcludedContainers)))
	}

	if selectedNamespace != "" && cols.Namespace != "" {
		conditions = append(conditions, fmt.Sprintf("%s = '%s'",
			cols.Namespace, escapeOpenObserveString(selectedNamespace)))
	}

	if selectedWorkload != "" {
		// Pods are named {workload}-{suffix}; fall back to the container name when the
		// stream carries no pod column.
		escaped := escapeOpenObserveString(selectedWorkload)
		switch {
		case cols.Pod != "":
			conditions = append(conditions, fmt.Sprintf("%s LIKE '%s-%%'", cols.Pod, escaped))
		case cols.Container != "":
			conditions = append(conditions, fmt.Sprintf("%s LIKE '%s%%'", cols.Container, escaped))
		}
	}

	return fmt.Sprintf(
		`SELECT %s FROM "%s" WHERE %s GROUP BY %s ORDER BY group_count DESC LIMIT %d`,
		strings.Join(selectCols, ", "),
		stream,
		strings.Join(conditions, " AND "),
		strings.Join(groupCols, ", "),
		limit,
	)
}

// openObserveTimeRangeMicros normalizes the request window (milliseconds) to the
// microseconds OpenObserve's search API expects, defaulting to the last hour.
func openObserveTimeRangeMicros(startMs, endMs int64, now time.Time) (int64, int64) {
	if endMs <= 0 {
		endMs = now.UnixMilli()
	}
	if startMs <= 0 {
		startMs = now.Add(-time.Hour).UnixMilli()
	}
	return startMs * 1000, endMs * 1000
}

// openObserveNumber coerces a decoded JSON cell to float64.
func openObserveNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}

// openObserveLevelFromSeverityNumber maps an OTel severity number onto a level name.
// Rows are already filtered to errors, so anything unrecognised reads as "error".
func openObserveLevelFromSeverityNumber(v any) string {
	if n, ok := openObserveNumber(v); ok && n >= openObserveOtelFatalSeverityNumber {
		return "fatal"
	}
	return "error"
}

// convertOpenObserveLogGroups maps aggregated rows onto the LogGroup contract shared by
// every provider. Timestamps are emitted in epoch seconds — the frontend multiplies by 1000.
func convertOpenObserveLogGroups(hits []map[string]any, fallbackTimestampSec int64) LogGroupOutput {
	groups := make([]LogGroup, 0, len(hits))

	for _, hit := range hits {
		count, ok := openObserveNumber(hit["group_count"])
		if !ok {
			continue
		}

		sample, _ := hit["group_sample"].(string)
		if sample == "" {
			continue
		}

		namespace, _ := hit["group_namespace"].(string)
		pod, _ := hit["group_pod"].(string)
		container, _ := hit["group_container"].(string)
		level, _ := hit["group_level"].(string)
		if level == "" {
			// Fall back to the numeric OTel severity when the stream has no string one,
			// so FATAL is not flattened into ERROR.
			level = openObserveLevelFromSeverityNumber(hit["group_level_num"])
		}

		// _timestamp is microseconds; the LogGroup contract is epoch seconds.
		timestampSec := fallbackTimestampSec
		if lastTs, ok := openObserveNumber(hit["group_last_ts"]); ok && lastTs > 0 {
			timestampSec = int64(lastTs) / 1_000_000
		}

		// Prefer the workload the shipper recorded; fall back to deriving it from the pod
		// name for streams (or pods) that carry no workload column — e.g. a StatefulSet on
		// a stream that only records k8s_deployment_name.
		workload, _ := hit["group_workload"].(string)
		if workload == "" {
			workload = extractWorkloadFromPodName(pod)
		}

		group := LogGroup{
			Sample:      sample,
			Namespace:   namespace,
			Workload:    workload,
			Container:   container,
			Level:       level,
			Count:       int64(count),
			Timestamps:  []int64{timestampSec},
			Values:      []float64{count},
			PatternHash: generatePatternHash(sample),
		}

		// container_id mirrors the Prometheus format so the UI can parse namespace and
		// workload back out of a single field.
		if group.Namespace != "" && group.Workload != "" {
			if group.Container != "" {
				group.ContainerID = fmt.Sprintf("/k8s/%s/%s/%s", group.Namespace, group.Workload, group.Container)
			} else {
				group.ContainerID = fmt.Sprintf("/k8s/%s/%s", group.Namespace, group.Workload)
			}
		}

		groups = append(groups, group)
	}

	return LogGroupOutput{Groups: groups}
}

// QueryLogGroup fetches aggregated error-log patterns from OpenObserve, making
// OpenObserveLogSource satisfy LogGroupSource so log grouping is routed through the log
func (s *OpenObserveLogSource) QueryLogGroup(ctx *security.RequestContext, req FetchLogGroupRequest) (LogGroupOutput, error) {
	cfg, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("OpenObserveLogSource.QueryLogGroup: failed to get configs", "error", err)
		return LogGroupOutput{}, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}

	// The stream schema decides which columns the aggregation can reference; guessing
	// would make OpenObserve reject the whole query with a column-not-found error.
	fields, err := fetchOpenObserveStreamFields(cfg.URL, cfg.OrgID, cfg.Username, cfg.Password, cfg.LogStream)
	if err != nil {
		ctx.GetLogger().Error("OpenObserveLogSource.QueryLogGroup: failed to read stream schema", "error", err)
		return LogGroupOutput{}, err
	}

	cols := resolveOpenObserveLogGroupCols(fields)
	if cols.Message == "" {
		return LogGroupOutput{}, fmt.Errorf(
			"OpenObserve stream %q has no log message column — expected one of: %s",
			cfg.LogStream, strings.Join(openObserveLogGroupCandidates.Message, ", "))
	}

	sql := buildOpenObserveLogGroupSQL(
		cfg.LogStream,
		cols,
		common.GetString(req.Request, "selectedNamespace"),
		common.GetString(req.Request, "selectedWorkload"),
		openObserveLogGroupLimit,
	)

	startTimeMicros, endTimeMicros := openObserveTimeRangeMicros(req.StartTime, req.EndTime, time.Now())

	ctx.GetLogger().Info("OpenObserve Log Group Query", "query", sql)

	searchReq := openObserveSearchRequest{}
	searchReq.Query.SQL = sql
	searchReq.Query.StartTime = startTimeMicros
	searchReq.Query.EndTime = endTimeMicros

	payloadBytes, err := json.Marshal(searchReq)
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("failed to marshal log group request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/%s/_search", cfg.URL, cfg.OrgID)

	resp, err := common.HttpPost(endpoint,
		common.HttpWithHeaders(map[string]string{
			"Authorization": openObserveAuthHeader(cfg.Username, cfg.Password),
			"Content-Type":  "application/json",
		}),
		common.HttpWithBody(io.NopCloser(bytes.NewReader(payloadBytes))),
		common.HttpWithTimeout(60*time.Second),
	)
	if err != nil {
		ctx.GetLogger().Error("OpenObserveLogSource.QueryLogGroup: search request failed", "query", sql, "error", err)
		if strings.Contains(strings.ToLower(err.Error()), "timeout") {
			return LogGroupOutput{}, fmt.Errorf(
				"log group query timed out — the selected time range contains too many logs. " +
					"Please apply more filters: select a specific Namespace or Workload to narrow the scope",
			)
		}
		return LogGroupOutput{}, fmt.Errorf("OpenObserve log group request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return LogGroupOutput{}, fmt.Errorf("OpenObserve log group query failed with status %d: %s",
			resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	var searchResp openObserveSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return LogGroupOutput{}, fmt.Errorf("failed to decode OpenObserve response: %w", err)
	}

	// Groups with no MAX(_timestamp) fall back to the end of the query window.
	return convertOpenObserveLogGroups(searchResp.Hits, endTimeMicros/1_000_000), nil
}
