package observability

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/integrations"
	"nudgebee/services/internal/database"
	"nudgebee/services/query"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nudgebee/logparser"
	"github.com/samber/lo"
)

func init() {
	playbooks.RegisterAction("signoz_logs_enricher", &signozLogsAction{})
	playbooks.RegisterAction("logs", &observabilityLogAction{})
	playbooks.RegisterAction("datadog_logs", &datadogLogsAction{})
}

// Get top 2 error logs based on severity
const actionLogMaxInsightErrors = 10
const logSummaryMaxPatterns = 10

// computeLogSummary runs Drain3 pattern clustering and level detection on log data.
// Returns a map suitable for additional_info containing level_breakdown and log_patterns.
// Expects logs as []map[string]any with "body" and optional "severity_text" keys.
func computeLogSummary(logs []map[string]any) map[string]any {
	if len(logs) == 0 {
		return nil
	}

	// Collect message bodies
	var allBodies []string
	for _, logData := range logs {
		body, _ := logData["body"].(string)
		if body != "" {
			allBodies = append(allBodies, body)
		}
	}

	if len(allBodies) == 0 {
		return nil
	}

	// Level breakdown
	levelCounts := map[string]int{}
	for _, logData := range logs {
		// Use severity_text if available
		if severity, ok := logData["severity_text"].(string); ok && severity != "" {
			levelCounts[strings.ToLower(severity)]++
			continue
		}
		// Fall back to logparser level detection
		body, _ := logData["body"].(string)
		if body != "" {
			level := logparser.GuessLevel(body)
			if level != logparser.LevelUnknown {
				levelCounts[level.String()]++
			} else {
				levelCounts["unknown"]++
			}
		}
	}

	// Extract top patterns using Drain3
	patterns := logparser.ExtractPatterns(allBodies, logSummaryMaxPatterns)
	patternList := make([]map[string]any, 0, len(patterns))
	for _, p := range patterns {
		patternList = append(patternList, map[string]any{
			"template":   p.Template,
			"count":      p.Count,
			"percentage": p.Percentage,
			"example":    p.Example,
		})
	}

	summary := map[string]any{
		"total_lines":     len(allBodies),
		"level_breakdown": levelCounts,
		"log_patterns":    patternList,
	}

	return summary
}

// computeLogSummaryFromText is a convenience wrapper that converts raw text (newline-separated)
// into the []map[string]any format expected by computeLogSummary.
func computeLogSummaryFromText(text string) map[string]any {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return nil
	}
	logs := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			logs = append(logs, map[string]any{"body": line})
		}
	}
	return computeLogSummary(logs)
}

type RegexLabelExtractor struct {
	Pattern   string `json:"pattern"`
	LabelName string `json:"label_name"`
}

type LabelExtractor struct {
	LabelName       string `json:"label_name"`
	PlaceHolderName string `json:"placeholder_name,omitempty"` // optional, if not provided, use label_name
}

type PlaybookQueryGenerator interface {
	CanGenerateQuery(ctx playbooks.PlaybookActionContext) bool
	GenerateQuery(ctx playbooks.PlaybookActionContext) (string, map[string]any, error)
}

// actionLogCompileRegexPatterns compiles regex patterns and returns valid ones
func actionLogCompileRegexPatterns(extractors []RegexLabelExtractor) map[string]*regexp.Regexp {
	compiledPatterns := make(map[string]*regexp.Regexp)
	for _, extractor := range extractors {
		if compiled, err := regexp.Compile(extractor.Pattern); err == nil {
			compiledPatterns[extractor.LabelName] = compiled
		} else {
			slog.Error("logsEnricherAction: invalid regex pattern",
				"pattern", extractor.Pattern,
				"label_name", extractor.LabelName,
				"error", err)
		}
	}
	return compiledPatterns
}

// actionLogApplyRegexExtractors applies regex patterns to extract labels from log body.
// Collects all unique values for each label.
func actionLogApplyRegexExtractors(body string, compiledPatterns map[string]*regexp.Regexp, extractors []RegexLabelExtractor, extractedLabels map[string]any) {
	if len(compiledPatterns) == 0 {
		return
	}

	for _, extractor := range extractors {
		pattern, exists := compiledPatterns[extractor.LabelName]
		if !exists {
			continue
		}

		matches := pattern.FindStringSubmatch(body)
		var val string
		if len(matches) > 1 {
			val = matches[1]
		} else if len(matches) == 1 {
			val = matches[0]
		} else {
			continue
		}

		existing, hasLabel := extractedLabels[extractor.LabelName]
		if !hasLabel {
			extractedLabels[extractor.LabelName] = []any{val}
			continue
		}

		// Regex matches are always strings — use direct comparison instead of reflect.DeepEqual.
		valArray := existing.([]any)
		alreadyExists := false
		for _, existingVal := range valArray {
			if s, ok := existingVal.(string); ok && s == val {
				alreadyExists = true
				break
			}
		}
		if !alreadyExists {
			extractedLabels[extractor.LabelName] = append(valArray, val)
		}
	}
}

// actionLogApplyLabelExtractors extracts labels from log data fields
// Now collects ALL unique values for each label, not just the first one
func actionLogApplyLabelExtractors(logData map[string]any, labelExtractor []LabelExtractor, extractedLabels map[string]any) {
	for _, le := range labelExtractor {
		placeholder := le.PlaceHolderName
		if placeholder == "" {
			placeholder = le.LabelName
		}

		var val any
		var found bool

		// First try direct access
		if v, ok := logData[placeholder]; ok && v != "" {
			val = v
			found = true
		} else if resourcesString, ok := logData["resources_string"].(map[string]any); ok {
			// Then try nested in resources_string
			if v, ok := resourcesString[le.LabelName]; ok && v != "" {
				val = v
				found = true
			}
		}

		if found {
			// Initialize array if this is the first value for this label
			if _, exists := extractedLabels[le.LabelName]; !exists {
				extractedLabels[le.LabelName] = []any{}
			}

			// Add value to array if it's not already present (collect unique values)
			// Use DeepEqual to handle complex types like maps and slices
			valArray := extractedLabels[le.LabelName].([]any)
			alreadyExists := false
			for _, existingVal := range valArray {
				if reflect.DeepEqual(existingVal, val) {
					alreadyExists = true
					break
				}
			}
			if !alreadyExists {
				extractedLabels[le.LabelName] = append(valArray, val)
			}
		}
	}
}

// actionLogExtractionResults logs the results for debugging
func actionLogExtractionResults(extractedLabels map[string]any, compiledPatternsCount int) {
	if len(extractedLabels) > 0 {
		// Count total values across all labels
		valueCounts := make(map[string]int)
		for labelName, values := range extractedLabels {
			if valArray, ok := values.([]any); ok {
				valueCounts[labelName] = len(valArray)
			} else {
				// Fallback for non-array values (shouldn't happen with new implementation)
				valueCounts[labelName] = 1
			}
		}
		slog.Debug("signozLogsEnricherAction: labels extracted successfully",
			"extracted_label_count", len(extractedLabels),
			"value_counts", valueCounts)
	} else {
		slog.Debug("signozLogsEnricherAction: no labels extracted",
			"compiled_patterns", compiledPatternsCount)
	}
}

