package observability

import (
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"math"
	"nudgebee/services/common"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"strconv"
	"strings"
	"time"
)

// NewRelicLogSource implements LogSource interface for New Relic
type NewRelicLogSource struct{}

// nrqlBaseLogFilter excludes eBPF entity-metadata records (infrastructure metadata with no
// log message) and ensures only entries with actual log content are returned.
const nrqlBaseLogFilter = "recordType != 'eBPF-Entity-Metadata' AND message IS NOT NULL AND message != ''"

// nrFieldServiceName is the New Relic field set by the eBPF agent. It is only present on
// eBPF-Entity-Metadata records (no message). Actual Fluent Bit log records do not carry this
// field — they use pod_name instead.
const nrFieldServiceName = "service.name"

// newRelicLogLabelMapping maps standard field names to New Relic field names
// Note: New Relic K8s logs may use different field names depending on the ingestion method:
// - Fluent Bit/Fluentd: namespace_name, pod_name, container_name
// - New Relic K8s integration: k8s.namespace.name, k8s.pod.name, k8s.container.name
// This mapping uses the most common field names; queries should handle both variants
var newRelicLogLabelMapping = map[string]string{
	"timestamp": "timestamp",
	"body":      "message",
	"message":   "message",
	"namespace": "namespace_name",
	"container": "container_name",
	"pod":       "pod_name",
	"node":      "k8s.node.name",
	"host":      "hostname",
	"hostname":  "hostname",
	"service":   nrFieldServiceName,
	"app":       nrFieldServiceName,
	"level":     "level",
	"severity":  "newrelic.log.severity",
}

// QueryLogs fetches logs from New Relic using NRQL
func (s *NewRelicLogSource) QueryLogs(ctx *security.RequestContext, req FetchLogRequest) ([]OutputLog, error) {
	// Get New Relic configs
	apiKey, nrAccountId, region, err := integrations.GetNewRelicConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("NewRelicLogSource.QueryLogs: failed to get configs", "error", err)
		return nil, fmt.Errorf("failed to get New Relic configs: %w", err)
	}

	// Build NRQL query
	nrqlQuery, err := s.buildNRQLLogQuery(req)
	if err != nil {
		return nil, fmt.Errorf("failed to build NRQL log query: %w", err)
	}

	ctx.GetLogger().Info("NewRelic Log Query", "query", nrqlQuery)

	// Execute NRQL query
	results, err := integrations.ExecuteNRQL(apiKey, nrAccountId, region, nrqlQuery)
	if err != nil {
		ctx.GetLogger().Error("NewRelicLogSource.QueryLogs: NRQL query failed", "query", nrqlQuery, "error", err)
		return nil, fmt.Errorf("failed to execute NRQL log query: %w", err)
	}

	// Convert to OutputLog format
	return s.convertNRLogsToOutputLogs(results), nil
}

// QueryLabels returns available log labels from New Relic
func (s *NewRelicLogSource) QueryLabels(ctx *security.RequestContext, req FetchLogLabelRequest) ([]OutputLogLabel, error) {
	apiKey, nrAccountId, region, err := integrations.GetNewRelicConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("NewRelicLogSource.QueryLabels: failed to get configs", "error", err)
		return nil, fmt.Errorf("failed to get New Relic configs: %w", err)
	}

	// Use keyset() to get available attributes
	nrqlQuery := "SELECT keyset() FROM Log SINCE 1 day ago LIMIT 1"

	results, err := integrations.ExecuteNRQL(apiKey, nrAccountId, region, nrqlQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch log labels: %w", err)
	}

	var labels []OutputLogLabel
	for _, r := range results {
		if key, ok := r["key"].(string); ok {
			labels = append(labels, OutputLogLabel{
				Label:      key,
				Attributes: map[string]any{},
			})
		}
	}

	return labels, nil
}

