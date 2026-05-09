package observability

import (
	"fmt"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"strings"
	"time"
)

// SplunkLogSource implements LogSource for Splunk Observability Cloud Log Observer.
type SplunkLogSource struct{}

// splunkO11yLogLabelMapping maps standard Nudgebee field names to Splunk O11y / OTel field names.
var splunkO11yLogLabelMapping = map[string]string{
	"timestamp": "timestamp",
	"body":      "message",
	"message":   "message",
	"namespace": "kubernetes.namespace.name",
	"container": "kubernetes.container.name",
	"pod":       "kubernetes.pod.name",
	"node":      "kubernetes.node.name",
	"host":      "host.name",
	"hostname":  "host.name",
	"service":   "service.name",
	"level":     "severity",
	"severity":  "severity",
	"trace_id":  "trace_id",
	"span_id":   "span_id",
}

// QueryLogs fetches logs from Splunk O11y Log Observer.
func (s *SplunkLogSource) QueryLogs(ctx *security.RequestContext, req FetchLogRequest) ([]OutputLog, error) {
	cfg, err := integrations.GetSplunkO11yConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("SplunkLogSource.QueryLogs: failed to get configs", "error", err)
		return nil, fmt.Errorf("failed to get Splunk O11y configs: %w", err)
	}

	logQuery, err := s.buildLogObserverQuery(req)
	if err != nil {
		return nil, fmt.Errorf("failed to build Log Observer query: %w", err)
	}

	ctx.GetLogger().Info("Splunk O11y Log Query", "query", logQuery)

	startMs, endMs := normalizeTimeRangeMs(req.StartTime, req.EndTime)
	limit := req.Limit
	if limit <= 0 || limit > 2000 {
		limit = 1000
	}

	entries, err := integrations.ExecuteO11yLogSearch(cfg, logQuery, startMs, endMs, limit)
	if err != nil {
		ctx.GetLogger().Error("SplunkLogSource.QueryLogs: log search failed", "query", logQuery, "error", err)
		return nil, fmt.Errorf("failed to execute Log Observer query: %w", err)
	}

	return s.convertEntriesToOutputLogs(entries), nil
}

// QueryLabels returns available log label names from Splunk O11y.
// Returns the well-known OTel/Splunk O11y field names used in log attributes.
func (s *SplunkLogSource) QueryLabels(ctx *security.RequestContext, req FetchLogLabelRequest) ([]OutputLogLabel, error) {
	// Use the label mapping keys plus common additional O11y fields as the known label set.
	// A full dynamic implementation would call a catalog API; for now return the standard set.
	knownFields := []string{
		"message", "severity", "timestamp",
		"kubernetes.namespace.name", "kubernetes.pod.name", "kubernetes.container.name",
		"kubernetes.node.name", "host.name", "service.name",
		"trace_id", "span_id",
	}

	labels := make([]OutputLogLabel, 0, len(knownFields))
	for _, f := range knownFields {
		labels = append(labels, OutputLogLabel{
			Label:      f,
			Attributes: map[string]any{},
		})
	}
	return labels, nil
}

// QueryLabelValues returns distinct values for a specific log field.
func (s *SplunkLogSource) QueryLabelValues(ctx *security.RequestContext, req FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	cfg, err := integrations.GetSplunkO11yConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get Splunk O11y configs: %w", err)
	}

	fieldName := req.LabelName
	if mapped, ok := splunkO11yLogLabelMapping[fieldName]; ok {
		fieldName = mapped
	}
	if fieldName == "" {
		return nil, fmt.Errorf("invalid label name")
	}

	// Query a small set of recent logs and extract distinct values for the field.
	startMs, endMs := normalizeTimeRangeMs(req.StartTime, req.EndTime)
	entries, err := integrations.ExecuteO11yLogSearch(cfg, "", startMs, endMs, 500)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Splunk O11y log label values: %w", err)
	}

	seen := make(map[string]bool)
	var values []OutputLogLabelValue
	for _, e := range entries {
		if val, ok := e.Attributes[fieldName]; ok {
			str := fmt.Sprintf("%v", val)
			if str != "" && !seen[str] {
				seen[str] = true
				values = append(values, OutputLogLabelValue{
					Value:      str,
					Attributes: map[string]any{},
				})
			}
		}
		if len(values) >= 100 {
			break
		}
	}
	return values, nil
}

// GetQuery returns the Log Observer query string for the given request (for debug/display).
func (s *SplunkLogSource) GetQuery(ctx *security.RequestContext, req FetchLogRequest) (string, error) {
	return s.buildLogObserverQuery(req)
}

// GetLabelMapping returns the field name mapping for Splunk O11y.
func (s *SplunkLogSource) GetLabelMapping() map[string]string {
	return splunkO11yLogLabelMapping
}

func (s *SplunkLogSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_in", "_not_in", "_like"}
}

// buildLogObserverQuery builds a Lucene-style query for the Log Observer API.
func (s *SplunkLogSource) buildLogObserverQuery(req FetchLogRequest) (string, error) {
	// If the request carries a pre-built query string, use it directly.
	if req.Query != "" {
		return req.Query, nil
	}

	// Build a Lucene filter from the structured QueryRequest.Where clause.
	if hasWhereConditions(req.QueryRequest.Where) {
		return buildO11yWhereClause(req.QueryRequest.Where)
	}

	return "", nil
}