func actionLogExtractErrorPatterns(logs []map[string]any, maxErrors int) []playbooks.PlaybookActionResponseInsight {
	// Filter logs that contain error indicators
	errorPatterns := []string{
		"exception",
		"error",
		"fatal",
	}

	// Create streaming pattern extractor (memory-efficient)
	extractor, err := logparser.NewPatternExtractor()
	if err != nil {
		return []playbooks.PlaybookActionResponseInsight{}
	}

	// Map to store severity for example logs
	errorMetadata := make(map[string]string) // Map from body to severity

	// Stream logs one at a time (memory-efficient)
	for _, logData := range logs {
		body, _ := logData["body"].(string)
		bodyLower := strings.ToLower(body)

		// Check if log contains any error pattern in the body
		hasError := false
		if body != "" {
			for _, pattern := range errorPatterns {
				if strings.Contains(bodyLower, pattern) {
					hasError = true
					break
				}
			}
		}

		// Check for API failures (statusCode >= 500) even if body is empty
		isAPIFailure := false
		var apiFailureMessage string
		if labels, ok := logData["labels"].(map[string]any); ok {
			// Check for statusCode in labels
			var statusCode int
			if sc, ok := labels["statusCode"].(float64); ok {
				statusCode = int(sc)
			} else if sc, ok := labels["statusCode"].(int); ok {
				statusCode = sc
			}

			// Check for statusCode in FIELDS
			if statusCode == 0 {
				if fields, ok := labels["FIELDS"].(map[string]any); ok {
					if sc, ok := fields["statusCode"].(float64); ok {
						statusCode = int(sc)
					} else if sc, ok := fields["statusCode"].(int); ok {
						statusCode = sc
					}
				}
			}

			// Treat 5xx status codes as errors
			if statusCode >= 500 {
				isAPIFailure = true
				// Build meaningful error message from API log metadata
				endpoint := ""
				httpMethod := ""
				requestURI := ""

				if fields, ok := labels["FIELDS"].(map[string]any); ok {
					endpoint, _ = fields["endpoint"].(string)
					httpMethod, _ = fields["httpMethod"].(string)
					requestURI, _ = fields["requestURI"].(string)
				}
				if endpoint == "" {
					endpoint, _ = labels["endpoint"].(string)
				}
				if httpMethod == "" {
					httpMethod, _ = labels["httpMethod"].(string)
				}
				if requestURI == "" {
					requestURI, _ = labels["requestURI"].(string)
				}

				apiFailureMessage = fmt.Sprintf("API Request Failed | %s %s | Status: %d | Endpoint: %s", httpMethod, requestURI, statusCode, endpoint)
			}
		}

		if hasError || isAPIFailure {
			// Build full error message with stack trace for better pattern matching
			fullErrorMessage := body
			if isAPIFailure && body == "" {
				fullErrorMessage = apiFailureMessage
			}

			// Try to get stack trace from labels.exception_stack_trace or labels.FIELDS.thrown.extendedStackTrace
			if labels, ok := logData["labels"].(map[string]any); ok {
				// First try exception_stack_trace (top-level extracted field)
				if stackTrace, ok := labels["exception_stack_trace"].(string); ok && stackTrace != "" {
					fullErrorMessage = fullErrorMessage + "\n" + stackTrace
				} else if fields, ok := labels["FIELDS"].(map[string]any); ok {
					// Try FIELDS.thrown.extendedStackTrace
					if thrown, ok := fields["thrown"].(map[string]any); ok {
						if extStackTrace, ok := thrown["extendedStackTrace"].(string); ok && extStackTrace != "" {
							fullErrorMessage = fullErrorMessage + "\n" + extStackTrace
						}
					}
				}
			}

			// Process log without buffering
			if fullErrorMessage != "" {
				if err := extractor.AddLog(fullErrorMessage); err == nil {
					// Store severity for this log
					severityText, _ := logData["severity_text"].(string)
					if severityText == "" {
						if isAPIFailure {
							severityText = "ERROR"
						} else {
							severityText = "ERROR"
						}
					}
					errorMetadata[fullErrorMessage] = severityText
				}
			}
		}
	}

	// Extract patterns from processed logs
	patterns := extractor.GetPatterns(maxErrors)

	// Convert patterns to insights
	insights := make([]playbooks.PlaybookActionResponseInsight, 0, len(patterns))
	for _, pattern := range patterns {
		// Get severity from the example log
		severity := errorMetadata[pattern.Example]
		if severity == "" {
			severity = "ERROR"
		}

		// Include full example error with complete stack trace
		insights = append(insights, playbooks.PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("%s: %s (occurred %d times, %.1f%%)", severity, pattern.Example, pattern.Count, pattern.Percentage),
			Severity: "High",
		})
	}

	return insights
}

// signozLogsAction is the action for signoz_logs_enricher.
type signozLogsAction struct{}
type signozLogsParams struct {
	Query           any                   `json:"query,omitempty"`
	Start           string                `json:"start,omitempty"`
	End             string                `json:"end,omitempty"`
	RawQuery        map[string]any        `json:"raw_query,omitempty"`
	Duration        int                   `json:"duration,omitempty"`
	RegexExtractors []RegexLabelExtractor `json:"regex_extractors,omitempty"`
	LabelExtractors []LabelExtractor      `json:"label_extractors,omitempty"`
}

func (a *signozLogsAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	securityCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)

	provider, err := GetDefaultProvider(securityCtx, ctx.GetAccountId(), "logs", "")
	if err != nil {
		securityCtx.GetLogger().Error("signozLogsEnricherAction: unable to get default log provider", "error", err)
		return false
	}
	if provider.Provider != "signoz" {
		return false
	}

	if ctx.GetEvent().Labels == nil {
		return false
	}

	if ctx.GetEvent().Labels["pod"] != "" && getEventNamespace(ctx.GetEvent()) != "" {
		return true
	}

	if ctx.GetEvent().Labels["trace_id"] != "" {
		return true
	}

	if ctx.GetEvent().Labels["relatedLogsParams"] != "" {
		return true
	}

	if ctx.GetEvent().Labels["service_name"] != "" || ctx.GetEvent().Labels["service"] != "" {
		return true
	}

	// Fallback: query by workload name + namespace
	if ctx.GetEvent().SubjectName != "" && getEventNamespace(ctx.GetEvent()) != "" {
		return true
	}

	return false
}