// QueryLabelValues returns values for a specific label
func (s *NewRelicLogSource) QueryLabelValues(ctx *security.RequestContext, req FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	apiKey, nrAccountId, region, err := integrations.GetNewRelicConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("NewRelicLogSource.QueryLabelValues: failed to get configs", "error", err)
		return nil, fmt.Errorf("failed to get New Relic configs: %w", err)
	}

	// Map label name if needed
	labelName := req.LabelName
	if mapped, ok := newRelicLogLabelMapping[labelName]; ok {
		labelName = mapped
	}

	// Build time range
	startTime, endTime := s.getTimeRangeSeconds(req.StartTime, req.EndTime)

	// Use uniques() to get distinct values
	nrqlQuery := fmt.Sprintf("SELECT uniques(`%s`, 100) FROM Log SINCE %d UNTIL %d",
		labelName, startTime, endTime)

	results, err := integrations.ExecuteNRQL(apiKey, nrAccountId, region, nrqlQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch label values: %w", err)
	}

	var values []OutputLogLabelValue
	for _, r := range results {
		// The key in the result is "uniques.{labelName}"
		uniquesKey := fmt.Sprintf("uniques.%s", labelName)
		if uniques, ok := r[uniquesKey].([]any); ok {
			for _, val := range uniques {
				if valStr, ok := val.(string); ok {
					values = append(values, OutputLogLabelValue{
						Value:      valStr,
						Attributes: map[string]any{},
					})
				}
			}
		}
	}

	return values, nil
}

// GetQuery returns the complete NRQL query string for the request.
// Note: This only uses the structured QueryRequest.Where clause (not req.Query)
// to prevent NRQL injection vulnerabilities.
func (s *NewRelicLogSource) GetQuery(ctx *security.RequestContext, req FetchLogRequest) (string, error) {
	var sb strings.Builder
	sb.WriteString("SELECT * FROM Log")

	// Only use structured WHERE clause (properly escaped), never raw req.Query
	var userWhereClause string
	if hasWhereConditions(req.QueryRequest.Where) {
		var err error
		userWhereClause, err = buildNRQLWhereClause(req.QueryRequest.Where)
		if err != nil {
			return "", fmt.Errorf("failed to build WHERE clause: %w", err)
		}
	}

	// Always apply base filter to exclude eBPF metadata records and empty messages
	if userWhereClause != "" {
		fmt.Fprintf(&sb, " WHERE (%s) AND %s", userWhereClause, nrqlBaseLogFilter)
	} else {
		sb.WriteString(" WHERE ")
		sb.WriteString(nrqlBaseLogFilter)
	}

	// Add time range
	startTime, endTime := s.getTimeRangeSeconds(req.StartTime, req.EndTime)
	fmt.Fprintf(&sb, " SINCE %d UNTIL %d", startTime, endTime)

	// Add limit
	limit := req.Limit
	if limit <= 0 || limit > 2000 {
		limit = 1000
	}
	fmt.Fprintf(&sb, " LIMIT %d", limit)

	return sb.String(), nil
}

// GetLabelMapping returns the field name mapping
func (s *NewRelicLogSource) GetLabelMapping() map[string]string {
	return newRelicLogLabelMapping
}

func (s *NewRelicLogSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_like", "_ilike", "_contains"}
}

// GetIgnoredQueryRequestKeys returns keys that should be stripped from
// QueryRequest.Where before query execution. trace_id is not a filterable
// field in New Relic log queries and must be excluded.
func (s *NewRelicLogSource) GetIgnoredQueryRequestKeys() []string {
	return []string{"trace_id"}
}

func (s *NewRelicLogSource) CanGenerateQuery(ctx playbooks.PlaybookActionContext) bool {
	return ctx.GetEvent().SubjectName != "" &&
		getEventNamespace(ctx.GetEvent()) != ""
}