// convertEntriesToOutputLogs converts Log Observer entries to OutputLog format.
func (s *SplunkLogSource) convertEntriesToOutputLogs(entries []integrations.O11yLogEntry) []OutputLog {
	logs := make([]OutputLog, 0, len(entries))
	for _, e := range entries {
		log := OutputLog{
			Labels: make(map[string]any),
		}

		// Timestamp: Log Observer returns Unix milliseconds.
		if e.Timestamp > 0 {
			log.Timestamp = time.UnixMilli(e.Timestamp).UTC().Format(time.RFC3339Nano)
		}

		attrs := e.Attributes

		// Extract message/body
		if msg, ok := attrs["message"].(string); ok {
			log.Message = msg
		} else if msg, ok := attrs["body"].(string); ok {
			log.Message = msg
		}

		// Extract severity
		if sev, ok := attrs["severity"].(string); ok {
			log.Severity = sev
		} else if sev, ok := attrs["level"].(string); ok {
			log.Severity = sev
		} else {
			log.Severity = inferSeverityFromMessage(log.Message)
		}

		// Store all attributes as labels
		for k, v := range attrs {
			if k != "message" && k != "body" && k != "severity" && k != "level" {
				log.Labels[k] = v
			}
		}

		logs = append(logs, log)
	}
	return logs
}

// normalizeTimeRangeMs ensures timestamps are in milliseconds and fills in defaults.
func normalizeTimeRangeMs(startTime, endTime int64) (int64, int64) {
	// Convert seconds to milliseconds if needed
	if startTime > 0 && startTime < 1e12 {
		startTime = startTime * 1000
	}
	if endTime > 0 && endTime < 1e12 {
		endTime = endTime * 1000
	}
	if startTime == 0 {
		startTime = time.Now().Add(-1 * time.Hour).UnixMilli()
	}
	if endTime == 0 {
		endTime = time.Now().UnixMilli()
	}
	return startTime, endTime
}

// --- Lucene query builder ---

// buildO11yWhereClause converts a QueryWhereClause to a Lucene filter expression.
func buildO11yWhereClause(where query.QueryWhereClause) (string, error) {
	if len(where.Binary) > 0 {
		return buildO11yBinaryClause(where.Binary)
	}

	if len(where.And) > 0 {
		var parts []string
		for _, c := range where.And {
			part, err := buildO11yWhereClause(c)
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
			part, err := buildO11yWhereClause(c)
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
		notPart, err := buildO11yWhereClause(*where.Not)
		if err != nil {
			return "", err
		}
		if notPart != "" {
			return "NOT (" + notPart + ")", nil
		}
	}

	return "", nil
}

// buildO11yBinaryClause converts field comparisons to Lucene expressions.
func buildO11yBinaryClause(binary query.BinaryWhereClause) (string, error) {
	var parts []string
	for field, ops := range binary {
		// Map standard field name to O11y field name
		if mapped, ok := splunkO11yLogLabelMapping[field]; ok {
			field = mapped
		}
		for op, val := range ops {
			clause, err := buildO11yOperatorClause(field, op, val)
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

// buildO11yOperatorClause builds a single Lucene field comparison expression.
func buildO11yOperatorClause(field string, op query.BinaryWhereClauseType, val any) (string, error) {
	safeField := integrations.EscapeO11yQueryString(field)
	strVal := integrations.EscapeO11yFieldValue(fmt.Sprintf("%v", val))

	switch op {
	case query.Eq:
		return fmt.Sprintf("%s:%s", safeField, strVal), nil
	case query.Nq:
		return fmt.Sprintf("NOT %s:%s", safeField, strVal), nil
	case query.Gt:
		return fmt.Sprintf("%s:{%v TO *}", safeField, val), nil
	case query.Lt:
		return fmt.Sprintf("%s:{* TO %v}", safeField, val), nil
	case query.Gte:
		return fmt.Sprintf("%s:[%v TO *]", safeField, val), nil
	case query.Lte:
		return fmt.Sprintf("%s:[* TO %v]", safeField, val), nil
	case query.In:
		if arr, ok := val.([]any); ok {
			var terms []string
			for _, v := range arr {
				terms = append(terms, fmt.Sprintf("%s:%s", safeField, integrations.EscapeO11yFieldValue(fmt.Sprintf("%v", v))))
			}
			if len(terms) == 0 {
				return "", nil
			}
			return "(" + strings.Join(terms, " OR ") + ")", nil
		}
		return fmt.Sprintf("%s:%s", safeField, strVal), nil
	case query.NotIn:
		if arr, ok := val.([]any); ok {
			var terms []string
			for _, v := range arr {
				terms = append(terms, fmt.Sprintf("%s:%s", safeField, integrations.EscapeO11yFieldValue(fmt.Sprintf("%v", v))))
			}
			if len(terms) == 0 {
				return "", nil
			}
			return "NOT (" + strings.Join(terms, " OR ") + ")", nil
		}
		return fmt.Sprintf("NOT %s:%s", safeField, strVal), nil
	case query.Like:
		// Treat like as a wildcard prefix match
		return fmt.Sprintf("%s:%s*", safeField, integrations.EscapeO11yQueryString(fmt.Sprintf("%v", val))), nil
	default:
		return fmt.Sprintf("%s:%s", safeField, strVal), nil
	}
}