func (a *signozLogsAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	params := map[string]any{}
	namespace := getEventNamespace(ctx.GetEvent())
	if ctx.GetEvent().Labels["pod"] != "" && namespace != "" {
		params = map[string]any{
			"query": map[string]any{
				"op": "AND",
				"items": []map[string]any{
					{
						"value": ctx.GetEvent().Labels["pod"],
						"op":    "=",
						"key": map[string]any{
							"key": "k8s.pod.name",
						},
					},
					{
						"value": namespace,
						"op":    "=",
						"key": map[string]any{
							"key": "k8s.namespace.name",
						},
					},
				},
			},
			"title": "Pod Logs For - " + ctx.GetEvent().Labels["pod"],
		}
	} else if ctx.GetEvent().Labels["trace_id"] != "" {
		params = map[string]any{
			"query": map[string]any{
				"op": "AND",
				"items": []map[string]any{
					{
						"value": ctx.GetEvent().Labels["trace_id"],
						"op":    "=",
						"key": map[string]any{
							"key": "trace_id",
						},
					},
				},
			},
			"title": "Trace Logs For - " + ctx.GetEvent().Labels["trace_id"],
		}
	} else if ctx.GetEvent().Labels["relatedLogsParams"] != "" {
		parsedQuery, err := a.parseRelatedLogsParams(ctx.GetEvent().Labels["relatedLogsParams"])
		if err != nil {
			return nil, err
		}
		params["title"] = "Related Logs"
		params["raw_query"] = parsedQuery
	} else if ctx.GetEvent().Labels["service_name"] != "" || ctx.GetEvent().Labels["service"] != "" {
		service_name := ctx.GetEvent().Labels["service_name"]
		if service_name == "" {
			service_name = ctx.GetEvent().Labels["service"]
		}
		params = map[string]any{
			"query": map[string]any{
				"op": "AND",
				"items": []map[string]any{
					{
						"value": service_name,
						"op":    "=",
						"key": map[string]any{
							"key": "service.name",
						},
					},
				},
			},
			"title": "Service Logs For - " + ctx.GetEvent().Labels["service_name"],
		}
	} else if ctx.GetEvent().SubjectName != "" && namespace != "" {
		workloadName := ctx.GetEvent().SubjectName
		params = map[string]any{
			"query": map[string]any{
				"op": "AND",
				"items": []map[string]any{
					{
						"value": workloadName,
						"op":    "contains",
						"key": map[string]any{
							"key": "k8s.deployment.name",
						},
					},
					{
						"value": namespace,
						"op":    "=",
						"key": map[string]any{
							"key": "k8s.namespace.name",
						},
					},
				},
			},
			"title": "Workload Logs For - " + workloadName,
		}
	}
	return a.Execute(ctx, params)
}

// parseRelatedLogsParams extracts and transforms related logs parameters
func (a *signozLogsAction) parseRelatedLogsParams(relatedLogsParams string) (map[string]any, error) {
	var rawQuery map[string]any
	if err := common.UnmarshalJsonString(relatedLogsParams, &rawQuery); err != nil {
		slog.Error("signozLogsEnricherAction: invalid relatedLogsParams json", "error", err, "relatedLogsParams", relatedLogsParams)
		return nil, errors.New("signozLogsEnricherAction: invalid relatedLogsParams json")
	}

	parsedQuery := map[string]any{}

	// Handle composite query
	if rawQuery["compositeQuery"] != nil {
		compositeQuery, err := a.parseCompositeQuery(rawQuery["compositeQuery"])
		if err != nil {
			return nil, err
		}
		parsedQuery["compositeQuery"] = compositeQuery
	}

	// Handle time range
	if timerange := rawQuery["timeRange"]; timerange != nil {
		timerangeMap, err := a.parseTimeRange(timerange)
		if err != nil {
			return nil, err
		}
		parsedQuery["start"] = timerangeMap["start"]
		parsedQuery["end"] = timerangeMap["end"]
		parsedQuery["step"] = 60
		parsedQuery["variables"] = map[string]any{}
	}

	return parsedQuery, nil
}

// parseCompositeQuery handles composite query parsing with proper error handling
func (a *signozLogsAction) parseCompositeQuery(compositeQueryRaw any) (map[string]any, error) {
	var compositeQuery map[string]any

	switch v := compositeQueryRaw.(type) {
	case string:
		if err := common.UnmarshalJsonString(v, &compositeQuery); err != nil {
			slog.Error("signozLogsEnricherAction: invalid compositeQuery json", "error", err, "compositeQuery", v)
			return nil, errors.New("signozLogsEnricherAction: invalid compositeQuery json")
		}
	case map[string]any:
		compositeQuery = v
	default:
		return nil, errors.New("signozLogsEnricherAction: compositeQuery must be string or map")
	}

	// Build final query structure
	finalQuery := map[string]any{
		"panelType":      "list",
		"queryType":      "builder",
		"fillGaps":       false,
		"builderQueries": map[string]any{},
	}

	// Extract and process query data
	if builder, ok := compositeQuery["builder"].(map[string]any); ok {
		if queryDataRaw, ok := builder["queryData"].([]any); ok {
			builderQueries := finalQuery["builderQueries"].(map[string]any)
			for _, qdRaw := range queryDataRaw {
				if qd, ok := qdRaw.(map[string]any); ok {
					if queryName, ok := qd["queryName"].(string); ok {
						builderQueries[strings.TrimSpace(queryName)] = qd
					}
				}
			}
		}
	}

	return finalQuery, nil
}

// parseTimeRange handles time range parsing with proper error handling
func (a *signozLogsAction) parseTimeRange(timerangeRaw any) (map[string]any, error) {
	var timerangeMap map[string]any

	switch v := timerangeRaw.(type) {
	case string:
		if err := common.UnmarshalJsonString(v, &timerangeMap); err != nil {
			slog.Error("signozLogsEnricherAction: invalid timeRange json", "error", err, "timeRange", v)
			return nil, errors.New("signozLogsEnricherAction: invalid timeRange json")
		}
	case map[string]any:
		timerangeMap = v
	default:
		return nil, errors.New("signozLogsEnricherAction: timeRange must be string or map")
	}

	return timerangeMap, nil
}

// getSignozBaseURL retrieves the SigNoz base URL from the agent table for the given account
func getSignozBaseURL(accountId string) string {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Warn("signozLogsAction: failed to get database manager for SigNoz URL", "error", err)
		return ""
	}

	query := `SELECT connection_status->>'logProviderUrl'
	          FROM agent
	          WHERE cloud_account_id = $1
	          AND connection_status->>'logsConnectionProvider' = 'signoz'
	          LIMIT 1`

	var logProviderUrl string
	err = dbms.Db.QueryRowx(query, accountId).Scan(&logProviderUrl)
	if err != nil {
		slog.Debug("signozLogsAction: no SigNoz URL found for account", "accountId", accountId, "error", err)
		return ""
	}

	return logProviderUrl
}

// transformToSignozURLFormat transforms the internal compositeQuery format to SigNoz URL format
// Internal format uses builderQueries (object), URL format uses builder.queryData (array)
func transformToSignozURLFormat(compositeQuery map[string]any) map[string]any {
	urlFormat := map[string]any{
		"queryType": "builder",
		"builder": map[string]any{
			"queryData":     []any{},
			"queryFormulas": []any{},
		},
		"promql": []map[string]any{
			{"name": "A", "query": "", "legend": "", "disabled": false},
		},
		"clickhouse_sql": []map[string]any{
			{"name": "A", "legend": "", "disabled": false, "query": ""},
		},
		"id": common.GenerateUUID(),
	}

	// Convert builderQueries (object) to builder.queryData (array)
	if builderQueries, ok := compositeQuery["builderQueries"].(map[string]any); ok {
		queryData := []any{}
		for _, query := range builderQueries {
			if queryMap, ok := query.(map[string]any); ok {
				queryData = append(queryData, transformQueryForSignozURL(queryMap))
			}
		}
		urlFormat["builder"].(map[string]any)["queryData"] = queryData
	}

	return urlFormat
}