func (s *NewRelicLogSource) GenerateQuery(ctx playbooks.PlaybookActionContext) (string, map[string]any, error) {
	workloadName := escapeNRQLValue(ctx.GetEvent().SubjectName)
	namespace := escapeNRQLValue(getEventNamespace(ctx.GetEvent()))
	// NRQL WHERE clause — try both Fluent Bit and NR K8s integration field names
	query := fmt.Sprintf(
		"(`k8s.deployment.name` = '%s' OR `deployment` = '%s') AND (`k8s.namespace.name` = '%s' OR `namespace_name` = '%s')",
		workloadName, workloadName, namespace, namespace,
	)
	return query, map[string]any{}, nil
}

// buildNRQLLogQuery constructs the complete NRQL query for logs
func (s *NewRelicLogSource) buildNRQLLogQuery(req FetchLogRequest) (string, error) {
	// If req.Query is already a complete query (starts with SELECT), return it directly
	// This happens when GetQuery() is called and the result is set to req.Query
	if req.Query != "" && strings.HasPrefix(strings.TrimSpace(strings.ToUpper(req.Query)), "SELECT") {
		return req.Query, nil
	}

	var sb strings.Builder
	sb.WriteString("SELECT * FROM Log")

	// Add WHERE clause if present
	var whereClause string
	var err error

	if req.Query != "" {
		// Use provided query as WHERE clause (without SELECT/FROM)
		whereClause = req.Query
	} else if hasWhereConditions(req.QueryRequest.Where) {
		whereClause, err = buildNRQLWhereClause(req.QueryRequest.Where)
		if err != nil {
			return "", fmt.Errorf("failed to build WHERE clause: %w", err)
		}
	}

	// Always apply base filter to exclude eBPF metadata records and empty messages
	if whereClause != "" {
		fmt.Fprintf(&sb, " WHERE (%s) AND %s", whereClause, nrqlBaseLogFilter)
	} else {
		sb.WriteString(" WHERE ")
		sb.WriteString(nrqlBaseLogFilter)
	}

	// Add time range
	startTime, endTime := s.getTimeRangeSeconds(req.StartTime, req.EndTime)
	fmt.Fprintf(&sb, " SINCE %d UNTIL %d", startTime, endTime)

	// Add limit
	limit := req.Limit
	if limit <= 0 || limit > 2000 {
		limit = 1000
	}
	fmt.Fprintf(&sb, " LIMIT %d", limit)

	return sb.String(), nil
}

// getTimeRangeSeconds converts timestamps to seconds (NRQL uses epoch seconds)
func (s *NewRelicLogSource) getTimeRangeSeconds(startTime, endTime int64) (int64, int64) {
	// Convert from milliseconds to seconds if needed
	if startTime > 1e12 {
		startTime = startTime / 1000
	}
	if endTime > 1e12 {
		endTime = endTime / 1000
	}

	// Default to last hour if not specified
	if startTime == 0 {
		startTime = time.Now().Add(-1 * time.Hour).Unix()
	}
	if endTime == 0 {
		endTime = time.Now().Unix()
	}

	return startTime, endTime
}

// convertNRLogsToOutputLogs converts NRQL results to OutputLog format
func (s *NewRelicLogSource) convertNRLogsToOutputLogs(results []map[string]any) []OutputLog {
	logs := make([]OutputLog, 0, len(results))

	for _, r := range results {
		log := OutputLog{
			Labels: make(map[string]any),
		}

		// Extract timestamp
		if ts, ok := r["timestamp"].(float64); ok {
			log.Timestamp = time.UnixMilli(int64(ts)).Format(time.RFC3339Nano)
		}

		// Extract message
		if msg, ok := r["message"].(string); ok {
			log.Message = msg
		}

		// Extract severity/level
		if level, ok := r["level"].(string); ok {
			log.Severity = level
		} else if severity, ok := r["newrelic.log.severity"].(string); ok {
			log.Severity = severity
		} else if severity, ok := r["severity"].(string); ok {
			log.Severity = severity
		} else {
			// Infer severity from message content
			log.Severity = inferSeverityFromMessage(log.Message)
		}

		// Store all other attributes as labels
		for k, v := range r {
			if k != "timestamp" && k != "message" {
				log.Labels[k] = v
			}
		}

		logs = append(logs, log)
	}

	return logs
}