// transformQueryForSignozURL transforms a single query to SigNoz URL format
func transformQueryForSignozURL(query map[string]any) map[string]any {
	result := map[string]any{
		"dataSource":        getSignozMapValue(query, "dataSource", "logs"),
		"queryName":         getSignozMapValue(query, "queryName", "A"),
		"aggregateOperator": getSignozMapValue(query, "aggregateOperator", "noop"),
		"aggregateAttribute": map[string]any{
			"id": "------false", "dataType": "", "key": "",
			"isColumn": false, "type": "", "isJSON": false,
		},
		"timeAggregation":  getSignozMapValue(query, "timeAggregation", "rate"),
		"spaceAggregation": getSignozMapValue(query, "spaceAggregation", "sum"),
		"functions":        getSignozMapValue(query, "functions", []any{}),
		"expression":       getSignozMapValue(query, "expression", "A"),
		"disabled":         getSignozMapValue(query, "disabled", false),
		"stepInterval":     getSignozMapValue(query, "stepInterval", 60),
		"having":           getSignozMapValue(query, "having", []any{}),
		"limit":            nil,
		"orderBy":          getSignozMapValue(query, "orderBy", []map[string]any{{"columnName": "timestamp", "order": "desc"}}),
		"groupBy":          getSignozMapValue(query, "groupBy", []any{}),
		"legend":           getSignozMapValue(query, "legend", ""),
		"reduceTo":         getSignozMapValue(query, "reduceTo", "avg"),
	}

	// Transform filters with enhanced key structure
	if filters, ok := query["filters"].(map[string]any); ok {
		result["filters"] = transformFiltersForSignozURL(filters)
	} else {
		result["filters"] = map[string]any{"items": []any{}, "op": "AND"}
	}

	return result
}

// getSignozMapValue returns the value from map or default if not present
func getSignozMapValue(m map[string]any, key string, defaultValue any) any {
	if v, ok := m[key]; ok {
		return v
	}
	return defaultValue
}

// transformFiltersForSignozURL transforms filters to SigNoz URL format
func transformFiltersForSignozURL(filters map[string]any) map[string]any {
	result := map[string]any{
		"items": []any{},
		"op":    getSignozMapValue(filters, "op", "AND"),
	}

	if items, ok := filters["items"].([]any); ok {
		transformedItems := []any{}
		for _, item := range items {
			if itemMap, ok := item.(map[string]any); ok {
				transformedItems = append(transformedItems, transformFilterItemForSignozURL(itemMap))
			}
		}
		result["items"] = transformedItems
	} else if items, ok := filters["items"].([]map[string]any); ok {
		transformedItems := []any{}
		for _, itemMap := range items {
			transformedItems = append(transformedItems, transformFilterItemForSignozURL(itemMap))
		}
		result["items"] = transformedItems
	}

	return result
}

// transformFilterItemForSignozURL transforms a single filter item to SigNoz URL format
func transformFilterItemForSignozURL(item map[string]any) map[string]any {
	// Get the key name from nested structure
	var keyName string
	if keyObj, ok := item["key"].(map[string]any); ok {
		if k, ok := keyObj["key"].(string); ok {
			keyName = k
		}
	}

	// Generate short ID (8 hex chars)
	shortID := common.GenerateUUID()[:8]

	return map[string]any{
		"id": shortID,
		"key": map[string]any{
			"key":      keyName,
			"dataType": "string",
			"type":     "resource",
			"isColumn": true,
			"isJSON":   false,
			"id":       fmt.Sprintf("%s--string--resource--true", keyName),
		},
		"op":    getSignozMapValue(item, "op", "="),
		"value": item["value"],
	}
}

// buildSignozLogsExplorerURL constructs the SigNoz logs explorer URL with encoded query parameters
func buildSignozLogsExplorerURL(baseURL string, rawQuery map[string]any, startTime, endTime time.Time) (string, error) {
	// Extract compositeQuery from rawQuery
	compositeQueryRaw, ok := rawQuery["compositeQuery"]
	if !ok {
		return "", fmt.Errorf("compositeQuery not found in rawQuery")
	}

	compositeQuery, ok := compositeQueryRaw.(map[string]any)
	if !ok {
		return "", fmt.Errorf("compositeQuery is not a map")
	}

	// Transform internal format to SigNoz URL format
	urlCompositeQuery := transformToSignozURLFormat(compositeQuery)

	// Marshal compositeQuery to JSON
	compositeQueryJSON := common.MarshalJsonSafeString(urlCompositeQuery)
	if compositeQueryJSON == "" {
		return "", fmt.Errorf("failed to marshal compositeQuery")
	}

	// Create options JSON
	options := map[string]any{
		"selectColumns": []string{},
		"maxLines":      2,
		"format":        "list",
	}
	optionsJSON := common.MarshalJsonSafeString(options)
	if optionsJSON == "" {
		return "", fmt.Errorf("failed to marshal options")
	}

	// Create timeRange JSON
	timeRange := map[string]any{
		"start":    startTime.UnixMilli(),
		"end":      endTime.UnixMilli(),
		"pageSize": 100,
	}
	timeRangeJSON := common.MarshalJsonSafeString(timeRange)
	if timeRangeJSON == "" {
		return "", fmt.Errorf("failed to marshal timeRange")
	}

	// Build URL with query parameters
	// compositeQuery needs to be double-encoded
	encodedCompositeQuery := url.QueryEscape(compositeQueryJSON)

	// Build the URL
	baseURL = strings.TrimRight(baseURL, "/")
	logsExplorerURL := fmt.Sprintf("%s/logs/logs-explorer?options=%s&compositeQuery=%s&timeRange=%s",
		baseURL,
		url.QueryEscape(optionsJSON),
		url.QueryEscape(encodedCompositeQuery),
		url.QueryEscape(timeRangeJSON),
	)

	return logsExplorerURL, nil
}