// buildNRQLWhereClause converts QueryWhereClause to NRQL WHERE syntax
func buildNRQLWhereClause(where query.QueryWhereClause) (string, error) {
	if len(where.Binary) > 0 {
		return buildNRQLBinaryClause(where.Binary)
	}

	if len(where.And) > 0 {
		var parts []string
		for _, c := range where.And {
			part, err := buildNRQLWhereClause(c)
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
			part, err := buildNRQLWhereClause(c)
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
		notPart, err := buildNRQLWhereClause(*where.Not)
		if err != nil {
			return "", err
		}
		if notPart != "" {
			return fmt.Sprintf("NOT (%s)", notPart), nil
		}
	}

	return "", nil
}

// buildNRQLBinaryClause handles individual field comparisons
func buildNRQLBinaryClause(binary query.BinaryWhereClause) (string, error) {
	var parts []string
	for field, ops := range binary {
		for op, val := range ops {
			clause, err := buildNRQLOperatorClause(field, op, val)
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

// buildNRQLOperatorClause builds a single NRQL operator clause
func buildNRQLOperatorClause(field string, op query.BinaryWhereClauseType, val any) (string, error) {
	// Handle backtick escaping for fields with special characters
	escapedField := escapeNRQLField(field)

	switch op {
	case query.Eq:
		// service.name is only set on eBPF-Entity-Metadata records (infrastructure metadata with
		// no log message). Actual Fluent Bit log records use pod_name with the deployment name as
		// a prefix (e.g., pod_name = "my-service-abc12-xyz89"). Translate to a LIKE match so the
		// query finds real log records instead of empty metadata entries.
		if field == nrFieldServiceName {
			return fmt.Sprintf("pod_name LIKE '%v%%'", escapeNRQLValue(val)), nil
		}
		// Some New Relic fields (e.g. http.response.status_code) are stored as numeric types.
		// When the value is a parseable number, use numeric comparison so that e.g. "200" matches
		// the stored float value 200.0.
		if numVal, isNum := toNumeric(val); isNum {
			return fmt.Sprintf("%s = %v", escapedField, numVal), nil
		}
		return fmt.Sprintf("%s = '%v'", escapedField, escapeNRQLValue(val)), nil
	case query.Nq:
		if numVal, isNum := toNumeric(val); isNum {
			return fmt.Sprintf("%s != %v", escapedField, numVal), nil
		}
		return fmt.Sprintf("%s != '%v'", escapedField, escapeNRQLValue(val)), nil
	case query.Gt:
		numVal, isNum := toNumeric(val)
		if isNum {
			return fmt.Sprintf("%s > %v", escapedField, numVal), nil
		}
		return fmt.Sprintf("%s > '%v'", escapedField, escapeNRQLValue(val)), nil
	case query.Lt:
		numVal, isNum := toNumeric(val)
		if isNum {
			return fmt.Sprintf("%s < %v", escapedField, numVal), nil
		}
		return fmt.Sprintf("%s < '%v'", escapedField, escapeNRQLValue(val)), nil
	case query.Gte:
		numVal, isNum := toNumeric(val)
		if isNum {
			return fmt.Sprintf("%s >= %v", escapedField, numVal), nil
		}
		return fmt.Sprintf("%s >= '%v'", escapedField, escapeNRQLValue(val)), nil
	case query.Lte:
		numVal, isNum := toNumeric(val)
		if isNum {
			return fmt.Sprintf("%s <= %v", escapedField, numVal), nil
		}
		return fmt.Sprintf("%s <= '%v'", escapedField, escapeNRQLValue(val)), nil
	case query.Like, query.ILike:
		return fmt.Sprintf("%s LIKE '%v'", escapedField, escapeNRQLValue(val)), nil
	case query.NLike:
		return fmt.Sprintf("%s NOT LIKE '%v'", escapedField, escapeNRQLValue(val)), nil
	case query.In:
		// Handle both array and single value for IN operator
		arr, ok := val.([]any)
		if !ok {
			// If not an array, treat as single value
			if numVal, isNum := toNumeric(val); isNum {
				return fmt.Sprintf("%s = %v", escapedField, numVal), nil
			}
			return fmt.Sprintf("%s = '%v'", escapedField, escapeNRQLValue(val)), nil
		}
		if len(arr) == 0 {
			return "", fmt.Errorf("IN operator requires non-empty array for field '%s'", field)
		}
		if len(arr) == 1 {
			// Single value: use equality instead of IN
			if numVal, isNum := toNumeric(arr[0]); isNum {
				return fmt.Sprintf("%s = %v", escapedField, numVal), nil
			}
			return fmt.Sprintf("%s = '%v'", escapedField, escapeNRQLValue(arr[0])), nil
		}
		var strVals []string
		for _, v := range arr {
			if numVal, isNum := toNumeric(v); isNum {
				strVals = append(strVals, fmt.Sprintf("%v", numVal))
			} else {
				strVals = append(strVals, fmt.Sprintf("'%v'", escapeNRQLValue(v)))
			}
		}
		return fmt.Sprintf("%s IN (%s)", escapedField, strings.Join(strVals, ", ")), nil
	case query.NotIn:
		arr, ok := val.([]any)
		if !ok {
			return "", fmt.Errorf("NOT IN operator requires array value for field '%s'", field)
		}
		var strVals []string
		for _, v := range arr {
			if numVal, isNum := toNumeric(v); isNum {
				strVals = append(strVals, fmt.Sprintf("%v", numVal))
			} else {
				strVals = append(strVals, fmt.Sprintf("'%v'", escapeNRQLValue(v)))
			}
		}
		return fmt.Sprintf("%s NOT IN (%s)", escapedField, strings.Join(strVals, ", ")), nil
	case query.Contains:
		return fmt.Sprintf("%s LIKE '%%%v%%'", escapedField, escapeNRQLValue(val)), nil
	case query.IContains:
		// Case-insensitive substring match using LOWER()
		// NRQL: LOWER(field) LIKE '%value%'
		// Performance: Good with proper indexing on lowercase fields
		if val == nil || fmt.Sprintf("%v", val) == "" {
			// Empty string or nil: skip this condition (return empty to be filtered out later)
			return "", nil
		}
		valueLower := strings.ToLower(escapeNRQLValue(val))
		return fmt.Sprintf("LOWER(%s) LIKE '%%%s%%'", escapedField, valueLower), nil
	case query.NIContains:
		// Negated case-insensitive substring match
		// NRQL: LOWER(field) NOT LIKE '%value%'
		if val == nil || fmt.Sprintf("%v", val) == "" {
			// Empty string or nil: skip this condition (return empty to be filtered out later)
			return "", nil
		}
		valueLower := strings.ToLower(escapeNRQLValue(val))
		return fmt.Sprintf("LOWER(%s) NOT LIKE '%%%s%%'", escapedField, valueLower), nil
	case query.HasKey:
		return fmt.Sprintf("%s IS NOT NULL", escapedField), nil
	case query.IsNull:
		if isNull, ok := val.(bool); ok && isNull {
			return fmt.Sprintf("%s IS NULL", escapedField), nil
		}
		return fmt.Sprintf("%s IS NOT NULL", escapedField), nil
	case query.Between:
		// Between expects a map with _gte/_lte or _gt/_lt keys
		betweenMap, ok := val.(map[string]any)
		if !ok {
			// Fallback: try array format [min, max]
			if arr, ok := val.([]any); ok && len(arr) == 2 {
				return fmt.Sprintf("%s >= %v AND %s <= %v", escapedField, arr[0], escapedField, arr[1]), nil
			}
			return "", fmt.Errorf("BETWEEN operator requires map with _gte/_lte keys or [min, max] array for field '%s'", field)
		}

		var parts []string
		for k, v := range betweenMap {
			// Convert ISO8601 timestamp strings to epoch milliseconds for NRQL
			var numericVal any
			if strVal, ok := v.(string); ok {
				if t, err := time.Parse(time.RFC3339, strVal); err == nil {
					numericVal = t.UnixMilli()
				} else {
					numericVal = v
				}
			} else {
				numericVal = v
			}

			switch k {
			case "_gte":
				parts = append(parts, fmt.Sprintf("%s >= %v", escapedField, numericVal))
			case "_gt":
				parts = append(parts, fmt.Sprintf("%s > %v", escapedField, numericVal))
			case "_lte":
				parts = append(parts, fmt.Sprintf("%s <= %v", escapedField, numericVal))
			case "_lt":
				parts = append(parts, fmt.Sprintf("%s < %v", escapedField, numericVal))
			}
		}

		if len(parts) == 0 {
			return "", fmt.Errorf("BETWEEN operator has no valid comparison keys (_gte/_lte/_gt/_lt) for field '%s'", field)
		}
		return strings.Join(parts, " AND "), nil
	default:
		// Handle unknown operators gracefully
		return fmt.Sprintf("%s = '%v'", escapedField, escapeNRQLValue(val)), nil
	}
}

// escapeNRQLField escapes field names that contain special characters.
// Anything that isn't a simple identifier ([A-Za-z_][A-Za-z0-9_]*) is wrapped
// in backticks. This covers dotted fields (k8s.pod.name), HTTP/2 pseudo-headers
// (:authority, :method), fields starting with digits, and any other attribute
// name New Relic's keyset() may surface.
func escapeNRQLField(field string) string {
	if isSimpleNRQLIdentifier(field) {
		return field
	}
	// Escape any backticks in the field name to prevent injection
	escapedField := strings.ReplaceAll(field, "`", "``")
	return fmt.Sprintf("`%s`", escapedField)
}

func isSimpleNRQLIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// escapeNRQLValue escapes backslashes and single quotes in values to prevent injection
func escapeNRQLValue(val any) string {
	str := fmt.Sprintf("%v", val)
	// Escape backslashes first, then single quotes (order matters!)
	str = strings.ReplaceAll(str, "\\", "\\\\")
	str = strings.ReplaceAll(str, "'", "\\'")
	return str
}

// isNumeric checks if a value is numeric
func isNumeric(val any) bool {
	switch val.(type) {
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return true
	default:
		return false
	}
}

// toNumeric tries to convert a value to numeric type
// Returns the numeric value and true if successful, otherwise returns original value and false
func toNumeric(val any) (any, bool) {
	// If already numeric, return as-is
	if isNumeric(val) {
		return val, true
	}

	// Try to parse string as float64
	if strVal, ok := val.(string); ok {
		if numVal, err := strconv.ParseFloat(strVal, 64); err == nil {
			return numVal, true
		}
	}

	return val, false
}

// hasWhereConditions checks if a QueryWhereClause has any conditions
func hasWhereConditions(where query.QueryWhereClause) bool {
	return len(where.Binary) > 0 || len(where.And) > 0 || len(where.Or) > 0 || where.Not != nil
}

// inferSeverityFromMessage attempts to infer log severity from message content
func inferSeverityFromMessage(message string) string {
	msgLower := strings.ToLower(message)
	switch {
	case strings.Contains(msgLower, "error") || strings.Contains(msgLower, "exception") || strings.Contains(msgLower, "fatal"):
		return "error"
	case strings.Contains(msgLower, "warn"):
		return "warn"
	case strings.Contains(msgLower, "debug"):
		return "debug"
	case strings.Contains(msgLower, "trace"):
		return "trace"
	default:
		return "info"
	}
}

// QueryLogGroup fetches log group aggregations from New Relic using NRQL.
// This makes NewRelicLogSource implement the LogGroupSource interface,
// so log grouping is routed through the log provider directly.
func (s *NewRelicLogSource) QueryLogGroup(ctx *security.RequestContext, req FetchLogGroupRequest) (LogGroupOutput, error) {
	// Get New Relic configs
	apiKey, nrAccountId, region, err := integrations.GetNewRelicConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("NewRelicLogGroupSource.QueryLogGroup: failed to get configs", "error", err)
		return LogGroupOutput{}, fmt.Errorf("failed to get New Relic configs: %w", err)
	}

	// Build WHERE clause from conditions slice for consistency
	// Error detection: check both 'level' field and message content patterns
	// Many K8s logs (e.g., from Fluent Bit) don't have a level field but contain error info in message
	conditions := []string{
		// Level filter OR message-based error detection (handles both NULL and empty string level)
		"(level IN ('error', 'critical', 'ERROR', 'CRITICAL', 'Error', 'Critical', 'fatal', 'FATAL') OR ((level IS NULL OR level = '') AND (message LIKE '%ERROR%' OR message LIKE '%FATAL%' OR message LIKE '%CRITICAL%')))",
		// Exclude system containers (always applied)
		"container_name NOT IN ('prometheus', 'grafana', 'nudgebee-agent')",
	}

	// Apply optional namespace/workload filters
	selectedNamespace := common.GetString(req.Request, "selectedNamespace")
	selectedWorkload := common.GetString(req.Request, "selectedWorkload")

	if selectedNamespace != "" {
		// Filter by namespace - support both Fluent Bit (namespace_name) and NR K8s integration (k8s.namespace.name) field names
		conditions = append(conditions, fmt.Sprintf("(namespace_name = '%s' OR `k8s.namespace.name` = '%s')",
			escapeNRQLValue(selectedNamespace), escapeNRQLValue(selectedNamespace)))
	}

	if selectedWorkload != "" {
		// Filter by workload - match container_name or pod_name (support both naming conventions)
		conditions = append(conditions, fmt.Sprintf("(container_name LIKE '%s%%' OR pod_name LIKE '%s%%' OR `k8s.pod.name` LIKE '%s%%')",
			escapeNRQLValue(selectedWorkload), escapeNRQLValue(selectedWorkload), escapeNRQLValue(selectedWorkload)))
	}

	// Build complete NRQL query with all grouping fields in FACET
	// Extract workload name from pod_name using regex to handle Deployment/StatefulSet/DaemonSet naming patterns
	// Pod naming: {workload}-{replica-hash}-{pod-hash} or {workload}-{pod-hash}
	// FACET order: message, namespace_name, workload_name, container_name
	startTime, endTime := s.getTimeRangeSeconds(req.StartTime, req.EndTime)
	nrqlQuery := fmt.Sprintf("SELECT count(*) as value FROM Log WHERE %s FACET message, namespace_name, capture(pod_name, r'^(?P<workload>.+?)(-[a-z0-9]{5,10})?-[a-z0-9]{5}$') as workload_name, container_name SINCE %d UNTIL %d LIMIT 100",
		strings.Join(conditions, " AND "), startTime, endTime)

	ctx.GetLogger().Info("NewRelic Log Group Query", "query", nrqlQuery)

	// Execute NRQL query
	results, err := integrations.ExecuteNRQL(apiKey, nrAccountId, region, nrqlQuery)
	if err != nil {
		ctx.GetLogger().Error("NewRelicLogGroupSource.QueryLogGroup: NRQL query failed", "query", nrqlQuery, "error", err)
		if strings.Contains(strings.ToLower(err.Error()), "timeout") {
			return LogGroupOutput{}, fmt.Errorf(
				"log group query timed out — the selected time range contains too many logs. " +
					"Please apply more filters: select a specific Namespace or Workload to narrow the scope",
			)
		}
		return LogGroupOutput{}, fmt.Errorf("failed to execute NRQL log group query: %w", err)
	}

	// Convert results to LogGroupOutput format
	// Use endTime as the timestamp since the data represents aggregations up to that point
	output := s.convertToLogGroupOutput(results, endTime)

	return output, nil
}

// extractStringField extracts a string value from a map, trying multiple field names in order
func extractStringField(data map[string]any, fieldNames ...string) string {
	for _, name := range fieldNames {
		if val, ok := data[name].(string); ok && val != "" {
			return val
		}
	}
	return ""
}

// extractCountValue extracts count/value from NRQL result
func extractCountValue(data map[string]any) (float64, bool) {
	if count, ok := data["count"].(float64); ok {
		return count, true
	}
	if value, ok := data["value"].(float64); ok {
		return value, true
	}
	return 0, false
}

// extractFirstNonEmpty returns the first non-empty string from a slice
func extractFirstNonEmpty(values []any) string {
	for _, v := range values {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// generatePatternHash creates a unique hash from log message for pattern grouping
// Uses SHA1 hash truncated to 14 characters (similar to Prometheus pattern_hash format)
func generatePatternHash(message string) string {
	if message == "" {
		return ""
	}

	// Create SHA1 hash of the message
	h := sha1.New()
	h.Write([]byte(message))
	hashBytes := h.Sum(nil)

	// Encode to base64 URL encoding (alphanumeric, URL-safe) and truncate to 14 chars
	// This matches the typical pattern_hash format: "Q2Q2H95LZ1JJY9"
	encoded := base64.RawURLEncoding.EncodeToString(hashBytes)

	// Take first 14 characters for consistency
	if len(encoded) > 14 {
		return encoded[:14]
	}
	return encoded
}

// convertToLogGroupOutput converts NRQL faceted results to LogGroupOutput format
func (s *NewRelicLogSource) convertToLogGroupOutput(results []map[string]any, timestamp int64) LogGroupOutput {
	groups := make([]LogGroup, 0, len(results))

	for _, r := range results {
		// Extract the count value
		count, ok := extractCountValue(r)
		if !ok {
			continue
		}

		group := LogGroup{
			Timestamps: []int64{timestamp},
			Values:     []float64{count},
			Count:      int64(math.Round(count)),
		}

		// Handle multi-field FACET results (returns array in "facet" key)
		// FACET order: message, namespace_name, workload_name, container_name
		if facetArr, ok := r["facet"].([]any); ok && len(facetArr) >= 4 {
			if message, ok := facetArr[0].(string); ok {
				group.Sample = message
			}
			if ns, ok := facetArr[1].(string); ok {
				group.Namespace = ns
			}
			if workload, ok := facetArr[2].(string); ok {
				group.Workload = workload
			}
			if container, ok := facetArr[3].(string); ok {
				group.Container = container
			}
		} else {
			// Fallback: try named fields (for single FACET or different result format)
			group.Sample = extractStringField(r, "message", "sample")
			group.Namespace = extractStringField(r, "namespace_name", "namespace")
			group.Workload = extractStringField(r, "workload_name", "workload")
			group.Container = extractStringField(r, "container_name", "container")
		}

		// Build container_id similar to Prometheus format for compatibility
		if group.Namespace != "" && group.Workload != "" {
			if group.Container != "" {
				group.ContainerID = fmt.Sprintf("/k8s/%s/%s/%s", group.Namespace, group.Workload, group.Container)
			} else {
				group.ContainerID = fmt.Sprintf("/k8s/%s/%s", group.Namespace, group.Workload)
			}
		}

		// Generate pattern_hash from message for ticket reference linking
		if group.Sample != "" {
			group.PatternHash = generatePatternHash(group.Sample)
		}

		groups = append(groups, group)
	}

	return LogGroupOutput{Groups: groups}
}