func (a *signozLogsAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params signozLogsParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}
	endTime := time.Now()
	durationMinues := 60
	if params.Duration != 0 {
		durationMinues = params.Duration
	}
	startTime := endTime.Add(-time.Duration(durationMinues) * time.Minute)
	if ctx.GetEvent().StartedAt != nil {
		startTime = *ctx.GetEvent().StartedAt
	}
	if ctx.GetEvent().EndedAt != nil {
		endTime = *ctx.GetEvent().EndedAt
	}

	filters := map[string]any{}
	if queryMap, ok := params.Query.(map[string]any); ok && queryMap["items"] != nil {
		filters = params.Query.(map[string]any)
	} else if queryArr, ok := params.Query.([]any); ok {
		filters = map[string]any{
			"op":    "AND",
			"items": queryArr,
		}
	} else if queryArr, ok := params.Query.([]map[string]any); ok {
		filters = map[string]any{
			"op":    "AND",
			"items": queryArr,
		}
	}

	var rawQuery map[string]any
	if params.RawQuery != nil {
		rawQuery = params.RawQuery
	} else {
		rawQuery = map[string]any{
			"compositeQuery": map[string]any{
				"builderQueries": map[string]any{
					"A": map[string]any{
						"aggregateAttribute": map[string]any{},
						"aggregateOperator":  "noop",
						"dataSource":         "logs",
						"disabled":           false,
						"expression":         "A",
						"functions":          []any{},
						"groupBy":            []any{},
						"having":             []any{},
						"legend":             "",
						"limit":              100,
						"offset":             0,
						"pageSize":           100,
						"queryName":          "A",
						"reduceTo":           "avg",
						"spaceAggregation":   "sum",
						"stepInterval":       60,
						"timeAggregation":    "rate",
						"filters":            filters,
						"orderBy": []map[string]any{
							{"columnName": "timestamp", "order": "desc"},
						},
					},
				},
				"fillGaps":  false,
				"panelType": "list",
				"queryType": "builder",
			},
			"end":       endTime.UnixMilli(),
			"start":     startTime.UnixMilli(),
			"step":      60,
			"variables": map[string]any{},
		}
	}

	relayRequest := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:    ctx.GetAccountId(),
			ActionName:   "signoz_query_range",
			ActionParams: rawQuery,
			Origin:       "services-server",
			NoSinks:      true,
		},
		NoSinks: true,
		Cache:   false,
	}

	relayResponse, err := relay.Execute(relayRequest)
	if err != nil {
		return nil, err
	}

	relayDataAny := relayResponse["data"]
	if relayDataAny == nil {
		ctx.GetLogger().Error("relay: unable to process request", "response", slog.AnyValue(relayResponse))
		return nil, errors.New("relay: unable to execute relay query")
	}

	relayData, ok := relayDataAny.(map[string]any)
	if !ok {
		ctx.GetLogger().Error("relay: unable to process request", "response", slog.AnyValue(relayResponse))
		return nil, errors.New("relay: unable to execute relay query")
	}

	if relayData["success"] != nil {
		if success, ok := relayData["success"].(bool); !ok || !success {
			ctx.GetLogger().Error("relay: relay query success is false",
				"relay_error_code", relayData["error_code"],
				"relay_msg", relayData["msg"],
				"response", slog.AnyValue(relayData))
			return nil, errors.New("relay: unable to execute relay query")
		}
	}

	dataAny, ok := relayData["data"]
	if !ok || dataAny == nil {
		ctx.GetLogger().Error("relay: relay query data is nil", "response", slog.AnyValue(relayData))
		return nil, errors.New("relay: unable to execute relay query")
	}
	data, ok := dataAny.(map[string]any)
	if !ok {
		ctx.GetLogger().Error("relay: relay query data is not a map", "response", slog.AnyValue(relayData))
		return nil, errors.New("relay: unable to execute relay query")
	}

	insight := []playbooks.PlaybookActionResponseInsight{}
	errorInsights := a.extractTopErrorLogs(data)
	insight = append(insight, errorInsights...)
	additionalInfo := map[string]any{}
	if rawParams["title"] != "" {
		additionalInfo["title"] = rawParams["title"]
	}

	// Add log summary (level breakdown + pattern grouping)
	logEntries := a.extractLogEntries(data)
	if summary := computeLogSummary(logEntries); summary != nil {
		additionalInfo["log_summary"] = summary
	}

	// Generate SigNoz logs explorer URL
	baseURL := getSignozBaseURL(ctx.GetAccountId())
	if baseURL != "" {
		logsExplorerURL, err := buildSignozLogsExplorerURL(baseURL, rawQuery, startTime, endTime)
		if err != nil {
			ctx.GetLogger().Warn("signozLogsAction: failed to build logs explorer URL", "error", err, "accountId", ctx.GetAccountId())
		} else {
			additionalInfo["reference_url"] = logsExplorerURL
			ctx.GetLogger().Debug("signozLogsAction: generated logs explorer URL", "url", logsExplorerURL)
		}
	}

	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
		"end":                  endTime.UnixMilli(),
		"start":                startTime.UnixMilli(),
	}
	// Create base response using the standard function
	baseResponse := playbooks.NewPlaybookActionResponseJson(data, additionalInfo, insight, metadata)

	// Create enhanced response with label extraction capability
	response := &playbooks.LogsActionResponse{
		Data:            baseResponse.Data,
		AdditionalInfo:  baseResponse.AdditionalInfo,
		Insight:         baseResponse.Insight,
		Metadata:        baseResponse.Metadata,
		ExtractedLabels: a.extractLabelsFromLogs(data, params.RegexExtractors, params.LabelExtractors),
	}
	return response, err
}

// extractLabelsFromLogs processes log data and applies regex patterns to extract labels
func (a *signozLogsAction) extractLabelsFromLogs(data map[string]any, extractors []RegexLabelExtractor, labelExtractor []LabelExtractor) map[string]any {
	extractedLabels := make(map[string]any)

	if len(extractors) == 0 && len(labelExtractor) == 0 {
		return extractedLabels
	}

	// Compile regex patterns
	compiledPatterns := actionLogCompileRegexPatterns(extractors)
	if len(compiledPatterns) == 0 && len(labelExtractor) == 0 {
		return extractedLabels
	}

	// Get log entries from data structure
	logEntries := a.extractLogEntries(data)
	if len(logEntries) == 0 {
		return extractedLabels
	}

	// Process ALL log entries to collect all unique values for each label
	for _, logData := range logEntries {
		body, _ := logData["body"].(string)

		// Apply regex extractors (even if body is empty, we might have label extractors)
		if body != "" {
			actionLogApplyRegexExtractors(body, compiledPatterns, extractors, extractedLabels)
		}

		// Apply label extractors
		actionLogApplyLabelExtractors(logData, labelExtractor, extractedLabels)
	}

	actionLogExtractionResults(extractedLabels, len(compiledPatterns))
	return extractedLabels
}

// extractLogEntries extracts log data from the nested data structure
func (a *signozLogsAction) extractLogEntries(data map[string]any) []map[string]any {
	var logEntries []map[string]any

	result, ok := data["data"].(map[string]any)
	if !ok {
		return logEntries
	}

	resultData, ok := result["result"].([]any)
	if !ok {
		return logEntries
	}

	for _, r := range resultData {
		resultMap, ok := r.(map[string]any)
		if !ok {
			continue
		}

		list, ok := resultMap["list"].([]any)
		if !ok {
			continue
		}

		for _, entry := range list {
			logEntry, ok := entry.(map[string]any)
			if !ok {
				continue
			}

			logData, ok := logEntry["data"].(map[string]any)
			if ok {
				logEntries = append(logEntries, logData)
			}
		}
	}

	return logEntries
}

func (a *signozLogsAction) extractTopErrorLogs(data map[string]any) []playbooks.PlaybookActionResponseInsight {
	var insights []playbooks.PlaybookActionResponseInsight

	resultData, ok := data["data"].(map[string]any)
	if !ok || len(resultData) == 0 {
		return insights
	}
	result, ok := resultData["result"].([]any)
	if !ok || len(result) == 0 {
		return insights
	}

	var errorLogs []map[string]any
	var allLogs []map[string]any

	for _, r := range result {
		resultMap, ok := r.(map[string]any)
		if !ok {
			continue
		}

		list, ok := resultMap["list"].([]any)
		if !ok {
			continue
		}

		for _, entry := range list {
			logEntry, ok := entry.(map[string]any)
			if !ok {
				continue
			}

			logData, ok := logEntry["data"].(map[string]any)
			if !ok {
				continue
			}

			severityText, _ := logData["severity_text"].(string)
			// Check if this is an error level log
			if severityText == "ERROR" || severityText == "FATAL" {
				errorLogs = append(errorLogs, logData)
			}
			// Store all logs for fallback pattern matching
			allLogs = append(allLogs, logData)
		}
	}

	for i, logData := range errorLogs {
		if i >= actionLogMaxInsightErrors {
			break
		}

		body, _ := logData["body"].(string)
		severityText, _ := logData["severity_text"].(string)

		if body != "" {
			insights = append(insights, playbooks.PlaybookActionResponseInsight{
				Message:  fmt.Sprintf("%s: %s", severityText, body),
				Severity: "Critical",
			})
		}
	}

	// Fallback: if no severity-based error logs found, search for error patterns
	if len(insights) == 0 {
		insights = actionLogExtractErrorPatterns(allLogs, actionLogMaxInsightErrors)
	}

	return insights
}

type observabilityLogAction struct{}
type observabilityLogActionParams struct {
	Query           string                `json:"query,omitempty"`
	QueryOptions    map[string]any        `json:"query_options,omitempty"`
	AccountId       string                `json:"account_id,omitempty"`
	Duration        int                   `json:"duration,omitempty"`
	RegexExtractors []RegexLabelExtractor `json:"regex_extractors,omitempty"`
	LabelExtractors []LabelExtractor      `json:"label_extractors,omitempty"`
}

// getEventNamespace returns the namespace for log collection from SubjectNamespace.
// For webhook events this is populated from Labels["namespace"] during enrichment.
// For agent events this is set directly from the k8s resource metadata.
func getEventNamespace(event playbooks.PlaybookEvent) string {
	return event.SubjectNamespace
}

func (a *observabilityLogAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)
	source, err := getLogSourceForAccount(requestCtx, ctx.GetAccountId(), "", "")
	namespace := getEventNamespace(ctx.GetEvent())
	if err != nil || source == nil {
		// No configured log source — allow relay fallback only for non-cloud events
		if ctx.GetEvent().SubjectName != "" && namespace != "" && !isCloudEventSource(ctx.GetEvent().Source) {
			return true
		}
		return false
	}

	if autoExecutor, ok := source.(PlaybookQueryGenerator); ok {
		return autoExecutor.CanGenerateQuery(ctx)
	}

	// Source exists but doesn't implement PlaybookQueryGenerator — use workload-based query
	if ctx.GetEvent().SubjectName != "" && namespace != "" {
		return true
	}

	return false
}

func (a *observabilityLogAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)
	source, err := getLogSourceForAccount(requestCtx, ctx.GetAccountId(), "", "")

	// Try PlaybookQueryGenerator if source supports it
	if err == nil && source != nil {
		if autoExecutor, ok := source.(PlaybookQueryGenerator); ok {
			query, request, genErr := autoExecutor.GenerateQuery(ctx)
			if genErr == nil {
				params := map[string]any{
					"query":         query,
					"query_options": request,
				}
				result, execErr := a.Execute(ctx, params)
				if execErr == nil && result != nil {
					return result, nil
				}
				ctx.GetLogger().Info("observability: PlaybookQueryGenerator Execute failed, falling back to workload query", "error", execErr)
			} else {
				ctx.GetLogger().Info("observability: PlaybookQueryGenerator failed, trying workload query", "error", genErr)
			}
		}
	}

	// Fallback: workload-based query via configured log source, then relay
	return a.autoExecuteByWorkload(ctx)
}

// autoExecuteByWorkload queries logs using the event's SubjectName as workload name
// and namespace label. If the configured log source returns no results, falls back
// to relay kubectl logs.
func (a *observabilityLogAction) autoExecuteByWorkload(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	workloadName := ctx.GetEvent().SubjectName
	namespace := getEventNamespace(ctx.GetEvent())

	endTime := time.Now().UnixMilli()
	startTime := endTime - int64(60*60*1000) // 1 hour default
	if ctx.GetEvent().StartedAt != nil {
		startTime = ctx.GetEvent().StartedAt.UnixMilli()
	}
	if ctx.GetEvent().EndedAt != nil {
		endTime = ctx.GetEvent().EndedAt.UnixMilli()
	}

	// Try configured log source with workload-based query
	requestCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)
	logoutput, err := FetchLogs(requestCtx, FetchLogRequest{
		AccountId: ctx.GetAccountId(),
		StartTime: startTime,
		EndTime:   endTime,
		Limit:     1000,
		QueryRequest: LogsQueryBuilderRequest{
			Where: buildWorkloadLogWhereClause(workloadName, namespace),
		},
	})

	if err != nil {
		ctx.GetLogger().Info("observability: log source query failed, falling back to relay", "error", err)
	}

	if len(logoutput) > 0 {
		ctx.GetLogger().Info("observability: logs auto action found results via configured source",
			"workload", workloadName, "namespace", namespace, "count", len(logoutput))
		return a.buildLogResponse(logoutput, map[string]any{
			"workload_name": workloadName,
			"namespace":     namespace,
			"source":        "configured_integration",
		})
	}

	// Skip relay fallback for cloud event sources — these accounts have no K8s agent
	if isCloudEventSource(ctx.GetEvent().Source) {
		ctx.GetLogger().Info("observability: skipping relay fallback for cloud event source",
			"source", ctx.GetEvent().Source, "workload", workloadName)
		return nil, nil
	}

	// Fallback: relay kubectl logs
	ctx.GetLogger().Info("observability: falling back to relay kubectl logs",
		"workload", workloadName, "namespace", namespace)
	return a.fetchLogsViaRelay(ctx, workloadName, namespace)
}

// fetchLogsViaRelay fetches logs via relay. For workload kinds (Deployment, DaemonSet,
// StatefulSet, ReplicaSet) it uses kubectl logs since logs_enricher expects a pod name.
func (a *observabilityLogAction) fetchLogsViaRelay(ctx playbooks.PlaybookActionContext, workloadName, namespace string) (playbooks.PlaybookActionResponse, error) {
	kind := ""
	if ctx.GetEvent().Labels != nil {
		kind = ctx.GetEvent().Labels["kind"]
	}
	// Fall back to SubjectType for agent-generated events (lowercase, e.g. "deployment")
	if kind == "" {
		kind = ctx.GetEvent().SubjectType
	}

	if strings.EqualFold(kind, "Deployment") || strings.EqualFold(kind, "DaemonSet") || strings.EqualFold(kind, "StatefulSet") || strings.EqualFold(kind, "ReplicaSet") {
		return a.fetchLogsViaKubectl(ctx, kind, workloadName, namespace)
	}

	return a.fetchLogsViaLogsEnricher(ctx, workloadName, namespace)
}

// fetchLogsViaLogsEnricher uses the relay agent's logs_enricher to fetch logs for a pod.
func (a *observabilityLogAction) fetchLogsViaLogsEnricher(ctx playbooks.PlaybookActionContext, workloadName, namespace string) (playbooks.PlaybookActionResponse, error) {
	relayRequest := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  ctx.GetAccountId(),
			ActionName: "logs_enricher",
			ActionParams: map[string]any{
				"name":      workloadName,
				"namespace": namespace,
			},
			Origin: "services-server",
		},
		NoSinks: true,
		Cache:   false,
	}
	relayResponse, additionalInfo, err := relay.ExecuteAndExtractResponse(relayRequest)
	if err != nil {
		return nil, err
	}
	data, ok := relayResponse["data"].(string)
	if !ok {
		return nil, errors.New("relay: unable to extract log data from response")
	}
	filename, _ := relayResponse["filename"].(string)
	typeVal, _ := relayResponse["type"].(string)
	insight := playbooks.InsightFromRelayResponse(relayResponse)
	if summary := computeLogSummaryFromText(data); summary != nil {
		additionalInfo["log_summary"] = summary
	}
	return playbooks.PlaybookActionResponseFile{
		AdditionalInfo: additionalInfo,
		Data:           data,
		Filename:       filename,
		Type:           typeVal,
		Insight:        insight,
	}, nil
}

// fetchLogsViaKubectl uses kubectl logs <kind>/<name> via kubectl_command_executor
// for workload kinds where logs_enricher cannot resolve the resource.
func (a *observabilityLogAction) fetchLogsViaKubectl(ctx playbooks.PlaybookActionContext, kind, workloadName, namespace string) (playbooks.PlaybookActionResponse, error) {
	command := fmt.Sprintf("kubectl logs %s/%s -n %s --tail=1000 --all-containers=true",
		strings.ToLower(kind), workloadName, namespace)

	ctx.GetLogger().Info("observability: fetching logs via kubectl", "command", command)

	relayRequest := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  ctx.GetAccountId(),
			ActionName: "kubectl_command_executor",
			ActionParams: map[string]any{
				"command": command,
			},
			Origin: "services-server",
		},
		NoSinks: true,
		Cache:   false,
	}
	relayResponse, additionalInfo, err := relay.ExecuteAndExtractResponse(relayRequest)
	if err != nil {
		return nil, err
	}
	// kubectl_command_executor on the agent (robusta/playbooks/nudgebee_playbooks/kubectl_actions.py)
	// returns its output wrapped in a JsonBlock, so stdout/stderr live one level deep inside
	// relayResponse["data"] (a JSON-encoded string), not at the top level.
	stdout, stderr := extractKubectlOutput(relayResponse)
	if stdout == "" {
		ctx.GetLogger().Error("relay: kubectl logs returned no output", "stderr", stderr, "response", slog.AnyValue(relayResponse))
		return nil, fmt.Errorf("relay: kubectl logs failed: %s", stderr)
	}
	insight := playbooks.InsightFromRelayResponse(relayResponse)
	if summary := computeLogSummaryFromText(stdout); summary != nil {
		additionalInfo["log_summary"] = summary
	}
	return playbooks.PlaybookActionResponseFile{
		AdditionalInfo: additionalInfo,
		Data:           stdout,
		Filename:       fmt.Sprintf("%s-%s.log", workloadName, namespace),
		Type:           "text/plain",
		Insight:        insight,
	}, nil
}

// extractKubectlOutput unwraps the kubectl_command_executor response to recover stdout/stderr.
//
// kubectl_command_executor in robusta publishes its output via add_enrichment([JsonBlock(json.dumps({...}))]),
// which ModelConversion.to_evidence_json serializes as {"type":"json","data":"<json_string>"}.
// After relay.ExecuteAndExtractResponse unwraps the outer envelope, the agent action response
// retains this JsonBlock shape, so stdout/stderr live inside the stringified "data" field rather
// than at the top level. This helper handles that indirection.
func extractKubectlOutput(relayResponse map[string]any) (stdout, stderr string) {
	// First try the fast-path: some agent actions (future or alternate shapes) may put
	// stdout/stderr at the top level already.
	if s, ok := relayResponse["stdout"].(string); ok && s != "" {
		stdout = s
	}
	if s, ok := relayResponse["stderr"].(string); ok {
		stderr = s
	}
	if stdout != "" {
		return stdout, stderr
	}

	// Fallback: unwrap JsonBlock payload — relayResponse["data"] is a JSON-encoded string
	// containing {"command":..., "stdout":..., "stderr":...}.
	dataStr, ok := relayResponse["data"].(string)
	if !ok || dataStr == "" {
		return stdout, stderr
	}
	var inner kubectlJsonBlockPayload
	if err := json.Unmarshal([]byte(dataStr), &inner); err != nil {
		return stdout, stderr
	}
	return inner.Stdout, inner.Stderr
}

// kubectlJsonBlockPayload is the payload robusta's kubectl_command_executor writes
// inside the JsonBlock data field — see robusta-ai/playbooks sdk: add_enrichment(
// [JsonBlock(json.dumps({"command": ..., "stdout": ..., "stderr": ...}))]).
type kubectlJsonBlockPayload struct {
	Command string `json:"command"`
	Stdout  string `json:"stdout"`
	Stderr  string `json:"stderr"`
}

// buildLogResponse creates a standard log action response from OutputLog results
func (a *observabilityLogAction) buildLogResponse(logoutput []OutputLog, queryInfo map[string]any) (playbooks.PlaybookActionResponse, error) {
	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                queryInfo,
	}
	logoutputMap := lo.Map(logoutput, func(item OutputLog, index int) map[string]any {
		return map[string]any{
			"body":          item.Message,
			"severity_text": item.Severity,
			"labels":        item.Labels,
		}
	})
	errorInsights := actionLogExtractErrorPatterns(logoutputMap, actionLogMaxInsightErrors)
	additionalInfo := map[string]any{}
	if summary := computeLogSummary(logoutputMap); summary != nil {
		additionalInfo["log_summary"] = summary
	}
	return playbooks.NewPlaybookActionResponseJson(map[string]any{"data": logoutput}, additionalInfo, errorInsights, metadata), nil
}

// buildWorkloadLogWhereClause creates a where clause to filter logs by workload name and namespace.
// Uses canonical keys "app" and "namespace" so that getMergedLabelMapping can translate them
// to provider-specific field names (e.g. via the UI's Log Label Mapper: app → deployment.keyword).
// For backends that use different native field names (Loki: service_name/workLoad_name),
// the static GetLabelMapping() handles the translation.
func buildWorkloadLogWhereClause(workloadName, namespace string) query.QueryWhereClause {
	return query.QueryWhereClause{
		And: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{"app": {query.Eq: workloadName}}},
			{Binary: query.BinaryWhereClause{"namespace": {query.Eq: namespace}}},
		},
	}
}

// isCloudEventSource returns true for event sources that originate from cloud providers
// (not K8s). These events should not fall back to relay kubectl logs since their accounts
// have no K8s agent connected.
func isCloudEventSource(source string) bool {
	switch source {
	case "Azure_Monitor_Alert", "azure_monitor_webhook",
		"AWS_CloudWatch_Alarm", "AWS_EventBridge",
		"GCP_Metric_Alert":
		return true
	}
	return false
}

// extractLabelsFromLogs processes log data and applies regex patterns to extract labels
func (a *observabilityLogAction) extractLabelsFromLogs(data []OutputLog, extractors []RegexLabelExtractor, labelExtractor []LabelExtractor) map[string]any {
	extractedLabels := make(map[string]any)

	if len(extractors) == 0 && len(labelExtractor) == 0 {
		return extractedLabels
	}

	// Compile regex patterns
	compiledPatterns := actionLogCompileRegexPatterns(extractors)
	if len(compiledPatterns) == 0 && len(labelExtractor) == 0 {
		return extractedLabels
	}

	// Process ALL log entries to collect all unique values for each label
	for _, logData := range data {
		// Apply regex extractors
		if logData.Message != "" {
			actionLogApplyRegexExtractors(logData.Message, compiledPatterns, extractors, extractedLabels)
		}

		// Apply label extractors
		actionLogApplyLabelExtractors(logData.Labels, labelExtractor, extractedLabels)
	}

	actionLogExtractionResults(extractedLabels, len(compiledPatterns))
	return extractedLabels
}

func (a *observabilityLogAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params observabilityLogActionParams
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

	logoutput, err := FetchLogs(security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil), FetchLogRequest{
		AccountId: params.AccountId,
		Query:     params.Query,
		Request:   params.QueryOptions,
		StartTime: startTime,
		EndTime:   endTime,
		Limit:     1000,
		Offset:    0,
	})

	if err != nil {
		return nil, err
	}

	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
	}
	insight := []playbooks.PlaybookActionResponseInsight{}
	logoutputMap := lo.Map(logoutput, func(item OutputLog, index int) map[string]any {
		result := map[string]any{
			"body":          item.Message,
			"severity_text": item.Severity,
			"labels":        item.Labels, // Include full labels for stack trace extraction
		}
		return result
	})
	errorInsights := actionLogExtractErrorPatterns(logoutputMap, actionLogMaxInsightErrors)
	insight = append(insight, errorInsights...)
	additionalInfo := map[string]any{}
	if summary := computeLogSummary(logoutputMap); summary != nil {
		additionalInfo["log_summary"] = summary
	}

	// Create base response using the standard function
	baseResponse := playbooks.NewPlaybookActionResponseJson(map[string]any{"data": logoutput}, additionalInfo, insight, metadata)

	// Create enhanced response with label extraction capability
	response := &playbooks.LogsActionResponse{
		Data:            baseResponse.Data,
		AdditionalInfo:  baseResponse.AdditionalInfo,
		Insight:         baseResponse.Insight,
		Metadata:        baseResponse.Metadata,
		ExtractedLabels: a.extractLabelsFromLogs(logoutput, params.RegexExtractors, params.LabelExtractors),
	}
	return response, err
}

// Datadog Logs Action
type datadogLogsAction struct{}

type datadogLogsActionParams struct {
	LogQuery  string `json:"log_query,omitempty"`
	LogFromTs int64  `json:"log_from_ts,omitempty"`
	LogToTs   int64  `json:"log_to_ts,omitempty"`
	LogURL    string `json:"log_url,omitempty"`
}

type DatadogLog struct {
	Timestamp  string                 `json:"timestamp"`
	Attributes map[string]interface{} `json:"attributes"`
}

func (a *datadogLogsAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	labels := ctx.GetEvent().Labels
	if labels == nil {
		return false
	}

	// Check if we have log URL or log query parameters
	if labels["log_url"] != "" || labels["log_query"] != "" {
		return true
	}

	return false
}

func (a *datadogLogsAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	labels := ctx.GetEvent().Labels
	params := map[string]any{}

	if logURL := labels["log_url"]; logURL != "" {
		params["log_url"] = logURL
	}

	if logQuery := labels["log_query"]; logQuery != "" {
		params["log_query"] = logQuery
	}

	// Prefer log time range from labels (extracted from Datadog payload during webhook ingestion)
	if fromTs := labels["log_from_ts"]; fromTs != "" {
		if v, err := strconv.ParseInt(fromTs, 10, 64); err == nil && v > 0 {
			params["log_from_ts"] = v
		}
	}
	if toTs := labels["log_to_ts"]; toTs != "" {
		if v, err := strconv.ParseInt(toTs, 10, 64); err == nil && v > 0 {
			params["log_to_ts"] = v
		}
	}

	// Fallback to event timestamps if no label timestamps
	if params["log_from_ts"] == nil || params["log_to_ts"] == nil {
		if ctx.GetEvent().StartedAt != nil && ctx.GetEvent().EndedAt != nil {
			params["log_from_ts"] = ctx.GetEvent().StartedAt.Unix()
			params["log_to_ts"] = ctx.GetEvent().EndedAt.Unix()
		}
	}

	return a.Execute(ctx, params)
}

func (a *datadogLogsAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params datadogLogsActionParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	sc := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)

	var datadogLog DatadogSource

	// Parse log URL if provided
	if params.LogURL != "" && params.LogQuery == "" {
		log, err := url.Parse(params.LogURL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse datadog log url: %w", err)
		}

		logQueryParams := log.Query()
		if query := logQueryParams.Get("query"); query != "" {
			params.LogQuery = query
		}

		if fromTs := logQueryParams.Get("from_ts"); fromTs != "" {
			if fromInt, err := strconv.Atoi(fromTs); err == nil {
				params.LogFromTs = int64(fromInt)
			}
		}

		if toTs := logQueryParams.Get("to_ts"); toTs != "" {
			if toInt, err := strconv.Atoi(toTs); err == nil {
				params.LogToTs = int64(toInt)
			}
		}
	}

	// Build a default log query from event context when log_url had no query param
	if params.LogQuery == "" {
		labels := ctx.GetEvent().Labels
		if labels != nil {
			var queryParts []string
			for _, key := range []string{"service", "host", "env"} {
				if v, ok := labels[key]; ok && v != "" {
					queryParts = append(queryParts, fmt.Sprintf("%s:%s", key, escapeDatadogTagValue(v)))
				}
			}
			if len(queryParts) > 0 {
				params.LogQuery = strings.Join(queryParts, " ")
			}
		}
	}

	// Set default time range if not provided
	if params.LogFromTs == 0 || params.LogToTs == 0 {
		now := time.Now()
		params.LogToTs = now.Unix()
		params.LogFromTs = now.Add(-10 * time.Minute).Unix()
	} else if params.LogFromTs == params.LogToTs {
		// Equal timestamps (e.g., newly triggered alert where starts_at == ends_at):
		// use a 10-minute window centered on the timestamp
		params.LogFromTs = params.LogFromTs - 300
		params.LogToTs = params.LogToTs + 300
	}

	if params.LogQuery == "" {
		return nil, errors.New("log_query or log_url is required")
	}

	// Create query params structure
	queryParams := integrations.DatadogQueryParams{
		LogQuery:  params.LogQuery,
		LogFromTs: params.LogFromTs,
		LogToTs:   params.LogToTs,
	}

	resp, err := datadogLog.QueryLogs(sc, FetchLogRequest{
		AccountId: ctx.GetAccountId(),
		Query:     queryParams.LogQuery,
		StartTime: queryParams.LogFromTs,
		EndTime:   queryParams.LogToTs,
		Limit:     1000,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get datadog logs: %w", err)
	}

	// Skip empty evidence — no value in adding a "Datadog Logs" card with no data
	if len(resp) == 0 {
		return nil, nil
	}

	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
	}

	// Convert OutputLog to format: {"data": [{timestamp, message}]}
	logOutput := []map[string]any{}
	logOutputForInsights := []map[string]any{}
	for _, item := range resp {
		logOutput = append(logOutput, map[string]any{
			"timestamp": item.Timestamp,
			"message":   item.Message,
		})
		// actionLogExtractErrorPatterns expects "body" key
		logOutputForInsights = append(logOutputForInsights, map[string]any{
			"body": item.Message,
		})
	}

	title := "Datadog Logs"
	if t, ok := rawParams["title"].(string); ok && t != "" {
		title = t
	}

	insights := actionLogExtractErrorPatterns(logOutputForInsights, 2)

	additionalInfo := map[string]any{
		"action_name": "cloud_logs",
		"title":       title,
	}
	if summary := computeLogSummary(logOutputForInsights); summary != nil {
		additionalInfo["log_summary"] = summary
	}

	return playbooks.NewPlaybookActionResponseJson(map[string]any{"data": logOutput}, additionalInfo, insights, metadata), nil
}
